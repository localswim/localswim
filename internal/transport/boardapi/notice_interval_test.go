package boardapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

type noticeFixture struct{ activityFixture }

func newNoticeFixture(t *testing.T, path string) (noticeFixture, *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	f := noticeFixture{activityFixture{t: t, conn: c, readAccess: readAccess{ctx, c}}}
	// Deliberately omit validation constraints to exercise corrupt evidence.
	f.exec(`CREATE TABLE board(singleton INTEGER,board_id TEXT,incarnation TEXT,revision INTEGER,next_event INTEGER,activity_floor TEXT);
		CREATE TABLE actor(incarnation TEXT,actor_id TEXT,kind TEXT);
		CREATE TABLE activity(incarnation TEXT,origin_board TEXT,origin_incarnation TEXT,event_id INTEGER,at TEXT,type TEXT,board_revision INTEGER,actor_id TEXT,affected TEXT,op_id TEXT);
		CREATE TABLE coverage_gap(gap_id INTEGER PRIMARY KEY,incarnation TEXT,origin_board TEXT,origin_incarnation TEXT,class TEXT,from_exclusive TEXT,through_inclusive TEXT,reason TEXT);`)
	f.exec("INSERT INTO board VALUES(1,?,?,9,1,NULL)", testBoard, testInc)
	f.exec("INSERT INTO actor VALUES(?,?,'human'),(?,?,'automation'),(?,?,'automation')", testInc, testActor, testInc, testCard, testInc, sourceInc)
	return f, db
}

func (f noticeFixture) gap(board, origin, class, reason string, from any, through string) {
	f.t.Helper()
	f.exec("INSERT INTO coverage_gap VALUES(NULL,?,?,?,?,?,?,?)", testInc, board, origin, class, from, through, reason)
}

func (f noticeFixture) capture(lower noticeinterval.Cut, now string) (noticeinterval.Evidence, error) {
	f.t.Helper()
	return captureNoticeInterval(f, activityIdentity, testCard, lower, noticeTime(f.t, now))
}

func noticeTime(t *testing.T, at string) time.Time {
	t.Helper()
	now, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatal(err)
	}
	return now
}

type noticePageReader struct {
	writer.Read
	before func([]any) error
}

func (r noticePageReader) Query(query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "ORDER BY a.event_id ASC") {
		if err := r.before(args); err != nil {
			return nil, err
		}
	}
	return r.Read.Query(query, args...)
}

func requireNoticeEvidence(t *testing.T, evidence noticeinterval.Evidence, class string, upper A.Number, count int) {
	t.Helper()
	if evidence.Upper == nil || !evidence.Upper.Valid() || evidence.Upper.Event != upper || evidence.Class != class {
		t.Fatalf("unexpected evidence class %q or upper %+v", evidence.Class, evidence.Upper)
	}
	if (count < 0 && evidence.Count != nil) || (count >= 0 && (evidence.Count == nil || *evidence.Count != A.Number(count))) {
		t.Fatalf("unexpected evidence count %v", evidence.Count)
	}
}

