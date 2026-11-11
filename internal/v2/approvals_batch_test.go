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

func TestLaneReviewApproval(t *testing.T) {
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
			!validApprovalPair(pair[0], pair[1], committed.Comments[index]) {
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
}
