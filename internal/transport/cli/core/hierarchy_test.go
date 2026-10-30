package core

import (
	"bytes"
	"context"
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"net/http"
	"strings"
	"testing"
)

func hierarchyBoard(t *testing.T, count int) (T.BoardRead, string) {
	b, e := T.Decode[T.BoardRead](fixture(t, "board-read.json"))
	if e != nil {
		t.Fatal(e)
	}
	root := b.Result.Cards[0]
	root.CommentIDs = []A.UUID{}
	root.LinkIDs = []A.UUID{}
	root.AttachmentIDs = []A.UUID{}
	root.CurrentApprovalID = nil
	root.OwnerID = nil
	root.Parent = nil
	b.Result.Comments = []A.Comment{}
	b.Result.Links = []A.Link{}
	b.Result.Attachments = []A.Attachment{}
	b.Result.Approvals = []A.Approval{}
	b.Result.Cards = []A.Card{root}
	for i := 1; i <= count; i++ {
		c := root
		c.CardID = A.UUID(fmt.Sprintf("70000000-0000-4000-8000-%012d", i))
		c.Ticket = A.Positive(i + 1)
		c.Title = fmt.Sprintf("Child %d", i)
		c.Archived = i%2 == 0
		c.Parent = &A.CardRef{BoardID: b.Result.Identity.BoardID, Incarnation: b.Result.Identity.Incarnation, CardID: root.CardID}
		b.Result.Cards = append(b.Result.Cards, c)
	}
	return b, string(root.CardID)
}
func TestHierarchyCLICompleteJSONAndReadableLocalPages(t *testing.T) {
	for _, count := range []int{0, 20, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			b, root := hierarchyBoard(t, count)
			wire, e := T.Readable(b)
			if e != nil {
				t.Fatal(e)
			}
			r := Runner{Provider: &provider{conn: connection(t, func(req *http.Request) (int, []byte, error) {
				if req.URL.Path != "/api/v1/boards/"+testBoard {
					t.Fatal("new graph route used")
				}
				return 200, wire, nil
			})}}
			raw := run(t, r, args("card", "children", root), nil, 0)
			h, e := A.Decode[HierarchyRead](raw)
			if e != nil || len(h.Result.Nodes) != count || !h.Result.Complete || int(h.Result.DescendantCount) != count {
				t.Fatalf("incomplete JSON %v %s", e, raw)
			}
			a := args("card", "children", root)
			a = a[1:]
			var out, diagnostic bytes.Buffer
			if exit := r.Run(context.Background(), a, nil, &out, &diagnostic); exit != 0 {
				t.Fatal("readable", exit)
			}
			if !strings.Contains(out.String(), fmt.Sprintf("of %d; page 1 of", count)) {
				t.Fatal("range missing")
			}
			if count == 21 {
				a = append(a, "--page", "2")
				out.Reset()
				if exit := r.Run(context.Background(), a, nil, &out, &diagnostic); exit != 0 || !strings.Contains(out.String(), "Rows 21-21 of 21; page 2 of 2") {
					t.Fatal("last page")
				}
			}
			run(t, r, args("card", "children", root, "--page", "1"), nil, 1)
			a = args("card", "children", root, "--page", "99")
			a = a[1:]
			if exit := r.Run(context.Background(), a, nil, &out, &diagnostic); exit != 1 {
				t.Fatal("out of range successful")
			}
		})
	}
}
