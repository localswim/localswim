package schema

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

// WorkflowStatements is the additive immutable definition inventory in physical8.
func WorkflowStatements() []string {
	return []string{expand(`CREATE TABLE workflow_definition (
 incarnation TEXT PRIMARY KEY REFERENCES board(incarnation),
 workflow_id TEXT NOT NULL CHECK({{u:workflow_id}}),
 revision INTEGER NOT NULL CHECK(revision=1),
 definition TEXT NOT NULL CHECK(json_valid(definition) AND length(CAST(definition AS BLOB))<=65536)
) STRICT`),
		`CREATE TRIGGER workflow_definition_immutable BEFORE UPDATE ON workflow_definition BEGIN SELECT RAISE(ABORT,'workflow definition immutable'); END`,
		`CREATE TRIGGER workflow_definition_retained BEFORE DELETE ON workflow_definition BEGIN SELECT RAISE(ABORT,'workflow definition retained'); END`}
}

// CardStatements returns the precise card objects rebuilt by explicit7->8.
func CardStatements() []string {
	var out []string
	for _, q := range Statements() {
		if strings.HasPrefix(q, "CREATE TABLE card (") || strings.HasPrefix(q, "CREATE TRIGGER ") && strings.Contains(q, " ON card BEGIN ") {
			out = append(out, q)
		}
	}
	return out
}

// ReadWorkflow returns nil for the exact default profile. It never fabricates a
// definition, accepts JSON null, or trusts a row's scalar metadata alone.
func ReadWorkflow(ctx context.Context, c *sql.Conn, id Identity) (*A.Workflow, error) {
	var boardID, incarnation string
	if err := c.QueryRowContext(ctx, "SELECT board_id,incarnation FROM board WHERE singleton=1").Scan(&boardID, &incarnation); err != nil || boardID != id.BoardID || incarnation != id.Incarnation {
		return nil, errors.New("workflow storage identity mismatch")
	}
	var workflowID, definition string
	var revision int
	err := c.QueryRowContext(ctx, `SELECT workflow_id,revision,definition FROM workflow_definition WHERE incarnation=?`, id.Incarnation).Scan(&workflowID, &revision, &definition)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w, err := A.DecodeWorkflow([]byte(definition))
	if err != nil || w == nil || string(w.WorkflowID) != workflowID || int(w.Revision) != revision {
		return nil, errors.New("invalid stored workflow definition")
	}
	canonical, err := A.WorkflowBytes(w)
	if err != nil || !bytes.Equal(canonical, []byte(definition)) {
		return nil, errors.New("noncanonical stored workflow definition")
	}
	return w, nil
}

func validateWorkflow(ctx context.Context, c *sql.Conn, id Identity) error {
	w, err := ReadWorkflow(ctx, c, id)
	if err != nil {
		return err
	}
	rows, err := c.QueryContext(ctx, `SELECT card_id,lane,revision,current_approval_id FROM card WHERE incarnation=?`, id.Incarnation)
	if err != nil {
		return err
	}
	type cardState struct {
		card, lane string
		revision   int64
		approval   sql.NullString
	}
	var cards []cardState
	for rows.Next() {
		var s cardState
		if err = rows.Scan(&s.card, &s.lane, &s.revision, &s.approval); err != nil {
			rows.Close()
			return err
		}
		cards = append(cards, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, s := range cards {
		if A.WorkflowRole(w, A.Lane(s.lane)) == "" {
			return errors.New("stored card lane outside workflow")
		}
		if w != nil {
			if err = validateWorkflowApproval(ctx, c, id, w, s.card, s.lane, s.revision, s.approval); err != nil {
				return err
			}
		}
	}
	rows, err = c.QueryContext(ctx, `SELECT card_id,card_revision,state FROM card_revision WHERE incarnation=? AND state IS NOT NULL`, id.Incarnation)
	if err != nil {
		return err
	}
	type historyState struct {
		card     string
		revision int64
		state    []byte
	}
	var history []historyState
	for rows.Next() {
		var s historyState
		if err = rows.Scan(&s.card, &s.revision, &s.state); err != nil {
			rows.Close()
			return err
		}
		history = append(history, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, h := range history {
		if w != nil {
			s, e := A.Decode[A.SnapshotState](h.state)
			if e != nil || A.WorkflowRole(w, s.Lane) == "" {
				return errors.New("stored snapshot outside configured workflow")
			}
			approval := sql.NullString{}
			if s.CurrentApprovalID != nil {
				approval = sql.NullString{String: string(*s.CurrentApprovalID), Valid: true}
			}
			if e = validateWorkflowApproval(ctx, c, id, w, h.card, string(s.Lane), h.revision, approval); e != nil {
				return e
			}
		} else {
			// Accepted legacy storage allowed opaque JSON snapshots. Preserve absent-lane
			// bytes; a present lane is still interpreted only under the default profile.
			var fields map[string]json.RawMessage
			if json.Unmarshal(h.state, &fields) != nil {
				continue
			}
			raw, has := fields["lane"]
			if !has {
				continue
			}
			if _, e := contract.DecodeJSON(h.state); e != nil {
				return errors.New("invalid stored snapshot lane")
			}
			var lane string
			if json.Unmarshal(raw, &lane) != nil || A.WorkflowRole(nil, A.Lane(lane)) == "" {
				return errors.New("stored snapshot lane outside default workflow")
			}
		}
	}
	if w == nil {
		return nil
	}
	// Administrative rights remain a separate native-owner boundary. Ordinary
	// rights on any stored grant must stay within the immutable actor-kind ceiling.
	rows, err = c.QueryContext(ctx, `SELECT a.kind,r.right_name FROM grant_right r JOIN grant_record g USING(incarnation,grant_id) JOIN actor a ON a.incarnation=g.incarnation AND a.actor_id=g.actor_id WHERE r.incarnation=?`, id.Incarnation)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, right string
		if err = rows.Scan(&kind, &right); err != nil {
			return err
		}
		switch right {
		case "restore", "grant-admin", "history-admin":
			if kind == "automation" && right != "restore" {
				return errors.New("invalid automation administration")
			}
		default:
			if !A.WorkflowAllowsRight(w, A.Kind(kind), right) {
				return errors.New("grant exceeds workflow ceiling")
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	var bad int
	if err = c.QueryRowContext(ctx, `SELECT count(*) FROM approval p JOIN actor a ON a.incarnation=p.incarnation AND a.actor_id=p.actor_id WHERE p.incarnation=? AND (a.kind<>'human' OR p.policy_version<>1)`, id.Incarnation).Scan(&bad); err != nil || bad != 0 {
		return errors.New("invalid stored workflow approval")
	}
	return nil
}

func validateWorkflowApproval(ctx context.Context, c *sql.Conn, id Identity, w *A.Workflow, card, lane string, revision int64, approval sql.NullString) error {
	completed := A.WorkflowRole(w, A.Lane(lane)) == "completed"
	if !completed {
		if approval.Valid {
			return errors.New("approval on noncompleted workflow state")
		}
		return nil
	}
	if !approval.Valid {
		return errors.New("completed workflow state lacks approval")
	}
	var n int
	err := c.QueryRowContext(ctx, `SELECT count(*) FROM approval p JOIN actor a ON a.incarnation=p.incarnation AND a.actor_id=p.actor_id WHERE p.incarnation=? AND p.card_id=? AND p.approval_id=? AND p.reviewed_card_revision=? AND p.policy_version=1 AND a.kind='human'`, id.Incarnation, card, approval.String, revision-1).Scan(&n)
	if err != nil || n != 1 {
		return errors.New("workflow approval does not match reviewed state")
	}
	return nil
}
