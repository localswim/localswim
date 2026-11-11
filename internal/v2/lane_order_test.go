package v2

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestVisibleLaneOrdering(t *testing.T) {
	tests := []struct {
		name  string
		cards []card
		links []relationship
		want  []uint64
	}{
		{name: "ticket order", cards: []card{laneTestCard(3, "normal", "a"), laneTestCard(1, "normal", "a"), laneTestCard(2, "normal", "a")}, want: []uint64{1, 2, 3}},
		{name: "priority precedes ticket", cards: []card{laneTestCard(1, "low", "a"), laneTestCard(3, "urgent", "a"), laneTestCard(2, "normal", "a")}, want: []uint64{3, 2, 1}},
		{name: "same rank blocker and eligible ticket", cards: []card{laneTestCard(1, "normal", "a"), laneTestCard(2, "normal", "a"), laneTestCard(3, "normal", "a")}, links: []relationship{laneTestLink(3, 1, "blocks")}, want: []uint64{2, 3, 1}},
		{name: "cross rank ignores edge", cards: []card{laneTestCard(1, "urgent", "a"), laneTestCard(2, "low", "a")}, links: []relationship{laneTestLink(2, 1, "blocks")}, want: []uint64{1, 2}},
		{name: "other relations ignored", cards: []card{laneTestCard(1, "normal", "a"), laneTestCard(2, "normal", "a")}, links: []relationship{laneTestLink(2, 1, "references")}, want: []uint64{1, 2}},
		{name: "cycle contiguous with outgoing dependency", cards: []card{laneTestCard(1, "normal", "a"), laneTestCard(2, "normal", "a"), laneTestCard(3, "normal", "a"), laneTestCard(4, "normal", "a"), laneTestCard(5, "normal", "a")}, links: []relationship{laneTestLink(5, 2, "blocks"), laneTestLink(2, 5, "blocks"), laneTestLink(5, 1, "blocks")}, want: []uint64{2, 5, 1, 3, 4}},
		{name: "cross lane path ignored", cards: []card{laneTestCard(1, "normal", "a"), laneTestCard(2, "normal", "b"), laneTestCard(3, "normal", "a")}, links: []relationship{laneTestLink(3, 2, "blocks"), laneTestLink(2, 1, "blocks")}, want: []uint64{1, 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := snapshot{Cards: test.cards, Relationships: test.links, Priorities: []priority{{entity: entity{ID: "urgent"}, Rank: 0}, {entity: entity{ID: "normal"}, Rank: 1}, {entity: entity{ID: "low"}, Rank: 2}}}
			before, _ := json.Marshal(s)
			got := laneTickets(orderedLaneCards(s, "a"))
			if !slices.Equal(got, test.want) {
				t.Fatalf("tickets = %v, want %v", got, test.want)
			}
			after, _ := json.Marshal(s)
			if string(before) != string(after) {
				t.Fatal("lane ordering mutated the snapshot")
			}
		})
	}
}

func TestVisibleBoardLanesExcludeRetiredAndArchived(t *testing.T) {
	archived := laneTestCard(2, "normal", "a")
	archived.Archived = true
	s := snapshot{
		Lanes:         []lane{{ID: "retired", Position: 0, Retired: true}, {ID: "b", Position: 2}, {ID: "a", Position: 1}, {ID: "empty", Position: 3}},
		Cards:         []card{laneTestCard(1, "normal", "a"), archived, laneTestCard(3, "normal", "a"), laneTestCard(4, "normal", "retired"), laneTestCard(5, "normal", "b")},
		Relationships: []relationship{laneTestLink(3, 2, "blocks"), laneTestLink(2, 1, "blocks")},
		Priorities:    []priority{{entity: entity{ID: "normal"}, Rank: 1}},
	}
	view := visibleBoardLanes(s)
	ids := make([]string, 0, len(view.Lanes))
	for _, lane := range view.Lanes {
		ids = append(ids, lane.ID)
	}
	if !slices.Equal(ids, []string{"a", "b", "empty"}) || !slices.Equal(laneTickets(view.Cards), []uint64{1, 3, 5}) {
		t.Fatalf("unexpected visible board view: %+v", view)
	}
	empty := visibleBoardLanes(snapshot{})
	if empty.Lanes == nil || empty.Cards == nil {
		t.Fatal("empty views must serialize arrays")
	}
	if !reflect.DeepEqual(view.Board, s.Board) {
		t.Fatal("view changed board metadata")
	}
}

func laneTestCard(ticket uint64, priorityID, laneID string) card {
	return card{ID: string(rune('a' + ticket)), Ticket: ticket, PriorityID: priorityID, LaneID: laneID}
}
func laneTestLink(source, target uint64, kind string) relationship {
	return relationship{relationshipLink: relationshipLink{SourceCardID: laneTestCard(source, "", "").ID, TargetCardID: laneTestCard(target, "", "").ID, Kind: kind}}
}
func laneTickets(cards []card) []uint64 {
	tickets := make([]uint64, 0, len(cards))
	for _, card := range cards {
		tickets = append(tickets, card.Ticket)
	}
	return tickets
}

func TestBoardLanesHTTPView(t *testing.T) {
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
	raw, _ := client.call("POST", "/boards", `{"name":"Visible lanes"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	tag := b.ETag
	cards := make([]card, 0, 3)
	for i := 0; i < 3; i++ {
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Card %d"}`, b.ID, tag, b.LaneIDs[0], i)
		raw, headers := client.call("POST", "/cards", body, "", 201)
		cards = append(cards, turnOneDecode[card](t, raw))
		tag = headers.Get("X-Board-ETag")
	}
	body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`, b.ID, tag, cards[2].ID, cards[0].ID)
	_, headers := client.call("POST", "/relationships", body, "", 201)
	tag = headers.Get("X-Board-ETag")
	raw, headers = client.call("GET", "/boards/"+b.ID+"?view=lanes", "", "", 200)
	view := turnOneDecode[boardLanesView](t, raw)
	if headers.Get("ETag") != tag || view.Board.Revision != 5 || len(view.Lanes) != len(b.LaneIDs) || !slices.Equal(laneTickets(view.Cards), []uint64{2, 3, 1}) {
		t.Fatalf("unexpected lanes response: %+v", view)
	}
	if view.Cards[0].Title != "Card 1" || view.Cards[0].Revision == 0 {
		t.Fatal("lanes response omitted full card fields")
	}
	raw, _ = client.call("GET", "/boards/"+b.ID, "", "", 200)
	if turnOneDecode[board](t, raw).ID != b.ID {
		t.Fatal("ordinary board read changed")
	}
}
