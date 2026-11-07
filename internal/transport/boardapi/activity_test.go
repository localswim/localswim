package boardapi

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

const activityFrom = "2026-10-01T00:00:00Z"
const activityTie = "2026-10-01T00:00:01Z"
const activityThrough = "2026-10-08T00:00:00Z"
const sourceInc = "20000000-0000-0000-0000-000000000002"
const sourceBoard = "10000000-0000-0000-0000-000000000002"

func TestActivityExactRequestBoundary(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, activityThrough)
	inc := "incarnation=" + testInc
	interval := "through=" + activityThrough + "&from=" + activityFrom
	for _, raw := range []string{inc, interval + "&" + inc, "cursor=e30.YQ&" + inc} {
		p, err := parseActivity(raw, now.Add(time.Millisecond))
		if err != nil || p.incarnation != testInc || (p.cursor == "" && (p.from != activityFrom || p.through != activityThrough)) {
			t.Fatalf("request form refused: %v", err)
		}
	}
	for _, raw := range []string{"", "&" + inc, inc + "&", inc + "&&cursor=e30.YQ", inc + "&" + inc,
		inc + "&from=", inc + "&from=" + activityFrom, inc + "&through=" + activityThrough,
		inc + "&" + interval + "&cursor=e30.YQ", inc + "&x=1", inc + "&cursor=%ff", inc + "&cursor=%",
		inc + "&cursor=e30.YQ%0A", inc + "&cursor=e30.YQ==", inc + "&cursor=" + strings.Repeat("A", 2048) + ".YQ",
		inc + "&" + strings.Repeat("A", 4096), inc + "&" + strings.Replace(interval, activityFrom, "2026-09-30T23:59:59Z", 1),
		inc + "&" + strings.Replace(interval, activityFrom, "2026-10-01T00:00:00.0Z", 1),
		inc + "&" + strings.Replace(interval, activityThrough, "2026-10-08T00:00:01Z", 1),
		inc + "&" + strings.Replace(interval, activityFrom, activityThrough, 1),
		inc + "&" + strings.Replace(interval, activityFrom, "2026-09-31T00:00:00Z", 1),
	} {
		if _, err := parseActivity(raw, now); err == nil {
			t.Fatal("malformed query accepted")
		}
	}
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", base + "/activity?" + inc, 0}, {"POST", base + "/activity?" + inc, 405},
		{"GET", base + "/%61ctivity?" + inc, 400}, {"GET", base + "/activity/", 404},
	} {
		q, status := selectRoute(httptest.NewRequest(test.method, test.path, nil))
		if status != test.status || status == 0 && q.kind != "activity" {
			t.Fatal("activity route mismatch")
		}
	}
}

// These deliberately unconstrained, private in-memory tables let the tests
// model bad retained evidence without changing a production store's schema.
type activityFixture struct {
	t    *testing.T
	conn *sql.Conn
	readAccess
}

func newActivityFixture(t *testing.T) activityFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	f := activityFixture{t: t, conn: c, readAccess: readAccess{ctx, c}}
	f.exec(`CREATE TABLE board(singleton INTEGER, board_id TEXT, incarnation TEXT, revision INTEGER, next_event INTEGER, activity_floor TEXT);
		CREATE TABLE actor(incarnation TEXT, actor_id TEXT, kind TEXT, label TEXT);
		CREATE TABLE activity(incarnation TEXT, origin_board TEXT, origin_incarnation TEXT, event_id INTEGER, at TEXT, type TEXT, board_revision INTEGER, actor_id TEXT, affected TEXT);
		CREATE TABLE coverage_gap(incarnation TEXT, origin_board TEXT, origin_incarnation TEXT, class TEXT, reason TEXT, from_exclusive TEXT, through_inclusive TEXT);`)
	f.exec("INSERT INTO board VALUES(1,?,?,9,14,NULL)", testBoard, testInc)
	f.exec("INSERT INTO actor VALUES(?,?,'human','PRIVATE-HUMAN'),(?,?,'automation','PRIVATE-BOT')", testInc, testActor, testInc, testCard)
	return f
}

func (f activityFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f activityFixture) event(board, inc string, id int, at, typ string, revision int, actor string) {
	f.t.Helper()
	f.exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,at,type,board_revision,actor_id,affected) VALUES(?,?,?,?,?,?,?,?,'PRIVATE-AFFECTED')", testInc, board, inc, id, at, typ, revision, actor)
}

var activityIdentity = A.Identity{BoardID: testBoard, Incarnation: testInc}
var activityInterval = activityRequest{incarnation: testInc, from: activityFrom, through: activityThrough}

