// Frozen physical-eight validation. Keep independent of current policy semantics.
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

// ReadWorkflow returns nil for the exact default profile. It never fabricates a
// definition, accepts JSON null, or trusts a row's scalar metadata alone.
func ReadWorkflowV8(ctx context.Context, c *sql.Conn, id Identity) (*A.Workflow, error) {
	return readWorkflowDefinition(ctx, c, id, true)
}

func readWorkflowDefinition(ctx context.Context, c *sql.Conn, id Identity, frozen bool) (*A.Workflow, error) {
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
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(definition), &raw) != nil || raw["profile"] != nil || frozen && raw["attention"] != nil {
		return nil, errors.New("invalid frozen workflow profile")
	}
	w, err := A.DecodeWorkflow([]byte(definition))
	if err != nil || w == nil || w.Revision != 1 || string(w.WorkflowID) != workflowID || int(w.Revision) != revision {
		return nil, errors.New("invalid stored workflow definition")
	}
	canonical, err := A.WorkflowBytes(w)
	if err != nil || !bytes.Equal(canonical, []byte(definition)) {
		return nil, errors.New("noncanonical stored workflow definition")
	}
	return w, nil
}

func validateWorkflowV8(ctx context.Context, c *sql.Conn, id Identity) error {
	w, err := ReadWorkflowV8(ctx, c, id)
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
			if err = validateWorkflowApprovalV8(ctx, c, id, w, s.card, s.lane, s.revision, s.approval); err != nil {
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
			var fields map[string]json.RawMessage
			if json.Unmarshal(h.state, &fields) != nil || fields["workflowPolicyVersion"] != nil {
				return errors.New("invalid frozen workflow snapshot")
			}
			s, e := A.Decode[A.SnapshotState](h.state)
			if e != nil || A.WorkflowRole(w, s.Lane) == "" {
				return errors.New("stored snapshot outside configured workflow")
			}
			approval := sql.NullString{}
			if s.CurrentApprovalID != nil {
				approval = sql.NullString{String: string(*s.CurrentApprovalID), Valid: true}
			}
			if e = validateWorkflowApprovalV8(ctx, c, id, w, h.card, string(s.Lane), h.revision, approval); e != nil {
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

func validateWorkflowApprovalV8(ctx context.Context, c *sql.Conn, id Identity, w *A.Workflow, card, lane string, revision int64, approval sql.NullString) error {
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

func StatementsV8() []string { return frozenStatements(frozenEight) }