func TestNoticeIntervalPinnedPages(t *testing.T) {
	// WAL is confined to the controlled fixture. It permits a real second
	// connection to commit during a snapshot. The native store uses DELETE.
	f, db := newNoticeFixture(t, filepath.Join(t.TempDir(), "synthetic.sqlite"))
	var mode string
	if err := f.Scan("PRAGMA journal_mode=WAL", nil, &mode); err != nil || mode != "wal" {
		t.Fatalf("fixture journal mode %q: %v", mode, err)
	}
	lower, err := noticeCut(f, activityIdentity, noticeTime(t, activityFrom))
	if err != nil || lower.Event != 0 {
		t.Fatalf("initial cut: %+v %v", lower, err)
	}
	for i := 1; i <= 45; i++ {
		at := activityTie
		if i%2 == 0 {
			at = "2026-09-30T23:59:59Z"
		}
		if i == 45 {
			at = "2026-10-09T00:00:00Z"
		}
		f.event(testBoard, testInc, i, at, "card.edit", 9, testCard)
		f.event(testBoard, sourceInc, i, activityTie, "card.edit", 100, testActor)
	}
	f.gap(testBoard, sourceInc, "activity", "cleared", nil, activityThrough)
	f.gap(sourceBoard, testInc, "activity", "expired", nil, activityThrough)
	f.exec("UPDATE board SET next_event=46")
	f.exec("BEGIN")
	var starts []A.Number
	r := noticePageReader{Read: f, before: func(args []any) error {
		start, limit := args[3].(A.Number), args[5].(int)
		if args[4] != A.Number(45) || limit != 20 {
			t.Fatalf("unexpected page upper %v or limit %d", args[4], limit)
		}
		starts = append(starts, start)
		if start == 20 {
			_, err := db.ExecContext(f.ctx, `BEGIN IMMEDIATE;
				INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,at,type,board_revision,actor_id,affected) SELECT incarnation,origin_board,origin_incarnation,46,'2026-09-29T00:00:00Z',type,10,?,affected
				FROM activity WHERE incarnation=? AND origin_board=? AND origin_incarnation=? AND event_id=45;
				UPDATE board SET next_event=47,revision=10; COMMIT;`, testActor, testInc, testBoard, testInc)
			return err
		}
		return nil
	}}
	first, err := captureNoticeInterval(r, activityIdentity, testCard, lower, noticeTime(t, activityThrough))
	if err != nil || !reflect.DeepEqual(starts, []A.Number{0, 20, 40}) {
		t.Fatalf("page seams %v: %v", starts, err)
	}
	requireNoticeEvidence(t, first, "all-automation", 45, 45)
	if first.Upper.Gap != 0 || first.Upper.Revision != 9 {
		t.Fatal("upper included foreign loss or later commit")
	}
	f.exec("ROLLBACK")
	second, err := f.capture(*first.Upper, activityTie)
	if err != nil {
		t.Fatal(err)
	}
	requireNoticeEvidence(t, second, "human", 46, 1)
	if second.Upper.Revision != 10 || second.Upper.At >= first.Upper.At {
		t.Fatal("regressed timestamp prevented catch-up")
	}
	page := f.page(&Handler{}, "same-grant", activityInterval)
	if len(page.Events) != 20 || page.Events[0].Origin.Incarnation != sourceInc || len(page.Coverage.Gaps) != 2 {
		t.Fatal("retained origins were lost from M11")
	}
}

func TestNoticeIntervalCoverage(t *testing.T) {
	for _, test := range []struct {
		name, class string
		floor       any
		hole        int
	}{
		{"middle-hole", "gapped", nil, 21}, {"end-hole", "gapped", nil, 45},
		{"floor-at-low-with-all-rows", "gapped", activityFrom, 0},
		{"floor-at-high-with-all-rows", "gapped", activityThrough, 0},
		{"floor-at-high-with-missing-rows", "expired", activityThrough, 45},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _ := newNoticeFixture(t, ":memory:")
			for i := 1; i <= 45; i++ {
				if i != test.hole {
					f.event(testBoard, testInc, i, activityTie, "card.edit", 9, testCard)
				}
			}
			f.event(testBoard, sourceInc, 21, activityTie, "card.edit", 9, testCard)
			f.exec("UPDATE board SET next_event=46,activity_floor=?", test.floor)
			evidence, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
			if err != nil {
				t.Fatal(err)
			}
			requireNoticeEvidence(t, evidence, test.class, 45, -1)
		})
	}
}