func (f activityFixture) page(h *Handler, authority string, request activityRequest) T.ActivityView {
	f.t.Helper()
	out, err := h.activity(f, activityIdentity, authority, request)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := T.Validate(out); err != nil {
		f.t.Fatal(err)
	}
	return out.Result
}

func TestActivityRetainedOriginPaginationAndStaleness(t *testing.T) {
	f := newActivityFixture(t)
	for id := 1; id <= 12; id++ {
		f.event(testBoard, testInc, id, activityTie, "card.create", 8, testActor)
		f.event(testBoard, sourceInc, id+9, activityTie, "comment.add", 95, testCard)
	}
	f.event(testBoard, testInc, 13, activityFrom, "card.edit", 8, testActor)
	f.event(testBoard, testInc, 14, activityTie, "card.edit", 10, testActor)
	f.event(testBoard, sourceInc, 2, activityThrough, "recovery.expire", 96, testCard)
	f.event(sourceBoard, testInc, 1, activityTie, "card.edit", 99, testCard)
	f.event(sourceBoard, testInc, 2, "2026-10-08T00:00:01Z", "card.edit", 99, testCard)
	h := &Handler{cursorKey: [32]byte{1}, cursorEpoch: [16]byte{2}}
	first := f.page(h, "authority", activityInterval)
	if len(first.Events) != 20 || first.NextCursor == nil || first.LocalEventUpper != 13 || first.Revision != 9 {
		t.Fatal("initial page capture")
	}
	continuation := activityRequest{incarnation: testInc, cursor: *first.NextCursor}
	second := f.page(h, "authority", continuation)
	if second.NextCursor != nil || len(second.Events) != 6 || second.From != first.From || second.Through != first.Through || !reflect.DeepEqual(first.Coverage, second.Coverage) {
		t.Fatal("final page capture")
	}
	if again := f.page(h, "authority", continuation); !reflect.DeepEqual(second, again) {
		t.Fatal("unchanged replay changed")
	}
	var expected []T.ActivityEvent
	human, automation := A.UUID(testActor), A.UUID(testCard)
	source := A.Identity{BoardID: testBoard, Incarnation: sourceInc}
	other := A.Identity{BoardID: sourceBoard, Incarnation: testInc}
	expected = append(expected,
		T.ActivityEvent{Origin: source, EventID: 2, At: activityThrough, Type: "recovery.expire", BoardRevision: 96, ActorID: &automation, ActorKind: "automation"},
		T.ActivityEvent{Origin: other, EventID: 1, At: activityTie, Type: "card.edit", BoardRevision: 99, ActorID: &automation, ActorKind: "automation"},
	)
	for id := A.Positive(21); id >= 10; id-- {
		expected = append(expected, T.ActivityEvent{Origin: source, EventID: id, At: activityTie, Type: "comment.add", BoardRevision: 95, ActorID: &automation, ActorKind: "automation"})
	}
	for id := A.Positive(12); id > 0; id-- {
		expected = append(expected, T.ActivityEvent{Origin: activityIdentity, EventID: id, At: activityTie, Type: "card.create", BoardRevision: 8, ActorID: &human, ActorKind: "human"})
	}
	if !reflect.DeepEqual(append(first.Events, second.Events...), expected) {
		t.Fatal("loss, rebasing or tuple ordering error")
	}
	assertStale := func(h *Handler, authority string, req activityRequest) {
		t.Helper()
		if _, err := h.activity(f, activityIdentity, authority, req); !errors.Is(err, errCursorStale) {
			t.Fatalf("expected stale, got %v", err)
		}
	}
	assertStale(h, "another-grant", continuation)
	assertStale(&Handler{cursorKey: [32]byte{3}, cursorEpoch: h.cursorEpoch}, "authority", continuation)
	assertStale(&Handler{cursorKey: h.cursorKey, cursorEpoch: [16]byte{3}}, "authority", continuation)
	tampered := continuation
	_, tag, _ := strings.Cut(tampered.cursor, ".")
	replacement := "A"
	if strings.HasPrefix(tag, replacement) {
		replacement = "B"
	}
	tampered.cursor = strings.Replace(tampered.cursor, "."+tag, "."+replacement+tag[1:], 1)
	assertStale(h, "authority", tampered)
	f.exec("UPDATE board SET next_event=15")
	assertStale(h, "authority", continuation)
	f.exec("UPDATE board SET revision=10")
	if fresh := f.page(h, "authority", activityInterval); fresh.Revision != 10 || fresh.Events[14].EventID != 14 {
		t.Fatal("fresh page did not capture new commit")
	}
}

