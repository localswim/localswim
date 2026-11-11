package v2

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSingleReviewApproval(t *testing.T) {
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

	raw, _ := human.call("POST", "/boards", `{"name":"Synthetic approval"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	raw, header := bot.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"title":"Review fixture","lane_id":%q,"owner_id":%q}`,
		b.ID, b.ETag, b.LaneIDs[0], botID), "", 201)
	c := turnOneDecode[card](t, raw)
	path := "/cards/" + c.ID
	approvalID := newID()
	body := func(boardTag, cardTag string) string {
		return fmt.Sprintf(`{"id":%q,"board_id":%q,"expected_board_etag":%q,"cards":[{"card_id":%q,"etag":%q}]}`,
			approvalID, b.ID, boardTag, c.ID, cardTag)
	}
	human.call("POST", "/approvals", body(header.Get("X-Board-ETag"), c.entityTag()), "", 409)

	// Ordinary moves set up the review card and make its original tag stale.
	for _, laneID := range b.LaneIDs[1:4] {
		raw, header = bot.call("PATCH", path, fmt.Sprintf(`{"lane_id":%q}`, laneID), c.entityTag(), 200)
		c = turnOneDecode[card](t, raw)
	}
	currentTag := header.Get("X-Board-ETag")
	before, _ := app.store.accepted(b.ID)
	good := body(currentTag, c.entityTag())
	bot.call("POST", "/approvals", good, "", 403)
	human.call("POST", "/approvals", body(b.ETag, c.entityTag()), "", 412)
	human.call("POST", "/approvals", body(currentTag, etag(c.ID, 1)), "", 412)
	human.call("POST", "/approvals", body("*", c.entityTag()), "", 400)
	human.call("POST", "/approvals", body(currentTag, "W/"+c.entityTag()), "", 400)
	human.call("POST", "/approvals", fmt.Sprintf(`{"board_id":%q,"cards":[]}`, b.ID), "", 428)
	human.call("POST", "/approvals", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"cards":[{"card_id":%q}]}`,
		b.ID, currentTag, c.ID), "", 428)
	human.call("POST", "/approvals", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"cards":[]}`, b.ID, currentTag), "", 400)
	human.call("POST", "/approvals", strings.Replace(good, `"cards":`, `"lane_id":null,"cards":`, 1), "", 400)
	human.call("POST", "/approvals", strings.Replace(good, `"card_id":`, fmt.Sprintf(`"author_id":%q,"card_id":`, botID), 1), "", 400)
	afterRefusals, _ := app.store.accepted(b.ID)
	if !reflect.DeepEqual(before, afterRefusals) {
		t.Fatal("a refused approval changed accepted state")
	}

	// Two submissions with identical preconditions can commit at most once.
	send := func(method, resource, payload, noneMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, Prefix+resource, strings.NewReader(payload))
		req.Host = server.Listener.Addr().String()
		req.Header.Set("Authorization", "Bearer "+humanToken)
		req.Header.Set("Content-Type", "application/json")
		if noneMatch != "" {
			req.Header.Set("If-None-Match", noneMatch)
		}
		response := httptest.NewRecorder()
		server.Config.Handler.ServeHTTP(response, req)
		return response
	}
	submitted := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { submitted <- send("POST", "/approvals", good, "") }()
	}
	created, refused := <-submitted, <-submitted
	if created.Code == 412 {
		created, refused = refused, created
	}
	if created.Code != 201 || refused.Code != 412 {
		t.Fatalf("concurrent approval statuses = %d, %d. Wanted 201 and 412.", created.Code, refused.Code)
	}
	result := turnOneDecode[approval](t, created.Body.Bytes())
	receiptFields := turnOneDecode[map[string]json.RawMessage](t, created.Body.Bytes())
	if _, present := receiptFields["board_etag"]; present {
		t.Fatal("approval receipt exposes board_etag")
	}
	if string(receiptFields["board_revision"]) != fmt.Sprint(result.BoardRevision) {
		t.Fatal("approval receipt board_revision is not a JSON integer")
	}
	for _, change := range result.Changes {
		if change.Revision != result.BoardRevision {
			t.Fatal("approval receipt revision disagrees with its changes")
		}
	}
	receiptPath := "/approvals/" + approvalID
	if result.ID != approvalID || result.ActorID != humanID || result.BoardID != b.ID ||
		!slices.Equal(result.CardIDs, []string{c.ID}) || result.BoardRevision != before.Board.Revision+1 ||
		etag(b.ID, result.BoardRevision) != created.Header().Get("X-Board-ETag") ||
		created.Header().Get("Location") != Prefix+receiptPath {
		t.Fatal("approval receipt did not identify its committed card and revision")
	}
	if len(result.Changes) != 2 || result.Changes[0].ID != approvalID ||
		result.Changes[0].Operation != approvalOperation ||
		snapshotString(result.Changes[0].Before) != c.LaneID ||
		snapshotString(result.Changes[0].After) != b.LaneIDs[4] {
		t.Fatal("approval receipt did not contain the approval lane change and comment")
	}
	committed, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	moved := committed.Cards[0]
	if committed.Board.Revision != result.BoardRevision || committed.Board.ETag != created.Header().Get("X-Board-ETag") ||
		!reflect.DeepEqual(moved.OwnerID, c.OwnerID) || moved.LaneID != b.LaneIDs[4] || moved.CompletedAt == nil ||
		!moved.CompletedAt.Equal(result.At) || moved.entityTag() == c.entityTag() || moved.CommentCount != c.CommentCount+1 ||
		committed.Board.Attention.ReadyForReview != 0 || committed.Board.LastCardEditAt == nil ||
		!committed.Board.LastCardEditAt.Equal(result.At) {
		t.Fatal("durable card, ownership, attention, or revision did not reflect approval")
	}
	if len(committed.Activity) != len(before.Activity)+2 || len(committed.Comments) != len(before.Comments)+1 {
		t.Fatal("expected one approval and its comment in one board revision")
	}
	note := committed.Comments[len(committed.Comments)-1]
	if note.Text != "Reviewed & approved, moving to Completed." ||
		!validApprovalPair(result.Changes[0], result.Changes[1], note) {
		t.Fatal("durable approval comment text or attribution changed")
	}
	for _, changedText := range []string{"Reviewed and approved, moving to Completed.", note.Text + " "} {
		tampered := cloneSnapshot(committed)
		tampered.Comments[len(tampered.Comments)-1].Text = changedText
		if err := validateSnapshot(tampered, b.ID); !errors.Is(err, errInvalidSnapshot) {
			t.Fatalf("altered approval comment passed verification: %v", err)
		}
	}

	for name, edit := range map[string]func(*snapshot){
		"relabeled approval":  func(s *snapshot) { s.Activity[len(s.Activity)-2].Operation = "patch" },
		"older card revision": func(s *snapshot) { s.Cards[0].Revision-- },
		"altered completion": func(s *snapshot) {
			at := s.Cards[0].CompletedAt.Add(-time.Second)
			s.Cards[0].CompletedAt = &at
		},
	} {
		tampered := cloneSnapshot(committed)
		edit(&tampered)
		if err := validateSnapshot(tampered, b.ID); !errors.Is(err, errInvalidSnapshot) {
			t.Errorf("%s passed native verification: %v", name, err)
		}
	}

	human.call("POST", "/approvals", body(committed.Board.ETag, moved.entityTag()), "", 409)

	// A policy-authorized reopening does not alter the historical approval receipt.
	p := committed.Policy
	p.MoveGrants = append(p.MoveGrants, moveGrant{humanID, moved.LaneID, c.LaneID})
	human.call("PUT", "/policies/"+p.ID, policyPutBody(t, p, committed.Board.ETag), p.ETag, 200)
	raw, _ = human.call("PATCH", path, fmt.Sprintf(`{"lane_id":%q}`, c.LaneID), moved.entityTag(), 200)
	reopened := turnOneDecode[card](t, raw)
	if reopened.CompletedAt != nil || reopened.LaneID != c.LaneID {
		t.Fatal("policy-authorized reopen did not return the card to review")
	}
	raw, headers := bot.call("GET", receiptPath, "", "", 200)
	if headers.Get("ETag") != etag(approvalID, 1) || !reflect.DeepEqual(turnOneDecode[approval](t, raw), result) {
		t.Fatal("receipt readback differs from the committed approval")
	}
	if response := send("HEAD", receiptPath, "", headers.Get("ETag")); response.Code != 304 || response.Body.Len() != 0 {
		t.Fatal("receipt conditional HEAD did not return an empty 304")
	}
}

