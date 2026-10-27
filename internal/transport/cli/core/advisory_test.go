package core

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func advisoryFixture(t *testing.T) T.BoardRead {
	t.Helper()
	b, err := T.Decode[T.BoardRead](fixture(t, "board-read.json"))
	if err != nil {
		t.Fatal(err)
	}
	b.Result.Cards[0].Title = "Alpha"
	b.Result.Cards[0].Lane = "Ready for Review"
	beta := b.Result.Cards[0]
	beta.CardID = "00000000-0000-4000-8000-000000000022"
	beta.Ticket, beta.Title, beta.Priority, beta.Lane = 2, "Beta", "P0", "Ready for Work"
	beta.CommentIDs, beta.AttachmentIDs = []A.UUID{}, []A.UUID{}
	link := A.Link{LinkID: "00000000-0000-4000-8000-000000000023", CardID: beta.CardID, Kind: "blocking", Active: true, Target: A.CardRef{BoardID: b.Result.Identity.BoardID, Incarnation: b.Result.Identity.Incarnation, CardID: b.Result.Cards[0].CardID}, CreatedIn: b.Result.Identity, CreatedAt: "2026-10-01T00:00:00Z"}
	beta.LinkIDs = []A.UUID{link.LinkID}
	b.Result.Cards = append(b.Result.Cards, beta)
	b.Result.Links = []A.Link{link}
	return b
}

func acceptedAdvisoryFixture(t *testing.T) T.BoardRead {
	b := advisoryFixture(t)
	id := A.UUID("00000000-0000-4000-8000-000000000024")
	b.Result.Cards[0].Lane, b.Result.Cards[0].CurrentApprovalID = "Completed", &id
	b.Result.Approvals = []A.Approval{{ApprovalID: id, CardID: b.Result.Cards[0].CardID, ActorID: b.Result.Actors[0].ActorID, ReviewedCardRevision: b.Result.Cards[0].Revision - 1, ReviewedBoardRevision: b.Result.Revision, PolicyVersion: b.Result.PolicyVersion, CreatedIn: b.Result.Identity, CreatedAt: "2026-10-01T00:00:00Z"}}
	return b
}

func TestDependencyAdvisoryReadinessBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*T.BoardView)
		satisfied bool
		fact      string
	}{
		{"accepted", func(*T.BoardView) {}, true, "#1 Alpha (P3; Completed) — satisfied"},
		{"review handoff", func(b *T.BoardView) { b.Cards[0].Lane = "Ready for Review" }, false, "awaiting human acceptance"},
		{"archived", func(b *T.BoardView) { b.Cards[0].Archived = true }, false, "; archived)"},
		{"policy", func(b *T.BoardView) { b.Approvals[0].PolicyVersion++ }, false, "not satisfied"},
		{"revision", func(b *T.BoardView) { b.Cards[0].Revision++ }, false, "not satisfied"},
		{"approval pointer", func(b *T.BoardView) { b.Cards[0].CurrentApprovalID = nil }, false, "not satisfied"},
		{"approval card", func(b *T.BoardView) { b.Approvals[0].CardID = b.Cards[1].CardID }, false, "not satisfied"},
		{"bot", func(b *T.BoardView) { b.Actors[0].Kind = "bot" }, false, "not satisfied"},
		{"missing actor", func(b *T.BoardView) { b.Actors = nil }, false, "not satisfied"},
		{"foreign board same UUID", func(b *T.BoardView) { b.Links[0].Target.BoardID = "00000000-0000-4000-8000-000000000099" }, false, "unavailable or foreign target"},
		{"foreign incarnation same UUID", func(b *T.BoardView) { b.Links[0].Target.Incarnation = "00000000-0000-4000-8000-000000000099" }, false, "unavailable or foreign target"},
		{"missing target", func(b *T.BoardView) { b.Links[0].Target.CardID = "00000000-0000-4000-8000-000000000099" }, false, "unavailable or foreign target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := acceptedAdvisoryFixture(t)
			tc.change(&b.Result)
			out := boardAdvisory(b.Result)
			if strings.Contains(out, "Prerequisites satisfied (advisory;") != tc.satisfied || !strings.Contains(out, tc.fact) {
				t.Fatalf("incorrect readiness: %s", out)
			}
			if b.Result.Cards[0].Priority != "P3" || b.Result.Cards[1].Priority != "P0" {
				t.Fatal("changed priority")
			}
			if strings.Contains(tc.name, "foreign") && strings.Contains(out, "Dependency pressure: #2") {
				t.Fatal("foreign UUID created incoming pressure")
			}
		})
	}
}

