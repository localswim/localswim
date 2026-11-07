package boardapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

func newNoticeTextFixture(t *testing.T, path string) (noticeFixture, *sql.DB) {
	t.Helper()
	f, db := newNoticeFixture(t, path)
	// No card, card_revision or operation_full tables: extraction must work for
	// untouched creation and compacted operations without ever reading prose.
	f.exec(`CREATE TABLE operation(incarnation TEXT,origin_board TEXT,origin_incarnation TEXT,op_id TEXT,current_ledger INTEGER,
		command TEXT,terminal_class TEXT,code TEXT,actor_id TEXT,terminal_at TEXT,target_kind TEXT,target_id TEXT,
		expected_revision INTEGER,committed_revision INTEGER,expected_card_revision INTEGER,committed_card_revision INTEGER);
		CREATE TABLE operation_reference(incarnation TEXT,origin_board TEXT,origin_incarnation TEXT,op_id TEXT,kind TEXT,entity_id TEXT,revision INTEGER);
		CREATE TABLE ticket(incarnation TEXT,ticket INTEGER,card_id TEXT,kind TEXT);
		CREATE TABLE entity(incarnation TEXT,kind TEXT,entity_id TEXT,card_id TEXT,origin_board TEXT,origin_incarnation TEXT);
		CREATE TABLE grant_fact(incarnation TEXT,grant_id TEXT);
		CREATE TABLE workflow_policy(incarnation TEXT,workflow_id TEXT,policy_version INTEGER);`)
	return f, db
}

func noticeOpID(id int) string     { return fmt.Sprintf("70000000-0000-4000-8000-%012x", id) }
func noticeEntityID(id int) string { return fmt.Sprintf("80000000-0000-4000-8000-%012x", id) }

func addNoticeTextOperation(f noticeFixture, id int, command, card string, ticket int) {
	f.t.Helper()
	var targetKind, target, expectedCard, committedCard any
	refs := []A.FactRef{}
	revision := A.Number(id)
	kinds := noticeOperationKinds(command)
	if kinds[0] == "card" {
		cr := A.Number(2)
		targetKind, target, expectedCard, committedCard = "card", card, 1, 2
		if command == "card.create" {
			targetKind, target, expectedCard, committedCard, cr = nil, nil, nil, 1, 1
		}
		refs = append(refs, A.FactRef{Kind: "card", ID: A.UUID(card), Revision: &cr})
		f.exec(`INSERT INTO entity SELECT ?,'card',?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM entity WHERE incarnation=? AND kind='card' AND entity_id=?)`, testInc, card, card, testBoard, testInc, testInc, card)
		f.exec(`INSERT INTO ticket SELECT ?,?,?,'card' WHERE NOT EXISTS(SELECT 1 FROM ticket WHERE incarnation=? AND card_id=?)`, testInc, ticket, card, testInc, card)
		if len(kinds) == 2 {
			aux := noticeEntityID(id + 10000)
			refs = append(refs, A.FactRef{Kind: A.FactKind(kinds[1]), ID: A.UUID(aux)})
			f.exec(`INSERT INTO entity VALUES(?,?,?,?,?,?)`, testInc, kinds[1], aux, card, testBoard, testInc)
		}
	} else {
		switch kinds[0] {
		case "board":
			refs = append(refs, A.FactRef{Kind: "board", ID: testBoard, Revision: &revision})
		case "grant":
			grant := noticeEntityID(id + 10000)
			refs = append(refs, A.FactRef{Kind: "grant", ID: A.UUID(grant)})
			f.exec(`INSERT INTO grant_fact VALUES(?,?)`, testInc, grant)
			if command == "grant.revoke" {
				targetKind, target = "grant", grant
			}
		case "workflow":
			workflow := noticeEntityID(id + 10000)
			wr := A.Number(2)
			refs = append(refs, A.FactRef{Kind: "workflow", ID: A.UUID(workflow), Revision: &wr})
			f.exec(`INSERT INTO workflow_policy VALUES(?,?,2)`, testInc, workflow)
		}
	}
	f.event(testBoard, testInc, id, activityTie, command, id, testActor)
	f.exec(`UPDATE activity SET op_id=? WHERE event_id=?`, noticeOpID(id), id)
	f.exec(`INSERT INTO operation VALUES(?,?,?,?,1,?,'COMMITTED','OK',?,?,?,?,?,?,?,?)`, testInc, testBoard, testInc, noticeOpID(id), command, testActor, activityTie, targetKind, target, id-1, id, expectedCard, committedCard)
	for _, ref := range refs {
		var rev any
		if ref.Revision != nil {
			rev = uint64(*ref.Revision)
		}
		f.exec(`INSERT INTO operation_reference VALUES(?,?,?,?,?,?,?)`, testInc, testBoard, testInc, noticeOpID(id), ref.Kind, ref.ID, rev)
	}
	f.exec(`UPDATE board SET next_event=?,revision=?`, id+1, id)
}

