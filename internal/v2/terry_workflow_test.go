package v2

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestTerryWorkflowPreset(t *testing.T) {
	app, human, bot := approvalTestClients(t)
	raw, headers := human.call("POST", "/boards", `{"name":"Terry workflow","preset":"terry"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	boardTag := headers.Get("ETag")
	initial, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Revision != 1 || b.DefaultOwnerID != botID || b.ActorPresentation.Human.Label != "Terry" {
		t.Fatalf("incorrect preset board settings: %+v", b)
	}
	lanes := []struct {
		key, label, role     string
		create, order, bulk  bool
		humanMoves, botMoves string
	}{
		{"backlog", "Backlog", "ordinary", true, true, false,
			"ready_for_work in_progress needs_terry_action blocked", "ready_for_work needs_terry_action blocked"},
		{"ready_for_work", "Ready For Work", "ordinary", true, false, false,
			"backlog in_progress needs_terry_action", "backlog in_progress blocked needs_terry_action"},
		{"in_progress", "In Progress", "ordinary", false, false, false,
			"ready_for_work blocked needs_terry_action ready_for_review", "backlog ready_for_work blocked needs_terry_action ready_for_review"},
		{"blocked", "Blocked", "blocked", true, false, false,
			"ready_for_work in_progress needs_terry_action", "backlog ready_for_work in_progress needs_terry_action ready_for_review"},
		{"needs_terry_action", "Needs Terry", "needs_input", true, false, false,
			"backlog ready_for_work in_progress ready_for_review", "backlog ready_for_work in_progress blocked ready_for_review"},
		{"ready_for_review", "Ready For Review", "ready_for_review", false, false, true,
			"backlog ready_for_work in_progress completed", "backlog ready_for_work in_progress blocked needs_terry_action"},
		{"completed", "Completed", "completed", false, false, false, "", ""},
	}
	if len(initial.Lanes) != len(lanes) {
		t.Fatalf("got %d preset lanes, want %d", len(initial.Lanes), len(lanes))
	}
	if len(initial.Policy.CreateGrants) != 8 || len(initial.Policy.MoveGrants) != 49 ||
		!initial.can(humanID, "approve") || initial.can(botID, "approve") {
		t.Fatal("preset policy has incorrect grant counts or approval authority")
	}
	for i, want := range lanes {
		got := initial.Lanes[i]
		if got.Key != want.key || got.Label != want.label || got.Role != want.role || got.Position != i || got.Revision != 1 ||
			got.ReorderEnabled != want.order || got.ApprovalEnabled != want.bulk || got.BulkApprovalComment != "" {
			t.Fatalf("incorrect preset lane at position %d: %+v", i, got)
		}
		for _, actor := range []struct{ id, moves string }{{humanID, want.humanMoves}, {botID, want.botMoves}} {
			if slices.Contains(initial.Policy.CreateGrants, createGrant{UserID: actor.id, LaneID: got.ID}) != want.create {
				t.Fatalf("incorrect creation grant for %s in %s", actor.id, want.key)
			}
			for _, target := range initial.Lanes {
				granted := slices.Contains(initial.Policy.MoveGrants, moveGrant{UserID: actor.id, FromLaneID: got.ID, ToLaneID: target.ID})
				if granted != slices.Contains(strings.Fields(actor.moves), target.Key) {
					t.Fatalf("incorrect move grant for %s from %s to %s", actor.id, want.key, target.Key)
				}
			}
		}
	}
	labels := []string{"On fire", "Urgent", "High", "Normal", "Low", "Only if idle"}
	if len(initial.Priorities) != len(labels) {
		t.Fatalf("got %d preset priorities, want %d", len(initial.Priorities), len(labels))
	}
	for rank, p := range initial.Priorities {
		if p.Key != fmt.Sprintf("P%d", rank) || p.Label != labels[rank] || p.Rank != rank {
			t.Fatalf("incorrect preset priority at rank %d: %+v", rank, p)
		}
	}
	if initial.Policy.DefaultPriorityID != initial.Priorities[3].ID {
		t.Fatal("preset default priority is not P3")
	}

	backlog, inProgress, blocked, needsTerry, review, completed := b.LaneIDs[0], b.LaneIDs[2], b.LaneIDs[3], b.LaneIDs[4], b.LaneIDs[5], b.LaneIDs[6]
	create := func(client turnOneClient, laneID, title string, status int) (card, string) {
		t.Helper()
		raw, headers := client.call("POST", "/cards", fmt.Sprintf(
			`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q}`, b.ID, boardTag, laneID, title), "", status)
		if status != 201 {
			return card{}, ""
		}
		boardTag = headers.Get("X-Board-ETag")
		return turnOneDecode[card](t, raw), headers.Get("ETag")
	}
	move := func(client turnOneClient, c card, tag, to string, status int) string {
		t.Helper()
		_, headers := client.call("PATCH", "/cards/"+c.ID, fmt.Sprintf(`{"lane_id":%q}`, to), tag, status)
		if status == 200 {
			boardTag = headers.Get("X-Board-ETag")
		}
		return headers.Get("ETag")
	}
	create(human, inProgress, "Refused Human creation", 403)
	create(bot, review, "Refused Bot creation", 403)
	first, firstTag := create(bot, backlog, "Ordinary Bot card", 201)
	if first.OwnerID != botID || first.PriorityID != initial.Priorities[3].ID || first.TriageScore != 400 {
		t.Fatalf("incorrect Bot card defaults: %+v", first)
	}
	move(bot, first, firstTag, inProgress, 403)
	firstTag = move(human, first, firstTag, inProgress, 200)
	firstTag = move(bot, first, firstTag, review, 200)
	move(bot, first, firstTag, completed, 403)
	create(human, blocked, "Human may create Blocked work", 201)
	second, secondTag := create(bot, needsTerry, "Bot may create Needs Terry work", 201)
	secondTag = move(bot, second, secondTag, review, 200)

	request := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"cards":[{"card_id":%q,"etag":%q},{"card_id":%q,"etag":%q}]}`,
		b.ID, boardTag, review, first.ID, firstTag, second.ID, secondTag)
	bot.call("POST", "/approvals", request, "", 403)
	raw, _ = human.call("POST", "/approvals", request, "", 201)
	receipt := turnOneDecode[approval](t, raw)
	if !slices.Equal(receipt.CardIDs, []string{first.ID, second.ID}) || receipt.ActorID != humanID || len(receipt.Changes) != 4 {
		t.Fatalf("incorrect preset approval receipt: %+v", receipt)
	}
	durable, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable.Comments) != 2 || durable.Comments[0].Text != reviewApprovalText || durable.Comments[1].Text != reviewApprovalText {
		t.Fatal("preset bulk approval did not save both default approval comments")
	}
}

