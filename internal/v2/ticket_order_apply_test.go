package v2

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func ticketOrderBody(t *testing.T, boardID, tag string, ids []string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"board_id": boardID, "expected_board_etag": tag, "card_ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestM22ApplyTicketOrderHTTP(t *testing.T) {
	app, client := attachmentHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Ticket application"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	tag := b.entityTag()
	cards := make([]card, 0, 4)
	for index := range 4 {
		extra := ""
		if index == 2 {
			extra = fmt.Sprintf(`,"parent_id":%q,"relationships":[{"target_card_id":%q,"kind":"blocks"}]`, cards[0].ID, cards[0].ID)
		}
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Preserve #0001"%s}`, b.ID, tag, b.LaneIDs[0], extra)
		bodyRaw, headers := client.call("POST", "/cards", body, "", 201)
		cards = append(cards, turnOneDecode[card](t, bodyRaw))
		tag = headers.Get("X-Board-ETag")
	}
	raw, _ = client.call("GET", "/cards/"+cards[0].ID, "", "", 200)
	first := turnOneDecode[card](t, raw)
	attachmentBytes := []byte("\x89PNG\r\n\x1a\n")
	raw, _ = attachmentHTTPUpload(t, client, []attachmentFormField{{"card_id", first.ID}, {"expected_card_etag", first.entityTag()}}, "stable.png", attachmentBytes, 201)
	attached := turnOneDecode[attachment](t, raw)
	raw, _ = client.call("GET", "/cards/"+first.ID, "", "", 200)
	first = turnOneDecode[card](t, raw)
	raw, _ = client.call("PATCH", "/cards/"+first.ID, fmt.Sprintf(`{"cover_attachment_id":%q}`, attached.ID), first.entityTag(), 200)
	first = turnOneDecode[card](t, raw)
	client.call("POST", "/comments", fmt.Sprintf(`{"card_id":%q,"expected_card_etag":%q,"text":"Historical #0001 stays exact"}`, first.ID, first.entityTag()), "", 201)
	client.call("PATCH", "/cards/"+cards[3].ID, `{"archived":true}`, cards[3].entityTag(), 200)
	before, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/ticket-orders/" + strings.ToUpper(b.LaneIDs[0])
	raw, headers := client.call("GET", path+"?preview=true&card_id="+cards[0].ID+"&card_id="+cards[2].ID, "", "", 200)
	preview := turnOneDecode[ticketOrder](t, raw)
	if !preview.Changed || !slices.EqualFunc(preview.Proposed, []ticketOrderEntry{{cards[2].ID, 1}, {cards[0].ID, 3}}, func(a, b ticketOrderEntry) bool { return a == b }) {
		t.Fatal("fixture did not preview selected renumbering")
	}
	ids := []string{strings.ToUpper(cards[2].ID), strings.ToUpper(cards[0].ID)}
	body := ticketOrderBody(t, strings.ToUpper(b.ID), headers.Get("X-Board-ETag"), ids)
	raw, headers = client.call("PUT", path, body, "", 200)
	result := turnOneDecode[ticketOrder](t, raw)
	if !result.Changed || result.BoardRevision != before.Board.Revision+1 || headers.Get("X-Board-ETag") != etag(b.ID, result.BoardRevision) ||
		!reflect.DeepEqual(result.Current, preview.Current) || !reflect.DeepEqual(result.Proposed, preview.Proposed) {
		t.Fatal("apply did not return the committed preview and revision")
	}
	after, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := cloneSnapshot(before)
	want.Board = after.Board
	want.Activity = after.Activity
	for index := range want.Cards {
		if index == 0 || index == 2 {
			want.Cards[index].Ticket = after.Cards[index].Ticket
			want.Cards[index].Revision = result.BoardRevision
			want.Cards[index].UpdatedAt = after.Cards[index].UpdatedAt
		}
	}
	if !reflect.DeepEqual(want, after) || after.Cards[0].Ticket != 3 || after.Cards[2].Ticket != 1 ||
		len(after.Activity) != len(before.Activity)+2 || !reflect.DeepEqual(after.Activity[:len(before.Activity)], before.Activity) {
		t.Fatal("renumbering changed unrelated state, versions, or historical activity")
	}
	for _, event := range after.Activity[len(before.Activity):] {
		if event.ActorID != humanID || event.Operation != ticketOrderOperation || event.Revision != result.BoardRevision || snapshotString(event.Field) != "ticket" {
			t.Fatal("renumbering audit lacks actor, operation, field, or committed revision")
		}
	}
	content, err := readVerifiedAttachment(app.store.root, attached)
	if err != nil || !bytes.Equal(content, attachmentBytes) {
		t.Fatal("renumbering changed the UUID attachment anchor or immutable bytes")
	}
	raw, _ = client.call("GET", "/cards/"+first.ID, "", "", 200)
	if got := turnOneDecode[card](t, raw); got.Ticket != 3 || got.ID != first.ID || got.CoverAttachmentID == nil || *got.CoverAttachmentID != attached.ID {
		t.Fatal("focused card read did not retain identity and cover with its new ticket")
	}
	client.call("PUT", path, body, "", 412)
	body = ticketOrderBody(t, b.ID, after.Board.entityTag(), []string{cards[2].ID, cards[0].ID})
	raw, headers = client.call("PUT", path, body, "", 200)
	noop := turnOneDecode[ticketOrder](t, raw)
	if noop.Changed || noop.BoardRevision != after.Board.Revision || headers.Get("X-Board-ETag") != after.Board.entityTag() {
		t.Fatal("no-op changed board revision or claimed a permutation")
	}
	client.call("PUT", path, ticketOrderBody(t, b.ID, after.Board.entityTag(), []string{}), "", 200)
	stable, err := app.store.durable(b.ID)
	if err != nil || !reflect.DeepEqual(after, stable) {
		t.Fatal("stale refusal or no-op changed durable state")
	}
	for _, ids := range [][]string{{cards[0].ID, cards[2].ID}, {cards[3].ID}, {newID()}, {cards[0].ID, strings.ToUpper(cards[0].ID)}} {
		client.call("PUT", path, ticketOrderBody(t, b.ID, after.Board.entityTag(), ids), "", 400)
	}
	client.call("PUT", "/ticket-orders/"+newID(), ticketOrderBody(t, b.ID, after.Board.entityTag(), []string{}), "", 404)
	client.call("PUT", path, fmt.Sprintf(`{"board_id":%q,"card_ids":[]}`, b.ID), "", 428)
	client.call("PUT", path, ticketOrderBody(t, newID(), after.Board.entityTag(), []string{}), "", 404)
	for name, damage := range map[string]func(*snapshot){
		"ticket continuity": func(s *snapshot) { s.Activity[len(before.Activity)].Before = new("2") },
		"ticket pool":       func(s *snapshot) { s.Activity[len(before.Activity)].After = new("99") },
		"operation":         func(s *snapshot) { s.Activity[len(before.Activity)].Operation = "patch" },
		"canonical number":  func(s *snapshot) { s.Activity[len(before.Activity)].Before = new("03") },
		"version":           func(s *snapshot) { s.Cards[0].Revision = before.Cards[0].Revision },
		"current ticket":    func(s *snapshot) { s.Cards[0].Ticket, s.Cards[2].Ticket = s.Cards[2].Ticket, s.Cards[0].Ticket },
	} {
		t.Run(name, func(t *testing.T) {
			broken := cloneSnapshot(after)
			damage(&broken)
			if err := validateSnapshot(broken, b.ID); err == nil {
				t.Fatal("invalid renumber replay accepted")
			}
		})
	}
	if err := app.store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(app.store.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	replayed, ok := reopened.accepted(b.ID)
	if !ok || !reflect.DeepEqual(replayed, after) {
		t.Fatal("reopened store did not replay the committed ticket order")
	}
}

func TestM22TicketOrderInput(t *testing.T) {
	id := newID()
	valid := map[string]any{"board_id": id, "expected_board_etag": etag(id, 1), "card_ids": []string{newID()}}
	for _, test := range []struct {
		name, field string
		value       any
	}{
		{"null", "card_ids", nil}, {"object", "card_ids", map[string]any{}}, {"member", "card_ids", []any{true}},
		{"duplicates", "card_ids", []string{id, strings.ToUpper(id)}}, {"large", "card_ids", make([]string, 1001)},
		{"weak", "expected_board_etag", "W/" + etag(id, 1)}, {"unknown", "other", true}, {"protected", "actor_id", humanID},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := make(map[string]any, len(valid)+1)
			for key, value := range valid {
				fields[key] = value
			}
			fields[test.field] = test.value
			data, _ := json.Marshal(fields)
			object, err := decodeObject(data)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readTicketOrderInput(object); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestM22TicketOrderAuthorizationAndConcurrentStale(t *testing.T) {
	app, client := attachmentHTTPServer(t)
	fixture := func() snapshot {
		id, now := newID(), canonicalTime(time.Now())
		value := freshSnapshot(id, "Permissions", defaultWorkflow(id, now, app.principals[:1]), humanID, now)
		value.Board.DefaultOwnerID = humanID
		return value
	}
	value := fixture()
	for index := range value.Policy.ResourceGrants {
		value.Policy.ResourceGrants[index].Capabilities = slices.DeleteFunc(value.Policy.ResourceGrants[index].Capabilities, func(s string) bool { return s == "reorder_tickets" })
	}
	if err := app.store.create(value); err != nil {
		t.Fatal(err)
	}
	path := "/ticket-orders/" + value.Lanes[0].ID
	before, err := os.ReadFile(app.store.snapshotPath(value.Board.ID))
	if err != nil {
		t.Fatal(err)
	}
	client.call("PUT", path, ticketOrderBody(t, value.Board.ID, value.Board.entityTag(), []string{}), "", 403)
	bot := client
	bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	bot.call("PUT", path, ticketOrderBody(t, value.Board.ID, value.Board.entityTag(), []string{}), "", 404)
	after, err := os.ReadFile(app.store.snapshotPath(value.Board.ID))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("authorization refusal wrote the board")
	}
	// Both callers enter through the same atomic board precondition.
	value = fixture()
	if err := app.store.create(value); err != nil {
		t.Fatal(err)
	}
	in := cardCreation{id: newID(), laneID: value.Lanes[0].ID, title: "First"}
	next, err := app.store.update(value.Board.ID, value.Board.Revision, func(s *snapshot) error { return in.apply(s, humanID) })
	if err != nil {
		t.Fatal(err)
	}
	in.id, in.title = newID(), "Second"
	in.relationships = []relationshipSeed{{targetID: next.Cards[0].ID, kind: "blocks"}}
	next, err = app.store.update(value.Board.ID, next.Board.Revision, func(s *snapshot) error { return in.apply(s, humanID) })
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := app.store.update(next.Board.ID, next.Board.Revision, func(s *snapshot) error {
				_, err := (ticketOrderInput{cardIDs: []string{next.Cards[1].ID, next.Cards[0].ID}}).apply(s, value.Lanes[0].ID, humanID)
				return err
			})
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	succeeded, stale := 0, 0
	for err := range errors {
		if err == nil {
			succeeded++
		} else if err == errStale {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || stale != 1 {
		t.Fatal("concurrent apply did not commit exactly once")
	}
}