func TestNoticeTextUntouchedCreationAndCompactedCommands(t *testing.T) {
	commands := []string{"card.create", "card.edit", "comment.add", "attachment.add", "attachment.detach", "link.add", "link.remove", "review-handoff", "approve"}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			f, _ := newNoticeTextFixture(t, ":memory:")
			addNoticeTextOperation(f, 1, command, testCard, 43)
			got, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
			if err != nil || got.Class != "human" || got.Facts == nil || !reflect.DeepEqual(got.Facts.Tickets, []A.Positive{43}) || got.Facts.Label != "Board "+testBoard {
				t.Fatalf("missing retained facts: %+v %v", got, err)
			}
		})
	}
}

func TestNoticeTextNoncardAndRecoveryEvents(t *testing.T) {
	for _, command := range []string{"grant.create", "grant.revoke", "history.clear", "retention.set", "workflow.edit", "recovery.expire"} {
		t.Run(command, func(t *testing.T) {
			f, _ := newNoticeTextFixture(t, ":memory:")
			if command == "recovery.expire" {
				f.event(testBoard, testInc, 1, activityTie, command, 1, testActor)
				raw, _ := json.Marshal([]A.FactRef{{Kind: "board", ID: testBoard, Revision: new(A.Number(1))}})
				f.exec(`UPDATE activity SET affected=?; UPDATE board SET next_event=2,revision=1`, string(raw))
			} else {
				addNoticeTextOperation(f, 1, command, "", 0)
			}
			got, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
			if err != nil || got.Facts == nil || got.Facts.Tickets == nil || len(got.Facts.Tickets) != 0 {
				t.Fatalf("proved noncard facts unavailable: %+v %v", got, err)
			}
		})
	}
}

func TestNoticeTextUnionClockRegressionAndSafeLabel(t *testing.T) {
	f, _ := newNoticeTextFixture(t, ":memory:")
	addNoticeTextOperation(f, 1, "card.create", testCard, 92)
	addNoticeTextOperation(f, 2, "card.edit", noticeEntityID(2), 4)
	addNoticeTextOperation(f, 3, "card.edit", testCard, 92)
	f.exec(`UPDATE activity SET at='2026-09-29T00:00:00Z' WHERE event_id=1; UPDATE operation SET terminal_at='2026-09-29T00:00:00Z' WHERE op_id=?`, noticeOpID(1))
	got, err := captureNoticeIntervalWithLabel(f, activityIdentity, testCard, noticeinterval.Cut{At: activityThrough}, noticeTime(t, activityFrom), "  Harbor 😀  ")
	if err != nil || got.Facts == nil || got.Facts.Label != "  Harbor 😀  " || got.Facts.From != "2026-09-29T00:00:00Z" || got.Facts.Through != activityThrough || !reflect.DeepEqual(got.Facts.Tickets, []A.Positive{4, 92}) {
		t.Fatalf("union/envelope/label: %+v %v", got.Facts, err)
	}
	got.Facts.Tickets[0] = 999
	got, err = captureNoticeIntervalWithLabel(f, activityIdentity, testCard, noticeinterval.Cut{At: activityThrough}, noticeTime(t, activityFrom), "unsafe\nlabel")
	if err != nil || got.Facts == nil || got.Facts.Label != "Board "+testBoard || got.Facts.Tickets[0] != 4 {
		t.Fatal("unsafe label or returned facts changed source")
	}
	// Historical entity provenance survives restore; its retained allocation is
	// still valid when a new exact local operation references that entity.
	f.exec(`UPDATE entity SET origin_incarnation=?`, sourceInc)
	got, err = f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
	if err != nil || got.Facts == nil || !reflect.DeepEqual(got.Facts.Tickets, []A.Positive{4, 92}) {
		t.Fatal("restored entity provenance rejected a current local operation")
	}
}

