//go:build windows && amd64

package attachments

import (
	"database/sql"
	"errors"
	"github.com/localswim/localswim/internal/board/grants"
	T "github.com/localswim/localswim/internal/contract/transport"
	"testing"
)

func TestCardFilesSnapshotAttributionAndRetainedFixture(t *testing.T) {
	f := fresh(t)
	for rev := uint64(1); rev <= 3; rev++ {
		data := []byte{byte(rev)}
		r, _ := f.add(t, f.upload(t, data, rev), data)
		if r.Terminal == nil || r.Terminal.Outcome != "committed" {
			t.Fatal("fixture add")
		}
	}
	q := ListQuery{View: "current", Limit: 1}
	first, e := f.a.List(tc(t), f.g, board, inc, actor, f.card, q)
	if e != nil || len(first.Result.Items) != 1 || first.Result.NextCursor == nil {
		t.Fatal("page", e)
	}
	if _, e := T.Readable(first); e != nil {
		t.Fatal("invalid read", e)
	}
	item := first.Result.Items[0]
	if item.Attribution.State != "known" || *item.Attribution.Basis != "operation" || string(*item.Attribution.ActorID) != actor {
		t.Fatal("operation attribution")
	}
	inspected, e := f.a.Inspect(tc(t), f.g, board, inc, actor, f.card, string(item.Attachment.AttachmentID))
	if e != nil || inspected.Result.File.Attachment != item.Attachment {
		t.Fatal("inspect", e)
	}
	q.Cursor = *first.Result.NextCursor
	next, e := f.a.List(tc(t), f.g, board, inc, actor, f.card, q)
	if e != nil || len(next.Result.Items) != 1 || next.Result.Items[0].Attachment.AttachmentID == item.Attachment.AttachmentID {
		t.Fatal("next", e)
	}
	q.View = "retained"
	if _, e = f.a.List(tc(t), f.g, board, inc, actor, f.card, q); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("view binding", e)
	}
	// Controlled physical10 fixture models retained membership before detach exists.
	// It changes only the selected current reference/pin, retaining immutable metadata.
	f.raw(t, func(c *sql.Conn) error {
		for _, query := range []string{"DELETE FROM current_attachment_pin WHERE incarnation=? AND attachment_id=?", "DELETE FROM current_reference WHERE incarnation=? AND kind='attachment' AND entity_id=?"} {
			if _, e := c.ExecContext(tc(t), query, inc, string(item.Attachment.AttachmentID)); e != nil {
				return e
			}
		}
		return nil
	})
	retained, e := f.a.List(tc(t), f.g, board, inc, actor, f.card, ListQuery{View: "retained", Limit: 50})
	if e != nil || len(retained.Result.Items) != 1 || retained.Result.Items[0].DownloadState != "not_current" {
		t.Fatal("retained fixture", e)
	}
	if _, e = f.a.Read(tc(t), f.g, board, inc, actor, string(item.Attachment.AttachmentID)); !errors.Is(e, ErrNotFound) {
		t.Fatal("retained download", e)
	}
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(tc(t), "UPDATE board SET revision=revision+1")
		return e
	})
	q.View = "current"
	if _, e = f.a.List(tc(t), f.g, board, inc, actor, f.card, q); !errors.Is(e, ErrRevisionConflict) {
		t.Fatal("stale cursor", e)
	}
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(tc(t), "DELETE FROM grant_right WHERE right_name='read'")
		return e
	})
	if _, e = f.a.Inspect(tc(t), f.g, board, inc, actor, f.card, string(item.Attachment.AttachmentID)); !errors.Is(e, grants.ErrDenied) {
		t.Fatal("read authority", e)
	}
}
func TestCardFileRetainedAttributionEvidence(t *testing.T) {
	for _, mode := range []string{"compact", "source", "activity", "unknown", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			f := fresh(t)
			data := []byte("evidence")
			result, _ := f.add(t, f.upload(t, data, 1), data)
			var id string
			for _, ref := range result.Terminal.ResultRefs {
				if ref.Kind == "attachment" {
					id = string(ref.ID)
				}
			}
			op := string(result.Terminal.OpID)
			f.raw(t, func(c *sql.Conn) error {
				exec := func(q string, args ...any) error { _, e := c.ExecContext(tc(t), q, args...); return e }
				if mode == "compact" {
					for _, q := range []string{"DELETE FROM operation_blob_pin WHERE op_id=?", "DELETE FROM operation_full WHERE op_id=?", "UPDATE operation SET removed_at=terminal_at,removal_reason='expired',removal_revision=committed_revision WHERE op_id=?"} {
						if e := exec(q, op); e != nil {
							return e
						}
					}
				}
				if mode != "compact" {
					source := "20000000-0000-0000-0000-000000000099"
					freshID := "40000000-0000-0000-0000-000000000099"
					if e := exec("INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at) SELECT incarnation,kind,?,card_id,origin_board,?,creator_id,created_at FROM entity WHERE entity_id=?", freshID, source, id); e != nil {
						return e
					}
					if e := exec("INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) SELECT incarnation,?,card_id,name,media_label,sha256,byte_length,created_at FROM attachment WHERE attachment_id=?", freshID, id); e != nil {
						return e
					}
					id = freshID
					if mode == "source" {
						if e := exec("INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint_version,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision) SELECT incarnation,origin_board,?,op_id,0,actor_id,fingerprint_version,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,terminal_at,'expired',committed_revision FROM operation WHERE op_id=?", source, op); e != nil {
							return e
						}
						return exec("INSERT INTO operation_reference(incarnation,origin_board,origin_incarnation,op_id,kind,entity_id,revision) VALUES(?,?,?,?, 'attachment',?,NULL)", inc, board, source, op, id)
					}
					if mode == "activity" || mode == "conflict" {
						affected := `[ {"kind":"attachment","id":"` + id + `","revision":null},{"kind":"card","id":"` + f.card + `","revision":2} ]`
						if e := exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) SELECT incarnation,origin_board,?,event_id,actor_id,op_id,type,board_revision,at,? FROM activity WHERE op_id=?", source, affected, op); e != nil {
							return e
						}
						if mode == "conflict" {
							return exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) SELECT incarnation,origin_board,origin_incarnation,event_id+100,actor_id,op_id,type,board_revision,at,affected FROM activity WHERE op_id=? AND origin_incarnation=?", op, source)
						}
					}
				}
				return nil
			})
			read, e := f.a.Inspect(tc(t), f.g, board, inc, actor, f.card, id)
			if mode == "conflict" {
				if !errors.Is(e, ErrUnavailable) {
					t.Fatal("ambiguous attribution accepted", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			a := read.Result.File.Attribution
			if mode == "unknown" {
				if a.State != "unavailable" || a.ActorID != nil {
					t.Fatal("fallback creator invented")
				}
				return
			}
			basis := "operation"
			if mode == "activity" {
				basis = "activity"
			}
			if a.State != "known" || string(*a.Basis) != basis {
				t.Fatal("attribution", a.State)
			}
		})
	}
}
