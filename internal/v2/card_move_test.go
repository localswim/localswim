package v2

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestCardMoveAndOwnerHTTP(t *testing.T) {
	humanToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	botToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), humanToken, botToken)
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
	bot := turnOneClient{t, server.URL, botToken, server.Client()}
	human := turnOneClient{t, server.URL, humanToken, server.Client()}

	raw, _ := bot.call("POST", "/boards", `{"name":"Move and owner fixture"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	state, ok := app.store.accepted(b.ID)
	if !ok {
		t.Fatal("created board was not accepted")
	}
	from, to, completed := state.Lanes[0].ID, state.Lanes[3].ID, state.Lanes[4].ID
	raw, header := bot.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"title":"Test card","lane_id":%q}`, b.ID, b.entityTag(), from), "", 201)
	initial := turnOneDecode[card](t, raw)
	initialBoardTag := header.Get("X-Board-ETag")
	path := "/cards/" + initial.ID
	move := fmt.Sprintf(`{"lane_id":%q}`, to)
	bot.call("PATCH", path, move, initial.entityTag(), 403)
	human.call("PATCH", path, move, initial.entityTag(), 403)
	if got, _ := app.store.accepted(b.ID); got.Board.entityTag() != initialBoardTag {
		t.Fatal("denied lane moves changed the board")
	}

	policyPath := "/policies/" + b.PolicyID
	raw, _ = bot.call("GET", policyPath, "", "", 200)
	p := turnOneDecode[policy](t, raw)
	p.MoveGrants = append(slices.Clone(p.MoveGrants), moveGrant{UserID: botID, FromLaneID: from, ToLaneID: to})
	bot.call("PUT", policyPath, policyPutBody(t, p, initialBoardTag), p.entityTag(), 200)
	human.call("PATCH", path, move, initial.entityTag(), 403)

	// Adding a grant changes the board version, but not the original card tag.
	raw, header = bot.call("PATCH", path, move, initial.entityTag(), 200)
	moved := turnOneDecode[card](t, raw)
	movedBoardTag := header.Get("X-Board-ETag")
	if moved.LaneID != to || moved.OwnerID != botID || moved.Revision != 4 || moved.entityTag() == initial.entityTag() ||
		header.Get("ETag") != moved.entityTag() || movedBoardTag == initialBoardTag {
		t.Fatal("move did not return the committed lane, owner, and version")
	}
	bot.call("PATCH", path, fmt.Sprintf(`{"lane_id":%q,"owner_id":%q}`, completed, botID), moved.entityTag(), 403)
	bot.call("PATCH", path, move, moved.entityTag(), 403)
	raw, _ = bot.call("GET", path, "", "", 200)
	boardRaw, _ := bot.call("GET", "/boards/"+b.ID, "", "", 200)
	if turnOneDecode[card](t, raw).entityTag() != moved.entityTag() || turnOneDecode[board](t, boardRaw).entityTag() != movedBoardTag {
		t.Fatal("refused same-lane patch changed versions")
	}
	assign := fmt.Sprintf(`{"owner_id":%q}`, humanID)
	human.call("PATCH", path, assign, initial.entityTag(), 412)
	raw, header = human.call("PATCH", path, assign, moved.entityTag(), 200)
	assigned := turnOneDecode[card](t, raw)
	if assigned.LaneID != to || assigned.OwnerID != humanID || assigned.Revision != 5 || assigned.entityTag() == moved.entityTag() {
		t.Fatal("owner edit did not independently persist")
	}
	bot.call("PATCH", path, `{"owner_id":null}`, assigned.entityTag(), 400)
	raw, _ = bot.call("PATCH", path, assign, assigned.entityTag(), 200)
	if unchanged := turnOneDecode[card](t, raw); unchanged.entityTag() != assigned.entityTag() || unchanged.OwnerID != humanID {
		t.Fatal("same owner assignment must preserve the version and owner")
	}

	durable, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable.Activity) != 5 || durable.Cards[0].entityTag() != assigned.entityTag() {
		t.Fatal("expected exactly one durable event for each successful change")
	}
	for i, want := range []struct{ actor, field, before, after string }{
		{botID, "lane_id", from, to},
		{humanID, "owner_id", botID, humanID},
	} {
		event := durable.Activity[i+3]
		if event.ActorID != want.actor || event.Operation != "patch" || event.ResourceType != "card" ||
			event.ResourceID != initial.ID || snapshotString(event.Field) != want.field ||
			snapshotString(event.Before) != want.before || snapshotString(event.After) != want.after {
			t.Fatalf("incorrect durable attribution or values for %s edit %d", want.field, i)
		}
	}
}
