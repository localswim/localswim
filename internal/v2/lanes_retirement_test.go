package v2

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
)

func TestLaneRetirementHTTP(t *testing.T) {
	app, human := eventHTTPServer(t)
	body, _ := human.call("POST", "/boards", `{"name":"Retirement fixture"}`, "", 201)
	created := turnOneDecode[board](t, body)
	initial, _ := app.store.accepted(created.ID)
	completed := initial.Lanes[slices.IndexFunc(initial.Lanes, func(l lane) bool { return l.Role == "completed" })]
	body, headers := human.call("POST", "/lanes", fmt.Sprintf(
		`{"board_id":%q,"board_etag":%q,"label":"Temporary review","role":"ready_for_review","create_for":[%q],"connect_for":[%q]}`,
		created.ID, created.ETag, humanID, humanID), "", 201)
	item := turnOneDecode[lane](t, body)
	lanePath := "/lanes/" + item.ID
	body, headers = human.call("PATCH", lanePath, fmt.Sprintf(
		`{"board_etag":%q,"approval_enabled":true}`, headers.Get("X-Board-ETag")), item.entityTag(), 200)
	item = turnOneDecode[lane](t, body)

	createCard := func(laneID, tag string, want int) ([]byte, string) {
		t.Helper()
		body, headers := human.call("POST", "/cards", fmt.Sprintf(
			`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Fixture"}`,
			created.ID, tag, laneID), "", want)
		return body, headers.Get("X-Board-ETag")
	}
	body, tag := createCard(initial.Lanes[0].ID, headers.Get("X-Board-ETag"), 201)
	survivor := turnOneDecode[card](t, body)
	body, tag = createCard(item.ID, tag, 201)
	cardInLane := turnOneDecode[card](t, body)
	cardPath := "/cards/" + cardInLane.ID

	retire := func(path, boardTag, laneTag string, value bool, want int) {
		t.Helper()
		human.call("PATCH", path, fmt.Sprintf(`{"board_etag":%q,"retired":%t}`, boardTag, value), laneTag, want)
	}
	retire(lanePath, tag, item.entityTag(), true, 409)
	retire("/lanes/"+completed.ID, tag, completed.entityTag(), true, 400)
	retire(lanePath, created.ETag, item.entityTag(), true, 412)
	retire(lanePath, tag, etag(item.ID, 99), true, 412)

	body, headers = human.call("PATCH", cardPath, `{"archived":true}`, cardInLane.entityTag(), 200)
	archived := turnOneDecode[card](t, body)
	before, _ := app.store.accepted(created.ID)
	human.call("PATCH", lanePath, fmt.Sprintf(`{"board_etag":%q,"retired":true,"approval_enabled":true}`,
		before.Board.ETag), item.entityTag(), 400)
	body, headers = human.call("PATCH", lanePath, fmt.Sprintf(
		`{"board_etag":%q,"retired":true}`, headers.Get("X-Board-ETag")), item.entityTag(), 200)
	retired := turnOneDecode[lane](t, body)
	tag = headers.Get("X-Board-ETag")
	expected := item
	expected.Retired, expected.UpdatedAt, expected.Revision = true, retired.UpdatedAt, before.Board.Revision+1
	if retired != expected || headers.Get("ETag") != retired.entityTag() || tag != etag(created.ID, expected.Revision) {
		t.Fatal("retirement changed lane identity or settings, or returned incorrect versions")
	}
	body, _ = human.call("GET", lanePath, "", "", 200)
	if got := turnOneDecode[lane](t, body); got != retired {
		t.Fatal("focused read lost the retired lane")
	}
	body, _ = human.call("GET", cardPath, "", "", 200)
	if got := turnOneDecode[card](t, body); !reflect.DeepEqual(got, archived) {
		t.Fatal("retirement changed the archived card")
	}

	path := app.store.snapshotPath(created.ID)
	actual, err := readSnapshot(path, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(actual.Board.LaneIDs, before.Board.LaneIDs) || actual.Policy.ETag != before.Policy.ETag ||
		policyAuditValue(actual.Policy) != policyAuditValue(before.Policy) {
		t.Fatal("retirement changed lane references or historical grants")
	}
	if len(actual.Activity) != len(before.Activity)+1 || !reflect.DeepEqual(actual.Activity[:len(before.Activity)], before.Activity) {
		t.Fatal("retirement did not append exactly one event while preserving history")
	}
	event := actual.Activity[len(actual.Activity)-1]
	if event.ActorID != humanID || event.ResourceType != "lane" || event.ResourceID != item.ID ||
		event.Revision != retired.Revision || event.Operation != "patch" || snapshotString(event.Field) != "retired" ||
		snapshotString(event.Before) != "false" || snapshotString(event.After) != "true" {
		t.Fatal("incorrect retirement audit")
	}
	tampered := cloneSnapshot(actual)
	tampered.Activity[len(tampered.Activity)-1].Before = archiveAuditValue(true)
	if err := validateSnapshot(tampered, created.ID); err == nil {
		t.Fatal("accepted discontinuous retirement audit")
	}
	tampered = cloneSnapshot(actual)
	field, oldName, newName := "name", actual.Board.Name, "Injected retirement edit"
	tampered.Board.Name = newName
	tampered.Activity = slices.Insert(tampered.Activity, len(tampered.Activity)-1, activity{
		ID: newOperationID(), BoardID: created.ID, Revision: event.Revision, At: event.At,
		ActorID: event.ActorID, Operation: "patch", ResourceType: "board", ResourceID: created.ID,
		Field: &field, Before: &oldName, After: &newName,
	})
	if err := validateSnapshot(tampered, created.ID); err == nil {
		t.Fatal("accepted a retirement spliced into an unrelated audit edit")
	}

	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	createCard(item.ID, tag, 400)
	human.call("PATCH", "/cards/"+survivor.ID, fmt.Sprintf(`{"lane_id":%q}`, item.ID), survivor.entityTag(), 400)
	human.call("PATCH", cardPath, `{"archived":false}`, archived.entityTag(), 400)
	retire(lanePath, tag, retired.entityTag(), false, 400)
	retire(lanePath, tag, retired.entityTag(), true, 200)
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(unchanged, committed) {
		t.Fatalf("refusal or no-op changed the durable board: %v", err)
	}
}