func TestTerryWorkflowInitialLaneAudit(t *testing.T) {
	app, human, _ := approvalTestClients(t)
	raw, headers := human.call("POST", "/boards", `{"name":"Preset settings","preset":"terry"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	original, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Activity) != 3 {
		t.Fatalf("got %d initial events, want board creation and two lane settings", len(original.Activity))
	}
	for _, field := range []string{"reorder_enabled", "approval_enabled"} {
		bad := cloneSnapshot(original)
		bad.Activity = slices.DeleteFunc(bad.Activity, func(event activity) bool {
			return snapshotString(event.Field) == field
		})
		if err := validateSnapshot(bad, b.ID); err == nil {
			t.Fatalf("accepted a missing initial %s audit", field)
		}
	}
	bad := cloneSnapshot(original)
	bad.Activity[2].ResourceID = b.LaneIDs[0]
	if err := validateSnapshot(bad, b.ID); err == nil {
		t.Fatal("accepted initial approval on an ordinary lane")
	}
	bad = cloneSnapshot(original)
	duplicate := bad.Activity[1]
	duplicate.ID = newOperationID()
	bad.Activity = append(bad.Activity, duplicate)
	if err := validateSnapshot(bad, b.ID); err == nil {
		t.Fatal("accepted a duplicated initial lane setting")
	}

	boardTag := headers.Get("ETag")
	for _, item := range []struct {
		laneID string
		field  string
	}{{b.LaneIDs[0], "reorder_enabled"}, {b.LaneIDs[5], "approval_enabled"}} {
		_, headers = human.call("PATCH", "/lanes/"+item.laneID, fmt.Sprintf(`{"board_etag":%q,%q:false}`,
			boardTag, item.field), etag(item.laneID, 1), 200)
		boardTag = headers.Get("X-Board-ETag")
	}
	edited, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Lanes[0].ReorderEnabled || edited.Lanes[5].ApprovalEnabled {
		t.Fatal("settings did not replay from the initial enabled values")
	}
}

func TestTerryWorkflowSelection(t *testing.T) {
	app, human, _ := approvalTestClients(t)
	for _, input := range []string{
		`{"name":"Unsupported","preset":"other"}`,
		`{"name":"Unsupported","preset":"Terry"}`,
		`{"name":"Wrong type","preset":null}`,
		`{"name":"Conflicting","preset":"terry","workflow":{}}`,
	} {
		raw, _ := human.call("POST", "/boards", input, "", 400)
		problem := turnOneDecode[problemBody](t, raw)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "preset" {
			t.Fatalf("unexpected preset error: %+v", problem)
		}
	}
	raw, _ := human.call("GET", "/boards", "", "", 200)
	if page := turnOnePage[boardSummary](t, raw, "boards"); len(page.Values) != 0 {
		t.Fatal("invalid preset request created a board")
	}

	raw, _ = human.call("POST", "/boards", `{"name":"Generic"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	state, err := readSnapshot(app.store.snapshotPath(b.ID), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Lanes) != 5 || len(state.Priorities) != 3 || state.Priorities[1].Key != "P2" ||
		state.Priorities[1].Label != "Normal" || state.Policy.DefaultPriorityID != state.Priorities[1].ID ||
		slices.ContainsFunc(state.Lanes, func(l lane) bool { return l.ReorderEnabled || l.ApprovalEnabled }) ||
		b.ActorPresentation != (actorPresentation{}) || len(state.Activity) != 1 {
		t.Fatal("the generic workflow changed")
	}
}

func TestTerryWorkflowPriorityRanks(t *testing.T) {
	_, human, _ := approvalTestClients(t)
	// Custom numeric ranks deliberately reverse the P-numbers and declaration order.
	custom := fmt.Sprintf(`{
		"name":"Custom ranks",
		"default_owner_id":%[1]q,
		"workflow":{
			"users":[{"id":%[1]q,"label":"Human","kind":"human"}],
			"lanes":[{"id":%[2]q,"label":"To Do","create_for":[%[1]q]},{"id":%[3]q,"label":"Done","role":"completed"}],
			"priorities":[{"id":%[4]q,"key":"P1","label":"First key","rank":7},{"id":%[5]q,"key":"P9","label":"Second key","rank":2}],
			"policy":{
				"default_priority_id":%[4]q,
				"create_grants":[],
				"move_grants":[{"user_id":%[1]q,"from_lane_id":%[2]q,"to_lane_id":%[3]q}],
				"resource_grants":[{"user_id":%[1]q,"capabilities":["read","create_card"]}]
			}
		}
	}`, humanID, "a0000000-0000-4000-8000-000000000003", "a0000000-0000-4000-8000-000000000004",
		"a0000000-0000-4000-8000-000000000005", "a0000000-0000-4000-8000-000000000006")
	for _, test := range []struct {
		name, input string
		low, high   int
		wantScores  []int
	}{
		{"preset", `{"name":"Preset ranks","preset":"terry"}`, 5, 0, []int{100, 600}},
		{"custom", custom, 0, 1, []int{300, 800}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, headers := human.call("POST", "/boards", test.input, "", 201)
			b := turnOneDecode[board](t, raw)
			boardTag := headers.Get("ETag")
			for i, index := range []int{test.low, test.high} {
				_, headers = human.call("POST", "/cards", fmt.Sprintf(
					`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"priority_id":%q,"title":"Card %d"}`,
					b.ID, boardTag, b.LaneIDs[0], b.PriorityIDs[index], i+1), "", 201)
				boardTag = headers.Get("X-Board-ETag")
			}
			raw, _ = human.call("GET", "/boards/"+b.ID+"?view=lanes", "", "", 200)
			visible := turnOneDecode[boardLanesView](t, raw)
			raw, _ = human.call("GET", "/cards?board_id="+b.ID+"&sort=triage", "", "", 200)
			ranked := turnOnePage[card](t, raw, "cards").Values
			if !slices.Equal(laneTickets(visible.Cards), []uint64{2, 1}) || !slices.Equal(laneTickets(ranked), []uint64{2, 1}) ||
				ranked[0].TriageScore != test.wantScores[0] || ranked[1].TriageScore != test.wantScores[1] {
				t.Fatalf("incorrect lane or triage priority order: lane %v, triage %+v", laneTickets(visible.Cards), ranked)
			}
		})
	}
}
