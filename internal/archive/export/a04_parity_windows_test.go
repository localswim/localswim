//go:build windows && amd64

package export

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
)

func TestNativePhysicalFourExactGroupsAndGapTuples(t *testing.T) {
	f := newNative(t)
	emptyInc := A.UUID("20000000-0000-0000-0000-000000000002")
	fullInc := A.UUID("20000000-0000-0000-0000-000000000003")
	opOnlyInc := A.UUID("20000000-0000-0000-0000-000000000004")
	lineageOnlyInc := A.UUID("20000000-0000-0000-0000-000000000005")
	sourceOp := A.UUID(testUUID(t))
	fromOne := A.Timestamp("2026-10-18T00:00:00Z")
	fromTwo := A.Timestamp("2026-10-19T00:00:00Z")
	wantGaps := []A.Gap{}
	var path string
	for _, row := range []struct {
		from *A.Timestamp
		at   A.Timestamp
	}{{nil, "2026-10-21T00:00:00Z"}, {nil, "2026-10-22T00:00:00Z"}, {&fromOne, "2026-10-21T00:00:00Z"}, {&fromTwo, "2026-10-21T00:00:00Z"}} {
		wantGaps = append(wantGaps, A.Gap{Origin: testIdentity, Class: "activity", Reason: "expired", FromExclusive: row.from, ThroughInclusive: "2026-10-20T00:00:00Z", RecordedAt: row.at, BoardRevision: 1})
	}
	f.raw(t, func(c *sql.Conn) error {
		var sequence int
		var name, database string
		if err := c.QueryRowContext(testCtx(t), "PRAGMA database_list").Scan(&sequence, &name, &database); err != nil {
			return err
		}
		path = filepath.Dir(database)
		run := func(q string, args ...any) error { _, err := c.ExecContext(testCtx(t), q, args...); return err }
		for _, inc := range []A.UUID{emptyInc, fullInc} {
			if err := run("INSERT INTO source_archive_group(incarnation,origin_board,origin_incarnation) VALUES(?,?,?)", testIdentity.Incarnation, testIdentity.BoardID, inc); err != nil {
				return err
			}
		}
		for _, inc := range []A.UUID{fullInc, opOnlyInc} {
			if err := run(`INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,expected_revision,terminal_class,code,terminal_at,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'card.create',1,'NOT_COMMITTED','NOT_FOUND',?,?,'cleared',1)`, testIdentity.Incarnation, testIdentity.BoardID, inc, sourceOp, testActor, digest([]byte("synthetic compact source")), testTime, testTime); err != nil {
				return err
			}
		}
		if err := run("INSERT INTO lineage VALUES(?,?,?,?,1)", testIdentity.Incarnation, testIdentity.BoardID, lineageOnlyInc, digest([]byte("synthetic lineage-only manifest"))); err != nil {
			return err
		}
		for _, g := range wantGaps {
			var from any
			if g.FromExclusive != nil {
				from = string(*g.FromExclusive)
			}
			if err := run("INSERT INTO coverage_gap(incarnation,origin_board,origin_incarnation,class,reason,from_exclusive,through_inclusive,recorded_at,board_revision) VALUES(?,?,?,?,?,?,?,?,?)", testIdentity.Incarnation, g.Origin.BoardID, g.Origin.Incarnation, g.Class, g.Reason, from, g.ThroughInclusive, g.RecordedAt, g.BoardRevision); err != nil {
				return err
			}
		}
		return schema.Validate(testCtx(t), c, schema.Identity{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation)})
	})
	// Verify persistence before exercising A02's real authenticated export.
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := S.Existing(testCtx(t), path, schema.Identity{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation)})
	if err != nil {
		t.Fatal(err)
	}
	f.s = reopened
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	_, data := readArchive(t, f.prepare(t))
	o, err := A.DecodeReadable[A.Outcomes](data["outcomes.json"])
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Operations) != 1 || len(o.SourceArchives) != 3 {
		t.Fatal("current/source ledger or exact group membership changed")
	}
	for i, inc := range []A.UUID{emptyInc, fullInc, opOnlyInc} {
		group := o.SourceArchives[i]
		if group.Identity != (A.Identity{BoardID: testIdentity.BoardID, Incarnation: inc}) {
			t.Fatal("source group order or identity changed")
		}
		if i == 0 {
			if group.Operations == nil || len(group.Operations) != 0 {
				t.Fatal("empty declared source group lost")
			}
		} else if len(group.Operations) != 1 || group.Operations[0].OpID != sourceOp {
			t.Fatal("source operation group changed")
		}
	}
	b, err := A.DecodeReadable[A.Board](data["board.json"])
	if err != nil || !reflect.DeepEqual(b.Coverage.Gaps, wantGaps) {
		t.Fatal("complete coverage-gap tuples changed", err)
	}
	m, err := A.DecodeReadable[A.Manifest](data["manifest.json"])
	if err != nil || len(m.Lineage) != 1 || m.Lineage[0].Identity.Incarnation != lineageOnlyInc {
		t.Fatal("lineage/group independence lost", err)
	}
	if o.SchemaVersion != 1 || b.SchemaVersion != 1 || m.SchemaVersion != 1 || m.FormatVersion != 1 {
		t.Fatal("logical archive versions changed")
	}
}
