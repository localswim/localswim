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

func TestM098CLITicketFilesAndBatch(t *testing.T) {
	human := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{'h'}, 32))
	bot := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{'b'}, 32))
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
	var before atomic.Pointer[func()]
	var mutations atomic.Int32
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodPost && r.URL.Path == v2.Prefix+"/attachments") ||
			(r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, v2.Prefix+"/attachments/")) ||
			(r.Method == http.MethodPatch && r.URL.Path == v2.Prefix+"/cards") {
			mutations.Add(1)
			if inject := before.Swap(nil); inject != nil {
				(*inject)()
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
	call := func(method, path, body, tag string) client.Response {
		t.Helper()
		reply, err := api.Do(method, path, []byte(body), tag)
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	boardReply := call("POST", "/boards", `{"name":"Ticket files and batch"}`, "")
	var board struct {
		ID          string   `json:"id"`
		LaneIDs     []string `json:"lane_ids"`
		PriorityIDs []string `json:"priority_ids"`
	}
	if err := json.Unmarshal(boardReply.JSON, &board); err != nil {
		t.Fatal(err)
	}
	createCard := func(title, tag string) client.Response {
		t.Helper()
		return call("POST", "/cards", fmt.Sprintf(
			`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q,"priority_id":%q}`,
			board.ID, tag, board.LaneIDs[0], title, board.PriorityIDs[2]), "")
	}
	first := createCard("First", boardReply.ETag)
	second := createCard("Second", first.BoardETag)
	var secondCard struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal(second.JSON, &secondCard); err != nil {
		t.Fatal(err)
	}
	env := func(name string) string {
		switch name {
		case "LOCALSWIM_URL":
			return server.URL
		case "LOCALSWIM_TOKEN":
			return human
		default:
			return ""
		}
	}
	binary := []byte{0, 0xff, 13, 10, 0x80}
	run := func(wantCode, writes int, wantError string, args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		start := mutations.Load()
		code := runCLIWithInput(args, bytes.NewReader(binary), &out, &stderr, env)
		if code != wantCode || !strings.Contains(stderr.String(), wantError) || mutations.Load()-start != int32(writes) {
			t.Fatalf("%v returned code %d with %d writes and error %q", args, code, mutations.Load()-start, &stderr)
		}
		if code != 0 && out.Len() != 0 {
			t.Fatal("refused command produced success output")
		}
		return out.Bytes()
	}

	const uploadID = "9faf9dce-2df4-4bd1-9747-9610a86846dd"
	const staleID = "54d96870-f7fa-4502-81c5-684b0df1df65"
	rename := func(label string) func() {
		return func() {
			current, err := api.Do(http.MethodGet, "/boards/"+board.ID, nil, "")
			if err == nil {
				_, err = api.Do(http.MethodPatch, "/boards/"+board.ID,
					[]byte(fmt.Sprintf(`{"name":%q}`, label)), current.ETag)
			}
			if err != nil {
				t.Errorf("could not change the board in the fixture: %v", err)
			}
		}
	}
	staleUpload := rename("Board edited during upload")
	before.Store(&staleUpload)
	run(1, 1, "HTTP 412", "attachment", "upload", "--id", staleID, board.ID, "2", "-")
	if _, err := api.Do(http.MethodGet, "/attachments/"+staleID, nil, ""); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("stale upload left a readable attachment: %v", err)
	}
	run(0, 1, "", "attachment", "upload", "--id", uploadID, "--revision", fmt.Sprint(secondCard.Revision),
		strings.ToUpper(board.ID), "0002", "-")
	resource := "/attachments/" + uploadID
	content, err := api.AttachmentContent(resource + "/content")
	if err != nil || !bytes.Equal(content, binary) {
		t.Fatalf("binary attachment readback differs: %x, %v", content, err)
	}
	run(1, 0, "active attachment on the resolved ticket", "attachment", "detach", board.ID, "1", uploadID)
	staleDetach := rename("Board edited during detachment")
	before.Store(&staleDetach)
	run(1, 1, "HTTP 412", "attachment", "detach", board.ID, "2", uploadID)
	if _, err := api.AttachmentContent(resource + "/content"); err != nil {
		t.Fatalf("stale detach removed access: %v", err)
	}
	if out := run(0, 1, "", "attachment", "detach", board.ID, "2", uploadID); len(out) != 0 {
		t.Fatal("detach returned unexpected output")
	}
	var detached struct {
		Active *bool  `json:"active"`
		CardID string `json:"card_id"`
	}
	if err := json.Unmarshal(call("GET", resource, "", "").JSON, &detached); err != nil ||
		detached.Active == nil || *detached.Active || detached.CardID != secondCard.ID {
		t.Fatal("resolved attachment did not become inactive on the intended card")
	}

	batch := []string{"card", "set-priorities", "--priority", board.PriorityIDs[0], "--tickets", board.ID}
	run(2, 0, "duplicate ticket", append(batch, "1", "0001")...)
	staleBatch := rename("Board edited during batch")
	before.Store(&staleBatch)
	run(1, 1, "HTTP 412", append(batch, "2", "1")...)
	readPriority := func(ticket uint64) string {
		t.Helper()
		target, err := api.ResolveTicket(board.ID, ticket)
		if err != nil {
			t.Fatal(err)
		}
		var item struct {
			Priority string `json:"priority_id"`
		}
		if err := json.Unmarshal(call("GET", "/cards/"+target.CardID, "", "").JSON, &item); err != nil {
			t.Fatal(err)
		}
		return item.Priority
	}
	if readPriority(1) != board.PriorityIDs[2] || readPriority(2) != board.PriorityIDs[2] {
		t.Fatal("stale batch changed a selected card")
	}
	run(0, 1, "", append(batch, "2", "1")...)
	if readPriority(1) != board.PriorityIDs[0] || readPriority(2) != board.PriorityIDs[0] {
		t.Fatal("batch did not change both selected cards")
	}
}

func TestM098RejectsInvalidTicketArguments(t *testing.T) {
	for _, args := range [][]string{
		{"attachment", "upload", t03Board, "0", "-"},
		{"attachment", "upload", t03Board, "9007199254740992", "-"},
		{"attachment", "detach", t03Board, "2", "bad"},
		{"card", "set-priorities", "--tickets", t03Board, "2"},
		{"card", "set-priorities", "--tickets", "--priority", t03Lane, t03Board, "0"},
	} {
		exit := runCLIWithInput(args, strings.NewReader(""), io.Discard, io.Discard, func(string) string {
			t.Fatal("invalid arguments reached transport setup")
			return ""
		})
		if exit != 2 {
			t.Fatalf("%v returned exit %d", args, exit)
		}
	}
}

func TestM098TicketDetachBindsAttachmentValidatorToRevision(t *testing.T) {
	const id = "9faf9dce-2df4-4bd1-9747-9610a86846dd"
	boardTag := fmt.Sprintf("%q", t03Board+"-8")
	attachmentTag := func(id string, revision int) string {
		return fmt.Sprintf("%q", fmt.Sprintf("%s-%d", id, revision))
	}
	for _, tc := range []struct {
		name, tag, revision, wantError string
	}{
		{"matching", attachmentTag(id, 6), "6", ""},
		{"different revision", attachmentTag(id, 7), "6", "matching attachment revision and validator"},
		{"different identity", attachmentTag(t03Card, 6), "6", "matching attachment revision and validator"},
		{"unbound strong validator", t03ResourceTag, "6", "matching attachment revision and validator"},
		{"null revision", attachmentTag(id, 6), "null", "invalid attachment metadata"},
		{"missing revision", attachmentTag(id, 6), "", "invalid attachment metadata"},
		{"newer than focus", attachmentTag(id, 9), "9", "matching attachment revision and validator"},
		{"requested revision differs", attachmentTag(id, 7), "7", "revision mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal(attachmentFixture(id, nil), &metadata); err != nil {
				t.Fatal(err)
			}
			if tc.revision == "" {
				delete(metadata, "revision")
			} else {
				metadata["revision"] = json.RawMessage(tc.revision)
			}
			var reads, deletes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/boards/"+t03Board &&
					r.URL.RawQuery == "view=ticket&ticket=2":
					reads.Add(1)
					w.Header().Set("X-Board-ETag", boardTag)
					_, _ = fmt.Fprintf(w, `{"board_id":%q,"board_revision":8,"card":{"id":%q,"ticket":2,"revision":6}}`, t03Board, t03Card)
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/attachments/"+id:
					reads.Add(1)
					w.Header().Set("ETag", tc.tag)
					_ = json.NewEncoder(w).Encode(metadata)
				case r.Method == http.MethodDelete && r.URL.Path == t03Root+"/attachments/"+id:
					deletes.Add(1)
					if r.Header.Get("If-Match") != tc.tag || r.Header.Get("X-Expected-Board-ETag") != boardTag {
						t.Error("DELETE lost the original attachment or board validator")
					}
					w.Header().Set("X-Board-ETag", fmt.Sprintf("%q", t03Board+"-9"))
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			var out, stderr bytes.Buffer
			exit := runCLIWithInput([]string{"attachment", "detach", "--revision", "6", t03Board, "2", id},
				strings.NewReader(""), &out, &stderr, t03Env(server.URL))
			wantExit, wantDeletes := 0, int32(1)
			if tc.wantError != "" {
				wantExit, wantDeletes = 1, 0
			}
			if exit != wantExit || reads.Load() != 2 || deletes.Load() != wantDeletes ||
				out.Len() != 0 || !strings.Contains(stderr.String(), tc.wantError) {
				t.Fatalf("exit %d, reads %d, deletes %d, output %q, error %q",
					exit, reads.Load(), deletes.Load(), &out, &stderr)
			}
		})
	}
}
