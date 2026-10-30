package core

import (
	"bytes"
	"context"
	"encoding/json"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCardSearchGrammar(t *testing.T) {
	q, code, _ := parse(args("card", "search", "--query", " Café .* ", "--lane", "In Progress", "--owner", "unassigned", "--priority", "P1", "--archive", "all", "--cursor", "opaque"))
	if code != "" {
		t.Fatal("search grammar")
	}
	u, _ := url.Parse(q.path)
	v := u.Query()
	if v.Get("q") != " Café .* " || v.Get("lane") != "In Progress" || v.Get("owner") != "unassigned" || v.Get("priority") != "P1" || v.Get("archive") != "all" || v.Get("cursor") != "opaque" {
		t.Fatal("search fields lost")
	}
	for _, tail := range [][]string{{"--query", strings.Repeat("é", 513)}, {"--owner", ""}, {"--owner", "invalid"}, {"--lane", "ready"}, {"--priority", "P6"}, {"--query", "a", "--query", "b"}} {
		if _, code, _ := parse(args(append([]string{"card", "search"}, tail...)...)); code == "" {
			t.Fatal("invalid search flags accepted")
		}
	}
	q, code, _ = parse(args("card", "search"))
	if code != "" || q.query != "" || q.archive != "active" {
		t.Fatal("empty defaults")
	}
}

func TestCardSearchValidatesResponseAndRefusals(t *testing.T) {
	for _, mode := range []string{"valid", "query", "lane", "owner", "priority", "archive", "scope", "items-type", "stale", "invalid", "forbidden"} {
		lane := A.Lane("In Progress")
		priority := A.Priority("P1")
		owner := "unassigned"
		page := T.CardSearchRead{APIVersion: 1, Scope: "card-search", Result: T.CardSearchView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, Revision: 0, Query: T.CardSearchQuery{Q: " Café ", Lane: &lane, Owner: &owner, Priority: &priority, Archive: "all"}, Items: []T.CardSummary{}}}
		switch mode {
		case "query":
			page.Result.Query.Q = " café "
		case "lane":
			page.Result.Query.Lane = nil
		case "owner":
			page.Result.Query.Owner = nil
		case "priority":
			page.Result.Query.Priority = nil
		case "archive":
			page.Result.Query.Archive = "active"
		case "scope":
			page.Scope = "board"
		}
		wire, _ := json.Marshal(page)
		if mode == "items-type" {
			wire = bytes.Replace(wire, []byte(`"items":[]`), []byte(`"items":null`), 1)
		}
		c := connection(t, func(req *http.Request) (int, []byte, error) {
			if req.URL.Query().Get("q") != " Café " {
				t.Fatal("query spelling lost")
			}
			status, code := 0, ""
			switch mode {
			case "stale":
				status, code = 409, "CURSOR_STALE"
			case "invalid":
				status, code = 400, "INVALID_INPUT"
			case "forbidden":
				status, code = 403, "FORBIDDEN"
			}
			if status != 0 {
				b, _ := json.Marshal(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}})
				return status, b, nil
			}
			return 200, wire, nil
		})
		var out bytes.Buffer
		got := (Runner{Provider: &provider{conn: c}}).Run(context.Background(), args("card", "search", "--query", " Café ", "--lane", "In Progress", "--owner", "unassigned", "--priority", "P1", "--archive", "all"), nil, &out, io.Discard)
		want := 3
		if mode == "valid" {
			want = 0
		}
		if mode == "stale" || mode == "invalid" || mode == "forbidden" {
			want = 2
		}
		if got != want {
			t.Errorf("mode %s exit %d want %d", mode, got, want)
		}
	}
}
