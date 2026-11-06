//go:build windows && amd64

package attachments

import (
	"bytes"
	"database/sql"
	"errors"
	T "github.com/localswim/localswim/internal/contract/transport"
	"strings"
	"testing"
)

func TestCardFilesLegacyLabelBytePaging(t *testing.T) {
	for _, test := range []struct {
		name              string
		size, count, want int
	}{{"20KiB", 20 * 1024, 1, 1}, {"fifty5000", 5000, 50, 50}, {"600KiB", 600 * 1024, 3, 1}, {"2MiB", 2 * 1024 * 1024, 2, 1}} {
		t.Run(test.name, func(t *testing.T) {
			f := fresh(t)
			for i := 0; i < test.count; i++ {
				data := []byte{byte(i)}
				r, _ := f.add(t, f.upload(t, data, uint64(i+1)), data)
				if r.Terminal == nil || r.Terminal.Outcome != "committed" {
					t.Fatal("add fixture")
				}
			}
			label := strings.Repeat("<", test.size)
			f.raw(t, func(c *sql.Conn) error {
				_, e := c.ExecContext(tc(t), "UPDATE actor SET label=? WHERE actor_id=?", label, actor)
				return e
			})
			query := ListQuery{View: "current", Limit: 100}
			seen := map[string]bool{}
			pages := 0
			for {
				page, e := f.a.List(tc(t), f.g, board, inc, actor, f.card, query)
				if e != nil {
					t.Fatal(e)
				}
				if len(page.Result.Items) != test.want {
					t.Fatal("page size", len(page.Result.Items))
				}
				wire, e := T.FileReadBytes(page)
				if e != nil {
					t.Fatal(e)
				}
				size, e := T.FileReadSize(page)
				if e != nil || size != int64(len(wire)) {
					t.Fatal("size accounting")
				}
				if test.name == "fifty5000" && (len(wire) <= 256*1024 || len(wire) > T.FileReadPageTarget) {
					t.Fatal("legacy page bound", len(wire))
				}
				if bytes.Contains(wire, []byte(`\u003c`)) {
					t.Fatal("optional HTML escaping")
				}
				for _, file := range page.Result.Items {
					id := string(file.Attachment.AttachmentID)
					if seen[id] || *file.Attribution.ActorLabel != label {
						t.Fatal("exact label/page conservation")
					}
					seen[id] = true
				}
				pages++
				if page.Result.NextCursor == nil {
					break
				}
				query.Cursor = *page.Result.NextCursor
				if pages > test.count {
					t.Fatal("cursor cycle")
				}
			}
			if len(seen) != test.count {
				t.Fatal("lost items")
			}
		})
	}
}
func TestCardFilesLabelPreflightUTF8AndNUL(t *testing.T) {
	for _, mode := range []string{"ascii", "utf8", "nul"} {
		t.Run(mode, func(t *testing.T) {
			f := fresh(t)
			data := []byte("x")
			r, _ := f.add(t, f.upload(t, data, 1), data)
			id := ""
			for _, ref := range r.Terminal.ResultRefs {
				if ref.Kind == "attachment" {
					id = string(ref.ID)
				}
			}
			label := strings.Repeat("a", T.FileActorLabelLimit+1)
			if mode == "utf8" {
				label = strings.Repeat("é", T.FileActorLabelLimit/2) + "é"
			}
			if mode == "nul" {
				label = "prefix\x00" + label
			}
			f.raw(t, func(c *sql.Conn) error {
				_, e := c.ExecContext(tc(t), "UPDATE actor SET label=? WHERE actor_id=?", label, actor)
				return e
			})
			if _, e := f.a.Inspect(tc(t), f.g, board, inc, actor, f.card, id); !errors.Is(e, ErrUnavailable) {
				t.Fatal("oversized label accepted", e)
			}
			if _, e := f.a.List(tc(t), f.g, board, inc, actor, f.card, ListQuery{View: "current", Limit: 50}); !errors.Is(e, ErrUnavailable) {
				t.Fatal("oversized list accepted", e)
			}
			f.raw(t, func(c *sql.Conn) error {
				var actual string
				if e := c.QueryRowContext(tc(t), "SELECT label FROM actor WHERE actor_id=?", actor).Scan(&actual); e != nil {
					return e
				}
				if actual != label {
					t.Fatal("stored label changed")
				}
				return nil
			})
		})
	}
}

func TestCardFilesNearArchiveLabel(t *testing.T) {
	f := fresh(t)
	data := []byte("legacy")
	r, _ := f.add(t, f.upload(t, data, 1), data)
	var id string
	for _, ref := range r.Terminal.ResultRefs {
		if ref.Kind == "attachment" {
			id = string(ref.ID)
		}
	}
	unit := "<>&\u2028\u2029"
	label := strings.Repeat(unit, (T.FileActorLabelLimit-1024)/len(unit))
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(tc(t), "UPDATE actor SET label=? WHERE actor_id=?", label, actor)
		return e
	})
	read, e := f.a.Inspect(tc(t), f.g, board, inc, actor, f.card, id)
	if e != nil || *read.Result.File.Attribution.ActorLabel != label {
		t.Fatal("accepted legacy label lost", e)
	}
	size, e := T.FileReadSize(read)
	if e != nil || size > T.FileReadResponseLimit || size <= int64(len(label)) {
		t.Fatal("legacy escaping bound", size, e)
	}
}
