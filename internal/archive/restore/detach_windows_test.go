//go:build windows && amd64

package restore

import (
	"bytes"
	"context"
	"testing"

	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
)

func TestDetachNativeRestoreRebuildsOnlyRequiredPins(t *testing.T) {
	ctx := context.Background()
	source := sourcePackage(t, detachedMembers(t))
	prepared, err := prepare(ctx, source, testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	dir := hierarchyNativeDir(t)
	if err = prepared.Construct(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err = prepared.NativeInventory(ctx, dir); err != nil {
		t.Fatal(err)
	}
	var original []byte
	for _, group := range prepared.outcomes.SourceArchives {
		for _, op := range group.Operations {
			if op.Command == "attachment.detach" {
				original = op.Full.Intent
			}
		}
	}
	if len(original) == 0 {
		t.Fatal("detach original missing")
	}
	err = S.InspectExisting(ctx, dir, schema.Identity{BoardID: string(prepared.board.Identity.BoardID), Incarnation: string(testInc)}, func(r S.InspectionRead) error {
		for _, q := range []string{
			"SELECT count(*) FROM operation WHERE command='attachment.detach' AND current_ledger=0",
			"SELECT count(*) FROM attachment WHERE attachment_id='00000000-0000-4000-8000-00000000000d'",
			"SELECT count(*) FROM history_blob_pin WHERE attachment_id='00000000-0000-4000-8000-00000000000d'",
			"SELECT count(*) FROM operation_blob_pin WHERE attachment_id='00000000-0000-4000-8000-00000000000d'",
		} {
			var n int
			if e := r.Scan(q, nil, &n); e != nil {
				return e
			}
			if n != 1 {
				t.Fatal("required retained record/pin missing", q, n)
			}
		}
		var n int
		if e := r.Scan("SELECT count(*) FROM current_attachment_pin WHERE attachment_id='00000000-0000-4000-8000-00000000000d'", nil, &n); e != nil {
			return e
		}
		if n != 0 {
			t.Fatal("detach restored current pin")
		}
		var got []byte
		if e := r.Scan("SELECT f.intent FROM operation_full f JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.command='attachment.detach'", nil, &got); e != nil {
			return e
		}
		if !bytes.Equal(got, original) {
			t.Fatal("restored canonical bytes changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
