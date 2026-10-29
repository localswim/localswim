package core

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestFocusedReadsHumanAndJSON(t *testing.T) {
	for _, kind := range []string{"read", "history"} {
		name := "card-read.json"
		if kind == "history" {
			name = "history-read.json"
		}
		wire := fixture(t, name)
		r := Runner{Provider: &provider{conn: connection(t, func(req *http.Request) (int, []byte, error) { return 200, wire, nil })}}
		a := args("card", kind, "00000000-0000-4000-8000-00000000000b")
		jsonOut := run(t, r, a, nil, 0)
		var expected any
		if kind == "history" {
			v, e := T.Decode[T.HistoryRead](wire)
			if e != nil {
				t.Fatal(e)
			}
			expected = v
		} else {
			v, e := T.Decode[T.CardRead](wire)
			if e != nil {
				t.Fatal(e)
			}
			expected = v
		}
		want, _ := T.Readable(expected)
		if !bytes.Equal(jsonOut, want) {
			t.Fatal("JSON envelope changed")
		}
		a = append([]string(nil), a[1:]...)
		var stdout, stderr bytes.Buffer
		if exit := r.Run(context.Background(), a, nil, &stdout, &stderr); exit != 0 {
			t.Fatalf("human read exit %d", exit)
		}
		text := stdout.String()
		for _, s := range []string{"Board revision: 5", "Synthetic reviewer 雪", "2026-09-01T00:00:00Z", "Comments returned: 1", "Coverage gaps returned: 3", "00000000-0000-4000-8000-000000000003"} {
			if !strings.Contains(text, s) {
				t.Fatalf("%s human output missing %q", kind, s)
			}
		}
		if kind == "read" {
			for _, s := range []string{"Committed current card #1: Alpha 雪", "Owner: none", "explicit priority: P3", "Description:\n  | Reviewed é\\r\n  |", "old state text unavailable"} {
				if !strings.Contains(text, s) {
					t.Fatalf("card missing %q", s)
				}
			}
		} else {
			for _, s := range []string{"Current state is separate", "Prior states returned: 3", "Old state text unavailable.", "Removal: cleared", "First draft", "Returned referenced facts (not current card collections)"} {
				if !strings.Contains(text, s) {
					t.Fatalf("history missing %q", s)
				}
			}
			p2 := strings.Index(text, "/00000000-0000-4000-8000-00000000000b revision 2")
			p1 := strings.Index(text, "/00000000-0000-4000-8000-00000000000b revision 1")
			if p2 < 0 || p1 < p2 {
				t.Fatal("foreign-origin revisions not descending")
			}
		}
	}
}

func TestFocusedReadTerminalSafetyAndOrdering(t *testing.T) {
	x, err := T.Decode[T.CardRead](fixture(t, "card-read.json"))
	if err != nil {
		t.Fatal(err)
	}
	x.Result.Card.Title = "<script> 船 \x1b[31m\u202e"
	x.Result.Card.Description = "first\nsecond\rthird\t\x00"
	owner := x.Result.Actors[0].ActorID
	x.Result.Card.OwnerID = &owner
	x.Result.Actors[0].Label = "Reviewer\x1b]0;spoof\a\u2066"
	c := x.Result.Comments[0]
	c.CommentID = A.UUID("00000000-0000-4000-8000-000000000009")
	c.Text = "earlier tie\nconfirmed board forged"
	x.Result.Comments = append(x.Result.Comments, c)
	text, ok := focusedRead(x)
	if !ok {
		t.Fatal("not rendered")
	}
	for _, bad := range []string{"\x1b", "\u202e", "\u2066", "\a", "\x00", "\r", "\t", "\nconfirmed board forged"} {
		if strings.Contains(text, bad) {
			t.Fatalf("raw control or unprefixed prose %q", bad)
		}
	}
	for _, s := range []string{"<script> 船", "\\x1b", "\\u202e", "first\n  | second\\rthird\\t\\x00", "Owner: Reviewer", "  | confirmed board forged"} {
		if !strings.Contains(text, s) {
			t.Fatalf("missing safe text %q", s)
		}
	}
	if strings.Index(text, "earlier tie") > strings.Index(text, "Synthetic check") {
		t.Fatal("comment ID tie order")
	}
}

