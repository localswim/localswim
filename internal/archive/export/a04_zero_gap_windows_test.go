//go:build windows && amd64

package export

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestA04ZeroGapCompleteArchiveParity(t *testing.T) {
	b, h, a, o := emptyDocuments()
	b.Coverage.Gaps = []A.Gap{{Origin: testIdentity, Class: "activity", Reason: "expired", ThroughInclusive: "2026-10-20T00:00:00Z", RecordedAt: testTime, BoardRevision: 0}}
	p, err := assemble(context.Background(), b, h, a, o, []A.Lineage{}, testTime, []A.ByteFact{}, func(A.ByteFact) ([]byte, error) { t.Fatal("unexpected blob"); return nil, nil })
	if err != nil {
		t.Fatal("complete logical package rejected", err)
	}
	var wire bytes.Buffer
	if err = p.Write(context.Background(), &wire); err != nil {
		t.Fatal(err)
	}
	validated, err := V.Read(context.Background(), bytes.NewReader(wire.Bytes()))
	if err != nil {
		t.Fatal("complete A03 package rejected", err)
	}
	manifest, err := validated.Manifest()
	if err != nil || len(manifest.Coverage.Gaps) != 1 || manifest.Coverage.Gaps[0].BoardRevision != 0 {
		t.Fatal("zero gap changed during A03 validation", err)
	}
	f := newNative(t)
	f.raw(t, func(c *sql.Conn) error {
		_, err := c.ExecContext(testCtx(t), "INSERT INTO coverage_gap(incarnation,origin_board,origin_incarnation,class,reason,through_inclusive,recorded_at,board_revision) VALUES(?,?,?,'activity','expired','2026-10-20T00:00:00Z',?,0)", testIdentity.Incarnation, testIdentity.BoardID, testIdentity.Incarnation, testTime)
		return err
	})
	_, members := readArchive(t, f.prepare(t))
	actual, err := A.DecodeReadable[A.Board](members["board.json"])
	if err != nil || len(actual.Coverage.Gaps) != 1 || actual.Coverage.Gaps[0].BoardRevision != 0 {
		t.Fatal("native A02 projection changed zero-valued gap revision", err)
	}
}
