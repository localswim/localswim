package schema

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

// WorkflowPolicyStatements adds immutable retained policies. The physical8
// workflow_definition remains intact as the exact original configured policy.
func WorkflowPolicyStatements() []string {
	return []string{expand(`CREATE TABLE workflow_policy (
 incarnation TEXT NOT NULL REFERENCES board(incarnation),
 policy_version INTEGER NOT NULL CHECK(policy_version BETWEEN 1 AND 128),
 workflow_id TEXT CHECK(workflow_id IS NULL OR {{u:workflow_id}}),
 definition TEXT CHECK(definition IS NULL OR (json_valid(definition) AND length(CAST(definition AS BLOB))<=65536)),
 PRIMARY KEY(incarnation,policy_version),
 CHECK((workflow_id IS NULL)=(definition IS NULL)),
 CHECK(policy_version=1 OR definition IS NOT NULL)
) STRICT`),
		`CREATE TRIGGER workflow_policy_immutable BEFORE UPDATE ON workflow_policy BEGIN SELECT RAISE(ABORT,'workflow policy immutable'); END`,
		`CREATE TRIGGER workflow_policy_retained BEFORE DELETE ON workflow_policy BEGIN SELECT RAISE(ABORT,'workflow policy retained'); END`}
}

// ReadWorkflowPolicies returns every policy in one coherent caller-owned read
// transaction, including the implicit default version1. Public readers omit
// this array until it has more than one entry.
func ReadWorkflowPolicies(ctx context.Context, c *sql.Conn, id Identity) ([]A.WorkflowPolicy, error) {
	var boardID, incarnation string
	if err := c.QueryRowContext(ctx, "SELECT board_id,incarnation FROM board WHERE singleton=1").Scan(&boardID, &incarnation); err != nil || boardID != id.BoardID || incarnation != id.Incarnation {
		return nil, errors.New("workflow storage identity mismatch")
	}
	rows, err := c.QueryContext(ctx, "SELECT policy_version,workflow_id,definition FROM workflow_policy WHERE incarnation=? ORDER BY policy_version", id.Incarnation)
	if err != nil {
		return nil, err
	}
	var policies []A.WorkflowPolicy
	for rows.Next() {
		var version int64
		var workflowID, definition sql.NullString
		if err = rows.Scan(&version, &workflowID, &definition); err != nil {
			rows.Close()
			return nil, err
		}
		p := A.WorkflowPolicy{PolicyVersion: A.PolicyVersion(version)}
		if definition.Valid {
			w, e := A.DecodeWorkflow([]byte(definition.String))
			if e != nil || w == nil || !workflowID.Valid || string(w.WorkflowID) != workflowID.String || int64(w.Revision) != version {
				rows.Close()
				return nil, errors.New("invalid stored workflow policy")
			}
			canonical, e := A.WorkflowBytes(w)
			if e != nil || !bytes.Equal(canonical, []byte(definition.String)) {
				rows.Close()
				return nil, errors.New("noncanonical stored workflow policy")
			}
			p.Workflow = w
		} else if workflowID.Valid || version != 1 {
			rows.Close()
			return nil, errors.New("invalid implicit workflow policy")
		}
		policies = append(policies, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = validatePolicies(policies); err != nil {
		return nil, err
	}
	var current int64
	if err = c.QueryRowContext(ctx, "SELECT policy_version FROM policy WHERE incarnation=?", id.Incarnation).Scan(&current); err != nil || current != int64(len(policies)) {
		return nil, errors.New("workflow current pointer mismatch")
	}
	original, err := readWorkflowDefinition(ctx, c, id, false)
	if err != nil {
		return nil, err
	}
	left, e1 := A.WorkflowBytes(original)
	right, e2 := A.WorkflowBytes(policies[0].Workflow)
	if e1 != nil || e2 != nil || !bytes.Equal(left, right) {
		return nil, errors.New("workflow original mismatch")
	}
	return policies, nil
}

func validatePolicies(policies []A.WorkflowPolicy) error {
	if len(policies) == 0 {
		return errors.New("missing retained workflow policy")
	}
	if len(policies) == 1 {
		if policies[0].PolicyVersion != 1 {
			return errors.New("invalid initial policy version")
		}
		return A.ValidateInitialWorkflow(policies[0].Workflow)
	}
	return A.ValidateWorkflowPolicies(A.PolicyVersion(len(policies)), policies[len(policies)-1].Workflow, policies)
}

func ReadWorkflow(ctx context.Context, c *sql.Conn, id Identity) (*A.Workflow, error) {
	policies, err := ReadWorkflowPolicies(ctx, c, id)
	if err != nil {
		return nil, err
	}
	return policies[len(policies)-1].Workflow, nil
}

// InstallWorkflowPolicies extends a freshly initialized restore target. The
// caller owns the transaction and initialized it with the exact policy1.
func InstallWorkflowPolicies(ctx context.Context, c *sql.Conn, id Identity, policies []A.WorkflowPolicy) error {
	if len(policies) == 0 {
		return nil
	}
	if err := validatePolicies(policies); err != nil {
		return err
	}
	old, err := ReadWorkflowPolicies(ctx, c, id)
	if err != nil {
		return err
	}
	if len(old) != 1 {
		return errors.New("restore policy target is not initial")
	}
	a, _ := A.Canonical(old[0])
	b, _ := A.Canonical(policies[0])
	if !bytes.Equal(a, b) {
		return errors.New("restore initial policy mismatch")
	}
	for _, p := range policies[1:] {
		definition, e := A.WorkflowBytes(p.Workflow)
		if e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "INSERT INTO workflow_policy(incarnation,policy_version,workflow_id,definition) VALUES(?,?,?,?)", id.Incarnation, p.PolicyVersion, p.Workflow.WorkflowID, string(definition)); e != nil {
			return e
		}
	}
	_, err = c.ExecContext(ctx, "UPDATE policy SET policy_version=? WHERE incarnation=?", len(policies), id.Incarnation)
	return err
}

func validateWorkflow(ctx context.Context, c *sql.Conn, id Identity) error {
	policies, err := ReadWorkflowPolicies(ctx, c, id)
	if err != nil {
		return err
	}
	current := policies[len(policies)-1].Workflow
	rows, err := c.QueryContext(ctx, "SELECT card_id,lane,revision,current_approval_id,workflow_policy_version FROM card WHERE incarnation=?", id.Incarnation)
	if err != nil {
		return err
	}
	type state struct {
		card, lane string
		revision   int64
		approval   sql.NullString
		pin        sql.NullInt64
	}
	var states []state
	for rows.Next() {
		var s state
		if err = rows.Scan(&s.card, &s.lane, &s.revision, &s.approval, &s.pin); err != nil {
			rows.Close()
			return err
		}
		states = append(states, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, s := range states {
		pin := int64(1)
		if s.pin.Valid {
			pin = s.pin.Int64
		}
		if pin < 1 || pin > int64(len(policies)) || (len(policies) == 1 && s.pin.Valid) {
			return errors.New("invalid current workflow pin")
		}
		if A.WorkflowRole(current, A.Lane(s.lane)) == "" || A.WorkflowRole(policies[pin-1].Workflow, A.Lane(s.lane)) == "" {
			return errors.New("stored card lane outside workflow")
		}
		if current != nil {
			if err = validatePolicyApproval(ctx, c, id, policies, s.card, s.lane, s.revision, s.approval, current, int64(len(policies))); err != nil {
				return err
			}
		}
	}
	rows, err = c.QueryContext(ctx, "SELECT card_id,card_revision,state,workflow_policy_version FROM card_revision WHERE incarnation=? AND state IS NOT NULL", id.Incarnation)
	if err != nil {
		return err
	}
	type historic struct {
		card     string
		revision int64
		data     []byte
		pin      sql.NullInt64
	}
	var history []historic
	for rows.Next() {
		var h historic
		if err = rows.Scan(&h.card, &h.revision, &h.data, &h.pin); err != nil {
			rows.Close()
			return err
		}
		history = append(history, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, h := range history {
		var fields map[string]json.RawMessage
		if json.Unmarshal(h.data, &fields) != nil {
			if h.pin.Valid || policies[0].Workflow != nil {
				return errors.New("invalid strict snapshot JSON")
			}
			continue
		}
		pin := A.PolicyVersion(1)
		if h.pin.Valid {
			pin = A.PolicyVersion(h.pin.Int64)
		}
		if pin < 1 || pin > A.PolicyVersion(len(policies)) || (h.pin.Valid && len(policies) == 1) {
			return errors.New("invalid historical workflow pin")
		}
		// Null metadata retains the exact historical default-one opaque branch.
		// New strict states bind their JSON field to the independently stored pin.
		if h.pin.Valid || policies[0].Workflow != nil {
			if err = ValidateSnapshotPolicyPin(h.data, h.pin); err != nil {
				return err
			}
		}
		w := policies[pin-1].Workflow
		if w == nil {
			raw, has := fields["lane"]
			if !has {
				continue
			}
			if _, e := contract.DecodeJSON(h.data); e != nil {
				return errors.New("invalid stored snapshot lane")
			}
			var lane string
			if json.Unmarshal(raw, &lane) != nil || A.WorkflowRole(nil, A.Lane(lane)) == "" {
				return errors.New("stored snapshot lane outside default workflow")
			}
			continue
		}
		s, e := A.Decode[A.SnapshotState](h.data)
		if e != nil || A.WorkflowRole(w, s.Lane) == "" {
			return errors.New("stored snapshot outside retained workflow")
		}
		approval := sql.NullString{}
		if s.CurrentApprovalID != nil {
			approval = sql.NullString{String: string(*s.CurrentApprovalID), Valid: true}
		}
		if e = validatePolicyApproval(ctx, c, id, policies, h.card, string(s.Lane), h.revision, approval, w, int64(pin)); e != nil {
			return e
		}
	}
	// Stored grants are independent facts. A changed ceiling changes effective
	// authority without making a retained, disabled, or historical grant invalid.
	var bad int
	if err = c.QueryRowContext(ctx, `SELECT count(*) FROM grant_right r JOIN grant_record g USING(incarnation,grant_id) JOIN actor a ON a.incarnation=g.incarnation AND a.actor_id=g.actor_id WHERE r.incarnation=? AND a.kind='automation' AND r.right_name IN ('approve','grant-admin','history-admin')`, id.Incarnation).Scan(&bad); err != nil || bad != 0 {
		return errors.New("invalid automation grant rights")
	}
	rows, err = c.QueryContext(ctx, `SELECT p.policy_version,a.kind FROM approval p JOIN actor a ON a.incarnation=p.incarnation AND a.actor_id=p.actor_id WHERE p.incarnation=?`, id.Incarnation)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		var kind string
		if err = rows.Scan(&version, &kind); err != nil {
			return err
		}
		if version < 1 || version > len(policies) {
			return errors.New("approval policy missing")
		}
		if policies[version-1].Workflow != nil && kind != "human" {
			return errors.New("invalid stored workflow approval")
		}
	}
	return rows.Err()
}

func validatePolicyApproval(ctx context.Context, c *sql.Conn, id Identity, policies []A.WorkflowPolicy, card, lane string, revision int64, approval sql.NullString, w *A.Workflow, limit int64) error {
	if A.WorkflowRole(w, A.Lane(lane)) != "completed" {
		if approval.Valid {
			return errors.New("approval on noncompleted workflow state")
		}
		return nil
	}
	if !approval.Valid {
		return errors.New("completed workflow state lacks approval")
	}
	var version int64
	err := c.QueryRowContext(ctx, `SELECT p.policy_version FROM approval p JOIN actor a ON a.incarnation=p.incarnation AND a.actor_id=p.actor_id JOIN entity e ON e.incarnation=p.incarnation AND e.kind='approval' AND e.entity_id=p.approval_id AND e.card_id=p.card_id WHERE p.incarnation=? AND p.card_id=? AND p.approval_id=? AND p.reviewed_card_revision=? AND a.kind='human' AND e.removed_at IS NULL`, id.Incarnation, card, approval.String, revision-1).Scan(&version)
	if err != nil || version < 1 || version > limit || version > int64(len(policies)) {
		return errors.New("workflow approval does not match reviewed state")
	}
	issuing := policies[version-1].Workflow
	if !A.WorkflowAllowsRight(issuing, "human", "approve") || A.WorkflowRole(issuing, A.Lane(lane)) != "completed" {
		return errors.New("workflow approval policy mismatch")
	}
	return nil
}

// ValidateSnapshotPolicyPin checks the full-read/export boundary without
// interpreting an old opaque JSON field as newly written policy metadata.
func ValidateSnapshotPolicyPin(raw []byte, pin sql.NullInt64) error {
	if _, err := contract.DecodeJSON(raw); err != nil {
		return errors.New("invalid snapshot JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return errors.New("invalid snapshot JSON")
	}
	encoded, has := fields["workflowPolicyVersion"]
	if !pin.Valid {
		if has {
			return errors.New("legacy snapshot has opaque policy field")
		}
		return nil
	}
	if !has || pin.Int64 < 1 || pin.Int64 > MaxInteger {
		return errors.New("snapshot policy metadata mismatch")
	}
	var version uint64
	if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) || json.Unmarshal(encoded, &version) != nil || version != uint64(pin.Int64) {
		return errors.New("snapshot policy metadata mismatch")
	}
	return nil
}