func TestFocusedReadEmptyUnavailableAndForeignOrdering(t *testing.T) {
	x, err := T.Decode[T.CardRead](fixture(t, "card-read.json"))
	if err != nil {
		t.Fatal(err)
	}
	x.Result.Card.Description = ""
	x.Result.Comments = nil
	x.Result.HistoryReferences = nil
	text, _ := focusedRead(x)
	for _, s := range []string{"Description: empty", "Comments returned: 0", "Prior state references returned: 0"} {
		if !strings.Contains(text, s) {
			t.Fatalf("missing %q", s)
		}
	}
	if _, ok := focusedRead(refused("UNAVAILABLE", 3, nil).value); ok {
		t.Fatal("refusal rendered as empty read")
	}
	a := A.Identity{BoardID: "b", Incarnation: "2"}
	b := A.Identity{BoardID: "a", Incarnation: "9"}
	current := A.Identity{BoardID: "z", Incarnation: "0"}
	if !priorLess(current, current, 1, b, 999) || !priorLess(current, b, 1, a, 999) || !priorLess(current, a, 9, a, 2) {
		t.Fatal("provenance ordering compares unlike revisions")
	}
}

func TestFocusedReadFailureRemainsUnavailable(t *testing.T) {
	r := Runner{Provider: &provider{conn: connection(t, func(req *http.Request) (int, []byte, error) { return 503, fixture(t, "unavailable-read.json"), nil })}}
	a := args("card", "history", "00000000-0000-4000-8000-00000000000b")[1:]
	var stdout, stderr bytes.Buffer
	if exit := r.Run(context.Background(), a, nil, &stdout, &stderr); exit != 3 {
		t.Fatalf("exit %d", exit)
	}
	if strings.Contains(stdout.String(), "Prior states returned:") || strings.Contains(stdout.String(), "Comments returned:") {
		t.Fatal("failure shown as empty collection")
	}
}

func TestFocusedReadReturnsAll25CommentsWithoutClipping(t *testing.T) {
	x, err := T.Decode[T.CardRead](fixture(t, "card-read.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := x.Result.Comments[0]
	x.Result.Comments = nil
	x.Result.Card.CommentIDs = nil
	for i := 1; i <= 25; i++ {
		c := base
		c.CommentID = A.UUID(fmt.Sprintf("10000000-0000-4000-8000-%012d", i))
		c.Text = fmt.Sprintf("Comment %02d café 船", i)
		x.Result.Comments = append(x.Result.Comments, c)
		x.Result.Card.CommentIDs = append(x.Result.Card.CommentIDs, c.CommentID)
	}
	wire, err := T.Readable(x)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the ordinary Runner decode and output boundary with a complete
	// synthetic read collection, rather than a display-only shortened page.
	r := Runner{Provider: &provider{conn: connection(t, func(*http.Request) (int, []byte, error) { return 200, wire, nil })}}
	var stdout, stderr bytes.Buffer
	a := args("card", "read", string(x.Result.Card.CardID))[1:]
	if exit := r.Run(context.Background(), a, nil, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, "Comments returned: 25") {
		t.Fatal("returned total missing")
	}
	last := -1
	for i := 1; i <= 25; i++ {
		needle := fmt.Sprintf("Comment %02d café 船", i)
		pos := strings.Index(text, needle)
		if pos <= last || strings.Count(text, needle) != 1 {
			t.Fatalf("comment %d lost, duplicated or unordered", i)
		}
		last = pos
	}
}

func TestFocusedReadSelectorsStayCanonicalWithoutPaging(t *testing.T) {
	for _, command := range [][]string{
		{"card", "read", "1"},
		{"card", "history", "00000000-0000-4000-8000-00000000000B"},
		{"card", "history", "00000000-0000-4000-8000-00000000000b", "--page", "2"},
	} {
		p := &provider{}
		run(t, Runner{Provider: p}, args(command...), nil, 1)
		if p.opened != 0 {
			t.Fatal("invalid selector reached authority")
		}
	}
}
