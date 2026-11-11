package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/localswim/localswim/internal/client"
	"github.com/localswim/localswim/internal/v2"
)

func TestM071TicketParentAndLinks(t *testing.T) {
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
	type driftEdit struct{ path, tag string }
	var drift atomic.Pointer[driftEdit]
	var loseRemoval atomic.Bool
	var requests atomic.Int32
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("view") == "ticket" && r.URL.Query().Has("include_prose") {
			t.Error("mutation lookup requested prose")
		}
		if r.Method == http.MethodDelete {
			if edit := drift.Swap(nil); edit != nil {
				patch := httptest.NewRequest(http.MethodPatch, edit.path, strings.NewReader(`{"name":"Changed after ticket read"}`))
				patch.Host = r.Host
				patch.Header.Set("Authorization", "Bearer "+human)
				patch.Header.Set("Content-Type", "application/json")
				patch.Header.Set("If-Match", edit.tag)
				applied := httptest.NewRecorder()
				handler.ServeHTTP(applied, patch)
				if applied.Code != http.StatusOK {
					t.Errorf("fixture board edit returned %d", applied.Code)
				}
			}
			if loseRemoval.Swap(false) {
				applied := httptest.NewRecorder()
				handler.ServeHTTP(applied, r)
				if applied.Code != http.StatusNoContent {
					t.Errorf("fixture removal returned %d", applied.Code)
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
		ID      string   `json:"id"`
		LaneIDs []string `json:"lane_ids"`
	}
	boardReply := call(http.MethodPost, "/boards", `{"name":"Ticket relationships fixture"}`)
	if err := json.Unmarshal(boardReply.JSON, &board); err != nil || len(board.LaneIDs) == 0 {
		t.Fatalf("invalid fixture board: %v", err)
	}
	type card struct {
		ID       string   `json:"id"`
		ParentID *string  `json:"parent_id"`
		ChildIDs []string `json:"child_ids"`
	}
	decodeCard := func(data []byte) card {
		t.Helper()
		var value card
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	create := func(tag, title string) client.Response {
		return call(http.MethodPost, "/cards", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q}`,
			board.ID, tag, board.LaneIDs[0], title))
	}
	first := create(boardReply.ETag, "Child")
	child := decodeCard(first.JSON)
	parent := decodeCard(create(first.BoardETag, "Parent").JSON)
	readCard := func(id string) card { return decodeCard(call(http.MethodGet, "/cards/"+id, "").JSON) }
	run := func(wantCode, wantRequests int, wantError string, args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		before := requests.Load()
		code := runCLIWithInput(args, nil, &out, &stderr, func(name string) string {
			return map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": human}[name]
		})
		if code != wantCode || requests.Load()-before != int32(wantRequests) ||
			!strings.Contains(stderr.String(), wantError) || (code != 0 && out.Len() != 0) {
			t.Fatalf("%v: exit=%d, requests=%d, stderr=%s", args, code, requests.Load()-before, &stderr)
		}
		return out.Bytes()
	}
	saved := decodeCard(run(0, 2, "", "card", "set-parent", strings.ToUpper(board.ID), "0001", "2"))
	children := readCard(parent.ID).ChildIDs
	if saved.ID != child.ID || saved.ParentID == nil || *saved.ParentID != parent.ID ||
		len(children) != 1 || children[0] != child.ID {
		t.Fatal("parent set did not update both cards")
	}
	run(1, 1, "revision mismatch", "card", "clear-parent", "--revision", "1", board.ID, "1")
	run(1, 2, "HTTP 400", "card", "set-parent", board.ID, "2", "1")

	const outgoingID = "3ef41b6a-f891-4f9b-8626-d14050233b58"
	const incomingID = "5f3702fa-bbb6-4f57-9341-6e9521f7fb7b"
	const symmetricID = "5305ba99-21c1-467b-b7ba-1ae268690444"
	run(0, 2, "", "relationship", "create", "--id", outgoingID, board.ID, "1", "references", "2")
	run(0, 2, "", "relationship", "create", "--id", incomingID, board.ID, "2", "references", "1")
	run(1, 2, "HTTP 409", "relationship", "create", board.ID, "1", "references", "2")
	run(0, 2, "", "relationship", "create", "--id", symmetricID, board.ID, "2", "relates_to", "1")
	run(0, 2, "", "relationship", "remove", board.ID, "1", "relates_to", "2")

	boardReply = call(http.MethodGet, "/boards/"+board.ID, "")
	drift.Store(&driftEdit{path: v2.Prefix + "/boards/" + board.ID, tag: boardReply.ETag})
	run(1, 2, "HTTP 412", "relationship", "remove", board.ID, "1", "references", "2")
	call(http.MethodGet, "/relationships/"+outgoingID, "")

	loseRemoval.Store(true)
	run(1, 2, "Read back with localswim relationship get "+outgoingID, "relationship", "remove", board.ID, "1", "references", "2")
	run(1, 1, "no relationship matches", "relationship", "remove", board.ID, "1", "references", "2")
	call(http.MethodGet, "/relationships/"+incomingID, "")

	cleared := decodeCard(run(0, 2, "", "card", "clear-parent", board.ID, "1"))
	if cleared.ID != child.ID || cleared.ParentID != nil || len(readCard(parent.ID).ChildIDs) != 0 {
		t.Fatal("parent clear did not update both cards")
	}
	run(2, 0, "", "relationship", "remove", board.ID, "1", "blocked_by", "2")
	run(2, 0, "", "card", "set-parent", board.ID, "1", "0001")
}
