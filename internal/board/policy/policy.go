// Package policy resolves retained workflow policy in the caller's snapshot.
package policy

import (
	"bytes"
	"database/sql"
	"errors"
	"github.com/localswim/localswim/internal/store/writer"

	A "github.com/localswim/localswim/internal/contract/archive"
)

// Reader is the narrow snapshot interface shared by domain and native readers.
type Reader interface {
	Scan(string, []any, ...any) error
}

// Load returns nil only for the exact legacy profile. Corrupt or unavailable
// configured policy fails closed; no default definition is synthesized.
func Load(r Reader, incarnation string) (*A.Workflow, error) {
	var id string
	var revision uint64
	var raw string
	err := r.Scan("SELECT coalesce(w.workflow_id,''),p.policy_version,coalesce(w.definition,'') FROM policy p JOIN workflow_policy w ON w.incarnation=p.incarnation AND w.policy_version=p.policy_version WHERE p.incarnation=?", []any{incarnation}, &id, &revision, &raw)
	if err != nil {
		return nil, err
	}
	if id == "" && raw == "" && revision == 1 {
		return nil, nil
	}
	definition, err := decodeStored(raw)
	if err != nil {
		return nil, err
	}
	if err = A.ValidateWorkflow(definition); err != nil {
		return nil, err
	}
	if string(definition.WorkflowID) != id || uint64(definition.Revision) != revision {
		return nil, errors.New("workflow identity mismatch")
	}
	return definition, nil
}

// AllowsRights applies the ordinary-right ceiling without changing native
// administration's separate authority boundary.
func AllowsRights(w *A.Workflow, kind string, rights ...string) bool {
	for _, right := range rights {
		switch right {
		case "attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff", "approve":
			if !A.WorkflowAllowsRight(w, A.Kind(kind), right) {
				return false
			}
		}
	}
	return true
}

// At loads an exact retained issuing policy. Missing versions fail closed.
func At(r Reader, incarnation string, version uint64) (*A.Workflow, error) {
	var id, raw string
	var stored uint64
	err := r.Scan("SELECT coalesce(workflow_id,''),policy_version,coalesce(definition,'') FROM workflow_policy WHERE incarnation=? AND policy_version=?", []any{incarnation, version}, &id, &stored, &raw)
	if err != nil {
		return nil, err
	}
	if stored != version {
		return nil, errors.New("policy version mismatch")
	}
	if version == 1 && id == "" && raw == "" {
		return nil, nil
	}
	w, err := decodeStored(raw)
	if err != nil {
		return nil, err
	}
	if uint64(w.Revision) != version || string(w.WorkflowID) != id {
		return nil, errors.New("policy identity mismatch")
	}
	return w, nil
}

