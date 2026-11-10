package v2

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestM02CardCreationAndReadback(t *testing.T) {
	root := t.TempDir()
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	defer server.Close()
	defer func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}()

	c := turnOneClient{t, server.URL, human, server.Client()}
	raw, _ := c.call("POST", "/boards", `{"name":"Cards"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	id := newID()
	title := "  🐝 e\u0301  "
	description := " first\r\nsecond\n "
	request := map[string]string{
		"id": strings.ToUpper(id), "board_id": strings.ToUpper(b.ID), "lane_id": strings.ToUpper(b.LaneIDs[0]),
		"expected_board_etag": b.ETag, "title": title, "description": description,
	}
	post := func(want int) ([]byte, string) {
		t.Helper()
		body, err := marshalCanonical(request)
		if err != nil {
			t.Fatal(err)
		}
		data, headers := c.call("POST", "/cards", string(body), "", want)
		if want == 201 {
			value := turnOneDecode[card](t, data)
			if headers.Get("Location") != Prefix+"/cards/"+value.ID || headers.Get("ETag") != value.ETag {
				t.Fatal("create did not expose the new card identity and tag")
			}
		}
		return data, headers.Get("X-Board-ETag")
	}
	delete(request, "expected_board_etag")
	post(428)
	request["expected_board_etag"] = b.ETag
	raw, boardTag := post(201)
	created := turnOneDecode[card](t, raw)
	if created.ID != id || created.BoardID != b.ID || created.Ticket != 1 ||
		created.Title != title || created.Description != description || created.PriorityID != b.PriorityIDs[1] ||
		created.LaneID != b.LaneIDs[0] || created.ChildIDs == nil || boardTag == "" || boardTag == b.ETag {
		t.Fatalf("unexpected created card: %+v", created)
	}
	post(409)
	request["id"] = newID()
	post(412)
	request["expected_board_etag"] = boardTag
	request["id"] = b.LaneIDs[0]
	post(409)
	request["id"] = newID()
	request["lane_id"] = b.LaneIDs[2]
	post(403)
	request["lane_id"] = b.LaneIDs[0]
	request["parent_id"] = id
	raw, _ = post(201)
	child := turnOneDecode[card](t, raw)
	if child.Ticket != 2 || child.ParentID == nil || *child.ParentID != id {
		t.Fatalf("unexpected child card: %+v", child)
	}
	raw, headers := c.call("GET", "/cards/"+strings.ToUpper(id), "", "", 200)
	parent := turnOneDecode[card](t, raw)
	if headers.Get("ETag") == created.ETag || len(parent.ChildIDs) != 1 || parent.ChildIDs[0] != child.ID {
		t.Fatal("parent links or tag did not update with child creation")
	}
	query := "/cards?board_id=" + b.ID
	raw, _ = c.call("GET", query+"&ticket=2", "", "", 200)
	if found := turnOnePage[card](t, raw, "cards"); len(found.Values) != 1 || found.Values[0].ID != child.ID {
		t.Fatal("ticket lookup did not return the created card")
	}
	raw, _ = c.call("GET", query+"&limit=1", "", "", 200)
	page := turnOnePage[card](t, raw, "cards")
	if len(page.Values) != 1 || page.Values[0].ID != id || page.NextCursor == nil {
		t.Fatal("first page did not return ticket 1 and a continuation")
	}
	raw, _ = c.call("GET", query+"&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", "", 200)
	if page := turnOnePage[card](t, raw, "cards"); len(page.Values) != 1 || page.Values[0].ID != child.ID || page.NextCursor != nil {
		t.Fatal("continuation did not return the terminal page with ticket 2")
	}

	saved, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Board.Revision != b.Revision+2 || len(saved.Activity) != 4 ||
		saved.Activity[1].ResourceID != id || saved.Activity[1].ActorID != humanID ||
		saved.Activity[2].ResourceID != child.ID || saved.Activity[2].Revision != saved.Board.Revision ||
		saved.Activity[3].Revision != saved.Board.Revision {
		t.Fatal("successful creation and parent assignment were not saved with exactly two board revisions")
	}
}

func TestM08ParentPatchUsesCardTagAndRelationshipGrant(t *testing.T) {
	app, editor := eventHTTPServer(t)
	id, now := newID(), canonicalTime(time.Now())
	wf := defaultWorkflow(id, now, app.principals)
	for i := range wf.Policy.ResourceGrants {
		grant := &wf.Policy.ResourceGrants[i]
		if grant.UserID == botID {
			grant.Capabilities = slices.DeleteFunc(grant.Capabilities, func(value string) bool { return value == "read" })
		}
	}
	value := freshSnapshot(id, "Parent authorization", wf, humanID, now)
	if err := app.store.create(value); err != nil {
		t.Fatal(err)
	}
	makeCard := func(tag string) (card, string) {
		body := fmt.Sprintf("{\"board_id\":%q,\"expected_board_etag\":%q,\"lane_id\":%q,\"title\":\"Synthetic\"}", id, tag, wf.Lanes[0].ID)
		raw, headers := editor.call("POST", "/cards", body, "", 201)
		return turnOneDecode[card](t, raw), headers.Get("X-Board-ETag")
	}
	parent, tag := makeCard(value.Board.ETag)
	child, _ := makeCard(tag)
	bot := editor
	bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	path := "/cards/" + child.ID
	bot.call("GET", path, "", "", 404)
	patch := fmt.Sprintf("{\"parent_id\":%q}", parent.ID)
	raw, _ := bot.call("PATCH", path, patch, child.ETag, 200)
	changed := turnOneDecode[card](t, raw)
	if changed.ParentID == nil || *changed.ParentID != parent.ID {
		t.Fatal("relationship grant did not permit parent edit")
	}
	bot.call("PATCH", path, "{\"parent_id\":null}", child.ETag, 412)
	raw, _ = editor.call("GET", path, "", "", 200)
	if found := turnOneDecode[card](t, raw); found.ETag != changed.ETag || found.ParentID == nil || *found.ParentID != parent.ID {
		t.Fatal("stale parent patch changed the card")
	}
}

func TestM02CardTextRejectsUnpairedSurrogates(t *testing.T) {
	for _, input := range []string{`"\uD800"`, `"\uDC00"`, `"\uD800text"`, `"\uD800\\uDC00"`} {
		if _, err := exactCardText([]byte(input), "title", 1, 1024); err == nil {
			t.Fatalf("accepted malformed Unicode %s", input)
		}
	}
	if got, err := exactCardText([]byte(`"\uD83D\uDC1D \\uD800 �"`), "title", 1, 1024); err != nil || got != `🐝 \uD800 �` {
		t.Fatalf("valid Unicode was not preserved: %q (%v)", got, err)
	}
}
