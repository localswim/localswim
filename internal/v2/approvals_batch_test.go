package v2

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func approvalTestClients(t *testing.T) (*Service, turnOneClient, turnOneClient) {
	t.Helper()
	humanToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	botToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), humanToken, botToken)
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
	bot := turnOneClient{t, server.URL, botToken, server.Client()}
	human := turnOneClient{t, server.URL, humanToken, server.Client()}
	return app, human, bot
}

func TestLaneReviewApproval(t *testing.T) {
	app, human, bot := approvalTestClients(t)

	raw, _ := human.call("POST", "/boards", `{"name":"Synthetic lane approval"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	reviewID, doneID := b.LaneIDs[3], b.LaneIDs[4]
	body := func(boardTag, laneID, cardVersions string) string {
		return fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"cards":%s}`,
			b.ID, boardTag, laneID, cardVersions)
	}
	human.call("POST", "/approvals", body(b.entityTag(), reviewID, "[]"), "", 409)
	_, header := human.call("PATCH", "/lanes/"+reviewID, fmt.Sprintf(
		`{"board_etag":%q,"approval_enabled":true}`, b.entityTag()), etag(reviewID, 1), 200)
	boardTag := header.Get("X-Board-ETag")
	human.call("POST", "/approvals", body(boardTag, b.LaneIDs[0], "[]"), "", 409)
	bot.call("POST", "/approvals", body(boardTag, reviewID, "[]"), "", 403)
	human.call("POST", "/approvals", body(b.entityTag(), reviewID, "[]"), "", 412)
	human.call("POST", "/approvals", body(boardTag, reviewID, "null"), "", 400)
	emptyBefore, _ := app.store.accepted(b.ID)
	raw, header = human.call("POST", "/approvals", body(boardTag, reviewID, "[]"), "", 200)
	noop := turnOneDecode[approvalNoop](t, raw)
	emptyAfter, _ := app.store.accepted(b.ID)
	if noop.BoardID != b.ID || noop.BoardRevision != emptyBefore.Board.Revision || noop.Changed ||
		noop.CardIDs == nil || len(noop.CardIDs) != 0 || header.Get("Location") != "" ||
		header.Get("X-Board-ETag") != boardTag || !reflect.DeepEqual(emptyBefore, emptyAfter) {
		t.Fatal("empty-lane approval changed state or returned an invalid no-op receipt")
	}

	makeReviewCard := func(title string) card {
		raw, header := bot.call("POST", "/cards", fmt.Sprintf(
			`{"board_id":%q,"expected_board_etag":%q,"title":%q,"lane_id":%q}`,
			b.ID, boardTag, title, b.LaneIDs[0]), "", 201)
		c := turnOneDecode[card](t, raw)
		for _, laneID := range b.LaneIDs[1:4] {
			raw, header = bot.call("PATCH", "/cards/"+c.ID, fmt.Sprintf(`{"lane_id":%q}`, laneID), c.entityTag(), 200)
			c = turnOneDecode[card](t, raw)
		}
		boardTag = header.Get("X-Board-ETag")
		return c
	}
	hidden := makeReviewCard("Archived review")
	raw, header = human.call("PATCH", "/cards/"+hidden.ID, `{"archived":true}`, hidden.entityTag(), 200)
	hidden = turnOneDecode[card](t, raw)
	boardTag = header.Get("X-Board-ETag")
	human.call("POST", "/approvals", body(boardTag, reviewID, "[]"), "", 200)
	first := makeReviewCard("First review")
	originalBoardTag := boardTag
	second := makeReviewCard("Second review")
	version := func(cardID, tag string) string { return fmt.Sprintf(`{"card_id":%q,"etag":%q}`, cardID, tag) }
	versions := func(values ...string) string { return "[" + strings.Join(values, ",") + "]" }
	one, two := version(first.ID, first.entityTag()), version(second.ID, second.entityTag())
	rendered := versions(two, one)
	good := body(boardTag, reviewID, rendered)
	before, _ := app.store.accepted(b.ID)
	human.call("POST", "/approvals", body(originalBoardTag, reviewID, versions(one)), "", 412)
	human.call("POST", "/approvals", body(boardTag, reviewID, versions(one)), "", 409)
	human.call("POST", "/approvals", body(boardTag, reviewID, "[]"), "", 409)
	human.call("POST", "/approvals", body(boardTag, reviewID, versions(one, version(hidden.ID, hidden.entityTag()))), "", 409)
	human.call("POST", "/approvals", body(boardTag, reviewID, versions(one, two, version(hidden.ID, hidden.entityTag()))), "", 409)
	human.call("POST", "/approvals", body(boardTag, reviewID, versions(one, version(second.ID, etag(second.ID, 1)))), "", 412)
	human.call("POST", "/approvals", body(boardTag, reviewID, versions(one, version(strings.ToUpper(first.ID), first.entityTag()))), "", 400)
	human.call("POST", "/approvals", body(boardTag, reviewID, versions(one, fmt.Sprintf(`{"card_id":%q}`, second.ID))), "", 428)
	bot.call("POST", "/approvals", good, "", 403)
	afterRefusals, _ := app.store.accepted(b.ID)
	if !reflect.DeepEqual(before, afterRefusals) {
		t.Fatal("a refused batch changed accepted state")
	}

	approvalID := newOperationID()
	good = strings.Replace(good, "{", fmt.Sprintf(`{"id":%q,`, approvalID), 1)
	raw, header = human.call("POST", "/approvals", good, "", 201)
	result := turnOneDecode[approval](t, raw)
	receiptPath := "/approvals/" + approvalID
	if result.ID != approvalID || result.BoardID != b.ID || result.ActorID != humanID ||
		result.BoardRevision != before.Board.Revision+1 || !slices.Equal(result.CardIDs, []string{second.ID, first.ID}) ||
		len(result.Changes) != 4 || header.Get("Location") != Prefix+receiptPath ||
		header.Get("X-Board-ETag") != etag(b.ID, result.BoardRevision) {
		t.Fatal("batch receipt did not identify all committed cards and their shared revision")
	}
	if fields := turnOneDecode[map[string]json.RawMessage](t, raw); fields["board_etag"] != nil {
		t.Fatal("committed receipt serialized a board entity tag")
	}
	committed, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Board.Revision != result.BoardRevision || len(committed.Activity) != len(before.Activity)+4 ||
		len(committed.Comments) != 2 || committed.Board.Attention.ReadyForReview != 0 ||
		committed.Board.LastCardEditAt == nil || !committed.Board.LastCardEditAt.Equal(result.At) {
		t.Fatal("batch did not durably commit exactly two move/comment pairs together")
	}
	hiddenAfter := committed.Cards[slices.IndexFunc(committed.Cards, func(c card) bool { return c.ID == hidden.ID })]
	if !reflect.DeepEqual(hiddenAfter, hidden) {
		t.Fatal("batch changed the archived review card")
	}
	for index, original := range []card{second, first} {
		pair := result.Changes[index*2 : index*2+2]
		if pair[0].ResourceID != original.ID || snapshotString(pair[0].Before) != reviewID ||
			snapshotString(pair[0].After) != doneID || pair[0].Revision != result.BoardRevision ||
			!validApprovalPair(pair[0], pair[1], committed.Comments[index], reviewApprovalText) {
			t.Fatal("approval pair differs from the move and fixed comment")
		}
		moved := committed.Cards[slices.IndexFunc(committed.Cards, func(c card) bool { return c.ID == original.ID })]
		if moved.OwnerID != original.OwnerID || moved.LaneID != doneID || moved.Revision != result.BoardRevision ||
			moved.CommentCount != original.CommentCount+1 || moved.CompletedAt == nil || !moved.CompletedAt.Equal(result.At) {
			t.Fatal("batch did not preserve ownership and update each card")
		}
	}
	human.call("POST", "/approvals", good, "", 412)
	human.call("GET", "/approvals/"+result.Changes[2].ID, "", "", 404)
	raw, _ = bot.call("GET", receiptPath, "", "", 200)
	if !reflect.DeepEqual(turnOneDecode[approval](t, raw), result) {
		t.Fatal("batch receipt readback differed from the original")
	}
	withoutCapturedText := cloneSnapshot(committed)
	withoutCapturedText.Activity[len(before.Activity)].BulkApprovalComment = ""
	if err := validateSnapshot(withoutCapturedText, b.ID); err != nil {
		t.Fatalf("default approval without a captured comment no longer verifies: %v", err)
	}
}