// LoadHistory returns all retained records in their immutable version order.
func LoadHistory(r writer.Read, incarnation string) ([]A.WorkflowPolicy, error) {
	rows, err := r.Query("SELECT policy_version,coalesce(workflow_id,''),coalesce(definition,'') FROM workflow_policy WHERE incarnation=? ORDER BY policy_version", incarnation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []A.WorkflowPolicy{}
	for rows.Next() {
		var version uint64
		var raw, id string
		if err = rows.Scan(&version, &id, &raw); err != nil {
			return nil, err
		}
		if version != uint64(len(records)+1) {
			return nil, errors.New("policy history gap")
		}
		record := A.WorkflowPolicy{PolicyVersion: A.PolicyVersion(version)}
		if raw != "" {
			if record.Workflow, err = decodeStored(raw); err != nil {
				return nil, err
			}
			if record.Workflow == nil || A.ValidateWorkflow(record.Workflow) != nil || uint64(record.Workflow.Revision) != version || string(record.Workflow.WorkflowID) != id {
				return nil, errors.New("invalid retained policy")
			}
		} else if version != 1 || id != "" {
			return nil, errors.New("missing retained definition")
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("policy history unavailable")
	}
	last := records[len(records)-1]
	publicHistory := records
	if len(records) == 1 {
		publicHistory = nil
	}
	if err = A.ValidateWorkflowPolicies(last.PolicyVersion, last.Workflow, publicHistory); err != nil {
		return nil, err
	}
	return records, nil
}

func decodeStored(raw string) (*A.Workflow, error) {
	w, err := A.DecodeWorkflow([]byte(raw))
	if err != nil {
		return nil, err
	}
	canonical, err := A.WorkflowBytes(w)
	if err != nil || !bytes.Equal(canonical, []byte(raw)) {
		return nil, errors.New("noncanonical retained workflow")
	}
	return w, nil
}

// PinCurrent records the policy of a newly committed card revision only.
func PinCurrent(w writer.Write, incarnation, cardID string) error {
	_, err := w.Exec("UPDATE card SET workflow_policy_version=(SELECT CASE WHEN policy_version=1 THEN NULL ELSE policy_version END FROM policy WHERE incarnation=?) WHERE incarnation=? AND card_id=?", incarnation, incarnation, cardID)
	return err
}

// CurrentApprovalValid establishes current completion from retained issuer facts.
func CurrentApprovalValid(r writer.Read, incarnation, cardID string, current *A.Workflow) (bool, error) {
	var lane, kind string
	var revision, reviewed, version uint64
	err := r.Scan("SELECT c.lane,c.revision,a.reviewed_card_revision,a.policy_version,h.kind FROM card c JOIN approval a ON a.incarnation=c.incarnation AND a.approval_id=c.current_approval_id AND a.card_id=c.card_id JOIN actor h ON h.incarnation=a.incarnation AND h.actor_id=a.actor_id JOIN entity e ON e.incarnation=a.incarnation AND e.kind='approval' AND e.entity_id=a.approval_id AND e.card_id=a.card_id AND e.removed_at IS NULL WHERE c.incarnation=? AND c.card_id=?", []any{incarnation, cardID}, &lane, &revision, &reviewed, &version, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	limit := uint64(1)
	if current != nil {
		limit = uint64(current.Revision)
	}
	if version < 1 || version > limit || A.WorkflowRole(current, A.Lane(lane)) != "completed" || kind != "human" || revision < 2 || reviewed != revision-1 {
		return false, nil
	}
	issuer, err := At(r, incarnation, version)
	if err != nil {
		return false, err
	}
	if A.WorkflowLaneForRole(issuer, "completed") != A.Lane(lane) || !A.WorkflowAllowsRight(issuer, "human", "approve") || !A.WorkflowAllows(issuer, A.WorkflowLaneForRole(issuer, "review"), A.WorkflowLaneForRole(issuer, "completed"), "human") {
		return false, nil
	}
	return true, nil
}

// ValidateCurrent applies the strict current invariant, including implicit pin1.
func ValidateCurrent(r writer.Read, incarnation string, current *A.Workflow) error {
	rows, err := r.Query("SELECT card_id,lane,current_approval_id,workflow_policy_version FROM card WHERE incarnation=?", incarnation)
	if err != nil {
		return err
	}
	type state struct {
		id, lane string
		approval sql.NullString
		pin      sql.NullInt64
	}
	var states []state
	for rows.Next() {
		var s state
		if err = rows.Scan(&s.id, &s.lane, &s.approval, &s.pin); err != nil {
			rows.Close()
			return err
		}
		states = append(states, s)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	for _, s := range states {
		version, limit := uint64(1), uint64(1)
		if s.pin.Valid {
			if s.pin.Int64 < 1 {
				return errors.New("invalid current policy pin")
			}
			version = uint64(s.pin.Int64)
		}
		if current != nil {
			limit = uint64(current.Revision)
		}
		if version > limit {
			return errors.New("future current policy pin")
		}
		pinned, err := At(r, incarnation, version)
		if err != nil {
			return err
		}
		if A.WorkflowRole(pinned, A.Lane(s.lane)) == "" {
			return errors.New("current lane outside state policy")
		}
		role := A.WorkflowRole(current, A.Lane(s.lane))
		if role == "" || role != "completed" && s.approval.Valid {
			return errors.New("incompatible current workflow state")
		}
		if role == "completed" {
			valid, err := CurrentApprovalValid(r, incarnation, s.id, current)
			if err != nil {
				return err
			}
			if !valid {
				return errors.New("incompatible current approval")
			}
		}
	}
	return nil
}
