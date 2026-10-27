//go:build windows && amd64

package cards

import (
	"database/sql"
	"encoding/json"
	S "github.com/localswim/localswim/internal/store/open"
	"path/filepath"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestAssignmentDurableHistoryAndAdmission(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	if value(t, db, "SELECT count(*) FROM card WHERE owner_id IS NULL") != 1 {
		t.Fatal("implicit assignee")
	}
	raw(t, db, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES(?,?,'automation','Synthetic assignee',?,?)", inc, grantee, board, inc)
		return err
	})
	submit(t, e, s, intent(t, "card.edit", id, 1, 1, map[string]any{"ownerId": grantee}), id, "committed")
	if value(t, db, "SELECT count(*) FROM card WHERE owner_id='"+grantee+"' AND revision=2 AND current_approval_id IS NULL") != 1 {
		t.Fatal("assignment not stored")
	}
	r := submit(t, e, s, intent(t, "card.edit", id, 2, 2, map[string]any{"ownerId": grantee}), id, "not_committed")
	if r.Terminal.Code != "NO_CHANGE" {
		t.Fatal("assignment no-change")
	}
	r = submit(t, e, s, intent(t, "card.edit", id, 2, 2, map[string]any{"ownerId": "90000000-0000-0000-0000-000000000001"}), id, "not_committed")
	if r.Terminal.Code != "INVALID_INPUT" {
		t.Fatal("unknown assignee accepted")
	}
	submit(t, e, s, intent(t, "card.edit", id, 2, 2, map[string]any{"title": "Retain owner"}), id, "committed")
	if value(t, db, "SELECT count(*) FROM card WHERE owner_id='"+grantee+"' AND revision=3") != 1 {
		t.Fatal("omission clears owner")
	}
	submit(t, e, s, intent(t, "card.edit", id, 3, 3, map[string]any{"ownerId": nil}), id, "committed")
	r = submit(t, e, s, intent(t, "card.edit", id, 4, 4, map[string]any{"ownerId": nil}), id, "not_committed")
	if r.Terminal.Code != "NO_CHANGE" {
		t.Fatal("null no-change")
	}
	if value(t, db, "SELECT count(*) FROM card WHERE owner_id IS NULL AND revision=4") != 1 {
		t.Fatal("clear not stored")
	}
	l, err := db.Reader(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	err = l.Do(testContext(t), func(c *sql.Conn) error {
		rs, err := c.QueryContext(testContext(t), "SELECT card_revision,state FROM card_revision ORDER BY card_revision")
		if err != nil {
			return err
		}
		defer rs.Close()
		count := 0
		for rs.Next() {
			var rev int
			var raw string
			if err = rs.Scan(&rev, &raw); err != nil {
				return err
			}
			var state A.SnapshotState
			if err = json.Unmarshal([]byte(raw), &state); err != nil {
				return err
			}
			if (rev == 1) != (state.OwnerID == nil) {
				t.Fatal("history loses owner")
			}
			if state.OwnerID != nil && string(*state.OwnerID) != grantee {
				t.Fatal("history wrong owner")
			}
			count++
		}
		if count != 3 {
			t.Fatal("history missing")
		}
		return rs.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if value(t, db, "SELECT count(*) FROM activity WHERE actor_id='"+actor+"'") != 4 || value(t, db, "SELECT count(*) FROM grant_record WHERE actor_id='"+grantee+"'") != 0 {
		t.Fatal("assignment changed authority or audit actor")
	}
}

func TestAssignmentCreateAndHistorySurviveReopen(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	r := submit(t, e, s, intent(t, "card.create", "", 0, 0, map[string]any{"title": "Synthetic assigned", "description": "", "ownerId": actor}), "", "committed")
	var result struct {
		CardID string `json:"cardId"`
	}
	json.Unmarshal(*r.Terminal.Result, &result)
	id := result.CardID
	approval := "80000000-0000-0000-0000-000000000001"
	var path string
	raw(t, db, func(c *sql.Conn) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", []any{inc, approval, id, board, inc}},
			{"INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", []any{inc, approval, id, actor}},
			{"UPDATE card SET current_approval_id=? WHERE card_id=?", []any{approval, id}},
		} {
			if _, err := c.ExecContext(testContext(t), q.sql, q.args...); err != nil {
				return err
			}
		}
		var seq int
		var name string
		return c.QueryRowContext(testContext(t), "PRAGMA database_list").Scan(&seq, &name, &path)
	})
	submit(t, e, s, intent(t, "card.edit", id, 1, 1, map[string]any{"ownerId": nil}), id, "committed")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := S.Existing(testContext(t), filepath.Dir(path), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if value(t, reopened, "SELECT count(*) FROM card WHERE owner_id IS NULL AND revision=2 AND current_approval_id IS NULL") != 1 || value(t, reopened, "SELECT count(*) FROM card_revision WHERE json_extract(state,'$.ownerId')='"+actor+"' AND json_extract(state,'$.currentApprovalId')='"+approval+"'") != 1 || value(t, reopened, "SELECT count(*) FROM revision_reference WHERE kind='approval'") != 1 {
		t.Fatal("assignment/approval history lost after reopen")
	}
}

func TestAssignmentDoesNotBypassAdmission(t *testing.T) {
	for _, mode := range []string{"stale", "completed", "permission"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			s := service(t, db)
			id := create(t, e, s, 0)
			raw(t, db, func(c *sql.Conn) error {
				q := "UPDATE board SET revision=2"
				if mode == "completed" {
					q = "UPDATE card SET lane='Completed'"
				}
				if mode == "permission" {
					q = "DELETE FROM grant_right WHERE right_name='edit'"
				}
				_, err := c.ExecContext(testContext(t), q)
				return err
			})
			r := submit(t, e, s, intent(t, "card.edit", id, 1, 1, map[string]any{"ownerId": actor}), id, "not_committed")
			want := map[string]string{"stale": "REVISION_CONFLICT", "completed": "COMPLETED", "permission": "FORBIDDEN"}[mode]
			if r.Terminal.Code != want || value(t, db, "SELECT count(*) FROM card WHERE owner_id IS NULL AND revision=1") != 1 || value(t, db, "SELECT count(*) FROM card_revision") != 0 {
				t.Fatal("assignment bypassed admission")
			}
		})
	}
}