func TestSingleReviewApprovalPolicyRefusals(t *testing.T) {
	now := canonicalTime(time.Now())
	id := newID()
	principals := []user{
		{entity: newEntity(humanID, now), Kind: "human", Label: "Human"},
		{entity: newEntity(botID, now), Kind: "bot", Label: "Bot"},
	}
	state := freshSnapshot(id, "Synthetic", defaultWorkflow(id, now, principals), humanID, now)
	item := card{ID: newID(), Revision: 1, CreatedAt: now, UpdatedAt: now, BoardID: id, LaneID: state.Lanes[3].ID, OwnerID: botID}
	state.Cards = append(state.Cards, item)
	in := approvalInput{id: newID(), boardID: id, boardTag: state.Board.ETag,
		cards: []approvalCardVersion{{item.ID, item.entityTag()}}}

	for _, tc := range []struct {
		name, actor string
		edit        func(*snapshot)
		want        error
	}{
		{"automation with forged grants", botID, func(s *snapshot) {
			s.Policy.ResourceGrants[1].Capabilities = append(s.Policy.ResourceGrants[1].Capabilities, "approve")
			s.Policy.MoveGrants = append(s.Policy.MoveGrants, moveGrant{botID, s.Lanes[3].ID, s.Lanes[4].ID})
		}, errForbidden},
		{"no approve capability", humanID, func(s *snapshot) {
			s.Policy.ResourceGrants[0].Capabilities = []string{"comment"}
		}, errForbidden},
		{"no move grant", humanID, func(s *snapshot) { s.Policy.MoveGrants = nil }, errForbidden},
		{"archived card", humanID, func(s *snapshot) { s.Cards[0].Archived = true }, errApprovalState},
		{"retired review lane", humanID, func(s *snapshot) { s.Lanes[3].Retired = true }, errApprovalState},
		{"closed board", humanID, func(s *snapshot) { s.Board.Availability = "closed" }, errApprovalState},
		{"identity collision", humanID, func(s *snapshot) { s.Activity[0].ID = in.id }, errIdentityConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := cloneSnapshot(state)
			tc.edit(&candidate)
			before := cloneSnapshot(candidate)
			if err := in.apply(&candidate, tc.actor); !errors.Is(err, tc.want) {
				t.Fatalf("approval returned %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(before, candidate) {
				t.Fatal("refusal mutated its candidate")
			}
		})
	}

	// A composite approval includes the fixed note even without the ordinary comment grant.
	state.Policy.ResourceGrants[0].Capabilities = []string{"approve"}
	state.Lanes[3].Label, state.Lanes[4].Label = "Owner review", "Done"
	if err := in.apply(&state, humanID); err != nil || len(state.Comments) != 1 ||
		state.Comments[0].Text != "Reviewed & approved, moving to Completed." {
		t.Fatal("approval did not use workflow roles and the composite grant", err)
	}
}
