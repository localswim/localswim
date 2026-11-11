package v2

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func eventHTTPServer(t *testing.T) (*Service, turnOneClient) {
	t.Helper()
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Config.WriteTimeout = time.Second
	server.Start()
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
		server.Close()
	})
	return app, turnOneClient{t, server.URL, human, &http.Client{Timeout: eventHeartbeat + 5*time.Second}}
}

func (c turnOneClient) openEvents(path, lastID string) (*bufio.Reader, func() error) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.url+Prefix+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	response, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" ||
		response.Header.Get("Cache-Control") != "no-store" {
		c.t.Fatalf("unexpected SSE response status or headers: %s", response.Status)
	}
	return bufio.NewReader(response.Body), response.Body.Close
}

func readEventFrame(t *testing.T, reader *bufio.Reader) eventFrame {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "data: ") {
		t.Fatalf("expected an SSE data frame, got %q", line)
	}
	if lineEnd, err := reader.ReadString('\n'); err != nil || lineEnd != "\n" {
		t.Fatalf("expected SSE frame terminator, got %q (%v)", lineEnd, err)
	}
	data := []byte(strings.TrimPrefix(line, "data: "))
	fields := turnOneDecode[map[string]any](t, data)
	frame := turnOneDecode[eventFrame](t, data)
	required := map[string][]string{
		"board":     {"type", "resource", "board_id", "board_revision"},
		"workspace": {"type", "resource", "workspace_revision"},
		"catalog":   {"type", "resource"},
	}[frame.Resource]
	if len(required) == 0 || len(fields) != len(required) {
		t.Fatalf("unexpected event payload: %s", data)
	}
	for _, field := range required {
		if _, ok := fields[field]; !ok {
			t.Fatalf("event payload missing %s", field)
		}
	}
	return frame
}

func assertEventBoard(t *testing.T, frame eventFrame, kind string, want board) {
	t.Helper()
	if frame.Type != kind || frame.Resource != "board" || frame.BoardID != want.ID ||
		frame.BoardRevision != want.Revision {
		t.Fatalf("expected %s board %s at revision %d, got %+v", kind, want.ID, want.Revision, frame)
	}
}

func assertEventWorkspace(t *testing.T, frame eventFrame, kind string, revision uint64) {
	t.Helper()
	if frame.Type != kind || frame.Resource != "workspace" || frame.WorkspaceRevision != revision {
		t.Fatalf("expected %s workspace at revision %d, got %+v", kind, revision, frame)
	}
}

func assertEventCatalog(t *testing.T, frame eventFrame, kind string) {
	t.Helper()
	if frame.Type != kind || frame.Resource != "catalog" {
		t.Fatalf("expected %s catalog event, got %+v", kind, frame)
	}
}

func checkEventConditionalGet(t *testing.T, c turnOneClient, want board, condition string, status int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.url+Prefix+"/boards/"+want.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("If-None-Match", condition)
	response, err := c.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || response.Header.Get("ETag") != want.entityTag() {
		t.Fatalf("conditional board GET returned status %d and ETag %q", response.StatusCode, response.Header.Get("ETag"))
	}
	if status == http.StatusNotModified {
		if len(body) != 0 {
			t.Fatal("304 carried a body")
		}
	} else if got := turnOneDecode[board](t, body); got.ID != want.ID || got.entityTag() != want.entityTag() {
		t.Fatal("conditional board GET did not match the streamed revision")
	}
}

func TestEventsCommittedStateAndReconnect(t *testing.T) {
	app, c := eventHTTPServer(t)
	newBoard := func(name string) board {
		raw, _ := c.call(http.MethodPost, "/boards", fmt.Sprintf("{%q:%q}", "name", name), "", 201)
		return turnOneDecode[board](t, raw)
	}
	selected, retained, other := newBoard("Selected"), newBoard("Retained"), newBoard("Other")
	raw, workspaceHeaders := c.call(http.MethodGet, "/workspace", "", "", 200)
	initialWorkspace := turnOneDecode[workspace](t, raw)
	path := "/events?board_id=" + strings.ToUpper(selected.ID) + "&watch=" + selected.ID + "&watch=" + retained.ID
	reader, disconnect := c.openEvents(path, "")
	assertEventWorkspace(t, readEventFrame(t, reader), "snapshot", initialWorkspace.Revision)
	assertEventCatalog(t, readEventFrame(t, reader), "snapshot")
	assertEventBoard(t, readEventFrame(t, reader), "snapshot", selected)
	assertEventBoard(t, readEventFrame(t, reader), "snapshot", retained)

	c.call(http.MethodPatch, "/boards/"+selected.ID, "{\"name\":\"Selected\"}", selected.entityTag(), 200)
	c.call(http.MethodPatch, "/boards/"+selected.ID, "{\"name\":\"Rejected\"}", "\"stale\"", 412)
	c.call(http.MethodPatch, "/boards/"+other.ID, "{\"name\":\"Other renamed\"}", other.entityTag(), 200)
	assertEventCatalog(t, readEventFrame(t, reader), "change")

	retainedBefore := retained.entityTag()
	raw, _ = c.call(http.MethodPatch, "/boards/"+retained.ID, "{\"name\":\"Retained renamed\"}", retained.entityTag(), 200)
	retained = turnOneDecode[board](t, raw)
	assertEventCatalog(t, readEventFrame(t, reader), "change")
	assertEventBoard(t, readEventFrame(t, reader), "change", retained)
	checkEventConditionalGet(t, c, retained, retainedBefore, http.StatusOK)
	checkEventConditionalGet(t, c, retained, retained.entityTag(), http.StatusNotModified)

	raw, _ = c.call(http.MethodPatch, "/workspace", fmt.Sprintf("{%q:[%q]}", "pinned_board_ids", selected.ID), workspaceHeaders.Get("ETag"), 200)
	updatedWorkspace := turnOneDecode[workspace](t, raw)
	assertEventWorkspace(t, readEventFrame(t, reader), "change", updatedWorkspace.Revision)

	if err := disconnect(); err != nil {
		t.Fatal(err)
	}
	raw, _ = c.call(http.MethodPatch, "/boards/"+selected.ID, "{\"name\":\"Selected renamed\"}", selected.entityTag(), 200)
	selected = turnOneDecode[board](t, raw)
	reader, _ = c.openEvents(path, "ignored-older-event")
	assertEventWorkspace(t, readEventFrame(t, reader), "snapshot", updatedWorkspace.Revision)
	assertEventCatalog(t, readEventFrame(t, reader), "snapshot")
	assertEventBoard(t, readEventFrame(t, reader), "snapshot", selected)
	assertEventBoard(t, readEventFrame(t, reader), "snapshot", retained)

	app.StopEvents()
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected shutdown to end the stream, got %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, c.url+Prefix+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatalf("stream opened after shutdown: %d", response.StatusCode)
	}
}

