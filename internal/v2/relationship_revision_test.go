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

func assertRelationshipRevision(t *testing.T, raw []byte, want uint64, tag string) relationship {
	t.Helper()
	fields := turnOneDecode[map[string]json.RawMessage](t, raw)
	if _, present := fields["etag"]; present {
		t.Fatal("relationship JSON contains an etag field")
	}
	if string(fields["revision"]) != fmt.Sprint(want) {
		t.Fatalf("relationship revision = %s, want numeric %d", fields["revision"], want)
	}
	value := turnOneDecode[relationship](t, raw)
	if tag != "" && tag != value.entityTag() {
		t.Fatalf("relationship header tag = %s, want %s", tag, value.entityTag())
	}
	return value
}

func TestRelationshipRevisionRepresentationAndRestart(t *testing.T) {
	app, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Relationship revisions"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"First"}`, b.ID, b.entityTag(), b.LaneIDs[0])
	raw, headers := client.call("POST", "/cards", body, "", 201)
	first := turnOneDecode[card](t, raw)
	body = fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Second"}`, b.ID, headers.Get("X-Board-ETag"), b.LaneIDs[0])
	raw, headers = client.call("POST", "/cards", body, "", 201)
	second := turnOneDecode[card](t, raw)
	body = fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`, b.ID, headers.Get("X-Board-ETag"), first.ID, second.ID)
	raw, headers = client.call("POST", "/relationships", body, "", 201)
	item := assertRelationshipRevision(t, raw, 4, headers.Get("ETag"))
	raw, _ = client.call("PATCH", "/boards/"+b.ID, `{"name":"Unrelated edit"}`, headers.Get("X-Board-ETag"), 200)
	b = turnOneDecode[board](t, raw)
	path := "/relationships/" + item.ID
	raw, headers = client.call("GET", path, "", "", 200)
	assertRelationshipRevision(t, raw, 4, headers.Get("ETag"))
	patch := fmt.Sprintf(`{"expected_board_etag":%q,"kind":"references"}`, b.entityTag())
	client.call("PATCH", path, patch, "W/"+item.entityTag(), 412)
	raw, headers = client.call("PATCH", path, patch, item.entityTag(), 200)
	item = assertRelationshipRevision(t, raw, 6, headers.Get("ETag"))
	boardTag := headers.Get("X-Board-ETag")
	patch = fmt.Sprintf(`{"expected_board_etag":%q,"kind":"references"}`, boardTag)
	client.call("PATCH", path, patch, etag(item.ID, 4), 412)
	raw, headers = client.call("PATCH", path, patch, item.entityTag(), 200)
	assertRelationshipRevision(t, raw, 6, headers.Get("ETag"))
	if headers.Get("X-Board-ETag") != boardTag {
		t.Fatal("same-kind replacement advanced board revision")
	}
	client.call("PATCH", "/cards/"+first.ID, `{"title":"Unrelated card edit"}`, etag(first.ID, 6), 200)
	raw, headers = client.call("GET", path, "", "", 200)
	assertRelationshipRevision(t, raw, 6, headers.Get("ETag"))
	raw, _ = client.call("GET", "/relationships?board_id="+b.ID, "", "", 200)
	page := turnOnePage[json.RawMessage](t, raw, "relationships")
	if len(page.Values) != 1 {
		t.Fatal("relationship list has wrong membership")
	}
	assertRelationshipRevision(t, page.Values[0], 6, "")
	state, _ := app.store.accepted(b.ID)
	data := marshalVerificationFixture(t, state)
	native := turnOneDecode[struct {
		Relationships []json.RawMessage `json:"relationships"`
	}](t, data)
	original := native.Relationships[0]
	assertRelationshipRevision(t, original, 6, "")
	for _, replacement := range []string{`"revision":0`, `"revision":1`, `"revision":4`, `"revision":7`, `"revision":8`, `"revision":9007199254740992`, `"revision":-1`, `"revision":1.5`, `"revision":"6"`, `"revision":null`, `"etag":"obsolete"`, `"revision":6,"etag":"obsolete"`} {
		t.Run(replacement, func(t *testing.T) {
			changed := strings.Replace(string(original), `"revision":6`, replacement, 1)
			damaged := strings.Replace(string(data), string(original), changed, 1)
			if damaged == string(data) {
				t.Fatal("native fixture was not replaced")
			}
			if _, err := parseSnapshot([]byte(damaged), b.ID); err == nil {
				t.Fatal("accepted invalid or obsolete native relationship revision")
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
	raw, headers = client.call("GET", path, "", "", 200)
	assertRelationshipRevision(t, raw, 6, headers.Get("ETag"))
	state, _ = reopened.store.accepted(b.ID)
	state.Relationships[0].Revision++
	accepted, _ := reopened.store.accepted(b.ID)
	if accepted.Relationships[0].Revision != 6 {
		t.Fatal("caller changed accepted relationship revision")
	}
}

func TestRelationshipRevisionIsServiceManaged(t *testing.T) {
	_, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Protected relationship revision"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	var cards []card
	boardTag := b.entityTag()
	for _, title := range []string{"First", "Second"} {
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q}`, b.ID, boardTag, b.LaneIDs[0], title)
		raw, headers := client.call("POST", "/cards", body, "", 201)
		cards = append(cards, turnOneDecode[card](t, raw))
		boardTag = headers.Get("X-Board-ETag")
	}
	body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`, b.ID, boardTag, cards[0].ID, cards[1].ID)
	raw, headers := client.call("POST", "/relationships", body, "", 201)
	item := turnOneDecode[relationship](t, raw)
	boardTag = headers.Get("X-Board-ETag")
	for _, field := range []string{"revision", "etag"} {
		t.Run(field, func(t *testing.T) {
			extra := fmt.Sprintf(`,%q:9}`, field)
			client.call("POST", "/relationships", strings.TrimSuffix(body, "}")+extra, "", 400)
			patch := fmt.Sprintf(`{"expected_board_etag":%q,"kind":"references"`, boardTag) + extra
			client.call("PATCH", "/relationships/"+item.ID, patch, item.entityTag(), 400)
			seed := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Seed","relationships":[{"target_card_id":%q,"kind":"blocks"%s]}`, b.ID, boardTag, b.LaneIDs[0], cards[0].ID, extra)
			client.call("POST", "/cards", seed, "", 400)
		})
	}
}