func TestLaneReviewApprovalLargeCustom(t *testing.T) {
	app, human, bot := approvalTestClients(t)
	raw, _ := human.call("POST", "/boards", `{"name":"Large synthetic lane approval"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	reviewID, doneID := b.LaneIDs[3], b.LaneIDs[4]
	lanePath := "/lanes/" + reviewID
	const custom = "  Checked and approved ✅\r\nReady to ship.  "
	setComment := func(boardTag, laneTag, text string) ([]byte, http.Header) {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"board_etag": boardTag, "bulk_approval_comment": text})
		if err != nil {
			t.Fatal(err)
		}
		return human.call("PATCH", lanePath, string(payload), laneTag, 200)
	}
	human.call("PATCH", "/lanes/"+b.LaneIDs[0], fmt.Sprintf(
		`{"board_etag":%q,"bulk_approval_comment":"Only review lanes can use this setting."}`,
		b.entityTag()), etag(b.LaneIDs[0], 1), 400)
	raw, _ = setComment(b.entityTag(), etag(reviewID, 1), custom)
	review := turnOneDecode[lane](t, raw)
	if review.ApprovalEnabled || review.BulkApprovalComment != custom {
		t.Fatal("custom text did not preserve its exact content while approval was disabled")
	}

	const count = 1001
	seed := seedReviewLane(t, app, b.ID, count+1)
	body := func(tag, laneID, approvalID string, cards []card) string {
		t.Helper()
		versions := make([]map[string]string, 0, len(cards))
		for _, item := range cards {
			versions = append(versions, map[string]string{"card_id": item.ID, "etag": item.entityTag()})
		}
		fields := map[string]any{"id": approvalID, "board_id": b.ID, "expected_board_etag": tag, "cards": versions}
		if laneID != "" {
			fields["lane_id"] = laneID
		}
		payload, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return string(payload)
	}
	bulkID := newOperationID()
	human.call("POST", "/approvals", body(seed.Board.entityTag(), reviewID, bulkID, seed.Cards), "", 409)
	raw, header := human.call("PATCH", lanePath, fmt.Sprintf(
		`{"board_etag":%q,"approval_enabled":true}`, seed.Board.entityTag()), review.entityTag(), 200)
	review = turnOneDecode[lane](t, raw)
	renderedTag := header.Get("X-Board-ETag")
	staleRequest := body(renderedTag, reviewID, bulkID, seed.Cards)
	tooMany := "[" + strings.Repeat("{},", maxApprovalCards) + "{}]"
	human.call("POST", "/approvals", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"cards":%s}`,
		b.ID, renderedTag, reviewID, tooMany), "", 400)
	empty := body(renderedTag, reviewID, bulkID, nil)
	padding := strings.Repeat(" ", maxApprovalBody-len(empty))
	human.call("POST", "/approvals", empty+padding, "", 409)
	human.call("POST", "/approvals", empty+padding+" ", "", 413)

	raw, header = human.call("POST", "/approvals", body(renderedTag, "", newOperationID(), seed.Cards[:1]), "", 201)
	single := turnOneDecode[approval](t, raw)
	current, _ := app.store.accepted(b.ID)
	if single.Changes[0].BulkApprovalComment != "" || len(current.Comments) != 1 ||
		current.Comments[0].Text != reviewApprovalText {
		t.Fatal("individual approval used the bulk comment")
	}
	currentTag := header.Get("X-Board-ETag")
	human.call("POST", "/approvals", staleRequest, "", 412)
	human.call("POST", "/approvals", body(currentTag, reviewID, bulkID, seed.Cards), "", 409)
	good := body(currentTag, reviewID, bulkID, seed.Cards[1:])
	bot.call("POST", "/approvals", good, "", 403)
	raw, header = human.call("POST", "/approvals", good, "", 201)
	receipt := turnOneDecode[approval](t, raw)
	if receipt.ID != bulkID || receipt.ActorID != humanID || receipt.BoardRevision != current.Board.Revision+1 ||
		len(receipt.CardIDs) != count || len(receipt.Changes) != 2*count || receipt.Changes[0].BulkApprovalComment != custom {
		t.Fatal("large receipt did not capture the custom comment and the exact committed batch")
	}
	raw, header = setComment(header.Get("X-Board-ETag"), review.entityTag(), "Subsequent approvals only.")
	updated := turnOneDecode[lane](t, raw)
	setComment(header.Get("X-Board-ETag"), updated.entityTag(), "")

	path := app.store.snapshotPath(b.ID)
	committed, err := readSnapshot(path, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Comments) != count+1 || committed.Board.Attention.ReadyForReview != 0 ||
		committed.Lanes[3].BulkApprovalComment != "" {
		t.Fatal("large approval or later reset was not durable")
	}
	for i, item := range committed.Cards[1:] {
		move, note := receipt.Changes[2*i], receipt.Changes[2*i+1]
		if item.ID != seed.Cards[i+1].ID || item.LaneID != doneID || item.CommentCount != 1 ||
			item.Revision != receipt.BoardRevision || receipt.CardIDs[i] != item.ID ||
			!validApprovalPair(move, note, committed.Comments[i+1], custom) ||
			(i > 0 && move.BulkApprovalComment != "") {
			t.Fatalf("card %d differs from the committed move and comment", i)
		}
	}
	raw, _ = human.call("GET", "/approvals/"+bulkID, "", "", 200)
	if !reflect.DeepEqual(turnOneDecode[approval](t, raw), receipt) {
		t.Fatal("subsequent settings edits changed the historical receipt")
	}

	tampered := cloneSnapshot(committed)
	for index := range tampered.Comments[1:] {
		tampered.Comments[index+1].Text = "Fabricated approval."
	}
	event := slices.IndexFunc(tampered.Activity, func(item activity) bool { return item.ID == bulkID })
	tampered.Activity[event].BulkApprovalComment = "Fabricated approval."
	if err := validateSnapshot(tampered, b.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("fabricated approval text passed verification: %v", err)
	}
}

func seedReviewLane(t *testing.T, app *Service, boardID string, count int) snapshot {
	t.Helper()
	app.store.mu.Lock()
	defer app.store.mu.Unlock()
	accepted := app.store.boards[boardID]
	state := cloneSnapshot(accepted)
	advance := func() {
		event := state.Activity[len(state.Activity)-1]
		state.Board.Revision, state.Board.UpdatedAt = event.Revision, event.At
	}
	for i := range count {
		in := cardCreation{id: newID(), title: fmt.Sprint("Fixture ", i), laneID: state.Board.LaneIDs[1]}
		if err := in.apply(&state, botID); err != nil {
			t.Fatal(err)
		}
		advance()
		item := &state.Cards[len(state.Cards)-1]
		for _, target := range state.Board.LaneIDs[2:4] {
			if _, err := applyCardLane(&state, item, target, botID, state.Board.UpdatedAt); err != nil {
				t.Fatal(err)
			}
			advance()
			item.Revision, item.UpdatedAt = state.Board.Revision, state.Board.UpdatedAt
		}
	}
	state.Board.Attention, state.Board.LastCardEditAt = deriveAttention(state)
	if err := app.store.commit(state, false, &accepted); err != nil {
		t.Fatal(err)
	}
	return state
}
