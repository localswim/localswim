package v2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLaneKeyPatchHTTP(t *testing.T) {
	app, client := eventHTTPServer(t)
	body, _ := client.call("POST", "/boards", `{"name":"Lane key fixture"}`, "", 201)
	created := turnOneDecode[board](t, body)
	initial, _ := app.store.accepted(created.ID)
	target := initial.Lanes[0]
	lanePath := "/lanes/" + target.ID
	body, _ = client.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Keeps its lane"}`,
		created.ID, created.ETag, target.ID), "", 201)
	cardBefore := turnOneDecode[card](t, body)
	before, _ := app.store.accepted(created.ID)
	path := app.store.snapshotPath(created.ID)
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patch := func(key any, boardTag, laneTag string, want int) ([]byte, string) {
		t.Helper()
		input, err := json.Marshal(map[string]any{"board_etag": boardTag, "key": key})
		if err != nil {
			t.Fatal(err)
		}
		body, headers := client.call("PATCH", lanePath, string(input), laneTag, want)
		return body, headers.Get("X-Board-ETag")
	}
	patch("changed", before.Board.ETag, "", 428)
	patch("changed", initial.Board.ETag, target.entityTag(), 412)
	patch("changed", before.Board.ETag, etag(target.ID, 99), 412)
	for _, key := range []any{nil, "", 5, true, strings.Repeat("界", 129)} {
		patch(key, before.Board.ETag, target.entityTag(), 400)
	}
	patch(initial.Lanes[1].Key, before.Board.ETag, target.entityTag(), 409)
	patch("completed", before.Board.ETag, target.entityTag(), 400)
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(committed, unchanged) {
		t.Fatalf("refused key patch changed durable state: %v", err)
	}

	key := "  Révision 界  "
	body, boardTag := patch(key, before.Board.ETag, target.entityTag(), 200)
	renamed := turnOneDecode[lane](t, body)
	expected := target
	expected.Key, expected.Revision, expected.UpdatedAt = key, before.Board.Revision+1, renamed.UpdatedAt
	if renamed != expected || boardTag != etag(created.ID, expected.Revision) {
		t.Fatal("key patch did not preserve exact text, identity, metadata, and versions")
	}
	actual, err := readSnapshot(path, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual.Cards, before.Cards) ||
		!reflect.DeepEqual(actual.Policy, before.Policy) ||
		!reflect.DeepEqual(actual.Board.LaneIDs, before.Board.LaneIDs) ||
		!reflect.DeepEqual(actual.Relationships, before.Relationships) {
		t.Fatal("key patch changed references, cards, or grants")
	}
	for _, item := range actual.Lanes {
		if item.ID != target.ID && item != before.Lanes[laneIndex(before.Lanes, item.ID)] {
			t.Fatal("key patch changed another lane")
		}
	}
	if len(actual.Activity) != len(before.Activity)+1 ||
		!reflect.DeepEqual(actual.Activity[:len(before.Activity)], before.Activity) {
		t.Fatal("key patch did not preserve history and append exactly one event")
	}
	event := actual.Activity[len(actual.Activity)-1]
	if event.ActorID != humanID || event.ResourceType != "lane" || event.ResourceID != target.ID ||
		event.Operation != "patch" || snapshotString(event.Field) != "key" ||
		snapshotString(event.Before) != target.Key || snapshotString(event.After) != key ||
		event.Revision != renamed.Revision || !event.At.Equal(renamed.UpdatedAt) {
		t.Fatal("key patch audit does not describe the committed change")
	}
	body, headers := client.call("GET", lanePath, "", "", 200)
	if turnOneDecode[lane](t, body) != renamed || headers.Get("ETag") != renamed.entityTag() {
		t.Fatal("focused lane read did not return its new key and validator")
	}
	body, _ = client.call("GET", "/cards/"+cardBefore.ID, "", "", 200)
	if !reflect.DeepEqual(turnOneDecode[card](t, body), cardBefore) {
		t.Fatal("card reference changed after lane key patch")
	}
	committed, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patch(key, actual.Board.ETag, renamed.entityTag(), 200)
	unchanged, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(committed, unchanged) {
		t.Fatalf("same-key patch changed durable state: %v", err)
	}
	for name, mutate := range map[string]func(*snapshot){
		"mismatched current key": func(value *snapshot) { value.Lanes[0].Key = "not-audited" },
		"historical collision": func(value *snapshot) {
			value.Activity[len(value.Activity)-1].Before = &value.Lanes[1].Key
		},
		"missing key value": func(value *snapshot) { value.Activity[len(value.Activity)-1].After = nil },
		"reserved historical key": func(value *snapshot) {
			value.Activity[len(value.Activity)-1].Before = new("completed")
		},
	} {
		t.Run(name, func(t *testing.T) {
			tampered := cloneSnapshot(actual)
			mutate(&tampered)
			if err := validateSnapshot(tampered, created.ID); err == nil {
				t.Fatal("accepted invalid lane key audit")
			}
		})
	}
}

