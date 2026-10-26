//go:build windows && amd64

package cards

import (
	"context"
	"database/sql"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

type workflowTestAccess struct {
	ctx context.Context
	c   *sql.Conn
}

func (a workflowTestAccess) Scan(q string, args []any, dest ...any) error {
	return a.c.QueryRowContext(a.ctx, q, args...).Scan(dest...)
}
func (a workflowTestAccess) Query(q string, args ...any) (*sql.Rows, error) {
	return a.c.QueryContext(a.ctx, q, args...)
}
func (a workflowTestAccess) Exec(q string, args ...any) (sql.Result, error) {
	return a.c.ExecContext(a.ctx, q, args...)
}

func TestWorkflowSnapshotPins(t *testing.T) {
	e, db := fixture(t)
	id := create(t, e, service(t, db), 0)
	const approval = "80000000-0000-0000-0000-000000000001"
	const attachment = "90000000-0000-0000-0000-000000000001"
	const digest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	raw(t, db, func(c *sql.Conn) error {
		a := workflowTestAccess{testContext(t), c}
		for _, q := range []struct {
			q string
			a []any
		}{
			{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", []any{inc, approval, id, board, inc}},
			{"INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", []any{inc, approval, id, actor}},
			{"UPDATE card SET current_approval_id=? WHERE card_id=?", []any{approval, id}},
			{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'attachment',?,?,?,?)", []any{inc, attachment, id, board, inc}},
			{"INSERT INTO blob VALUES(?,?,0,?)", []any{inc, digest, []byte{}}},
			{"INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'Synthetic','text/plain',?,0,'2026-10-26T00:00:00Z')", []any{inc, attachment, id, digest}},
			{"INSERT INTO current_reference VALUES(?,?,'attachment',?)", []any{inc, id, attachment}},
			{"INSERT INTO current_attachment_pin VALUES(?,?,?,0)", []any{inc, attachment, digest}},
		} {
			if _, err := a.Exec(q.q, q.a...); err != nil {
				return err
			}
		}
		if _, err := c.ExecContext(a.ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		state, err := LoadSnapshot(a, inc, id)
		if err != nil {
			return err
		}
		if state.CurrentApprovalID == nil || *state.CurrentApprovalID != A.UUID(approval) {
			t.Fatal("prior approval missing")
		}
		if _, err = a.Exec("UPDATE card SET revision=2,current_approval_id=NULL WHERE card_id=?", id); err != nil {
			return err
		}
		if err = WriteSnapshot(a, inc, id, 1, state); err != nil {
			return err
		}
		if _, err = a.Exec("DELETE FROM current_reference WHERE card_id=?", id); err != nil {
			return err
		}
		_, err = c.ExecContext(a.ctx, "COMMIT")
		return err
	})
	if value(t, db, "SELECT count(*) FROM card_revision WHERE origin_board='"+board+"' AND origin_incarnation='"+inc+"' AND actor_id='"+actor+"' AND card_revision=1 AND json_extract(state,'$.currentApprovalId')='"+approval+"'") != 1 {
		t.Fatal("prior provenance/state changed")
	}
	if value(t, db, "SELECT count(*) FROM revision_reference WHERE kind='attachment' AND entity_id='"+attachment+"'") != 1 || value(t, db, "SELECT count(*) FROM revision_reference WHERE kind='approval' AND entity_id='"+approval+"'") != 1 || value(t, db, "SELECT count(*) FROM history_blob_pin WHERE attachment_id='"+attachment+"' AND sha256='"+digest+"' AND byte_length=0") != 1 {
		t.Fatal("prior refs/pins changed")
	}
}
