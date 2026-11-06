//go:build windows && amd64

package attachments

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	E "github.com/localswim/localswim/internal/archive/export"
	R "github.com/localswim/localswim/internal/archive/restore"
	V "github.com/localswim/localswim/internal/archive/validate"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

// Exercise the existing authorized retention boundary after a real detach.
// No fixture deletion or metadata mutation stands in for pruning.
func TestDetachAuthorizedLaterRetentionAndNativeRestore(t *testing.T) {
	f := fresh(t)
	f.raw(t, func(c *sql.Conn) error {
		for _, right := range []string{"history-admin", "export"} {
			if _, err := c.ExecContext(tc(t), "INSERT INTO grant_right VALUES(?,?,?)", inc, grant, right); err != nil {
				return err
			}
		}
		return nil
	})
	data := []byte("F26 retained exact bytes")
	add, _ := f.add(t, f.upload(t, data, 1), data)
	id := committed(t, add)
	addTime := add.Terminal.TerminalAt
	inspected, err := f.a.Inspect(tc(t), f.g, board, inc, actor, f.card, id)
	if err != nil {
		t.Fatal(err)
	}
	metadata := inspected.Result.File.Attachment
	// Selection is by integral UTC seconds. Keep the full detach outside the
	// first clear cutoff without altering any original timestamp or payload.
	time.Sleep(1100 * time.Millisecond)
	detached, _ := detachResult(t, f, id, 2)
	committed(t, detached)
	if len(detached.Terminal.ByteFacts) != 0 {
		t.Fatal("detach acquired byte facts")
	}
	for query, want := range map[string]uint64{
		"SELECT count(*) FROM blob":                                                      1,
		"SELECT count(*) FROM attachment":                                                1,
		"SELECT count(*) FROM history_blob_pin":                                          1,
		"SELECT count(*) FROM operation_blob_pin":                                        1,
		"SELECT count(*) FROM entity WHERE kind='attachment' AND removed_at IS NOT NULL": 0,
	} {
		if f.value(t, query) != want {
			t.Fatal("detach performed implicit retention", query)
		}
	}
	svc := recovery.New(f.s, f.g)
	clear := func(through A.Timestamp) {
		t.Helper()
		input, _ := json.Marshal(A.ClearInput{ThroughTime: through})
		preview, err := svc.Preview(tc(t), recovery.Route{BoardID: board, Incarnation: inc}, actor, A.PreviewRequest{Incarnation: inc, ExpectedRevision: A.Number(f.value(t, "SELECT revision FROM board")), Command: "history.clear", Input: input})
		if err != nil {
			t.Fatal("authorized preview", err)
		}
		op, err := uuid()
		if err != nil {
			t.Fatal(err)
		}
		wire, _ := json.Marshal(A.Confirmation{OpID: A.UUID(op), Incarnation: inc, ExpectedRevision: preview.ExpectedRevision, Command: "history.clear", Input: input, PreviewHash: preview.PreviewHash})
		intent, err := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "history.clear", BoardID: board}, contract.AuthenticatedActor{ActorID: actor})
		if err != nil {
			t.Fatal(err)
		}
		result := svc.Confirm(tc(t), recovery.Route{BoardID: board, Incarnation: inc}, intent)
		if result.Terminal == nil || result.Terminal.Outcome != "committed" {
			t.Fatal("authorized clear did not commit")
		}
	}
	clear(addTime)
	for query, want := range map[string]uint64{
		"SELECT count(*) FROM blob":               0,
		"SELECT count(*) FROM attachment":         1,
		"SELECT count(*) FROM history_blob_pin":   0,
		"SELECT count(*) FROM operation_blob_pin": 0,
		"SELECT count(*) FROM operation WHERE command='attachment.add' AND removed_at IS NOT NULL": 1,
		"SELECT count(*) FROM operation WHERE command='attachment.detach' AND removed_at IS NULL":  1,
		"SELECT count(*) FROM entity WHERE kind='attachment' AND removed_at IS NOT NULL":           0,
	} {
		if f.value(t, query) != want {
			t.Fatal("full detach retention state", query)
		}
	}
	exportAndRestoreDetached(t, f, false, *add.Terminal, metadata)
	clear(detached.Terminal.TerminalAt)
	for query, want := range map[string]uint64{
		"SELECT count(*) FROM blob":       0,
		"SELECT count(*) FROM attachment": 0,
		"SELECT count(*) FROM operation WHERE command='attachment.detach' AND removed_at IS NOT NULL": 1,
		"SELECT count(*) FROM entity WHERE kind='attachment' AND removed_at IS NOT NULL":              1,
		"SELECT count(*) FROM byte_fact": 1,
		"SELECT count(*) FROM byte_fact f JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.command='attachment.detach'": 0,
	} {
		if f.value(t, query) != want {
			t.Fatal("compact detach retention state", query)
		}
	}
	exportAndRestoreDetached(t, f, true, *add.Terminal, metadata)
}

