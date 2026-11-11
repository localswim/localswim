package v2

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const stopServiceBody = `{"state":"stopped"}`

func lifecycleTokens() (string, string) {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{'h'}, 32)),
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{'b'}, 32))
}

func TestM39ShutdownJoinsAdmittedMutationAndDrainsEvents(t *testing.T) {
	human, bot := lifecycleTokens()
	app, err := Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	handler := app.Handler(server.Listener.Addr().(*net.TCPAddr))
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery == "hold-response=1" {
			w = heldBoardWrite{w, entered, release}
		}
		handler.ServeHTTP(w, r)
	})
	server.Start()
	t.Cleanup(func() {
		unblock()
		app.StopEvents()
		server.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	editor := turnOneClient{t, server.URL, human, &http.Client{Timeout: 15 * time.Second}}
	serviceBody, headers := editor.call("GET", "/service", "", "", 200)
	original := turnOneDecode[serviceStatus](t, serviceBody)
	if original.State != "running" || headers.Get("ETag") != original.ETag {
		t.Fatal("unexpected initial service representation")
	}
	editor.call("PATCH", "/service", stopServiceBody, "", 428)
	editor.call("PATCH", "/service", stopServiceBody, etag(newID(), 1), 412)
	editor.call("PATCH", "/service", `{"state":"stopped","actor":"human"}`, original.ETag, 400)

	reader, cancelEvents := editor.openEvents("/events", "")
	defer cancelEvents()
	assertEventWorkspace(t, readEventFrame(t, reader), "snapshot", 1)
	assertEventCatalog(t, readEventFrame(t, reader), "snapshot")

	mutation := make(chan boardHTTPResult, 1)
	go func() {
		mutation <- boardRequest(editor, "POST", "/boards?hold-response=1", `{"name":"Admitted during shutdown"}`, "")
	}()
	select {
	case <-entered:
	case result := <-mutation:
		t.Fatalf("admitted mutation did not hold its response: %+v", result)
	case <-time.After(5 * time.Second):
		t.Fatal("admitted mutation did not reach response")
	}

	shutdown := make(chan boardHTTPResult, 1)
	go func() { shutdown <- boardRequest(editor, "PATCH", "/service", stopServiceBody, original.ETag) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := editor.call("GET", "/service", "", "", 200)
		if turnOneDecode[serviceStatus](t, raw).State == "draining" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not close command admission")
		}
		time.Sleep(10 * time.Millisecond)
	}
	raw, _ := editor.call("POST", "/boards", `{"name":"Refused during shutdown"}`, "", 503)
	if problem := turnOneDecode[problemBody](t, raw); problem.Code != "unavailable" || problem.Outcome != "not_applied" {
		t.Fatalf("unexpected drain refusal: %+v", problem)
	}
	editor.call("GET", "/events", "", "", 503)
	select {
	case result := <-shutdown:
		t.Fatalf("shutdown did not wait for admitted mutation: %+v", result)
	default:
	}

	unblock()
	committed := <-mutation
	if committed.err != nil || committed.status != 201 {
		t.Fatalf("admitted mutation did not commit: %+v", committed)
	}
	receipt := <-shutdown
	if receipt.err != nil || receipt.status != 202 || receipt.header.Get("ETag") != "" {
		t.Fatalf("unexpected shutdown receipt: %+v", receipt)
	}
	if status := turnOneDecode[serviceStatus](t, receipt.data); status.State != "draining" ||
		status.Port != original.Port || status.ServiceInstanceID != original.ServiceInstanceID {
		t.Fatalf("shutdown receipt identity changed: %+v", status)
	}
	select {
	case request := <-app.lifecycle.exit:
		if request.Replacement != nil || request.ActivationID != "" {
			t.Fatal("shutdown unexpectedly scheduled activation")
		}
	default:
		t.Fatal("native exit was not scheduled")
	}

	app.BeginEventDrain()
	assertEventCatalog(t, readEventFrame(t, reader), "change")
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("drained stream ended with %v", err)
	}
	b := turnOneDecode[board](t, committed.data)
	editor.call("GET", "/boards/"+b.ID, "", "", 200)
}

