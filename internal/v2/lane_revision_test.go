package v2

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func assertLaneRevision(t *testing.T, raw []byte, want uint64, tag string) lane {
	t.Helper()
	fields := turnOneDecode[map[string]json.RawMessage](t, raw)
	if _, present := fields["etag"]; present {
		t.Fatal("lane JSON contains an etag field")
	}
	if string(fields["revision"]) != fmt.Sprint(want) {
		t.Fatalf("lane revision = %s, want numeric %d", fields["revision"], want)
	}
	value := turnOneDecode[lane](t, raw)
	if tag != "" && tag != value.entityTag() {
		t.Fatalf("lane header tag = %s, want %s", tag, value.entityTag())
	}
	return value
}

func TestLaneRevisionRepresentationAndRestart(t *testing.T) {
	app, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Lane revisions"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	raw, headers := client.call("GET", "/lanes/"+b.LaneIDs[0], "", "", 200)
	original := assertLaneRevision(t, raw, 1, headers.Get("ETag"))
	raw, _ = client.call("PATCH", "/boards/"+b.ID, `{"name":"Before lane creation"}`, b.entityTag(), 200)
	b = turnOneDecode[board](t, raw)
	body := fmt.Sprintf(`{"board_id":%q,"board_etag":%q,"label":"Extra","connect_for":[%q]}`, b.ID, b.entityTag(), humanID)
	raw, headers = client.call("POST", "/lanes", body, "", 201)
	added := assertLaneRevision(t, raw, 3, headers.Get("ETag"))
	currentBoardTag := headers.Get("X-Board-ETag")
	raw, headers = client.call("GET", "/lanes/"+original.ID, "", "", 200)
	assertLaneRevision(t, raw, 1, headers.Get("ETag"))

	patch := fmt.Sprintf(`{"board_etag":%q,"label":"Renamed"}`, currentBoardTag)
	client.call("PATCH", "/lanes/"+original.ID, patch, "W/"+original.entityTag(), 412)
	staleBoardPatch := fmt.Sprintf(`{"board_etag":%q,"label":"Renamed"}`, b.entityTag())
	client.call("PATCH", "/lanes/"+original.ID, staleBoardPatch, original.entityTag(), 412)
	raw, headers = client.call("PATCH", "/lanes/"+original.ID, patch, original.entityTag(), 200)
	renamed := assertLaneRevision(t, raw, 4, headers.Get("ETag"))
	currentBoardTag = headers.Get("X-Board-ETag")
	patch = fmt.Sprintf(`{"board_etag":%q,"label":"Renamed"}`, currentBoardTag)
	client.call("PATCH", "/lanes/"+original.ID, patch, original.entityTag(), 412)
	raw, headers = client.call("PATCH", "/lanes/"+original.ID, patch, renamed.entityTag(), 200)
	assertLaneRevision(t, raw, 4, headers.Get("ETag"))
	if headers.Get("X-Board-ETag") != currentBoardTag {
		t.Fatal("same-label rename advanced the board revision")
	}
	client.call("PATCH", "/boards/"+b.ID, `{"name":"After lane rename"}`, currentBoardTag, 200)
	raw, headers = client.call("GET", "/lanes/"+renamed.ID, "", "", 200)
	assertLaneRevision(t, raw, 4, headers.Get("ETag"))
	raw, _ = client.call("GET", "/lanes?board_id="+b.ID, "", "", 200)
	page := turnOnePage[json.RawMessage](t, raw, "lanes")
	if len(page.Values) != len(b.LaneIDs)+1 {
		t.Fatal("lane collection has the wrong membership")
	}
	for _, member := range page.Values {
		value := turnOneDecode[lane](t, member)
		want := uint64(1)
		if value.ID == renamed.ID {
			want = 4
		}
		if value.ID == added.ID {
			want = 3
		}
		assertLaneRevision(t, member, want, "")
	}

	state, _ := app.store.accepted(b.ID)
	data := marshalVerificationFixture(t, state)
	native := turnOneDecode[struct {
		Lanes []json.RawMessage `json:"lanes"`
	}](t, data)
	originalJSON := native.Lanes[0]
	assertLaneRevision(t, originalJSON, 4, "")
	for _, replacement := range []string{`"revision":0`, `"revision":2`, `"revision":5`, `"revision":6`, `"revision":-1`, `"revision":1.5`, `"revision":"4"`, `"revision":null`, `"etag":"obsolete"`, `"revision":4,"etag":"obsolete"`} {
		t.Run(replacement, func(t *testing.T) {
			changed := strings.Replace(string(originalJSON), `"revision":4`, replacement, 1)
			damaged := strings.Replace(string(data), string(originalJSON), changed, 1)
			if damaged == string(data) {
				t.Fatal("native lane fixture was not replaced")
			}
			if _, err := parseSnapshot([]byte(damaged), b.ID); err == nil {
				t.Fatal("accepted invalid or obsolete native lane revision")
			}
		})
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	botToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	reopened, err := Open(app.store.root, client.token, botToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = reopened.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)
	client = turnOneClient{t, server.URL, client.token, server.Client()}
	for _, expected := range []lane{renamed, added} {
		raw, headers = client.call("GET", "/lanes/"+expected.ID, "", "", 200)
		assertLaneRevision(t, raw, expected.Revision, headers.Get("ETag"))
	}
	state, _ = reopened.store.accepted(b.ID)
	state.Lanes[0].Revision++
	accepted, _ := reopened.store.accepted(b.ID)
	if accepted.Lanes[0].Revision != renamed.Revision {
		t.Fatal("changing a caller's lane revision changed accepted state")
	}
}

func TestLaneRevisionIsServiceManaged(t *testing.T) {
	_, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Protected lane revision"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	for _, field := range []string{"revision", "etag"} {
		t.Run(field, func(t *testing.T) {
			extra := fmt.Sprintf(`,%q:9`, field)
			create := fmt.Sprintf(`{"board_id":%q,"board_etag":%q,"label":"Extra","connect_for":[%q]%s}`, b.ID, b.entityTag(), humanID, extra)
			client.call("POST", "/lanes", create, "", 400)
			patch := fmt.Sprintf(`{"board_etag":%q,"label":"Renamed"%s}`, b.entityTag(), extra)
			client.call("PATCH", "/lanes/"+b.LaneIDs[0], patch, etag(b.LaneIDs[0], 1), 400)
			custom := strings.Replace(workflowTestInput, `"future":{"flag":true}`, fmt.Sprintf(`%q:9`, field), 1)
			if _, err := customWorkflow(json.RawMessage(custom), workflowTestBoardID, time.Now()); err == nil {
				t.Fatal("custom workflow accepted service-managed lane metadata")
			}
		})
	}
	wf, err := customWorkflow(json.RawMessage(workflowTestInput), workflowTestBoardID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range wf.Lanes {
		encoded, err := marshalCanonical(item)
		if err != nil {
			t.Fatal(err)
		}
		assertLaneRevision(t, encoded, 1, item.entityTag())
	}
}
