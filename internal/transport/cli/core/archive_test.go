package core

import (
	"bytes"
	"context"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestArchiveAliasesRequireMatchingExclusiveFlag(t *testing.T) {
	for _, tc := range []struct {
		action, input string
		valid         bool
	}{{"archive", `{"archived":true}`, true}, {"unarchive", `{"archived":false}`, true}, {"archive", `{"archived":false}`, false}, {"unarchive", `{}`, false}, {"archive", `{"archived":null}`, false}, {"archive", `{"archived":"true"}`, false}, {"archive", `{"archived":true,"title":"x"}`, false}} {
		if archiveAlias([]byte(`{"input":`+tc.input+`}`), tc.action == "archive") != tc.valid {
			t.Fatal("alias mismatch")
		}
		if !tc.valid {
			p := &provider{}
			var out bytes.Buffer
			exit := (Runner{Provider: p}).Run(context.Background(), args("card", tc.action, testActor, "--input", "-"), strings.NewReader(`{"input":`+tc.input+`}`), &out, io.Discard)
			if exit != 2 || p.opened != 0 {
				t.Fatal("bad alias opened provider")
			}
		}
	}
}
func TestCardListGrammarAndActiveReadableProjection(t *testing.T) {
	q, code, _ := parse(args("card", "list", "--archive", "archived", "--cursor", "opaque"))
	if code != "" || q.path != "/api/v1/boards/"+testBoard+"/cards?archive=archived&cursor=opaque" {
		t.Fatal("listing path")
	}
	for _, tail := range [][]string{{"--archive", ""}, {"--archive", "Active"}, {"--archive", "all", "--archive", "active"}, {"--query", "text"}, {"--cursor", ""}, {"--input", "-"}} {
		if _, code, _ := parse(args(append([]string{"card", "list"}, tail...)...)); code == "" {
			t.Fatal("invalid listing flag accepted")
		}
	}
	b := advisoryFixture(t)
	b.Result.Cards[0].Archived = true
	b.Result.Cards[1].Priority = A.Priority("P0")
	text, _ := focusedRead(b)
	if strings.Contains(text, "#1 Alpha (P3; Ready for Review)\n") || !strings.Contains(text, "#2 Beta") {
		t.Fatal("active projection")
	}
	if !b.Result.Cards[0].Archived || len(b.Result.Cards) != 2 {
		t.Fatal("projection mutated source")
	}
}

func TestCardListValidatesCanonicalEchoAndRefusal(t *testing.T) {
	for _, mode := range []string{"valid", "mismatch", "stale"} {
		view := T.CardSearchRead{APIVersion: 1, Scope: "card-search", Result: T.CardSearchView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, Revision: 0, Query: T.CardSearchQuery{Archive: "active"}, Items: []T.CardSummary{}}}
		if mode == "mismatch" {
			view.Result.Query.Archive = "all"
		}
		wire, err := T.Readable(view)
		if err != nil {
			t.Fatal(err)
		}
		c := connection(t, func(req *http.Request) (int, []byte, error) {
			if req.URL.Path != "/api/v1/boards/"+testBoard+"/cards" || req.URL.Query().Get("archive") != "active" {
				t.Fatal("listing request")
			}
			if mode == "stale" {
				b, _ := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "CURSOR_STALE", Message: "Request refused"}})
				return 409, b, nil
			}
			return 200, wire, nil
		})
		var out bytes.Buffer
		exit := (Runner{Provider: &provider{conn: c}}).Run(context.Background(), args("card", "list"), nil, &out, io.Discard)
		want := 0
		if mode == "mismatch" {
			want = 3
		}
		if mode == "stale" {
			want = 2
		}
		if exit != want {
			t.Fatal("listing response classification")
		}
		if mode == "stale" && !strings.Contains(out.String(), "CURSOR_STALE") {
			t.Fatal("stale category lost")
		}
	}
}

func TestReadableCardListIncludesOwnerAndCardRevision(t *testing.T) {
	owner := A.UUID("00000000-0000-4000-8000-000000000099")
	page := T.CardSearchRead{APIVersion: 1, Scope: "card-search", Result: T.CardSearchView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, Revision: 4, Query: T.CardSearchQuery{Archive: "archived"}, Items: []T.CardSummary{{CardID: "00000000-0000-4000-8000-000000000088", Ticket: 2, Revision: 3, Title: "Retained", Lane: "Ready for Review", Priority: "P2", OwnerID: &owner, Archived: true}}}}
	text, ok := focusedRead(page)
	if !ok || !strings.Contains(text, "owner: "+string(owner)) || !strings.Contains(text, "card revision: 3") || !strings.Contains(text, "archived: true") {
		t.Fatal("readable summary omitted committed facts")
	}
	page.Result.Items[0].OwnerID = nil
	text, _ = focusedRead(page)
	if !strings.Contains(text, "owner: Unassigned") {
		t.Fatal("unassigned readable summary")
	}
}