func TestM39ShutdownRefusesFailedPushAndFlushesLatestEnabledArtifacts(t *testing.T) {
	fixture := m34PublicationFixture(t)
	editor := fixture.editor
	path := "/boards/" + fixture.state.Board.ID
	raw, _ := editor.call("GET", "/service", "", "", 200)
	original := turnOneDecode[serviceStatus](t, raw)
	raw, _ = editor.call("GET", path, "", "", 200)
	b := turnOneDecode[board](t, raw)
	raw, _ = editor.call("PATCH", path, `{"publishing":{"enabled":true}}`, b.ETag, 200)
	b = turnOneDecode[board](t, raw)
	_, workspaceHeaders := editor.call("GET", "/workspace", "", "", 200)
	editor.call("PATCH", "/workspace", `{"publishing":{"enabled":true}}`, workspaceHeaders.Get("ETag"), 200)

	offline := fixture.remote + "-offline"
	if err := os.Rename(fixture.remote, offline); err != nil {
		t.Fatal(err)
	}
	raw, _ = editor.call("PATCH", "/service", stopServiceBody, original.ETag, 503)
	if problem := turnOneDecode[problemBody](t, raw); problem.Code != "unavailable" || problem.Outcome != "not_applied" {
		t.Fatalf("unexpected publication refusal: %+v", problem)
	}
	raw, _ = editor.call("GET", "/service", "", "", 200)
	refused := turnOneDecode[serviceStatus](t, raw)
	if refused.State != "running" || refused.ETag == original.ETag ||
		refused.ServiceInstanceID != original.ServiceInstanceID || snapshotString(refused.RestartErrorCode) != "git_push_failed" {
		t.Fatalf("refused shutdown did not retain running service and failure status: %+v", refused)
	}
	select {
	case <-fixture.app.lifecycle.exit:
		t.Fatal("refused shutdown scheduled exit")
	default:
	}
	editor.call("PATCH", path, `{"name":"Final admitted revision"}`, b.ETag, 200)
	wantBoard, err := os.ReadFile(fixture.app.store.snapshotPath(b.ID))
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspace, err := os.ReadFile(fixture.app.workspace.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(offline, fixture.remote); err != nil {
		t.Fatal(err)
	}
	editor.call("PATCH", "/service", stopServiceBody, refused.ETag, 202)
	gitRoot := "refs/heads/main:" + fixture.prefix + "/"
	gotBoard := m34Git(t, fixture.remote, "show", gitRoot+"boards/"+b.ID+".json")
	gotWorkspace := m34Git(t, fixture.remote, "show", gitRoot+workspaceFileName)
	if !bytes.Equal(gotBoard, wantBoard) || !bytes.Equal(gotWorkspace, wantWorkspace) {
		t.Fatal("origin did not receive the final durable board and workspace files")
	}
}

func TestM39ActivationReceiptValidatorsAndDurableClosure(t *testing.T) {
	human, bot := lifecycleTokens()
	root := t.TempDir()
	original, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = original.Close() })
	service := original.builds.service()
	id, buildID := newID(), newID()
	record := activationRecord{
		activation: activation{ID: id, BuildID: buildID, State: "accepted"},
		Origin:     service.ServiceInstanceID,
		Version:    "v2-test",
		Digest:     strings.Repeat("a", 64),
	}
	if _, err := original.lifecycle.begin(service, service.ETag, id); err != nil {
		t.Fatal(err)
	}
	if err := original.saveActivation(record); err != nil {
		t.Fatal(err)
	}
	path := "/activations/" + id
	accepted := lifecycleRequest(original, human, http.MethodGet, path, "")
	if accepted.Code != 200 || accepted.Header().Get("ETag") == "" ||
		turnOneDecode[activation](t, accepted.Body.Bytes()).State != "accepted" {
		t.Fatalf("invalid accepted receipt: status %d", accepted.Code)
	}
	oldBrowser := original.browserToken
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}

	replacement, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	if got := lifecycleRequest(replacement, oldBrowser, http.MethodGet, path, ""); got.Code != 401 {
		t.Fatal("old browser capability survived replacement")
	}
	pending := lifecycleRequest(replacement, human, http.MethodGet, path, "")
	if got := turnOneDecode[activation](t, pending.Body.Bytes()); got.State != "failed" ||
		snapshotString(got.ErrorCode) != "activation_interrupted" {
		t.Fatalf("unfinished durable receipt was reported as still active: %+v", got)
	}
	if err := replacement.CompleteActivation(id); err != nil {
		t.Fatal(err)
	}
	activated := lifecycleRequest(replacement, human, http.MethodGet, path, accepted.Header().Get("ETag"))
	tag := activated.Header().Get("ETag")
	if activated.Code != 200 || tag == "" || tag == accepted.Header().Get("ETag") ||
		turnOneDecode[activation](t, activated.Body.Bytes()).State != "activated" ||
		replacement.builds.service().BuildID != buildID {
		t.Fatal("terminal receipt or running build identity did not change")
	}
	head := lifecycleRequest(replacement, human, http.MethodHead, path, "")
	if head.Code != 200 || head.Header().Get("ETag") != tag || head.Body.Len() != 0 {
		t.Fatal("HEAD did not expose the terminal receipt validator")
	}
	match := lifecycleRequest(replacement, human, http.MethodGet, path, tag)
	if match.Code != 304 || match.Body.Len() != 0 || match.Header().Get("ETag") != tag {
		t.Fatal("matching terminal receipt validator did not return 304")
	}
	receiptPath := filepath.Join(root, activationDirectory, id+".json")
	for _, suffix := range []string{`,"unexpected":true`, ""} {
		code := `null`
		if suffix == "" {
			code = `"unexpected_failure"`
		}
		malformed := fmt.Sprintf(`{"id":%q,"build_id":%q,"state":"activated","error_code":%s,"origin":%q,"staged_version":%q,"staged_digest":%q%s}`,
			id, buildID, code, record.Origin, record.Version, record.Digest, suffix)
		if err := os.WriteFile(receiptPath, []byte(malformed), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := lifecycleRequest(replacement, human, http.MethodGet, path, ""); got.Code != 503 {
			t.Fatalf("malformed durable receipt returned status %d", got.Code)
		}
	}
}

func lifecycleRequest(app *Service, token, method, path, noneMatch string) *httptest.ResponseRecorder {
	address := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
	request := httptest.NewRequest(method, Prefix+path, nil)
	request.Host = address.String()
	request.Header.Set("Authorization", "Bearer "+token)
	if noneMatch != "" {
		request.Header.Set("If-None-Match", noneMatch)
	}
	response := httptest.NewRecorder()
	app.Handler(address).ServeHTTP(response, request)
	return response
}