func TestNoticeTextInvalidAssociationsFallBack(t *testing.T) {
	for _, query := range []string{
		`DELETE FROM operation`, `UPDATE operation SET origin_incarnation='ffffffff-ffff-ffff-ffff-ffffffffffff'`,
		`UPDATE operation SET incarnation='ffffffff-ffff-ffff-ffff-ffffffffffff'`, `UPDATE operation SET current_ledger=0`,
		`UPDATE operation SET command='card.create'`, `UPDATE operation SET actor_id='ffffffff-ffff-ffff-ffff-ffffffffffff'`,
		`UPDATE operation SET terminal_class='NOT_COMMITTED'`, `UPDATE operation SET code='DENIED'`,
		`UPDATE operation SET terminal_at='2026-10-01T00:00:00Z'`, `UPDATE operation SET committed_revision=2`,
		`UPDATE operation SET expected_revision=2`, `UPDATE operation SET committed_card_revision=3`,
		`UPDATE operation SET expected_card_revision=0`, `UPDATE operation SET target_id='ffffffff-ffff-ffff-ffff-ffffffffffff'`,
		`DELETE FROM operation_reference`, `UPDATE operation_reference SET revision=1`,
		`UPDATE operation_reference SET origin_incarnation='ffffffff-ffff-ffff-ffff-ffffffffffff'`,
		`INSERT INTO operation_reference SELECT * FROM operation_reference`,
		`UPDATE operation_reference SET kind='board'`, `UPDATE operation_reference SET entity_id='ffffffff-ffff-ffff-ffff-ffffffffffff'`,
		`DELETE FROM ticket`, `UPDATE ticket SET ticket=0`, `UPDATE ticket SET ticket=9007199254740992`,
		`UPDATE ticket SET incarnation='ffffffff-ffff-ffff-ffff-ffffffffffff'`, `INSERT INTO ticket SELECT * FROM ticket`,
		`DELETE FROM entity`, `UPDATE entity SET card_id='ffffffff-ffff-ffff-ffff-ffffffffffff'`,
		`UPDATE activity SET op_id=NULL`, `UPDATE activity SET op_id='malformed'`,
		`UPDATE operation SET command='` + strings.Repeat("x", 10000) + `'`,
	} {
		t.Run(fmt.Sprintf("case-%d", len(query))+query[:min(25, len(query))], func(t *testing.T) {
			f, _ := newNoticeTextFixture(t, ":memory:")
			addNoticeTextOperation(f, 1, "card.edit", testCard, 2)
			f.exec(query)
			got, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
			if err != nil || got.Class != "human" || got.Facts != nil || got.Count == nil || *got.Count != 1 {
				t.Fatalf("invalid metadata did not preserve safe fallback classification: %+v %v", got, err)
			}
		})
	}
}

func TestNoticeTextCreationAndAuxiliaryShapeFallback(t *testing.T) {
	for _, test := range []struct{ command, query string }{
		{"card.create", `UPDATE operation SET committed_card_revision=2; UPDATE operation_reference SET revision=2`},
		{"card.create", `UPDATE operation SET target_kind='card',target_id='` + testCard + `'`},
		{"comment.add", `UPDATE entity SET card_id='ffffffff-ffff-ffff-ffff-ffffffffffff' WHERE kind='comment'`},
		{"link.add", `INSERT INTO operation_reference SELECT incarnation,origin_board,origin_incarnation,op_id,'card','ffffffff-ffff-ffff-ffff-ffffffffffff',2 FROM operation_reference LIMIT 1`},
		{"grant.create", `DELETE FROM grant_fact`},
		{"workflow.edit", `UPDATE operation_reference SET revision=1`},
	} {
		t.Run(test.command, func(t *testing.T) {
			f, _ := newNoticeTextFixture(t, ":memory:")
			addNoticeTextOperation(f, 1, test.command, testCard, 2)
			f.exec(test.query)
			got, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
			if err != nil || got.Facts != nil || got.Class != "human" {
				t.Fatalf("unsupported reference shape became complete: %+v %v", got, err)
			}
		})
	}
}

