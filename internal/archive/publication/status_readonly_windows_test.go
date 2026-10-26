//go:build windows && amd64

package publication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
)

func statusTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = fmt.Sprintf("%d:%x", len(raw), sha256.Sum256(raw))
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func TestNativeStatusNeverRepairsOrphanAndRepeatedLookupStaysUnknown(t *testing.T) {
	for _, invalidResult := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid-result-%t", invalidResult), func(t *testing.T) {
			n, q, source := actualSource(t)
			if got := Restore(ctx(t), q); got.Exit != 0 {
				t.Fatal("restore", got.Exit)
			}
			r, e := n.receipt(q.Destination)
			if e != nil {
				t.Fatal(e)
			}
			s, e := S.Existing(ctx(t), filepath.Join(q.Destination, "data"), schema.Identity{BoardID: string(r.Destination.BoardID), Incarnation: string(r.Destination.Incarnation)})
			if e != nil {
				t.Fatal(e)
			}
			l, e := s.Writer(ctx(t))
			if e != nil {
				t.Fatal(e)
			}
			orphan := []byte("unprotected evidence must survive observational native status")
			digest := fmt.Sprintf("%x", sha256.Sum256(orphan))
			e = l.Do(ctx(t), func(c *sql.Conn) error {
				if _, e := c.ExecContext(ctx(t), "INSERT INTO blob(incarnation,sha256,byte_length,bytes) VALUES(?,?,?,?)", r.Destination.Incarnation, digest, len(orphan), orphan); e != nil {
					return e
				}
				if invalidResult {
					var inc, board, origin, op, intent string
					if e := c.QueryRowContext(ctx(t), "SELECT f.incarnation,f.origin_board,f.origin_incarnation,f.op_id,f.intent FROM operation_full f WHERE NOT EXISTS(SELECT 1 FROM operation_blob_pin p WHERE p.incarnation=f.incarnation AND p.origin_board=f.origin_board AND p.origin_incarnation=f.origin_incarnation AND p.op_id=f.op_id) LIMIT 1").Scan(&inc, &board, &origin, &op, &intent); e != nil {
						return e
					}
					if _, e := c.ExecContext(ctx(t), "BEGIN IMMEDIATE"); e != nil {
						return e
					}
					if _, e := c.ExecContext(ctx(t), "DELETE FROM operation_full WHERE incarnation=? AND origin_board=? AND origin_incarnation=? AND op_id=?", inc, board, origin, op); e != nil {
						return e
					}
					if _, e := c.ExecContext(ctx(t), "INSERT INTO operation_full VALUES(?,?,?,?,?,'{}')", inc, board, origin, op, intent); e != nil {
						return e
					}
					if _, e := c.ExecContext(ctx(t), "COMMIT"); e != nil {
						return e
					}
				}
				return nil
			})
			ce := l.Close()
			se := s.Close()
			if e != nil || ce != nil || se != nil {
				t.Fatal(e, ce, se)
			}
			root := filepath.Dir(filepath.Dir(q.Destination))
			before := statusTree(t, root)
			for attempt := 0; attempt < 2; attempt++ {
				if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 4 {
					t.Fatal("orphan was accepted or repaired", attempt, got.Exit)
				}
				if after := statusTree(t, root); !reflect.DeepEqual(before, after) {
					t.Fatal("status changed candidate/source bytes or filesystem inventory", attempt)
				}
			}
			after, e := os.ReadFile(q.SourceArchive)
			if e != nil || !bytes.Equal(source, after) {
				t.Fatal("status changed source archive")
			}
		})
	}
}

func TestNativeStatusRefusalsAndCancellationPreserveEntireTree(t *testing.T) {
	for _, kind := range []string{"malformed-receipt", "missing-lock", "incomplete", "cancelled", "receipt-read-denied"} {
		t.Run(kind, func(t *testing.T) {
			n, q, _ := actualSource(t)
			if kind == "incomplete" {
				if got := restoreWithFault(ctx(t), q, time.Now, func(stage string) error {
					if stage == "after-prepared" {
						return context.Canceled
					}
					return nil
				}); got.Exit != 4 {
					t.Fatal("incomplete setup", got.Exit)
				}
			} else {
				if got := Restore(ctx(t), q); got.Exit != 0 {
					t.Fatal("restore", got.Exit)
				}
			}
			var denied *os.File
			switch kind {
			case "malformed-receipt":
				if e := os.WriteFile(filepath.Join(q.Destination, receiptName), []byte("{malformed\n"), 0600); e != nil {
					t.Fatal(e)
				}
			case "missing-lock":
				if e := os.Remove(filepath.Join(q.Destination, "data", "board.lock")); e != nil {
					t.Fatal(e)
				}
			case "receipt-read-denied":
				var e error
				denied, e = n.file(filepath.Join(q.Destination, receiptName), true, false, true, false)
				if e != nil {
					t.Fatal(e)
				}
			}
			root := filepath.Dir(filepath.Dir(q.Destination))
			before := statusTree(t, root)
			for attempt := 0; attempt < 2; attempt++ {
				callCtx := ctx(t)
				if kind == "cancelled" {
					cancelled, cancel := context.WithCancel(callCtx)
					cancel()
					callCtx = cancelled
				}
				got := Status(callCtx, q.Destination, q.RequestID)
				if got.Exit != 4 {
					t.Fatal("unavailable evidence confirmed", kind, got.Exit)
				}
				if after := statusTree(t, root); !reflect.DeepEqual(before, after) {
					t.Fatal("observational status changed files", kind)
				}
			}
			if denied != nil {
				if e := denied.Close(); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
