//go:build windows && amd64

package cards

import (
	"database/sql"
	"testing"
)

func TestArchiveRoundTripAndAdmission(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	submit(t, e, s, intent(t, "comment.add", id, 1, 1, map[string]any{"text": "retained exact comment"}), id, "committed")
	archive := intent(t, "card.edit", id, 2, 2, map[string]any{"archived": true})
	submit(t, e, s, archive, id, "committed")
	submit(t, e, s, archive, id, "committed")
	if value(t, db, "SELECT archived FROM card") != 1 || value(t, db, "SELECT revision FROM board") != 3 || value(t, db, "SELECT count(*) FROM activity") != 3 || value(t, db, "SELECT count(*) FROM card_revision WHERE json_extract(state,'$.archived')=0") != 2 {
		t.Fatal("archive did not commit once with prior snapshot")
	}
	no := submit(t, e, s, intent(t, "card.edit", id, 3, 3, map[string]any{"archived": true}), id, "not_committed")
	if no.Terminal.Code != "NO_CHANGE" {
		t.Fatal("equal flag not no change")
	}
	stale := submit(t, e, s, intent(t, "card.edit", id, 2, 2, map[string]any{"archived": true}), id, "not_committed")
	if stale.Terminal.Code != "REVISION_CONFLICT" {
		t.Fatal("stale flag bypassed admission")
	}
	// Existing unfinished-card edits and comments retain their existing authority.
	submit(t, e, s, intent(t, "comment.add", id, 3, 3, map[string]any{"text": "archived comment"}), id, "committed")
	submit(t, e, s, intent(t, "card.edit", id, 4, 4, map[string]any{"archived": false}), id, "committed")
	if value(t, db, "SELECT count(*) FROM card WHERE archived=0 AND lane='Ready for Work' AND priority='P3' AND title=' 雪🌊 ' AND description='A'||char(13)||char(10)||'B'||char(10)||' café'") != 1 || value(t, db, "SELECT count(*) FROM comment") != 2 || value(t, db, "SELECT next_ticket FROM board") != 2 {
		t.Fatal("unarchive changed conserved card facts")
	}
	raw(t, db, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "UPDATE card SET lane='Completed'")
		return err
	})
	completed := submit(t, e, s, intent(t, "card.edit", id, 5, 5, map[string]any{"archived": false}), id, "not_committed")
	if completed.Terminal.Code != "COMPLETED" {
		t.Fatal("completed no-op bypassed terminal rule")
	}
	if value(t, db, "SELECT revision FROM board") != 5 || value(t, db, "SELECT count(*) FROM activity") != 5 {
		t.Fatal("refusal changed revision/activity")
	}
}
