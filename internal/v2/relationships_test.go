package v2

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestM09RelationshipLifecycle(t *testing.T) {
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)

	c := turnOneClient{t, server.URL, human, server.Client()}
	botClient := turnOneClient{t, server.URL, bot, server.Client()}
	raw, _ := c.call("POST", "/boards", `{"name":"Relationships"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	firstBody := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"First"}`,
		b.ID, b.entityTag(), b.LaneIDs[0])
	raw, headers := c.call("POST", "/cards", firstBody, "", 201)
	first := turnOneDecode[card](t, raw)
	boardTag := headers.Get("X-Board-ETag")
	secondID := newID()
	secondBody := func(targetID string) string {
		return fmt.Sprintf(`{"id":%q,"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Second",`+
			`"relationships":[{"target_card_id":%q,"kind":"REFERENCES"},{"target_card_id":%q,"kind":"relates_to"}]}`,
			secondID, b.ID, boardTag, b.LaneIDs[0], first.ID, targetID)
	}

	c.call("POST", "/cards", secondBody(newID()), "", 400)
	refused, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refused.Board.entityTag() != boardTag || len(refused.Cards) != 1 || len(refused.Relationships) != 0 ||
		len(refused.Activity) != 2 || refused.Cards[0].Revision != first.Revision {
		t.Fatal("refused seed changed board, ticket allocation, endpoint or audit")
	}

	raw, headers = c.call("POST", "/cards", secondBody(first.ID), "", 201)
	second := turnOneDecode[card](t, raw)
	boardTag = headers.Get("X-Board-ETag")
	if second.ID != secondID || second.Ticket != 2 || second.Revision != b.Revision+2 {
		t.Fatalf("unexpected seeded card: %+v", second)
	}
	seeded, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if seeded.Board.Revision != b.Revision+2 || len(seeded.Relationships) != 2 || len(seeded.Activity) != 5 ||
		seeded.Cards[0].Revision != seeded.Board.Revision {
		t.Fatal("card, endpoint versions, seed links and audit did not commit together")
	}
	for _, item := range seeded.Activity[2:] {
		if item.Revision != seeded.Board.Revision || item.ActorID != humanID {
			t.Fatal("seed audit did not use the card transaction revision and Human actor")
		}
	}
	listPath := "/relationships?board_id=" + b.ID + "&card_id="
	raw, _ = c.call("GET", listPath+first.ID, "", "", 200)
	page := turnOnePage[json.RawMessage](t, raw, "relationships")
	for _, value := range page.Values {
		assertRelationshipRevision(t, value, seeded.Board.Revision, "")
	}
	links := turnOnePage[relationship](t, raw, "relationships").Values
	if len(links) != 2 || links[0].Kind != "references" ||
		links[0].SourceCardID != second.ID || links[0].TargetCardID != first.ID {
		t.Fatalf("unexpected links from the first endpoint: %+v", links)
	}
	raw, _ = c.call("GET", listPath+strings.ToUpper(second.ID), "", "", 200)
	other := turnOnePage[relationship](t, raw, "relationships").Values
	if len(other) != 2 || other[0] != links[0] || other[1] != links[1] {
		t.Fatal("listing from the second endpoint did not include both link directions")
	}

	createBody := func(source, target, kind, tag string) string {
		return fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":%q}`,
			b.ID, tag, source, target, kind)
	}

	for _, kind := range []string{"blocked_by", "DUPLICATED_BY", "referenced_by"} {
		c.call("POST", "/relationships", createBody(first.ID, second.ID, kind, boardTag), "", 400)
		invalidSeed := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Refused","relationships":[{"target_card_id":%q,"kind":%q}]}`, b.ID, boardTag, b.LaneIDs[0], first.ID, kind)
		c.call("POST", "/cards", invalidSeed, "", 400)
	}
	c.call("POST", "/relationships", createBody(first.ID, second.ID, "blocks", `"short"`), "", 400)
	c.call("POST", "/relationships", createBody(first.ID, second.ID, "relates_to", boardTag), "", 409)
	c.call("POST", "/relationships", createBody(first.ID, first.ID, "references", boardTag), "", 400)
	c.call("POST", "/relationships", createBody(first.ID, newID(), "references", boardTag), "", 404)
	raw, headers = c.call("POST", "/relationships", createBody(second.ID, first.ID, "blocks", boardTag), "", 201)
	block := assertRelationshipRevision(t, raw, seeded.Board.Revision+1, headers.Get("ETag"))
	boardTag = headers.Get("X-Board-ETag")
	if headers.Get("Location") != Prefix+"/relationships/"+block.ID || headers.Get("ETag") != block.entityTag() ||
		block.SourceCardID != second.ID || block.TargetCardID != first.ID || block.Kind != "blocks" {
		t.Fatalf("unexpected relationship creation response: %+v", block)
	}

	path := "/relationships/" + links[0].ID
	patchBody := func(tag, kind string) string {
		return fmt.Sprintf(`{"expected_board_etag":%q,"kind":%q}`, tag, kind)
	}
	for _, kind := range []string{"blocked_by", "duplicated_by", "REFERENCED_BY"} {
		c.call("PATCH", path, patchBody(boardTag, kind), links[0].entityTag(), 400)
	}
	c.call("PATCH", path, patchBody(`"contains.invalid.punctuation"`, "blocks"), links[0].entityTag(), 400)
	c.call("PATCH", path, patchBody(boardTag, "blocks"), links[0].entityTag(), 409)
	c.call("PATCH", path, patchBody(boardTag, "DUPLICATES"), block.entityTag(), 412)
	c.call("PATCH", path, patchBody(seeded.Board.entityTag(), "DUPLICATES"), links[0].entityTag(), 412)
	raw, headers = botClient.call("PATCH", path, patchBody(boardTag, "DUPLICATES"), links[0].entityTag(), 200)
	patched := assertRelationshipRevision(t, raw, seeded.Board.Revision+2, headers.Get("ETag"))
	boardTag = headers.Get("X-Board-ETag")
	if patched.ID != links[0].ID || patched.entityTag() == links[0].entityTag() || patched.Kind != "duplicates" ||
		patched.SourceCardID != second.ID || patched.TargetCardID != first.ID {
		t.Fatalf("unexpected replacement response: %+v", patched)
	}
	raw, headers = botClient.call("PATCH", path, patchBody(boardTag, "duplicates"), patched.entityTag(), 200)
	assertRelationshipRevision(t, raw, patched.Revision, headers.Get("ETag"))
	if headers.Get("X-Board-ETag") != boardTag || headers.Get("ETag") != patched.entityTag() {
		t.Fatal("unchanged relationship replacement advanced a version")
	}
	raw, headers = c.call("GET", path, "", "", 200)
	if headers.Get("ETag") != patched.entityTag() || turnOneDecode[relationship](t, raw) != patched {
		t.Fatal("direct readback did not return the replacement")
	}
	for _, id := range []string{first.ID, second.ID} {
		raw, _ = c.call("GET", listPath+id, "", "", 200)
		readback := turnOnePage[relationship](t, raw, "relationships").Values
		if len(readback) != 3 || readback[0] != patched {
			t.Fatal("collection readback did not include the replacement at both endpoints")
		}
	}

	c.call("DELETE", path, "", patched.entityTag(), 428)
	req, err := http.NewRequest(http.MethodDelete, server.URL+Prefix+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+human)
	req.Header.Set("If-Match", patched.entityTag())
	req.Header.Set("X-Expected-Board-ETag", boardTag)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(res.Body)
	closeErr := res.Body.Close()
	if readErr != nil || closeErr != nil || res.StatusCode != 204 || len(data) != 0 ||
		res.Header.Get("X-Board-ETag") == "" || res.Header.Get("X-Board-ETag") == boardTag {
		t.Fatalf("unexpected removal result: %d %s", res.StatusCode, data)
	}
	c.call("GET", path, "", "", 404)
	reuse := fmt.Sprintf(`{"id":%q,`+strings.TrimPrefix(
		createBody(first.ID, second.ID, "references", res.Header.Get("X-Board-ETag")), "{"), patched.ID)
	c.call("POST", "/relationships", reuse, "", 409)

	saved, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Board.Revision != seeded.Board.Revision+3 || len(saved.Relationships) != 2 || len(saved.Activity) != 8 {
		t.Fatal("durable relationship lifecycle advanced unexpected revisions or audit")
	}
	replaced, removed := saved.Activity[6], saved.Activity[7]
	if replaced.ActorID != botID || replaced.Operation != "patch" || replaced.ResourceID != patched.ID ||
		removed.ActorID != humanID || removed.Operation != "remove" || removed.ResourceID != patched.ID ||
		removed.After != nil || !equalSnapshotStrings(removed.Before, replaced.After) ||
		saved.Cards[0].Revision != saved.Board.Revision ||
		saved.Cards[1].Revision != saved.Board.Revision {
		t.Fatal("removal and replacement audit or endpoint versions were inconsistent")
	}
	for _, edit := range []func(*snapshot){
		func(value *snapshot) {
			value.Cards[0].Revision = saved.Board.Revision - 1
		},
		func(value *snapshot) {
			value.Activity[7].At = saved.Board.UpdatedAt.Add(time.Second)
			value.Board.UpdatedAt = value.Activity[7].At
			value.Board.Attention, value.Board.LastCardEditAt = deriveAttention(*value)
		},
		func(value *snapshot) { value.Relationships[0].TargetCardID = newID() },
		func(value *snapshot) {
			value.Relationships[0].Revision = saved.Board.Revision
		},
		func(value *snapshot) { value.Activity[7].Before = value.Activity[6].Before },
		func(value *snapshot) { value.Activity[2], value.Activity[3] = value.Activity[3], value.Activity[2] },
	} {
		tampered := cloneSnapshot(saved)
		edit(&tampered)
		if err := validateSnapshot(tampered, b.ID); !errors.Is(err, errInvalidSnapshot) {
			t.Fatalf("tampered relationship or audit accepted: %v", err)
		}
	}
}