func TestNoticeEqualCutsCoverage(t *testing.T) {
	const before = "2026-09-30T23:59:59Z"
	for _, test := range []struct {
		name, upperTime, class string
		oldFloor, newFloor     any
		gaps                   func(noticeFixture)
	}{
		{"historic-floor-and-gap", activityFrom, "observed-empty", activityFrom, activityFrom, nil},
		{"new-floor-before", activityFrom, "observed-empty", nil, before, nil},
		{"new-floor-ending-at-instant", activityFrom, "expired", before, activityFrom, nil},
		{"new-floor-starting-at-instant", activityFrom, "observed-empty", activityFrom, activityTie, nil},
		{"new-floor-overlap", activityTie, "gapped", activityFrom, activityTie, nil},
		{"new-gap-ending-at-instant", activityFrom, "gapped", nil, nil, func(f noticeFixture) { f.gap(testBoard, testInc, "activity", "expired", before, activityFrom) }},
		{"new-gap-starting-at-instant", activityFrom, "observed-empty", nil, nil, func(f noticeFixture) { f.gap(testBoard, testInc, "activity", "expired", activityFrom, activityTie) }},
		{"new-gap-degenerate", activityFrom, "observed-empty", nil, nil, func(f noticeFixture) { f.gap(testBoard, testInc, "activity", "expired", activityFrom, activityFrom) }},
		{"new-gap-outside", activityFrom, "observed-empty", nil, nil, func(f noticeFixture) { f.gap(testBoard, testInc, "activity", "expired", nil, before) }},
		{"new-gap-foreign", activityFrom, "observed-empty", nil, nil, func(f noticeFixture) { f.gap(testBoard, sourceInc, "activity", "expired", nil, activityThrough) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _ := newNoticeFixture(t, ":memory:")
			f.gap(testBoard, testInc, "activity", "cleared", nil, activityThrough)
			f.exec("UPDATE board SET activity_floor=?", test.oldFloor)
			lower, err := noticeCut(f, activityIdentity, noticeTime(t, activityFrom))
			if err != nil {
				t.Fatal(err)
			}
			if test.gaps != nil {
				test.gaps(f)
			}
			f.exec("UPDATE board SET activity_floor=?", test.newFloor)
			evidence, err := f.capture(lower, test.upperTime)
			if err != nil {
				t.Fatal(err)
			}
			count := -1
			if test.class == "observed-empty" {
				count = 0
			}
			requireNoticeEvidence(t, evidence, test.class, 0, count)
			repeated, err := f.capture(*evidence.Upper, test.upperTime)
			if err != nil {
				t.Fatal(err)
			}
			requireNoticeEvidence(t, repeated, "observed-empty", 0, 0)
		})
	}
}

func TestNoticeEqualCutsRejectMalformedNewGapBounds(t *testing.T) {
	for _, test := range []struct {
		name, through string
		from          any
	}{
		{"short-through-before-envelope", "", nil},
		{"invalid-through-before-envelope", "2026-09-31T00:00:00Z", nil},
		{"invalid-from-after-envelope", activityThrough, "z"},
		{"reversed-outside-envelope", activityThrough, "2026-10-09T00:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _ := newNoticeFixture(t, ":memory:")
			f.gap(testBoard, testInc, "activity", "expired", test.from, test.through)
			evidence, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityFrom)
			if err == nil || evidence.Count != nil || evidence.Class != "" || evidence.Upper == nil || evidence.Upper.Gap != 1 {
				t.Fatalf("corrupt gap classified as a complete empty interval: %+v %v", evidence, err)
			}
		})
	}
}

func TestNoticeIntervalDistinctGapBudget(t *testing.T) {
	f, _ := newNoticeFixture(t, ":memory:")
	for i := range 32 {
		at := fmt.Sprintf("2026-10-08T00:00:%02dZ", i)
		f.gap(testBoard, testInc, "activity", "expired", nil, at)
		f.gap(testBoard, testInc, "activity", "expired", nil, at)
	}
	evidence, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
	if err != nil {
		t.Fatal(err)
	}
	requireNoticeEvidence(t, evidence, "gapped", 0, -1)
	if evidence.Upper.Gap != 64 {
		t.Fatal("tuple deduplication changed the native watermark")
	}
}