func TestActivityCoverageAttributionAndFailure(t *testing.T) {
	f := newActivityFixture(t)
	h := &Handler{cursorKey: [32]byte{1}}
	page := func() T.ActivityView { return f.page(h, "authority", activityInterval) }
	if p := page(); p.Coverage.Status != "complete" || len(p.Events) != 0 {
		t.Fatal("complete empty")
	}
	f.exec("UPDATE board SET activity_floor=?", activityFrom)
	if p := page(); p.Coverage.Status != "complete" {
		t.Fatal("floor at exclusive lower bound")
	}
	f.exec("UPDATE board SET activity_floor=?", activityThrough)
	if p := page(); p.Coverage.Status != "expired" || len(p.Events) != 0 {
		t.Fatal("expired empty")
	}
	f.exec("UPDATE board SET activity_floor=NULL")
	gap := func(inc, class, reason string, from any, through string) {
		f.exec("INSERT INTO coverage_gap VALUES(?,?,?,?,?,?,?)", testInc, testBoard, inc, class, reason, from, through)
	}
	gap(sourceInc, "activity", "expired", nil, activityTie)
	gap(sourceInc, "activity", "expired", nil, activityTie)
	gap(testInc, "activity", "cleared", nil, activityFrom)
	gap(testInc, "activity", "cleared", activityThrough, "2026-10-08T00:00:01Z")
	gap(testInc, "activity", "cleared", activityTie, activityTie)
	gap(testInc, "history", "expired", nil, activityThrough)
	if p := page(); p.Coverage.Status != "gapped" || p.Coverage.ActivityFloor != nil || len(p.Coverage.Gaps) != 1 || p.Coverage.Gaps[0].Origin.Incarnation != sourceInc || len(p.Events) != 0 {
		t.Fatal("restored and applicable coverage")
	}
	f.event(testBoard, sourceInc, 1, activityTie, "card.create", 42, testBoard)
	f.event(testBoard, sourceInc, 2, activityTie, "card.create", 42, "INVALID-PRIVATE-ACTOR")
	f.exec("INSERT INTO actor VALUES(?,?,'automation','PRIVATE-DUPLICATE')", testInc, testActor)
	f.event(testBoard, sourceInc, 3, activityTie, "card.create", 42, testActor)
	f.exec("INSERT INTO actor VALUES(?,?,'invalid','PRIVATE-UNKNOWN')", testInc, sourceInc)
	f.event(testBoard, sourceInc, 4, activityTie, "card.create", 42, sourceInc)
	p := page()
	if p.Coverage.Status != "gapped" || len(p.Events) != 4 || p.Events[2].ActorID != nil {
		t.Fatal("gapped rows and invalid actor ID")
	}
	for index, event := range p.Events {
		if event.ActorKind != "unknown" || (index != 2 && event.ActorID == nil) {
			t.Fatal("historical attribution fallback")
		}
	}
	b, err := T.Readable(T.ActivityRead{APIVersion: 1, Scope: "activity", Result: p})
	if err != nil || bytes.Contains(b, []byte("PRIVATE")) {
		t.Fatal("private storage fields leaked")
	}
	for id := 5; id <= 21; id++ {
		f.event(testBoard, sourceInc, id, activityTie, "card.create", 42, testCard)
	}
	first := page()
	cont := activityRequest{cursor: *first.NextCursor}
	gap(testInc, "activity", "cleared", nil, activityThrough)
	if _, err := h.activity(f, activityIdentity, "authority", cont); !errors.Is(err, errCursorStale) {
		t.Fatal("coverage changed without revision but cursor survived")
	}
	first = page()
	cont.cursor = *first.NextCursor
	for id := 2; id <= 32; id++ {
		at, _ := time.Parse(time.RFC3339, activityFrom)
		gap(testInc, "activity", "cleared", nil, at.Add(time.Duration(id)*time.Second).Format(time.RFC3339))
	}
	if _, err := h.activity(f, activityIdentity, "authority", cont); !errors.Is(err, errCursorStale) {
		t.Fatal("coverage overflow continuation")
	}
	if _, err := h.activity(f, activityIdentity, "authority", activityInterval); !errors.Is(err, ErrUnavailable) {
		t.Fatal("coverage overflow first page")
	}
	f.exec("DELETE FROM coverage_gap")
	// The 21st row must be validated before advertising a next page.
	f.exec("UPDATE activity SET type='unrecognized' WHERE event_id=1")
	if _, err := h.activity(f, activityIdentity, "authority", activityInterval); err == nil {
		t.Fatal("malformed candidate treated as absent")
	}
	f.exec("DROP TABLE activity")
	if _, err := h.activity(f, activityIdentity, "authority", activityInterval); err == nil {
		t.Fatal("query failure treated as empty")
	}
}
