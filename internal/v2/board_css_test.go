package v2

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBoardCSSConfiguration(t *testing.T) {
	app, client := eventHTTPServer(t)
	const initial = "  .lanes { color: #246; } /* Café 😀 */\n "
	const changed = "  .lanes { color: #468; }\n "
	body, _ := client.call("POST", "/boards", `{"name":"Styled","user_css":"  .lanes { color: #246; } /* Café \uD83D\uDE00 */\n "}`, "", 201)
	created := turnOneDecode[board](t, body)
	if created.UserCSS != initial {
		t.Fatal("creation changed the supplied stylesheet")
	}
	path := "/boards/" + created.ID
	for _, input := range []string{`null`, `{}`, `"\uD800"`, `"` + strings.Repeat("é", maxBoardCSSLength+1) + `"`} {
		client.call("PATCH", path, `{"name":"Must remain Styled","user_css":`+input+`}`, created.ETag, 400)
	}
	client.call("POST", "/boards", `{"name":"Invalid","user_css":null}`, "", 400)
	body, _ = client.call("POST", "/boards", `{"name":"Unstyled"}`, "", 201)
	other := turnOneDecode[board](t, body)
	if other.UserCSS != "" {
		t.Fatal("a separate board inherited the stylesheet")
	}
	const patch = `{"user_css":"  .lanes { color: #468; }\n "}`
	body, _ = client.call("PATCH", path, patch, created.ETag, 200)
	updated := turnOneDecode[board](t, body)
	if updated.UserCSS != changed || updated.Revision != created.Revision+1 || updated.Name != created.Name {
		t.Fatal("board stylesheet did not commit at the next revision")
	}
	client.call("PATCH", path, `{"user_css":""}`, created.ETag, 412)
	body, _ = client.call("PATCH", path, patch, updated.ETag, 200)
	if same := turnOneDecode[board](t, body); same.ETag != updated.ETag {
		t.Fatal("identical stylesheet advanced the board revision")
	}
	durable, err := app.store.durable(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	event := durable.Activity[len(durable.Activity)-1]
	if durable.Board.UserCSS != changed || snapshotString(event.Field) != "user_css" ||
		snapshotString(event.Before) != initial || snapshotString(event.After) != changed ||
		event.Revision != updated.Revision || event.ActorID != humanID {
		t.Fatal("native storage did not retain the exact stylesheet and attributed change")
	}
	durable.Board.UserCSS = initial
	if err := validateSnapshot(durable, created.ID); err == nil {
		t.Fatal("native verification accepted CSS inconsistent with its audit")
	}
	body, _ = client.call("GET", path+"?view=lanes", "", "", 200)
	if current := turnOneDecode[boardLanesView](t, body); current.Board.UserCSS != changed {
		t.Fatal("browser lane view lost the stylesheet")
	}
	body, _ = client.call("PATCH", path, `{"user_css":""}`, updated.ETag, 200)
	if cleared := turnOneDecode[board](t, body); cleared.UserCSS != "" || cleared.Revision != updated.Revision+1 {
		t.Fatal("empty text did not clear the stylesheet")
	}
}

func TestBoardCSSPatchRequiresManageBoard(t *testing.T) {
	app, client := eventHTTPServer(t)
	id, now := newID(), canonicalTime(time.Now())
	value := freshSnapshot(id, "Permissions", defaultWorkflow(id, now, app.principals), humanID, now)
	for i := range value.Policy.ResourceGrants {
		grant := &value.Policy.ResourceGrants[i]
		grant.Capabilities = slices.DeleteFunc(grant.Capabilities, func(s string) bool { return s == "manage_board" })
	}
	if err := app.store.create(value); err != nil {
		t.Fatal(err)
	}
	path := "/boards/" + id
	client.call("PATCH", path, `{"user_css":"h1 { color: red; }"}`, value.Board.ETag, 403)
	body, _ := client.call("GET", path, "", "", 200)
	if current := turnOneDecode[board](t, body); current.UserCSS != "" || current.ETag != value.Board.ETag {
		t.Fatal("refused patch modified the board")
	}
}
