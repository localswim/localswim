package v2

import (
	"fmt"
	"strings"
	"testing"
)

func TestRequiredOwnerDefaultsAndBoardChanges(t *testing.T) {
	app, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Ownership"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	if b.DefaultOwnerID != botID {
		t.Fatal("default workflow must default ownership to Bot")
	}
	state, _ := app.store.accepted(b.ID)
	path := "/boards/" + b.ID
	for _, owner := range []string{`null`, `""`, fmt.Sprintf("%q", newID())} {
		client.call("PATCH", path, `{"name":"Should not persist","default_owner_id":`+owner+`}`, b.entityTag(), 400)
	}
	raw, _ = client.call("GET", path, "", "", 200)
	if got := turnOneDecode[board](t, raw); got.entityTag() != b.entityTag() || got.Name != b.Name {
		t.Fatal("invalid default owner partially changed board")
	}
	raw, _ = client.call("PATCH", path, fmt.Sprintf(`{"default_owner_id":%q}`, humanID), b.entityTag(), 200)
	b = turnOneDecode[board](t, raw)
	raw, _ = client.call("PATCH", path, fmt.Sprintf(`{"default_owner_id":%q}`, humanID), b.entityTag(), 200)
	if turnOneDecode[board](t, raw).entityTag() != b.entityTag() {
		t.Fatal("same default owner changed board version")
	}
	body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"title":"Owned","lane_id":%q}`, b.ID, b.entityTag(), state.Lanes[0].ID)
	client.call("POST", "/cards", strings.TrimSuffix(body, "}")+`,"owner_id":null}`, "", 400)
	raw, _ = client.call("POST", "/cards", body, "", 201)
	c := turnOneDecode[card](t, raw)
	if c.OwnerID != humanID {
		t.Fatal("omitted owner did not use board default")
	}
	b.Revision = c.Revision
	raw, _ = client.call("PATCH", path, fmt.Sprintf(`{"default_owner_id":%q}`, botID), b.entityTag(), 200)
	if got, _ := app.store.accepted(b.ID); got.Cards[0].OwnerID != humanID {
		t.Fatal("default change reassigned an existing card")
	}
	durable, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Board.DefaultOwnerID != botID || durable.Cards[0].OwnerID != humanID {
		t.Fatal("durable owners did not survive admission")
	}
	event := durable.Activity[len(durable.Activity)-1]
	if snapshotString(event.Field) != "default_owner_id" || snapshotString(event.Before) != humanID || snapshotString(event.After) != botID {
		t.Fatal("default owner change lacks exact audit")
	}
	event.Before = nil
	durable.Activity[len(durable.Activity)-1] = event
	if err := validateSnapshot(durable, b.ID); err == nil {
		t.Fatal("accepted invalid default owner audit")
	}
}

func TestCustomWorkflowRequiresMemberDefaultOwner(t *testing.T) {
	_, client := eventHTTPServer(t)
	body := `{"name":"Custom owner","workflow":` + workflowTestInput
	client.call("POST", "/boards", body+`}`, "", 400)
	member := "a0000000-0000-4000-8000-000000000002"
	raw, _ := client.call("POST", "/boards", body+fmt.Sprintf(`,"default_owner_id":%q}`, member), "", 201)
	if turnOneDecode[board](t, raw).DefaultOwnerID != member {
		t.Fatal("custom owner was not persisted")
	}
}

func TestNativeSnapshotRequiresOwners(t *testing.T) {
	for _, invalidOwner := range []string{"", newID()} {
		value := nativeVerificationFixture()
		value.Board.DefaultOwnerID = invalidOwner
		if err := validateSnapshot(value, value.Board.ID); err == nil {
			t.Fatal("accepted missing or foreign default owner")
		}
	}
	app, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Native ownership"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	value, _ := app.store.accepted(b.ID)
	client.call("POST", "/cards", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"title":"Owned","lane_id":%q}`, b.ID, b.entityTag(), value.Lanes[0].ID), "", 201)
	value, _ = app.store.accepted(b.ID)
	if err := validateSnapshot(value, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, invalidOwner := range []string{"", newID()} {
		value.Cards[0].OwnerID = invalidOwner
		if err := validateSnapshot(value, value.Board.ID); err == nil {
			t.Fatal("accepted missing or foreign card owner")
		}
	}
}
