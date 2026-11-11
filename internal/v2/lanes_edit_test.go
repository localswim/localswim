package v2

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLaneEditsHTTP(t *testing.T) {
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
	client := turnOneClient{t, server.URL, human, server.Client()}
	body, _ := client.call("POST", "/boards", `{"name":"Lane edits"}`, "", 201)
	created := turnOneDecode[board](t, body)
	before, _ := app.store.accepted(created.ID)
	path := app.store.snapshotPath(created.ID)
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	anchor := before.Lanes[1]
	id := newID()
	laneInput := map[string]any{
		"id": strings.ToUpper(id), "board_id": created.ID, "board_etag": created.entityTag(), "label": "Check QA",
		"before_id": anchor.ID, "create_for": []string{botID}, "connect_for": []string{humanID},
	}
	asJSON := func(value map[string]any) string {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	body, headers := client.call("POST", "/lanes", asJSON(laneInput), "", 201)
	after, _ := app.store.accepted(created.ID)
	if after.Board.Revision != 2 {
		t.Fatalf("insert revision = %d", after.Board.Revision)
	}
	inserted := after.Lanes[1]
	if turnOneDecode[lane](t, body) != inserted || headers.Get("ETag") != inserted.entityTag() ||
		headers.Get("X-Board-ETag") != after.Board.entityTag() || headers.Get("Location") != Prefix+"/lanes/"+id {
		t.Fatal("insert did not return the committed lane, versions, and location")
	}
	if inserted.ID != id || inserted.Key != "check_qa" || inserted.Position != 1 || inserted.Revision != 2 ||
		after.Board.LaneIDs[1] != inserted.ID || after.Lanes[2].ID != anchor.ID || after.Lanes[2].Position != 2 ||
		after.Lanes[2].Revision != 2 || after.Lanes[0].Revision != 1 || after.Lanes[2].entityTag() == anchor.entityTag() || after.Lanes[0] != before.Lanes[0] {
		t.Fatal("insert did not preserve the anchored order and stable identities")
	}
	if !laneGrantAddition(before.Policy, after.Policy, id) ||
		len(after.Policy.CreateGrants) != len(before.Policy.CreateGrants)+1 ||
		!slices.Contains(after.Policy.CreateGrants, createGrant{UserID: botID, LaneID: id}) ||
		len(after.Policy.MoveGrants) != len(before.Policy.MoveGrants)+2*len(before.Lanes) ||
		after.Policy.entityTag() == before.Policy.entityTag() {
		t.Fatal("insert did not apply the requested grants")
	}
	committedBytes, err := os.ReadFile(path)
	if err != nil || bytes.Equal(beforeBytes, committedBytes) {
		t.Fatalf("insert was not durable: %v", err)
	}

	lanePath := "/lanes/" + inserted.ID
	body, headers = client.call("GET", lanePath, "", "", 200)
	if headers.Get("ETag") != inserted.entityTag() || turnOneDecode[lane](t, body) != inserted {
		t.Fatal("lane GET did not return the committed lane and ETag")
	}
	client.call("POST", "/lanes", asJSON(laneInput), "", 412)
	laneInput["board_etag"] = after.Board.entityTag()
	client.call("POST", "/lanes", asJSON(laneInput), "", 409)
	laneInput["id"], laneInput["label"], laneInput["connect_for"] = newID(), "Unconnected", []string{}
	client.call("POST", "/lanes", asJSON(laneInput), "", 400)
	laneInput["connect_for"], laneInput["after_id"] = []string{humanID}, anchor.ID
	client.call("POST", "/lanes", asJSON(laneInput), "", 400)
	patch := map[string]any{"board_etag": after.Board.entityTag(), "label": "QA desk"}
	client.call("PATCH", lanePath, asJSON(patch), "", 428)
	client.call("PATCH", lanePath, asJSON(patch), etag(inserted.ID, 99), 412)
	patch["position"] = 12
	client.call("PATCH", lanePath, asJSON(patch), inserted.entityTag(), 400)
	delete(patch, "position")
	refusalBytes, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(committedBytes, refusalBytes) {
		t.Fatalf("a refusal changed durable state: %v", err)
	}

	body, headers = client.call("PATCH", lanePath, asJSON(patch), inserted.entityTag(), 200)
	renamed := turnOneDecode[lane](t, body)
	if renamed.Label != "QA desk" || renamed.entityTag() == inserted.entityTag() || headers.Get("ETag") != renamed.entityTag() ||
		headers.Get("X-Board-ETag") != etag(created.ID, 3) {
		t.Fatal("rename did not return the new label and both committed ETags")
	}
	actual, err := readSnapshot(path, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	missingCreation := cloneSnapshot(actual)
	missingCreation.Activity = slices.DeleteFunc(missingCreation.Activity, func(event activity) bool {
		return event.ResourceType == "lane" && event.ResourceID == inserted.ID && event.Operation == "create"
	})
	if err := validateSnapshot(missingCreation, created.ID); err == nil {
		t.Fatal("accepted a lane insertion without its creation audit")
	}
	if actual.Policy.entityTag() != after.Policy.entityTag() || policyAuditValue(actual.Policy) != policyAuditValue(after.Policy) {
		t.Fatal("rename changed policy grants")
	}
	patch["board_etag"] = actual.Board.entityTag()
	client.call("PATCH", lanePath, asJSON(patch), renamed.entityTag(), 200)
	noOp, _ := app.store.accepted(created.ID)
	if noOp.Board.entityTag() != actual.Board.entityTag() {
		t.Fatal("same-label rename advanced the board")
	}
	renamed.Label, renamed.Revision, renamed.UpdatedAt = inserted.Label, inserted.Revision, inserted.UpdatedAt
	if renamed != inserted {
		t.Fatal("rename changed lane identity or settings")
	}
}