func exportAndRestoreDetached(t *testing.T, f *fixture, compacted bool, add T.Terminal, metadata A.Attachment) {
	t.Helper()
	f.raw(t, func(c *sql.Conn) error {
		assertRetentionValues(t, func(query string, args []any, dest ...any) error {
			return c.QueryRowContext(tc(t), query, args...).Scan(dest...)
		}, add, metadata, !compacted)
		return nil
	})
	now := A.Timestamp(time.Now().UTC().Truncate(time.Second).Format(time.RFC3339))
	prepared, err := E.Prepare(tc(t), f.s, E.Request{Identity: A.Identity{BoardID: board, Incarnation: inc}, Revision: A.Number(f.value(t, "SELECT revision FROM board")), ExportedAt: now, Verify: func(ctx context.Context, r writer.Read) error {
		return f.g.Require(ctx, r, board, inc, actor, "read", "export")
	}})
	if err != nil {
		t.Fatal("export retained lifecycle", err)
	}
	var archive bytes.Buffer
	if err = prepared.Write(tc(t), &archive); err != nil {
		t.Fatal(err)
	}
	source, err := V.Read(tc(t), bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal("validate retained lifecycle", err)
	}
	restored, err := R.Prepare(tc(t), source, now)
	if err != nil {
		t.Fatal("prepare retained lifecycle", err)
	}
	destination := filepath.Join(os.Getenv("S01_TEST_ROOT"), "m9d-retention-restore-"+nonce(t)[:8])
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.CreateDirectory(windows.StringToUTF16Ptr(destination), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	if err = restored.Construct(tc(t), destination); err != nil {
		t.Fatal("native restore lifecycle", err)
	}
	if _, err = restored.NativeInventory(tc(t), destination); err != nil {
		t.Fatal("native inventory lifecycle", err)
	}
	receipt, err := restored.Receipt("90000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	err = S.InspectExisting(tc(t), destination, schema.Identity{BoardID: string(receipt.Destination.BoardID), Incarnation: string(receipt.Destination.Incarnation)}, func(r S.InspectionRead) error {
		assertRetentionValues(t, r.Scan, add, metadata, !compacted)
		expectedRows := 1
		if compacted {
			expectedRows = 0
		}
		for query, want := range map[string]int{
			"SELECT count(*) FROM blob":                                                             0,
			"SELECT count(*) FROM attachment":                                                       expectedRows,
			"SELECT count(*) FROM current_attachment_pin":                                           0,
			"SELECT count(*) FROM history_blob_pin":                                                 0,
			"SELECT count(*) FROM operation_blob_pin":                                               0,
			"SELECT count(*) FROM byte_fact":                                                        1,
			"SELECT count(*) FROM operation WHERE command='attachment.detach' AND current_ledger=0": 1,
			"SELECT count(*) FROM operation_full f JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.command='attachment.detach'": expectedRows,
			"SELECT count(*) FROM entity WHERE kind='attachment' AND removed_at IS NOT NULL":                                                                      1 - expectedRows,
			"SELECT count(*) FROM current_reference WHERE kind='attachment'":                                                                                      0,
			"SELECT count(*) FROM byte_fact f JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.command='attachment.detach'":      0,
		} {
			var got int
			if err := r.Scan(query, nil, &got); err != nil {
				return err
			}
			if got != want {
				t.Fatal("restored lifecycle disagreement", query, got, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Count and compare the source-qualified compact add fact independently of
// metadata. The second phase intentionally has no full attachment row to
// detect a substituted digest or length on behalf of this assertion.
func assertRetentionValues(t *testing.T, scan func(string, []any, ...any) error, add T.Terminal, expected A.Attachment, full bool) {
	t.Helper()
	var count int
	if err := scan("SELECT count(*) FROM byte_fact", nil, &count); err != nil || count != 1 {
		t.Fatal("retained byte fact cardinality", err)
	}
	var origin A.Identity
	var op A.UUID
	var fact A.ByteFact
	if err := scan("SELECT origin_board,origin_incarnation,op_id,sha256,byte_length FROM byte_fact", nil, &origin.BoardID, &origin.Incarnation, &op, &fact.SHA256, &fact.ByteLength); err != nil {
		t.Fatal(err)
	}
	if len(add.ByteFacts) != 1 || origin.BoardID != add.BoardID || origin.Incarnation != add.Incarnation || op != add.OpID || fact != add.ByteFacts[0] {
		t.Fatal("exact source-qualified add byte fact changed")
	}
	if !full {
		return
	}
	var actual A.Attachment
	if err := scan("SELECT a.attachment_id,a.card_id,a.name,a.media_label,a.sha256,a.byte_length,a.created_at,e.origin_board,e.origin_incarnation FROM attachment a JOIN entity e ON e.incarnation=a.incarnation AND e.kind='attachment' AND e.entity_id=a.attachment_id WHERE a.attachment_id=?", []any{string(expected.AttachmentID)}, &actual.AttachmentID, &actual.CardID, &actual.Name, &actual.MediaLabel, &actual.SHA256, &actual.ByteLength, &actual.CreatedAt, &actual.CreatedIn.BoardID, &actual.CreatedIn.Incarnation); err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatal("full immutable attachment metadata changed")
	}
}