func TestNoticeIntervalActorClasses(t *testing.T) {
	for _, test := range []struct {
		class  string
		actors []string
	}{
		{"human", []string{testActor, testActor}}, {"other-actor", []string{sourceInc}},
		{"mixed", []string{testActor, testCard}}, {"mixed", []string{testCard, sourceInc}},
		{"unknown-actor", []string{testActor, testCard, sourceBoard}}, {"unknown-actor", []string{"malformed-id"}},
	} {
		t.Run(test.class, func(t *testing.T) {
			f, _ := newNoticeFixture(t, ":memory:")
			for i, actor := range test.actors {
				f.event(testBoard, testInc, i+1, activityTie, "card.edit", 9, actor)
			}
			f.exec("UPDATE board SET next_event=?", len(test.actors)+1)
			evidence, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
			if err != nil {
				t.Fatal(err)
			}
			requireNoticeEvidence(t, evidence, test.class, A.Number(len(test.actors)), len(test.actors))
		})
	}
}

func TestNoticeIntervalUnavailableAndContinuity(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(noticeFixture)
		upper *A.Number
	}{
		{"event-budget", func(f noticeFixture) { f.exec("UPDATE board SET next_event=1002") }, new(A.Number(1001))},
		{"counter-overflow", func(f noticeFixture) { f.exec("UPDATE board SET next_event=9007199254740992") }, nil},
		{"malformed-floor", func(f noticeFixture) { f.exec("UPDATE board SET activity_floor='malformed'") }, nil},
		{"malformed-row", func(f noticeFixture) { f.event(testBoard, testInc, 1, activityTie, "invalid", 9, testCard) }, new(A.Number(1))},
		{"malformed-gap", func(f noticeFixture) { f.gap(testBoard, testInc, "activity", "invalid", nil, activityThrough) }, new(A.Number(1))},
		{"gap-budget", func(f noticeFixture) {
			for i := range 33 {
				f.gap(testBoard, testInc, "activity", "expired", nil, fmt.Sprintf("2026-10-08T00:00:%02dZ", i))
			}
		}, new(A.Number(1))},
		{"malformed-gap-id", func(f noticeFixture) {
			f.gap(testBoard, testInc, "activity", "expired", nil, activityThrough)
			f.exec("UPDATE coverage_gap SET gap_id=0")
		}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _ := newNoticeFixture(t, ":memory:")
			f.exec("UPDATE board SET next_event=2")
			test.setup(f)
			evidence, err := f.capture(noticeinterval.Cut{At: activityFrom}, activityThrough)
			if err == nil || evidence.Count != nil || evidence.Class != "" || (evidence.Upper == nil) != (test.upper == nil) ||
				(test.upper != nil && (!evidence.Upper.Valid() || evidence.Upper.Event != *test.upper)) {
				t.Fatalf("untrusted evidence classified or descriptor mismatch: %+v %v", evidence, err)
			}
		})
	}
	f, _ := newNoticeFixture(t, ":memory:")
	for _, lower := range []noticeinterval.Cut{
		{At: activityFrom, Event: 1}, {At: activityFrom, Revision: 10},
		{At: activityFrom, Floor: activityTie}, {At: activityFrom, Gap: 1},
	} {
		if evidence, err := f.capture(lower, activityThrough); !errors.Is(err, ErrNoticeForeign) || evidence.Upper != nil {
			t.Fatalf("regression returned evidence: %+v %v", evidence, err)
		}
	}
	if evidence, err := f.capture(noticeinterval.Cut{At: "invalid"}, activityThrough); !errors.Is(err, ErrUnavailable) || evidence.Upper != nil {
		t.Fatalf("malformed lower returned evidence: %+v %v", evidence, err)
	}
	f.exec("UPDATE board SET next_event=22")
	for i := 1; i <= 21; i++ {
		f.event(testBoard, testInc, i, activityTie, "card.edit", 9, testCard)
	}
	r := noticePageReader{Read: f, before: func(args []any) error {
		if args[3] == A.Number(20) {
			return context.Canceled
		}
		return nil
	}}
	evidence, err := captureNoticeInterval(r, activityIdentity, testCard, noticeinterval.Cut{At: activityFrom}, noticeTime(t, activityThrough))
	if !errors.Is(err, context.Canceled) || evidence.Count != nil || evidence.Class != "" || evidence.Upper == nil || evidence.Upper.Event != 21 {
		t.Fatalf("interrupted read returned incomplete evidence as complete: %+v %v", evidence, err)
	}
}
