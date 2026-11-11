package v2

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPriorityFloorBlockingRelationshipTransaction(t *testing.T) {
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
	raw, _ := client.call("POST", "/boards", `{"name":"Priority floors"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	urgent, normal, low := b.PriorityIDs[0], b.PriorityIDs[1], b.PriorityIDs[2]
	boardTag := b.entityTag()
	createCard := func(title, priorityID, extra string) card {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q,"priority_id":%q%s}`,
			b.ID, boardTag, b.LaneIDs[0], title, priorityID, extra)
		raw, headers := client.call("POST", "/cards", body, "", 201)
		boardTag = headers.Get("X-Board-ETag")
		return turnOneDecode[card](t, raw)
	}
	a := createCard("Upstream", low, "")
	middle := createCard("Archived", low, "")
	upstreamBody := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`, b.ID, boardTag, a.ID, middle.ID)
	_, upstreamHeaders := client.call("POST", "/relationships", upstreamBody, "", 201)
	boardTag = upstreamHeaders.Get("X-Board-ETag")
	raw, _ = client.call("GET", "/cards/"+middle.ID, "", "", 200)
	middle = turnOneDecode[card](t, raw)
	target := createCard("Target", normal, "")
	_, headers := client.call("PATCH", "/cards/"+middle.ID, `{"archived":true}`, middle.entityTag(), 200)
	boardTag = headers.Get("X-Board-ETag")

	body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"references"}`,
		b.ID, boardTag, middle.ID, target.ID)
	raw, headers = client.call("POST", "/relationships", body, "", 201)
	link := turnOneDecode[relationship](t, raw)
	linkTag := headers.Get("ETag")
	before, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/relationships/" + link.ID
	body = fmt.Sprintf(`{"expected_board_etag":%q,"kind":"blocks"}`, before.Board.entityTag())
	client.call("PATCH", path, body, linkTag, 200)
	saved, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	cards := relationshipCards(&saved)
	promotions := saved.Activity[len(before.Activity)+1:]
	if saved.Board.Revision != before.Board.Revision+1 || len(promotions) != 2 ||
		cards[a.ID].PriorityID != normal || cards[middle.ID].PriorityID != normal || !cards[middle.ID].Archived ||
		cards[a.ID].Revision != saved.Board.Revision || cards[middle.ID].Revision != saved.Board.Revision {
		t.Fatal("blocking link did not atomically promote both upstream cards")
	}
	for _, event := range promotions {
		if event.Operation != "promote" || event.Revision != saved.Board.Revision || event.ActorID != humanID ||
			snapshotString(event.Field) != "priority_id" || snapshotString(event.Before) != low || snapshotString(event.After) != normal {
			t.Fatalf("unexpected promotion event: %+v", event)
		}
	}

	body = fmt.Sprintf(`{"priority_id":%q}`, low)
	raw, headers = client.call("PATCH", "/cards/"+a.ID, body, cards[a.ID].entityTag(), 200)
	if turnOneDecode[card](t, raw).PriorityID != normal || headers.Get("X-Board-ETag") != saved.Board.entityTag() {
		t.Fatal("requested demotion below the floor should return the effective priority without a write")
	}

	body = fmt.Sprintf(`{"priority_id":%q}`, urgent)
	client.call("PATCH", "/cards/"+target.ID, body, cards[target.ID].entityTag(), 200)
	raised, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range raised.Cards {
		if item.PriorityID != urgent || item.Revision != raised.Board.Revision {
			t.Fatal("raising the downstream card did not promote upstream cards at the same revision")
		}
	}
	_, headers = client.call("GET", path, "", "", 200)
	body = fmt.Sprintf(`{"expected_board_etag":%q,"kind":"references"}`, raised.Board.entityTag())
	client.call("PATCH", path, body, headers.Get("ETag"), 200)
	after, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Activity) != len(raised.Activity)+1 {
		t.Fatal("removing a blocking edge changed more than the relationship")
	}
	for _, item := range after.Cards {
		if item.PriorityID != urgent {
			t.Fatal("removing a blocking edge demoted a card")
		}
	}
}

func TestPriorityFloorCyclesAndCompletedCutoff(t *testing.T) {
	now := time.Now()
	s := snapshot{
		Priorities: []priority{
			{entity: newEntity("high", now), Rank: 0},
			{entity: newEntity("low", now), Rank: 1},
		},
		Cards: []card{
			{ID: "a", PriorityID: "low"},
			{ID: "b", PriorityID: "low", Archived: true},
			{ID: "completed", PriorityID: "low", CompletedAt: &now},
			{ID: "urgent", PriorityID: "high"},
			{ID: "unrelated", PriorityID: "low"},
		},
	}
	add := func(from, to, kind string) {
		s.Relationships = append(s.Relationships, relationship{
			relationshipLink: relationshipLink{SourceCardID: from, TargetCardID: to, Kind: kind},
		})
	}
	add("a", "b", "blocks")
	add("b", "a", "blocks")
	add("b", "completed", "blocks")
	add("completed", "urgent", "blocks")
	add("unrelated", "urgent", "duplicates")
	if got := calculatePriorityFloor(&s, nil).ids; !slices.Equal(got, []string{"low", "low", "low", "high", "low"}) {
		t.Fatalf("Completed or a nonblocking link transmitted urgency: %v", got)
	}
	add("b", "urgent", "blocks")
	if got := calculatePriorityFloor(&s, nil).ids; !slices.Equal(got, []string{"high", "high", "low", "high", "low"}) {
		t.Fatalf("unfinished cycle failed to inherit urgency: %v", got)
	}
	// A pre-floor snapshot can have unpropagated urgency. An unrelated no-op
	// priority edit has no basis for issuing automatic promotions.
	s.Board.UserIDs = []string{"actor"}
	s.Board.PriorityIDs = []string{"high", "low"}
	s.Policy.ResourceGrants = []resourceGrant{{UserID: "actor", Capabilities: []string{"set_priority"}}}
	if err := applyCardPriority(&s, &s.Cards[4], "low", "actor", false, now); err != nil {
		t.Fatal(err)
	}
	if len(s.Activity) > 0 || s.Cards[0].PriorityID != "low" || s.Cards[1].PriorityID != "low" {
		t.Fatal("unrelated no-op priority edit changed preexisting cards")
	}
}