func TestNoticeTextLimitsSuppressionAndSourceFailure(t *testing.T) {
	f, _ := newNoticeTextFixture(t, ":memory:")
	for i := 1; i <= 33; i++ {
		addNoticeTextOperation(f, i, "card.create", noticeEntityID(i), i)
	}
	got, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
	if err != nil || got.Class != "human" || got.Facts != nil {
		t.Fatal("33 tickets were truncated into complete facts")
	}
	f.exec(`UPDATE activity SET actor_id=?; DROP TABLE operation`, testCard)
	got, err = f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
	if err != nil || got.Class != "all-automation" || got.Facts != nil {
		t.Fatal("suppression unnecessarily depended on text extraction")
	}
	f.exec(`UPDATE activity SET actor_id=?`, testActor)
	got, err = f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
	if err == nil || got.Upper == nil || got.Count != nil || got.Facts != nil || got.Class != "" {
		t.Fatal("source failure exposed complete evidence")
	}
}

func TestNoticeTextEventCorrespondenceAndRecoveryFallback(t *testing.T) {
	for _, query := range []string{
		`UPDATE activity SET board_revision=1 WHERE event_id=2; UPDATE operation SET committed_revision=1,expected_revision=0 WHERE op_id='` + noticeOpID(2) + `'`,
		`UPDATE activity SET op_id='` + noticeOpID(1) + `' WHERE event_id=2`,
	} {
		f, _ := newNoticeTextFixture(t, ":memory:")
		addNoticeTextOperation(f, 1, "card.create", testCard, 1)
		addNoticeTextOperation(f, 2, "card.edit", testCard, 1)
		f.exec(query)
		got, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
		if err != nil || got.Facts != nil || got.Class != "human" {
			t.Fatal("duplicated operation or regressed event revision proved text facts")
		}
	}
	f, _ := newNoticeTextFixture(t, ":memory:")
	addNoticeTextOperation(f, 1, "card.create", testCard, 1)
	addNoticeTextOperation(f, 2, "card.create", noticeEntityID(2), 2)
	got, err := f.capture(noticeinterval.Cut{Event: 1, Revision: 1, At: activityFrom}, activityThrough)
	if err != nil || got.Facts == nil || !reflect.DeepEqual(got.Facts.Tickets, []A.Positive{2}) {
		t.Fatal("facts included a card before the exact sequence cut")
	}
	got, err = f.capture(noticeinterval.Cut{Revision: 1, At: activityFrom}, activityThrough)
	if err != nil || got.Facts != nil || got.Class != "human" {
		t.Fatal("event below lower revision proved complete text facts")
	}
	for _, affected := range []string{
		`[]`, `null`, `[{"kind":"board","id":"` + testBoard + `","revision":2}]`,
		`[{"kind":"board","id":"` + sourceBoard + `","revision":1}]`,
		`[{"kind":"board","id":"` + testBoard + `","revision":1,"revision":1}]`,
		strings.Repeat(" ", 513),
	} {
		f, _ := newNoticeTextFixture(t, ":memory:")
		f.event(testBoard, testInc, 1, activityTie, "recovery.expire", 1, testActor)
		f.exec(`UPDATE activity SET affected=?; UPDATE board SET next_event=2,revision=1`, affected)
		got, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
		if err != nil || got.Facts != nil || got.Class != "human" {
			t.Fatal("unsupported recovery metadata proved an empty ticket set")
		}
	}
}

func TestNoticeTextMaximumBoundedInterval(t *testing.T) {
	f, _ := newNoticeTextFixture(t, ":memory:")
	f.exec(`BEGIN`)
	for i := 1; i <= noticeExpectedLimit; i++ {
		addNoticeTextOperation(f, i, "history.clear", "", 0)
	}
	f.exec(`COMMIT`)
	got, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
	if err != nil || got.Facts == nil || got.Facts.Tickets == nil || len(got.Facts.Tickets) != 0 || got.Count == nil || *got.Count != noticeExpectedLimit {
		t.Fatalf("maximum bounded interval not supported: %+v %v", got, err)
	}
}