func TestLaneKeyProtectedAndRetiredHTTP(t *testing.T) {
	app, client := eventHTTPServer(t)
	body, _ := client.call("POST", "/boards", `{"name":"Protected keys"}`, "", 201)
	created := turnOneDecode[board](t, body)
	initial, _ := app.store.accepted(created.ID)
	completed := initial.Lanes[slices.IndexFunc(initial.Lanes, func(item lane) bool { return item.Role == "completed" })]
	client.call("PATCH", "/lanes/"+completed.ID, fmt.Sprintf(
		`{"board_etag":%q,"key":"renamed_completed","label":"Done"}`, created.ETag), completed.entityTag(), 400)
	client.call("PATCH", "/lanes/"+completed.ID, fmt.Sprintf(
		`{"board_etag":%q,"key":"completed"}`, created.ETag), completed.entityTag(), 200)
	body, headers := client.call("POST", "/lanes", fmt.Sprintf(
		`{"board_id":%q,"board_etag":%q,"label":"Temporary","create_for":[%q],"connect_for":[%q]}`,
		created.ID, created.ETag, humanID, humanID), "", 201)
	temporary := turnOneDecode[lane](t, body)
	body, headers = client.call("PATCH", "/lanes/"+temporary.ID, fmt.Sprintf(
		`{"board_etag":%q,"key":"retired key","label":"Former temporary","retired":true}`,
		headers.Get("X-Board-ETag")), temporary.entityTag(), 200)
	retired := turnOneDecode[lane](t, body)
	if !retired.Retired || retired.Key != "retired key" || retired.Label != "Former temporary" {
		t.Fatal("combined key, label, and retirement patch was not atomic")
	}
	current, _ := app.store.accepted(created.ID)
	ordinary := current.Lanes[0]
	client.call("PATCH", "/lanes/"+ordinary.ID, fmt.Sprintf(
		`{"board_etag":%q,"key":"retired key"}`, current.Board.ETag), ordinary.entityTag(), 409)
	// Retired lanes retain their board-local key and can still be identified by a new key.
	body, _ = client.call("PATCH", "/lanes/"+retired.ID, fmt.Sprintf(
		`{"board_etag":%q,"key":"historical key"}`, headers.Get("X-Board-ETag")), retired.entityTag(), 200)
	if got := turnOneDecode[lane](t, body); !got.Retired || got.ID != retired.ID || got.Key != "historical key" {
		t.Fatal("retired lane key edit changed its identity or retirement")
	}
	if _, err := readSnapshot(app.store.snapshotPath(created.ID), created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestLaneKeyHistoryAllowsReuseAfterRelease(t *testing.T) {
	original, replacement := "old", "new"
	value := snapshot{
		Lanes: []lane{{ID: "first", Key: replacement}, {ID: "second", Key: original}},
		Activity: []activity{
			{ResourceType: "lane", ResourceID: "first", Operation: "patch", Field: new("key"), Before: &original, After: &replacement},
			{ResourceType: "lane", ResourceID: "second", Operation: "create"},
		},
	}
	if !validLaneKeyHistory(value, map[string]bool{"first": true}) {
		t.Fatal("rejected reuse of a key after its previous lane released it")
	}
	value.Activity[0], value.Activity[1] = value.Activity[1], value.Activity[0]
	if validLaneKeyHistory(value, map[string]bool{"first": true}) {
		t.Fatal("accepted key reuse before its previous lane released it")
	}
}
