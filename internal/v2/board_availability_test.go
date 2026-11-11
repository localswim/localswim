package v2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type heldBoardWrite struct {
	http.ResponseWriter
	entered chan<- struct{}
	release <-chan struct{}
}

func (w heldBoardWrite) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.release
	return w.ResponseWriter.Write(data)
}

type boardHTTPResult struct {
	status int
	header http.Header
	data   []byte
	err    error
}

func boardRequest(c turnOneClient, method, path, body, tag string, boardTag ...string) boardHTTPResult {
	req, err := http.NewRequest(method, c.url+Prefix+path, strings.NewReader(body))
	if err != nil {
		return boardHTTPResult{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if tag != "" {
		req.Header.Set("If-Match", tag)
	}
	if len(boardTag) > 0 {
		req.Header.Set("X-Expected-Board-ETag", boardTag[0])
	}
	res, err := c.client.Do(req)
	if err != nil {
		return boardHTTPResult{err: err}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	return boardHTTPResult{res.StatusCode, res.Header, data, err}
}

func TestM37CloseDrainsSelectedBoardAndReopensSameResources(t *testing.T) {
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	handler := app.Handler(server.Listener.Addr().(*net.TCPAddr))
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery == "disposition=attachment" && strings.HasSuffix(r.URL.Path, "/content") {
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
	raw, _ := editor.call("POST", "/boards", `{"name":"Board to close"}`, "", 201)
	initial := turnOneDecode[board](t, raw)
	path := "/boards/" + initial.ID
	item := attachmentHTTPCard(t, editor, initial)
	contents := []byte("retained attachment")
	upload, uploaded := attachmentHTTPUpload(t, editor, []attachmentFormField{
		{"card_id", item.ID}, {"expected_card_etag", item.entityTag()},
	}, "note.txt", contents, 201)
	attached := turnOneDecode[attachment](t, upload)
	contentPath := "/attachments/" + attached.ID + "/content"
	postUpload := initial
	postUpload.ETag = uploaded.Get("X-Board-ETag")
	otherCard := attachmentHTTPCard(t, editor, postUpload)
	raw, _ = editor.call("GET", path, "", "", 200)
	withCards := turnOneDecode[board](t, raw)
	relationshipBody := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"references"}`,
		withCards.ID, withCards.ETag, item.ID, otherCard.ID)
	raw, _ = editor.call("POST", "/relationships", relationshipBody, "", 201)
	linked := turnOneDecode[relationship](t, raw)
	relationshipPath := "/relationships/" + linked.ID
	assertDeletesRefused := func(boardTag string) {
		t.Helper()
		targets := []struct{ path, tag string }{
			{"/attachments/" + attached.ID, attached.entityTag()},
			{relationshipPath, linked.entityTag()},
		}
		for _, target := range targets {
			result := boardRequest(editor, "DELETE", target.path, "", target.tag, boardTag)
			if result.err != nil || result.status != 503 {
				t.Fatalf("DELETE %s returned status %d (%v)", target.path, result.status, result.err)
			}
			problem := turnOneDecode[problemBody](t, result.data)
			if problem.Code != "unavailable" || problem.Outcome != "not_applied" {
				t.Fatalf("DELETE %s returned an unexpected outcome: %+v", target.path, problem)
			}
		}
	}
	raw, _ = editor.call("GET", "/cards/"+item.ID, "", "", 200)
	item = turnOneDecode[card](t, raw)
	raw, _ = editor.call("GET", path, "", "", 200)
	before := turnOneDecode[board](t, raw)
	raw, _ = editor.call("POST", "/boards", `{"name":"Other board"}`, "", 201)
	other := turnOneDecode[board](t, raw)

	for _, body := range []string{`{"availability":"closed "}`, `{"availability":"closing"}`, `{"availability":null}`, `{"availability":42}`} {
		editor.call("PATCH", path, body, before.ETag, 400)
	}
	editor.call("PATCH", path, `{"availability":"closed"}`, initial.ETag, 412)
	editor.call("PATCH", path, `{"availability":"closed"}`, "", 428)
	raw, _ = editor.call("PATCH", path, `{"availability":"OPEN"}`, before.ETag, 200)
	if current := turnOneDecode[board](t, raw); current.ETag != before.ETag {
		t.Fatal("availability no-op advanced revision")
	}

	reader, endEvents := editor.openEvents("/events?board_id="+before.ID, "")
	defer endEvents()
	assertEventWorkspace(t, readEventFrame(t, reader), "snapshot", 1)
	assertEventCatalog(t, readEventFrame(t, reader), "snapshot")
	assertEventBoard(t, readEventFrame(t, reader), "snapshot", before)

	streamed := make(chan boardHTTPResult, 1)
	go func() { streamed <- boardRequest(editor, "GET", contentPath+"?disposition=attachment", "", "") }()
	select {
	case <-entered:
	case result := <-streamed:
		t.Fatalf("attachment response did not reach stream gate: %+v", result)
	case <-time.After(5 * time.Second):
		t.Fatal("attachment stream did not start")
	}

	closedResult := make(chan boardHTTPResult, 1)
	go func() {
		closedResult <- boardRequest(editor, "PATCH", path, `{"availability":"CloSEd","name":"Closed board"}`, before.ETag)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		result := boardRequest(editor, "GET", path+"?view=lanes", "", "")
		if result.err != nil || result.status != 200 && result.status != 503 {
			t.Fatalf("unexpected lane view while closing: %+v", result)
		}
		if result.status == 503 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("close did not stop new board admission")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case result := <-closedResult:
		t.Fatalf("close completed before the admitted stream drained: %+v", result)
	default:
	}
	raw, _ = editor.call("GET", path, "", "", 200)
	if current := turnOneDecode[board](t, raw); current.Availability != "open" || current.ETag != before.ETag || current.Name != before.Name {
		t.Fatal("uncommitted close candidate was visible")
	}
	editor.call("PATCH", path, `{"availability":"open"}`, before.ETag, 503)
	assertDeletesRefused(before.ETag)
	editor.call("GET", "/cards/"+item.ID, "", "", 503)
	_ = attachmentHTTPCard(t, editor, other)
	editor.call("GET", "/workspace", "", "", 200)

	unblock()
	if result := <-streamed; result.err != nil || result.status != 200 || !bytes.Equal(result.data, contents) {
		t.Fatalf("admitted stream did not finish: %+v", result)
	}
	result := <-closedResult
	if result.err != nil || result.status != 200 {
		t.Fatalf("close failed: %+v", result)
	}
	closed := turnOneDecode[board](t, result.data)
	if closed.Availability != "closed" || closed.Name != "Closed board" || closed.ID != before.ID ||
		closed.Revision != before.Revision+1 || result.header.Get("ETag") != closed.ETag || result.header.Get("X-Board-ETag") != closed.ETag {
		t.Fatalf("unexpected closed board: %+v", closed)
	}
	// The unrelated card also causes a catalog event. Consume it if the
	// worker observed its commit separately from the selected board close.
	for frame := readEventFrame(t, reader); ; frame = readEventFrame(t, reader) {
		if frame.Resource == "board" {
			assertEventBoard(t, frame, "change", closed)
			break
		}
		assertEventCatalog(t, frame, "change")
	}

	for _, route := range []string{path + "?view=lanes", "/lanes/" + before.LaneIDs[0], "/cards/" + item.ID,
		"/cards?board_id=" + before.ID, contentPath} {
		editor.call("GET", route, "", "", 503)
	}
	editor.call("PATCH", "/cards/"+item.ID, `{"title":"Refused"}`, item.entityTag(), 503)
	assertDeletesRefused(closed.ETag)
	editor.call("GET", "/policies/"+closed.PolicyID, "", "", 200)
	editor.call("PATCH", path, `{"availability":"closed","name":"Refused"}`, closed.ETag, 503)
	raw, headers := editor.call("PATCH", path, `{"availability":"cLoSeD"}`, closed.ETag, 200)
	noOp := turnOneDecode[board](t, raw)
	if noOp.ETag != closed.ETag || noOp.Revision != closed.Revision || noOp.Name != closed.Name ||
		headers.Get("X-Board-ETag") != closed.ETag {
		t.Fatalf("availability no-op changed closed board: %+v", noOp)
	}
	raw, _ = editor.call("GET", "/boards", "", "", 200)
	rows := turnOnePage[boardSummary](t, raw, "boards").Values
	index := slices.IndexFunc(rows, func(row boardSummary) bool { return row.ID == closed.ID })
	if index < 0 {
		t.Fatal("closed board disappeared from the catalog")
	}
	row := rows[index]
	if row.Availability != "closed" || row.Attention != nil || row.LastCardEditAt != nil {
		t.Fatal("closed catalog row still advertises attention")
	}
	durable, err := app.store.durable(closed.ID)
	if err != nil || durable.Board.ETag != closed.ETag {
		t.Fatal("closed revision is not durable", err)
	}
	audit := durable.Activity[len(durable.Activity)-2]
	if snapshotString(audit.Field) != "availability" || snapshotString(audit.Before) != "open" ||
		snapshotString(audit.After) != "closed" || audit.ActorID != humanID || audit.Revision != closed.Revision {
		t.Fatalf("invalid availability audit: %+v", audit)
	}

	_ = endEvents()
	server.Close()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	_, restarted := restartBoardPublication(t, app.store.root, human)
	raw, _ = restarted.call("GET", path, "", "", 200)
	if persisted := turnOneDecode[board](t, raw); persisted.ETag != closed.ETag || persisted.Availability != "closed" {
		t.Fatal("service restart changed board availability")
	}
	restarted.call("PATCH", path, `{"availability":"open"}`, before.ETag, 412)
	raw, headers = restarted.call("PATCH", path, `{"availability":"OpEN","name":"Reopened board"}`, closed.ETag, 200)
	opened := turnOneDecode[board](t, raw)
	if opened.ID != closed.ID || opened.Availability != "open" || opened.Name != "Reopened board" ||
		opened.Revision != closed.Revision+1 || headers.Get("X-Board-ETag") != opened.ETag {
		t.Fatalf("unexpected reopened board: %+v", opened)
	}
	raw, _ = restarted.call("GET", "/cards/"+item.ID, "", "", 200)
	if retained := turnOneDecode[card](t, raw); retained.ID != item.ID || retained.Revision != item.Revision || retained.Title != item.Title {
		t.Fatal("reopen changed the retained card")
	}
	restarted.call("GET", relationshipPath, "", "", 200)
	if download := boardRequest(restarted, "GET", contentPath, "", ""); download.err != nil ||
		download.status != 200 || !bytes.Equal(download.data, contents) {
		t.Fatal("reopen did not preserve attachment content")
	}
}

func TestM37FailedFlushLeavesOpenAndRetryPublishesExactLatestRevision(t *testing.T) {
	fixture := m34PublicationFixture(t)
	editor := fixture.editor
	path := "/boards/" + fixture.state.Board.ID
	raw, _ := editor.call("GET", path, "", "", 200)
	start := turnOneDecode[board](t, raw)
	raw, _ = editor.call("PATCH", path, `{"publishing":{"enabled":true}}`, start.ETag, 200)
	enabled := turnOneDecode[board](t, raw)
	offline := fixture.remote + "-offline"
	if err := os.Rename(fixture.remote, offline); err != nil {
		t.Fatal(err)
	}

	raw, _ = editor.call("PATCH", path, `{"availability":"closed","name":"Should not apply"}`, enabled.ETag, 503)
	problem := turnOneDecode[problemBody](t, raw)
	if problem.Code != "unavailable" || problem.Outcome != "not_applied" {
		t.Fatalf("expected known flush refusal: %+v", problem)
	}
	raw, _ = editor.call("GET", path, "", "", 200)
	failed := turnOneDecode[board](t, raw)
	if failed.Availability != "open" || failed.ETag != enabled.ETag || failed.Name != start.Name ||
		!failed.Publishing.Pending || snapshotString(failed.Publishing.ErrorCode) != "git_push_failed" {
		t.Fatalf("failed flush changed availability or hid retry state: %+v", failed)
	}
	editor.call("GET", path+"?view=lanes", "", "", 200)
	raw, _ = editor.call("PATCH", path, `{"name":"Final open revision"}`, failed.ETag, 200)
	latest := turnOneDecode[board](t, raw)
	want, err := os.ReadFile(fixture.app.store.snapshotPath(latest.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(offline, fixture.remote); err != nil {
		t.Fatal(err)
	}
	raw, _ = editor.call("PATCH", path, `{"availability":"closed","publishing":{"enabled":false}}`, latest.ETag, 200)
	closed := turnOneDecode[board](t, raw)
	if closed.Availability != "closed" || closed.Revision != latest.Revision+1 || closed.Publishing.Enabled ||
		closed.Publishing.Pending || closed.Publishing.ErrorCode != nil || closed.Publishing.LastSuccessAt == nil {
		t.Fatalf("close did not settle both the pending and latest publication: %+v", closed)
	}
	artifact := fixture.prefix + "/boards/" + latest.ID + ".json"
	got := m34Git(t, fixture.remote, "show", "refs/heads/main:"+artifact)
	if !bytes.Equal(got, want) {
		t.Fatal("origin does not contain the final accepted open revision")
	}
	published := turnOneDecode[snapshot](t, got)
	if published.Board.Availability != "open" || published.Board.Revision != latest.Revision {
		t.Fatal("the uncommitted closed candidate was sent to origin")
	}
	raw, _ = editor.call("PATCH", path, `{"availability":"open","publishing":{"enabled":true}}`, closed.ETag, 200)
	reopened := turnOneDecode[board](t, raw)
	if reopened.Availability != "open" || !reopened.Publishing.Pending || reopened.Publishing.LastSuccessAt == nil {
		t.Fatalf("reopen did not restart the board publisher: %+v", reopened)
	}
}