func TestDependencyAdvisoryDirectionRemovalAndInertText(t *testing.T) {
	b := advisoryFixture(t)
	out := boardAdvisory(b.Result)
	if !strings.Contains(out, "#1 Alpha (P3; Ready for Review)\n  Dependency pressure: #2 Beta (P0; Ready for Work)") || !strings.Contains(out, "Prerequisites/readiness: #1 Alpha (P3; Ready for Review) — awaiting human acceptance (not satisfied)") {
		t.Fatal("wrong blocking direction")
	}
	for _, change := range []func(*A.Link){func(l *A.Link) { l.Active = false }, func(l *A.Link) { l.Kind = "informational" }} {
		b := advisoryFixture(t)
		change(&b.Result.Links[0])
		out := boardAdvisory(b.Result)
		if strings.Count(out, "Dependency pressure: none.") != 2 || strings.Count(out, "Prerequisites/readiness: none.") != 2 {
			t.Fatal("inactive/informational link contributed")
		}
	}
	b.Result.Cards[0].Title = "Alpha\n\x1b[31m\u202e"
	out = boardAdvisory(b.Result)
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\u202e") || !strings.Contains(out, `Alpha\n\x1b[31m\u202e`) {
		t.Fatal("active terminal controls")
	}
}

func TestDependencyAdvisoryOnlyDirectRelationships(t *testing.T) {
	b := advisoryFixture(t)
	gamma := b.Result.Cards[1]
	gamma.CardID, gamma.Ticket, gamma.Title = "00000000-0000-4000-8000-000000000025", 3, "Gamma"
	b.Result.Cards = append(b.Result.Cards, gamma)
	link := b.Result.Links[0]
	link.CardID, link.Target.CardID = gamma.CardID, b.Result.Cards[1].CardID
	b.Result.Links = append(b.Result.Links, link)
	out := boardAdvisory(b.Result)
	alpha := strings.Split(out, "\n#2 Beta")[0]
	if strings.Contains(alpha, "Gamma") || !strings.Contains(alpha, "Dependency pressure: #2 Beta") {
		t.Fatal("transitive pressure or omitted direct dependent")
	}
	gammaRows := strings.Split(out, "\n#3 Gamma")[1]
	if strings.Contains(gammaRows, "Alpha") || !strings.Contains(gammaRows, "Prerequisites/readiness: #2 Beta") {
		t.Fatal("transitive prerequisite or omitted direct target")
	}
}

func TestDependencyAdvisoryOrdinaryOutputAndExactJSON(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		b := advisoryFixture(t)
		if accepted {
			b = acceptedAdvisoryFixture(t)
		}
		wire, err := T.Readable(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, jsonMode := range []bool{false, true} {
			c := connection(t, func(*http.Request) (int, []byte, error) { return 200, wire, nil })
			a := args("board", "read")
			if !jsonMode {
				a = a[1:]
			}
			var out bytes.Buffer
			if exit := (Runner{Provider: &provider{conn: c}}).Run(context.Background(), a, nil, &out, io.Discard); exit != 0 {
				t.Fatalf("exit %d", exit)
			}
			if jsonMode {
				if !bytes.Equal(out.Bytes(), wire) {
					t.Fatal("JSON changed")
				}
			} else {
				want := "confirmed board " + testBoard + "\n" + string(wire) + boardAdvisory(b.Result)
				if out.String() != want {
					t.Fatal("ordinary advisory missing or altered envelope")
				}
			}
		}
	}
	// Partial or unavailable reads must never claim complete-board readiness.
	for _, tc := range []struct {
		cmd    []string
		status int
		file   string
		exit   int
	}{
		{[]string{"card", "read", "00000000-0000-4000-8000-00000000000b"}, 200, "card-read.json", 0},
		{[]string{"board", "read"}, 503, "unavailable-read.json", 3},
	} {
		c := connection(t, func(*http.Request) (int, []byte, error) { return tc.status, fixture(t, tc.file), nil })
		var out bytes.Buffer
		a := args(tc.cmd...)
		if exit := (Runner{Provider: &provider{conn: c}}).Run(context.Background(), a[1:], nil, &out, io.Discard); exit != tc.exit || strings.Contains(out.String(), "Dependency advisory") {
			t.Fatal("partial/unavailable advisory")
		}
	}
}
