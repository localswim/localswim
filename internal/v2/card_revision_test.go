package v2

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertCardRevision(t *testing.T, raw []byte, want uint64, tag string) card {
	t.Helper()
	fields := turnOneDecode[map[string]json.RawMessage](t, raw)
	if _, present := fields["etag"]; present {
		t.Fatal("card JSON contains an etag field")
	}
	if string(fields["revision"]) != fmt.Sprint(want) {
		t.Fatalf("card revision = %s, want numeric %d", fields["revision"], want)
	}
	value := turnOneDecode[card](t, raw)
	if tag != "" && tag != value.entityTag() {
		t.Fatalf("card header tag = %s, want %s", tag, value.entityTag())
	}
	return value
}

func TestCardRevisionRepresentationAndRestart(t *testing.T) {
	app, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Card revisions"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	raw, _ = client.call("PATCH", "/boards/"+b.ID, `{"name":"Before creation"}`, b.ETag, 200)
	b = turnOneDecode[board](t, raw)
	body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"First"}`, b.ID, b.ETag, b.LaneIDs[0])
	raw, headers := client.call("POST", "/cards", body, "", 201)
	first := assertCardRevision(t, raw, 3, headers.Get("ETag"))
	body = fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Second"}`, b.ID, headers.Get("X-Board-ETag"), b.LaneIDs[0])
	raw, headers = client.call("POST", "/cards", body, "", 201)
	second := assertCardRevision(t, raw, 4, headers.Get("ETag"))
	client.call("PATCH", "/boards/"+b.ID, `{"name":"Unrelated board edit"}`, headers.Get("X-Board-ETag"), 200)
	raw, headers = client.call("GET", "/cards/"+first.ID, "", "", 200)
	assertCardRevision(t, raw, 3, headers.Get("ETag"))

	// The resource tag remains valid after unrelated board and card creation edits.
	raw, headers = client.call("PATCH", "/cards/"+first.ID, `{"title":"Updated"}`, first.entityTag(), 200)
	first = assertCardRevision(t, raw, 6, headers.Get("ETag"))
	client.call("PATCH", "/cards/"+first.ID, `{"title":"Stale"}`, etag(first.ID, 3), 412)
	raw, headers = client.call("PATCH", "/cards/"+first.ID, `{"title":"Updated"}`, first.entityTag(), 200)
	assertCardRevision(t, raw, 6, headers.Get("ETag"))
	raw, _ = client.call("GET", "/cards?board_id="+b.ID, "", "", 200)
	page := turnOnePage[json.RawMessage](t, raw, "cards")
	if len(page.Values) != 2 {
		t.Fatal("card list did not contain both cards")
	}
	assertCardRevision(t, page.Values[0], 6, "")
	assertCardRevision(t, page.Values[1], 4, "")

	state, ok := app.store.accepted(b.ID)
	if !ok {
		t.Fatal("board disappeared")
	}
	data := marshalVerificationFixture(t, state)
	native := turnOneDecode[struct {
		Cards []json.RawMessage `json:"cards"`
	}](t, data)
	assertCardRevision(t, native.Cards[0], 6, "")
	assertCardRevision(t, native.Cards[1], 4, "")
	for _, replacement := range []string{`"revision":0`, `"revision":7`, `"revision":-1`, `"revision":1.5`, `"revision":"6"`, `"revision":null`, `"etag":"obsolete"`, `"revision":6,"etag":"obsolete"`} {
		t.Run(replacement, func(t *testing.T) {
			changed := strings.Replace(string(native.Cards[0]), `"revision":6`, replacement, 1)
			damaged := strings.Replace(string(data), string(native.Cards[0]), changed, 1)
			if damaged == string(data) {
				t.Fatal("native card revision fixture was not replaced")
			}
			if _, err := parseSnapshot([]byte(damaged), b.ID); err == nil {
				t.Fatal("accepted invalid or obsolete native card revision")
			}
		})
	}

	// Closing and reopening exercises native admission, not just an in-memory read.
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
	raw, headers = client.call("GET", "/cards/"+first.ID, "", "", 200)
	assertCardRevision(t, raw, first.Revision, headers.Get("ETag"))
	raw, headers = client.call("GET", "/cards/"+second.ID, "", "", 200)
	assertCardRevision(t, raw, second.Revision, headers.Get("ETag"))

	// Numeric revision is ordinary cloned card state and cannot mutate accepted state.
	state, _ = reopened.store.accepted(b.ID)
	state.Cards[0].Revision++
	accepted, _ := reopened.store.accepted(b.ID)
	if accepted.Cards[0].Revision != first.Revision {
		t.Fatal("changing a caller's card revision changed accepted state")
	}
}
