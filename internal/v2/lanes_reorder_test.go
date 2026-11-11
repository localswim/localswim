package v2

import (
	"fmt"
	"testing"
)

func TestM63LaneDependencyReorder(t *testing.T) {
	app, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Lane dependency reorder"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	path := "/lanes/" + b.LaneIDs[0]
	raw, header := client.call("GET", path, "", "", 200)
	item := turnOneDecode[lane](t, raw)
	initialLaneTag := header.Get("ETag")
	if item.ReorderEnabled {
		t.Fatal("initial lane opted in")
	}
	config := func(boardTag, value string) string {
		return fmt.Sprintf(`{"board_etag":%q,"reorder_enabled":%s}`, boardTag, value)
	}
	for _, invalid := range []string{"null", "1", `"true"`} {
		client.call("PATCH", path, config(b.entityTag(), invalid), initialLaneTag, 400)
	}
	raw, header = client.call("PATCH", path, config(b.entityTag(), "true"), initialLaneTag, 200)
	item = turnOneDecode[lane](t, raw)
	boardTag, laneTag := header.Get("X-Board-ETag"), header.Get("ETag")
	if !item.ReorderEnabled || item.Revision != b.Revision+1 || boardTag == b.entityTag() || laneTag == initialLaneTag {
		t.Fatal("lane opt-in did not advance the lane and board versions")
	}
	client.call("PATCH", path, config(b.entityTag(), "false"), laneTag, 412)
	client.call("PATCH", path, config(boardTag, "false"), initialLaneTag, 412)
	_, header = client.call("PATCH", path, config(boardTag, "true"), laneTag, 200)
	if header.Get("ETag") != laneTag || header.Get("X-Board-ETag") != boardTag {
		t.Fatal("repeating the setting advanced the version")
	}
	state, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	event := state.Activity[len(state.Activity)-1]
	if !state.Lanes[laneIndex(state.Lanes, item.ID)].ReorderEnabled ||
		state.Lanes[laneIndex(state.Lanes, b.LaneIDs[1])].ReorderEnabled ||
		event.ResourceType != "lane" || event.ResourceID != item.ID || event.Revision != item.Revision ||
		snapshotString(event.Field) != "reorder_enabled" || snapshotString(event.Before) != "false" ||
		snapshotString(event.After) != "true" {
		t.Fatal("durable lane or audit does not match the opt-in")
	}
	tampered := cloneSnapshot(state)
	tampered.Lanes[laneIndex(tampered.Lanes, item.ID)].ReorderEnabled = false
	if err := validateSnapshot(tampered, b.ID); err == nil {
		t.Fatal("accepted a snapshot setting inconsistent with the reorder audit")
	}

	raw, header = client.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"First"}`,
		b.ID, boardTag, item.ID), "", 201)
	first := turnOneDecode[card](t, raw)
	raw, _ = client.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Blocker","relationships":[{"target_card_id":%q,"kind":"blocks"}]}`,
		b.ID, header.Get("X-Board-ETag"), item.ID, first.ID), "", 201)
	blocker := turnOneDecode[card](t, raw)
	orderPath := "/ticket-orders/" + item.ID
	raw, header = client.call("GET", orderPath+"?preview=true", "", "", 200)
	preview := turnOneDecode[ticketOrder](t, raw)
	if !preview.Changed || len(preview.Proposed) != 2 || preview.Proposed[0] != (ticketOrderEntry{blocker.ID, first.Ticket}) {
		t.Fatal("enabled Backlog did not preview the dependency permutation")
	}
	ids := []string{preview.Proposed[0].CardID, preview.Proposed[1].CardID}
	apply := ticketOrderBody(t, b.ID, header.Get("X-Board-ETag"), ids)
	raw, header = client.call("PUT", orderPath, apply, "", 200)
	applied := turnOneDecode[ticketOrder](t, raw)
	if !applied.Changed || applied.BoardRevision != preview.BoardRevision+1 {
		t.Fatal("ticket order did not commit")
	}
	client.call("PUT", orderPath, apply, "", 412)
	boardTag = header.Get("X-Board-ETag")
	raw, header = client.call("PATCH", path, config(boardTag, "false"), laneTag, 200)
	item = turnOneDecode[lane](t, raw)
	if item.ReorderEnabled || header.Get("X-Board-ETag") == boardTag || header.Get("ETag") == laneTag {
		t.Fatal("lane opt-out did not advance the lane and board versions")
	}
}
