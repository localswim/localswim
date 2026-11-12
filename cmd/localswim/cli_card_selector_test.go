package main

import (
	"encoding/json"
	"fmt"
	"github.com/localswim/localswim/internal/client"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBoardCardSelectorGrammar(t *testing.T) {
	board := "11111111-1111-4111-8111-111111111111"
	card := "22222222-2222-4222-8222-222222222222"
	lane := "33333333-3333-4333-8333-333333333333"
	for _, ref := range []string{card, "42"} {
		for _, args := range [][]string{
			{"card", "get", board, ref}, {"card", "inspect", board, ref},
			{"card", "patch", board, ref, "-"}, {"card", "description", board, ref, "-"},
			{"card", "title", board, ref, "title"}, {"card", "move", board, ref, lane},
			{"card", "assign", board, ref, lane}, {"card", "set-priority", board, ref, lane},
			{"card", "cover", board, ref, "none"}, {"card", "archive", board, ref}, {"card", "unarchive", board, ref},
			{"comment", "post", "--text", "hello", board, ref}, {"comment", "list", board, ref},
			{"attachment", "list", board, ref}, {"attachment", "upload", board, ref, "-"},
			{"attachment", "detach", board, ref, lane}, {"card", "set-parent", board, ref, lane},
			{"relationship", "create", board, ref, "blocks", lane},
		} {
			if _, err := parseCLI(args); err != nil {
				t.Errorf("%v: %v", args, err)
			}
		}
	}
	for _, args := range [][]string{
		{"card", "get", card}, {"card", "patch", card, "-"}, {"card", "archive", card},
		{"attachment", "upload", card, "-"}, {"attachment", "list", card},
		{"card", "get", board, "0"}, {"card", "get", board, "9007199254740992"},
		{"card", "create", "-"},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Errorf("accepted missing or invalid scope: %v", args)
		}
	}
}

func TestUUIDCardResolutionChecksScopeAndSameView(t *testing.T) {
	const board = "11111111-1111-4111-8111-111111111111"
	const card = "22222222-2222-4222-8222-222222222222"
	const other = "33333333-3333-4333-8333-333333333333"
	for _, tc := range []struct {
		name, returnedBoard, focusedID string
		focusedRevision                uint64
		wantError                      bool
	}{
		{"valid", board, card, 4, false},
		{"wrong board", other, card, 4, true},
		{"renumbered", board, other, 4, true},
		{"changed revision", board, card, 5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != http.MethodGet {
					t.Error("resolution wrote data")
				}
				if r.URL.Path == t03Root+"/cards/"+card {
					w.Header().Set("ETag", fmt.Sprintf("%q", card+"-4"))
					json.NewEncoder(w).Encode(map[string]any{"id": card, "board_id": tc.returnedBoard, "ticket": 7, "revision": 4})
					return
				}
				if r.URL.Path != t03Root+"/boards/"+board || r.URL.Query().Get("ticket") != "7" {
					t.Errorf("unexpected focused route %s", r.URL)
				}
				w.Header().Set("X-Board-ETag", fmt.Sprintf("%q", board+"-9"))
				json.NewEncoder(w).Encode(map[string]any{"board_id": board, "board_revision": 9, "card": map[string]any{"id": tc.focusedID, "ticket": 7, "revision": tc.focusedRevision}})
			}))
			defer server.Close()
			api, err := client.New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolveCardTarget(api, &cliTicketTarget{boardID: board, cardID: card})
			if (err != nil) != tc.wantError {
				t.Fatalf("result %+v, error %v", got, err)
			}
			if !tc.wantError && (got.CardID != card || got.Ticket != 7 || reads != 2) {
				t.Fatalf("incorrect target %+v after %d reads", got, reads)
			}
			if tc.returnedBoard != board && reads != 1 {
				t.Fatal("wrong board was followed")
			}
		})
	}
}

func TestFilteredCardListsRejectForeignItems(t *testing.T) {
	const board = "11111111-1111-4111-8111-111111111111"
	const card = "22222222-2222-4222-8222-222222222222"
	for _, data := range []string{
		`{}`, `{"comments":null}`, `{"comments":[{"board_id":"wrong","card_id":"` + card + `"}]}`,
		`{"comments":[{"board_id":"` + board + `","card_id":"wrong"}]}`,
	} {
		if validateCardListResponse(json.RawMessage(data), "/comments", board, card) == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if err := validateCardListResponse(json.RawMessage(`{"comments":[]}`), "/comments", board, card); err != nil {
		t.Fatal(err)
	}
}

func TestCardGetRejectsChangedResolvedTicketOrRevision(t *testing.T) {
	const board = "11111111-1111-4111-8111-111111111111"
	const card = "22222222-2222-4222-8222-222222222222"
	for _, final := range []struct{ ticket, revision uint64 }{{2, 4}, {1, 5}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("view") == "ticket" {
				w.Header().Set("X-Board-ETag", fmt.Sprintf("%q", board+"-9"))
				json.NewEncoder(w).Encode(map[string]any{"board_id": board, "board_revision": 9, "card": map[string]any{"id": card, "ticket": 1, "revision": 4}})
				return
			}
			w.Header().Set("ETag", fmt.Sprintf("%q", fmt.Sprintf("%s-%d", card, final.revision)))
			json.NewEncoder(w).Encode(map[string]any{"id": card, "board_id": board, "ticket": final.ticket, "revision": final.revision})
		}))
		api, err := client.New(server.URL, "test-token")
		if err != nil {
			t.Fatal(err)
		}
		err = executeCardRead(api, cliCommand{resource: "/cards", cardTarget: &cliTicketTarget{boardID: board, ticket: 1}}, io.Discard)
		server.Close()
		if err == nil {
			t.Fatalf("accepted changed card %+v", final)
		}
	}
}
