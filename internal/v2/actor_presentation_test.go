package v2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBoardActorPresentationAndLaneNotes(t *testing.T) {
	app, client := eventHTTPServer(t)

	body, _ := client.call("POST", "/boards", `{"name":"Presentation","actor_presentation":{"human":{"label":" Réviewer "}}}`, "", 201)
	created := turnOneDecode[board](t, body)
	before, _ := app.store.accepted(created.ID)
	if created.ActorPresentation.Human.Label != " Réviewer " {
		t.Fatal("board creation changed the supplied label")
	}
	boardPath := "/boards/" + created.ID
	client.call("PATCH", boardPath, `{"actor_presentation":{"human":{"accent":"red; background: url(a)"}}}`, created.entityTag(), 400)
	client.call("PATCH", boardPath, `{"actor_presentation":{"bot":{"kind":"human"}}}`, created.entityTag(), 400)
	client.call("PATCH", boardPath, `{"actor_presentation":{"human":{"label":"\uD800"}}}`, created.entityTag(), 400)
	client.call("PATCH", boardPath, `{"user_ids":[]}`, created.entityTag(), 400)
	body, _ = client.call("PATCH", boardPath, `{"actor_presentation":{"bot":{"label":" Automation ","accent":"#123aBC"}}}`, created.entityTag(), 200)
	updated := turnOneDecode[board](t, body)
	if updated.ActorPresentation.Human != (actorDecoration{}) ||
		updated.ActorPresentation.Bot != (actorDecoration{" Automation ", "#123aBC"}) {
		t.Fatal("board patch did not replace presentation while preserving exact text")
	}
	client.call("PATCH", boardPath, `{"actor_presentation":{}}`, created.entityTag(), 412)

	item := before.Lanes[0]
	lanePath := "/lanes/" + item.ID
	note := "  Étape <initiale>\nNext line. "
	payload, err := json.Marshal(map[string]string{"board_etag": updated.entityTag(), "notes": note})
	if err != nil {
		t.Fatal(err)
	}
	body, headers := client.call("PATCH", lanePath, string(payload), item.entityTag(), 200)
	edited := turnOneDecode[lane](t, body)
	if edited.Notes != note || edited.ID != item.ID || edited.Position != item.Position ||
		edited.Revision != updated.Revision+1 {
		t.Fatal("lane patch changed text, identity, position or revision unexpectedly")
	}
	boardTag := headers.Get("X-Board-ETag")
	noop, err := json.Marshal(map[string]string{"board_etag": boardTag, "notes": note})
	if err != nil {
		t.Fatal(err)
	}
	_, headers = client.call("PATCH", lanePath, string(noop), edited.entityTag(), 200)
	if headers.Get("X-Board-ETag") != boardTag {
		t.Fatal("unchanged note advanced the board revision")
	}
	durable, err := readSnapshot(app.store.snapshotPath(created.ID), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Lanes[0].Notes != note || durable.Board.ActorPresentation != updated.ActorPresentation ||
		!reflect.DeepEqual(before.Users, durable.Users) || !reflect.DeepEqual(before.Policy, durable.Policy) {
		t.Fatal("durable presentation did not preserve text, global users and policy")
	}
	malformed := cloneSnapshot(durable)
	malformed.Lanes[0].Notes = "Different text"
	if err := validateSnapshot(malformed, created.ID); err == nil {
		t.Fatal("native verification accepted notes inconsistent with their audit")
	}
	body, _ = client.call("PATCH", boardPath, `{"actor_presentation":{}}`, boardTag, 200)
	if reset := turnOneDecode[board](t, body); reset.ActorPresentation != (actorPresentation{}) {
		t.Fatal("empty presentation did not restore the default")
	}
}
