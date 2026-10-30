//go:build windows && amd64

package cards

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
)

func parentRef(id string) map[string]string {
	return map[string]string{"boardId": board, "incarnation": inc, "cardId": id}
}
func TestHierarchyAtomicChildHistoryNoopAndCompleted(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	parent := create(t, e, s, 0)
	result := submit(t, e, s, intent(t, "card.create", "", 1, 0, map[string]any{"title": "Child", "description": "", "parent": parentRef(parent)}), "", "committed")
	var created struct {
		CardID string `json:"cardId"`
	}
	json.Unmarshal(*result.Terminal.Result, &created)
	child := created.CardID
	if value(t, db, "SELECT count(*) FROM card_parent WHERE child_id='"+child+"' AND parent_id='"+parent+"'") != 1 {
		t.Fatal("child edge missing")
	}
	for _, p := range []any{parentRef(parent)} {
		r := submit(t, e, s, intent(t, "card.edit", child, 2, 1, map[string]any{"parent": p}), child, "not_committed")
		if r.Terminal.Code != "NO_CHANGE" {
			t.Fatal("equal parent changed")
		}
	}
	r := submit(t, e, s, intent(t, "card.edit", parent, 2, 1, map[string]any{"parent": parentRef(child)}), parent, "not_committed")
	if r.Terminal.Code != "INVALID_INPUT" {
		t.Fatal("cycle accepted")
	}
	bad := parentRef(parent)
	bad["incarnation"] = "90000000-0000-0000-0000-000000000001"
	r = submit(t, e, s, intent(t, "card.create", "", 2, 0, map[string]any{"title": "Refused", "description": "", "parent": bad}), "", "not_committed")
	if r.Terminal.Code != "INVALID_INPUT" || value(t, db, "SELECT next_ticket FROM board") != 3 {
		t.Fatal("refused create consumed ticket")
	}
	submit(t, e, s, intent(t, "card.edit", child, 2, 1, map[string]any{"parent": nil}), child, "committed")
	if value(t, db, "SELECT count(*) FROM card_parent") != 0 || value(t, db, "SELECT count(*) FROM card_revision WHERE card_id='"+child+"' AND json_extract(state,'$.parent.cardId')='"+parent+"'") != 1 || value(t, db, "SELECT revision FROM card WHERE card_id='"+parent+"'") != 1 {
		t.Fatal("child-only snapshot/revision")
	}
	raw(t, db, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "UPDATE card SET lane='Completed',archived=1 WHERE card_id=?", parent)
		return err
	})
	submit(t, e, s, intent(t, "card.edit", child, 3, 2, map[string]any{"parent": parentRef(parent)}), child, "committed")
	r = submit(t, e, s, intent(t, "card.edit", parent, 4, 1, map[string]any{"parent": nil}), parent, "not_committed")
	if r.Terminal.Code != "COMPLETED" {
		t.Fatal("completed clear-root reached no-change")
	}
	if value(t, db, "SELECT count(*) FROM activity") != 4 || value(t, db, "SELECT revision FROM card WHERE card_id='"+parent+"'") != 1 {
		t.Fatal("parent activity/revision advanced")
	}
}

// Controlled larger fixtures exercise the actual sole-writer admission rather
// than treating the pure forest predicate as a transaction acceptance proof.
func TestHierarchyWholeFamilyAndMovedSubtreeBounds(t *testing.T) {
	for _, kind := range []string{"family", "depth"} {
		t.Run(kind, func(t *testing.T) {
			e, db := fixture(t)
			service := service(t, db)
			child := create(t, e, service, 0)
			count := 1000
			if kind == "depth" {
				count = 65
			}
			target := fmt.Sprintf("70000000-0000-4000-8000-%012d", 1)
			raw(t, db, func(c *sql.Conn) error {
				if _, err := c.ExecContext(testContext(t), "BEGIN IMMEDIATE"); err != nil {
					return err
				}
				if _, err := c.ExecContext(testContext(t), "UPDATE board SET next_ticket=?", count+2); err != nil {
					return err
				}
				for n := 1; n <= count; n++ {
					id := fmt.Sprintf("70000000-0000-4000-8000-%012d", n)
					for _, q := range []struct {
						sql  string
						args []any
					}{
						{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'card',?,?,?,?)", []any{inc, id, id, board, inc}},
						{"INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,?,?)", []any{inc, n + 1, id}},
						{"INSERT INTO card(incarnation,card_id,revision,title,description,priority,lane,archived) VALUES(?,?,1,'Bound fixture','','P3','Completed',1)", []any{inc, id}},
					} {
						if _, err := c.ExecContext(testContext(t), q.sql, q.args...); err != nil {
							return err
						}
					}
					parent := ""
					if kind == "family" && n > 1 {
						parent = target
					}
					if kind == "depth" && n <= 64 {
						parent = child
						if n > 1 {
							parent = fmt.Sprintf("70000000-0000-4000-8000-%012d", n-1)
						}
					}
					if parent != "" {
						if _, err := c.ExecContext(testContext(t), "INSERT INTO card_parent VALUES(?,?,?)", inc, id, parent); err != nil {
							return err
						}
					}
				}
				if kind == "depth" {
					target = fmt.Sprintf("70000000-0000-4000-8000-%012d", 65)
				}
				if _, err := c.ExecContext(testContext(t), "UPDATE board SET next_ticket=?", count+2); err != nil {
					return err
				}
				_, err := c.ExecContext(testContext(t), "COMMIT")
				return err
			})
			result := submit(t, e, service, intent(t, "card.edit", child, 1, 1, map[string]any{"parent": parentRef(target)}), child, "not_committed")
			if result.Terminal.Code != "INVALID_INPUT" || value(t, db, "SELECT revision FROM board") != 1 || value(t, db, "SELECT count(*) FROM card_revision") != 0 || value(t, db, "SELECT count(*) FROM activity") != 1 {
				t.Fatal("over-bound proposal changed state")
			}
			if kind == "depth" {
				raw(t, db, func(c *sql.Conn) error {
					_, err := c.ExecContext(testContext(t), "DELETE FROM card_parent WHERE child_id=?", fmt.Sprintf("70000000-0000-4000-8000-%012d", 64))
					return err
				})
				submit(t, e, service, intent(t, "card.edit", child, 1, 1, map[string]any{"parent": parentRef(target)}), child, "committed")
				if value(t, db, "SELECT revision FROM card WHERE card_id='"+child+"'") != 2 {
					t.Fatal("depth64 boundary refused")
				}
			}
		})
	}
}