func TestEventsAuthorizationAndRequestValidation(t *testing.T) {
	_, c := eventHTTPServer(t)
	raw, _ := c.call(http.MethodPost, "/boards", "{\"name\":\"Authorized\"}", "", 201)
	allowed := turnOneDecode[board](t, raw)
	restrictedWorkflow := strings.NewReplacer(
		"A0000000-0000-4000-8000-000000000002", humanID,
		"a0000000-0000-4000-8000-000000000002", humanID,
		`"label":" Owner "`, `"label":"Human"`,
		`"read","approve","create_card"`, `"approve","create_card"`,
	).Replace(workflowTestInput)
	raw, _ = c.call(http.MethodPost, "/boards", fmt.Sprintf(`{"name":"Restricted","default_owner_id":%q,"workflow":%s}`, humanID, restrictedWorkflow), "", 201)
	restricted := turnOneDecode[board](t, raw)
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	for _, tc := range []struct {
		name, method, path, token, accept string
		status                            int
	}{
		{"bearer only", http.MethodGet, "/events?token=" + c.token, "", "text/event-stream", 401},
		{"bad ID", http.MethodGet, "/events?board_id=invalid", c.token, "text/event-stream", 400},
		{"duplicate selection", http.MethodGet, "/events?board_id=" + allowed.ID + "&board_id=" + allowed.ID, c.token, "text/event-stream", 400},
		{"excess watch entries", http.MethodGet, "/events?board_id=" + allowed.ID + strings.Repeat("&watch="+allowed.ID, maxWatchedBoards+1), c.token, "text/event-stream", 400},
		{"unavailable watch", http.MethodGet, "/events?board_id=" + allowed.ID + "&watch=" + newID(), c.token, "text/event-stream", 404},
		{"member without read", http.MethodGet, "/events?board_id=" + allowed.ID + "&watch=" + restricted.ID, c.token, "text/event-stream", 403},
		{"nonmember watch", http.MethodGet, "/events?board_id=" + allowed.ID + "&watch=" + restricted.ID, bot, "text/event-stream", 404},
		{"wrong media", http.MethodGet, "/events", c.token, "application/json", 406},
		{"explicit media refusal", http.MethodGet, "/events", c.token, "text/event-stream;q=0, */*;q=1", 406},
		{"wrong method", http.MethodHead, "/events", c.token, "text/event-stream", 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, c.url+Prefix+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			req.Header.Set("Accept", tc.accept)
			response, err := c.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status || response.Header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("unexpected refusal status or media: %d %q", response.StatusCode, response.Header.Get("Content-Type"))
			}
		})
	}
}

func TestEventsRefusesRequestBody(t *testing.T) {
	_, c := eventHTTPServer(t)
	request, err := http.NewRequest(http.MethodGet, c.url+Prefix+"/events", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := c.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || !response.Close {
		t.Fatalf("expected a bad request and connection close, got %d (close: %t)", response.StatusCode, response.Close)
	}
}

func TestEventsHeartbeatAvoidsStateReads(t *testing.T) {
	app, c := eventHTTPServer(t)
	reader, _ := c.openEvents("/events", "")
	assertEventWorkspace(t, readEventFrame(t, reader), "snapshot", 1)
	assertEventCatalog(t, readEventFrame(t, reader), "snapshot")
	if err := os.WriteFile(app.workspace.path, []byte("unreadable workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Workspace rereads now fail. The idle stream must survive the server's
	// ordinary write timeout and still send a comment without reading state.
	line, err := reader.ReadString('\n')
	if err != nil || line != ": heartbeat\n" {
		t.Fatalf("expected SSE heartbeat comment, got %q (%v)", line, err)
	}
	line, err = reader.ReadString('\n')
	if err != nil || line != "\n" {
		t.Fatalf("expected SSE heartbeat terminator, got %q (%v)", line, err)
	}
}