func TestNoticeTextPinnedImmutableMappings(t *testing.T) {
	f, db := newNoticeTextFixture(t, filepath.Join(t.TempDir(), "facts.sqlite"))
	var mode string
	if err := f.Scan("PRAGMA journal_mode=WAL", nil, &mode); err != nil || mode != "wal" {
		t.Fatal("WAL fixture unavailable")
	}
	addNoticeTextOperation(f, 1, "card.create", testCard, 7)
	f.exec(`BEGIN`)
	changed := false
	r := noticeTextRaceRead{Read: f, before: func() {
		if changed {
			return
		}
		changed = true
		if _, err := db.ExecContext(f.ctx, `UPDATE ticket SET ticket=88; UPDATE operation_reference SET entity_id='ffffffff-ffff-ffff-ffff-ffffffffffff'`); err != nil {
			t.Fatal(err)
		}
	}}
	got, err := captureNoticeInterval(r, activityIdentity, testCard, noticeinterval.Cut{At: activityFrom}, noticeTime(t, activityThrough))
	f.exec(`ROLLBACK`)
	if err != nil || !changed || got.Facts == nil || !reflect.DeepEqual(got.Facts.Tickets, []A.Positive{7}) {
		t.Fatalf("later mapping contaminated pinned interval: %+v %v", got, err)
	}
}

type noticeTextRaceRead struct {
	writer.Read
	before func()
}

func (r noticeTextRaceRead) Query(query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "FROM operation WHERE") {
		r.before()
	}
	return r.Read.Query(query, args...)
}

// Inspect actual production SELECT results before the collector scans them.
// The fixture is unchanged between this read and the repeated production query;
// no SQL projection is duplicated in the test.
type noticeTextProjectionRead struct {
	writer.Read
	seen, oversized bool
	projection      string
}

func (r *noticeTextProjectionRead) Query(query string, args ...any) (*sql.Rows, error) {
	var limits []int
	switch {
	case r.projection == "operation" && strings.Contains(query, "FROM operation WHERE"):
		limits = []int{32, 16, 32, 36, 20, 16, 36, 0, 0, 0, 0}
	case r.projection == "reference" && strings.Contains(query, "FROM operation_reference WHERE"):
		limits = []int{16, 36, 0}
	case r.projection == "event" && strings.Contains(query, "FROM activity WHERE"):
		limits = []int{0, 20, 32, 0, 36, 36, 512, 0}
	}
	if limits != nil {
		rows, err := r.Read.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			values := make([]any, len(limits))
			destinations := make([]any, len(limits))
			for i := range values {
				destinations[i] = &values[i]
			}
			if err := rows.Scan(destinations...); err != nil {
				_ = rows.Close()
				return nil, err
			}
			r.seen = true
			for i, value := range values {
				switch value := value.(type) {
				case string:
					r.oversized = r.oversized || limits[i] == 0 || len(value) > limits[i]
				case []byte:
					r.oversized = r.oversized || limits[i] == 0 || len(value) > limits[i]
				}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return r.Read.Query(query, args...)
}

func TestNoticeTextProjectionByteBoundsRejectNULSuffix(t *testing.T) {
	for _, test := range []struct{ projection, query, prefix string }{
		{"operation", "UPDATE operation SET code=?", "OK"},
		{"reference", "UPDATE operation_reference SET entity_id=?", testCard},
		{"event", "UPDATE activity SET op_id=?", noticeOpID(1)},
	} {
		t.Run(test.projection, func(t *testing.T) {
			f, _ := newNoticeTextFixture(t, ":memory:")
			addNoticeTextOperation(f, 1, "card.edit", testCard, 2)
			f.exec(test.query, test.prefix+"\x00"+strings.Repeat("x", 10000))
			read := &noticeTextProjectionRead{Read: f, projection: test.projection}
			got, err := captureNoticeInterval(read, activityIdentity, testCard, noticeinterval.Cut{At: activityFrom}, noticeTime(t, activityThrough))
			if !read.seen || read.oversized {
				t.Errorf("production %s projection exceeded its raw byte bounds or was not inspected", test.projection)
			}
			if err != nil || got.Class != "human" || got.Facts != nil {
				t.Fatal("malformed NUL-suffix metadata did not preserve conservative classification")
			}
		})
	}
}
