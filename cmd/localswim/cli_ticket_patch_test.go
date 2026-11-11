package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/localswim/localswim/internal/client"
	"github.com/localswim/localswim/internal/v2"
)

func TestM070CLITicketCardEdits(t *testing.T) {
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	service, err := v2.Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	handler := service.Handler(server.Listener.Addr().(*net.TCPAddr))
	var lookups, edits, requests atomic.Int32
	type reorderRequest struct{ path, body string }
	var reorder atomic.Pointer[reorderRequest]
	var loseResponse atomic.Bool
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method == http.MethodGet && r.URL.Query().Get("view") == "ticket" {
			lookups.Add(1)
			if r.URL.Query().Has("include_prose") {
				t.Error("ticket mutation requested prose")
			}
		}
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, v2.Prefix+"/cards/") {
			edits.Add(1)
			if request := reorder.Swap(nil); request != nil {
				put := httptest.NewRequest(http.MethodPut, request.path, strings.NewReader(request.body))
				put.Host = r.Host
				put.Header.Set("Content-Type", "application/json")
				put.Header.Set("Authorization", "Bearer "+human)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, put)
				if response.Code != http.StatusOK {
					t.Errorf("could not apply fixture reorder: %d %s", response.Code, response.Body)
				}
			}
			if loseResponse.Swap(false) {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, r)
				if response.Code != http.StatusOK {
					t.Errorf("could not apply uncertain-response fixture: %d %s", response.Code, response.Body)
				}
				w.WriteHeader(http.StatusBadGateway)
				return
			}
		}
		handler.ServeHTTP(w, r)
	})
	server.Start()
	t.Cleanup(server.Close)
	api, err := client.New(server.URL, human)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) client.Response {
		t.Helper()
		var data json.RawMessage
		if body != "" {
			data = json.RawMessage(body)
		}
		reply, err := api.Do(method, path, data, "")
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	var board struct {
		ID           string   `json:"id"`
		LaneIDs      []string `json:"lane_ids"`
		UserIDs      []string `json:"user_ids"`
		DefaultOwner string   `json:"default_owner_id"`
	}
	boardReply := call("POST", "/boards", `{"name":"Ticket edits fixture"}`)
	if err := json.Unmarshal(boardReply.JSON, &board); err != nil {
		t.Fatal(err)
	}
	type card struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
		LaneID   string `json:"lane_id"`
		OwnerID  string `json:"owner_id"`
		Ticket   uint64 `json:"ticket"`
		Archived bool   `json:"archived"`
	}
	decodeCard := func(data json.RawMessage) card {
		t.Helper()
		var value card
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	createCard := func(boardTag, title, extra string) client.Response {
		return call("POST", "/cards", fmt.Sprintf(
			`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q%s}`,
			board.ID, boardTag, board.LaneIDs[0], title, extra))
	}
	firstReply := createCard(boardReply.ETag, "First", "")
	first := decodeCard(firstReply.JSON)
	secondReply := createCard(firstReply.BoardETag, "Second",
		fmt.Sprintf(`,"relationships":[{"target_card_id":%q,"kind":"blocks"}]`, first.ID))
	second := decodeCard(secondReply.JSON)
	read := func(id string) card { return decodeCard(call("GET", "/cards/"+id, "").JSON) }
	first = read(first.ID)
	owner := board.UserIDs[0]
	if owner == board.DefaultOwner {
		owner = board.UserIDs[1]
	}

	getenv := func(name string) string {
		switch name {
		case "LOCALSWIM_URL":
			return server.URL
		case "LOCALSWIM_TOKEN":
			return human
		default:
			return ""
		}
	}
	run := func(wantCode, wantRequests int, wantError string, args ...string) card {
		t.Helper()
		var out, stderr bytes.Buffer
		before := requests.Load()
		code := runCLIWithInput(append([]string{"card"}, args...), strings.NewReader(""), &out, &stderr, getenv)
		if count := requests.Load() - before; count != int32(wantRequests) {
			t.Fatalf("card %s made %d requests, want %d", args[0], count, wantRequests)
		}
		if code != wantCode || !strings.Contains(stderr.String(), wantError) {
			t.Fatalf("card %s exit %d, want %d: %s", args[0], code, wantCode, &stderr)
		}
		if wantCode != 0 {
			if out.Len() != 0 {
				t.Fatal("refused command wrote success output")
			}
			return card{}
		}
		return decodeCard(out.Bytes())
	}
	moved := run(0, 2, "", "move", "--revision", fmt.Sprint(first.Revision), strings.ToUpper(board.ID), "0001", board.LaneIDs[1])
	assigned := run(0, 2, "", "assign", board.ID, "1", owner)
	archived := run(0, 2, "", "archive", board.ID, "1")
	restored := run(0, 2, "", "unarchive", board.ID, "1")
	if moved.ID != first.ID || moved.LaneID != board.LaneIDs[1] || assigned.OwnerID != owner ||
		!archived.Archived || restored.Archived || read(first.ID) != restored {
		t.Fatal("ticket commands did not commit the intended changes")
	}
	run(1, 1, "revision mismatch", "archive", "--revision", fmt.Sprint(first.Revision), board.ID, "1")
	if lookups.Load() != 5 || edits.Load() != 4 {
		t.Fatal("ticket commands made extra requests or dispatched after a revision mismatch")
	}

	run(0, 2, "", "move", first.ID, board.LaneIDs[0])
	current := call("GET", "/boards/"+board.ID, "")
	reorder.Store(&reorderRequest{
		path: v2.Prefix + "/ticket-orders/" + board.LaneIDs[0],
		body: fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"card_ids":[%q,%q]}`,
			board.ID, current.ETag, second.ID, first.ID),
	})
	run(1, 2, "HTTP 412", "archive", board.ID, "1")
	currentFirst, currentSecond := read(first.ID), read(second.ID)
	if lookups.Load() != 6 || edits.Load() != 6 || currentFirst.Ticket != 2 || currentSecond.Ticket != 1 ||
		currentFirst.Archived || currentSecond.Archived {
		t.Fatal("stale command retried, changed a card, or did not exercise an intervening reorder")
	}

	loseResponse.Store(true)
	run(1, 2, "Read back with localswim card get "+first.ID, "archive", board.ID, "2")
	if lookups.Load() != 7 || edits.Load() != 7 || !read(first.ID).Archived || read(second.ID).Archived {
		t.Fatal("uncertain response retried or affected the wrong card")
	}
}

func TestM070CLITicketPatchRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"card", "move", t03Board, "0", t03Lane},
		{"card", "assign", t03Board, "9007199254740992", t03Lane},
		{"card", "archive", t03Board, "-1"},
		{"card", "unarchive", t03Board, t03Card},
		{"card", "move", t03Board, "1", "invalid-lane"},
	} {
		code := runCLIWithInput(args, strings.NewReader(""), io.Discard, io.Discard, func(string) string {
			t.Fatal("invalid arguments reached transport setup")
			return ""
		})
		if code != 2 {
			t.Fatalf("invalid %s arguments returned exit %d", args[1], code)
		}
	}
}
