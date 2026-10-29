//go:build windows && amd64

package cards

import (
	"database/sql"
	"testing"
)

// A repeated comment is new intent; an unchanged field is not. Exercise that
// distinction after ordinary edits, including preservation of omitted fields.
func TestDailyFieldEditsAndDeliberateRepeatedComment(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	title := "Dispatch café 船 🚲"
	description := "Reviewed <script> & café\nSecond line"
	rev := uint64(1)
	for _, input := range []map[string]any{
		{"title": title}, {"description": description}, {"ownerId": actor},
		{"priority": "P2"}, {"priority": "P3"},
	} {
		submit(t, e, s, intent(t, "card.edit", id, rev, rev, input), id, "committed")
		rev++
	}
	checkCurrent := func(wantDescription string) {
		t.Helper()
		l, err := db.Reader(testContext(t))
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		err = l.Do(testContext(t), func(c *sql.Conn) error {
			var gotID, gotTitle, gotDescription, priority, lane, owner string
			var ticket, cardRevision uint64
			err := c.QueryRowContext(testContext(t), `SELECT c.card_id,t.ticket,c.revision,c.title,c.description,c.priority,c.lane,c.owner_id FROM card c JOIN ticket t USING(incarnation,card_id)`).Scan(&gotID, &ticket, &cardRevision, &gotTitle, &gotDescription, &priority, &lane, &owner)
			if err != nil {
				return err
			}
			if gotID != id || ticket != 1 || cardRevision != rev || gotTitle != title || gotDescription != wantDescription || priority != "P3" || lane != "Ready for Work" || owner != actor {
				t.Fatal("daily edit changed identity, omitted field, or exact prose")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	checkCurrent(description)
	submit(t, e, s, intent(t, "card.edit", id, rev, rev, map[string]any{"description": ""}), id, "committed")
	rev++
	checkCurrent("")
	for n := 0; n < 2; n++ {
		i := intent(t, "comment.add", id, rev, rev, map[string]any{"text": description})
		submit(t, e, s, i, id, "committed")
		// Looking up/submitting the identical immutable original cannot append again.
		submit(t, e, s, i, id, "committed")
		rev++
	}
	checkCurrent("")
	if value(t, db, "SELECT count(*) FROM comment") != 2 || value(t, db, "SELECT count(DISTINCT comment_id) FROM comment") != 2 || value(t, db, "SELECT count(*) FROM comment WHERE actor_id='"+actor+"' AND text='Reviewed <script> & café'||char(10)||'Second line'") != 2 || value(t, db, "SELECT count(*) FROM card_revision") != int64(rev-1) || value(t, db, "SELECT count(*) FROM activity") != int64(rev) || value(t, db, "SELECT revision FROM board") != int64(rev) || value(t, db, "SELECT next_ticket FROM board") != 2 {
		t.Fatal("equal new comments did not append exactly once per operation")
	}
}
