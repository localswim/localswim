package v2

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestM070CardPatchBoardGuard(t *testing.T) {
	app, client := attachmentHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Ticket patch fixture"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	lane := b.LaneIDs[0]
	raw, headers := client.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"First"}`, b.ID, b.ETag, lane), "", 201)
	first := turnOneDecode[card](t, raw)
	raw, headers = client.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Second","relationships":[{"target_card_id":%q,"kind":"blocks"}]}`,
		b.ID, headers.Get("X-Board-ETag"), lane, first.ID), "", 201)
	second := turnOneDecode[card](t, raw)
	viewTag := headers.Get("X-Board-ETag")
	path := "/cards/" + first.ID

	raw, _ = client.call("GET", path, "", "", 200)
	first = turnOneDecode[card](t, raw)
	raw, _ = client.call("PATCH", "/boards/"+b.ID, `{"name":"Unrelated edit"}`, viewTag, 200)
	currentBoard := turnOneDecode[board](t, raw)
	archive := `{"archived":true}`
	check := func(cardTag, boardTag string, status int) boardHTTPResult {
		t.Helper()
		result := boardRequest(client, "PATCH", path, archive, cardTag, boardTag)
		if result.err != nil || result.status != status {
			t.Fatalf("ticket patch status = %d, want %d: %v %s", result.status, status, result.err, result.data)
		}
		return result
	}
	check(first.entityTag(), viewTag, http.StatusPreconditionFailed)
	check(viewTag, currentBoard.ETag, http.StatusPreconditionFailed)
	check(first.entityTag(), "W/"+currentBoard.ETag, http.StatusBadRequest)
	check(first.entityTag(), currentBoard.ETag+","+viewTag, http.StatusBadRequest)
	check("", currentBoard.ETag, http.StatusPreconditionRequired)
	raw, _ = client.call("GET", path, "", "", 200)
	if got := turnOneDecode[card](t, raw); got.Archived || got.Revision != first.Revision {
		t.Fatal("refused patch changed the card")
	}

	// The order commits after the original ticket and validators were read.
	raw, headers = client.call("PUT", "/ticket-orders/"+lane,
		ticketOrderBody(t, b.ID, currentBoard.ETag, []string{second.ID, first.ID}), "", 200)
	order := turnOneDecode[ticketOrder](t, raw)
	if !order.Changed || order.Proposed[0].CardID != second.ID || order.Proposed[0].Ticket != first.Ticket {
		t.Fatal("test fixture did not move the other card into the original ticket")
	}
	before, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(first.entityTag(), currentBoard.ETag, http.StatusPreconditionFailed)
	after, err := app.store.durable(b.ID)
	if err != nil || !reflect.DeepEqual(before, after) || after.Cards[0].Archived || after.Cards[1].Archived {
		t.Fatal("stale patch changed a card or audit after renumbering")
	}

	raw, _ = client.call("GET", path, "", "", 200)
	currentCard := turnOneDecode[card](t, raw)
	result := check(currentCard.entityTag(), headers.Get("X-Board-ETag"), http.StatusOK)
	if got := turnOneDecode[card](t, result.data); got.ID != first.ID || !got.Archived || got.Ticket == first.Ticket {
		t.Fatal("matching validators did not archive the intended card at its new ticket")
	}
}
