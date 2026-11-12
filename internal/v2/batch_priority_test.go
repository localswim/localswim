package v2

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPriorityBatchAtomicFloorsAndNoop(t *testing.T) {
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
	raw, _ := client.call("POST", "/boards", `{"name":"Batch priorities"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	high, low := b.PriorityIDs[0], b.PriorityIDs[2]
	boardTag := b.entityTag()
	create := func(title string, target *card) card {
		t.Helper()
		extra := ""
		if target != nil {
			extra = fmt.Sprintf(`,"relationships":[{"target_card_id":%q,"kind":"blocks"}]`, target.ID)
		}
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q,"priority_id":%q%s}`,
			b.ID, boardTag, b.LaneIDs[0], title, high, extra)
		raw, headers := client.call("POST", "/cards", body, "", 201)
		boardTag = headers.Get("X-Board-ETag")
		return turnOneDecode[card](t, raw)
	}
	downstream := create("Downstream", nil)
	middle := create("Middle", &downstream)
	upstream := create("Upstream", &middle)
	_, headers := client.call("PATCH", "/cards/"+upstream.ID, `{"archived":true}`, upstream.entityTag(), 200)
	boardTag = headers.Get("X-Board-ETag")
	batch := func(ids []string, priority, tag string, status int) (priorityBatchResult, string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"board_id": b.ID, "expected_board_etag": tag,
			"card_ids": ids, "priority_id": priority})
		if err != nil {
			t.Fatal(err)
		}
		raw, headers := client.call("PATCH", "/cards", string(body), "", status)
		if status != 200 {
			return priorityBatchResult{}, ""
		}
		return turnOneDecode[priorityBatchResult](t, raw), headers.Get("X-Board-ETag")
	}
	before, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, tag := batch([]string{middle.ID, downstream.ID}, low, boardTag, 200)
	if tag == boardTag || result.BoardRevision != before.Board.Revision+1 || result.BoardID != b.ID ||
		len(result.Cards) != 2 || result.Cards[0].ID != middle.ID || result.Cards[1].ID != downstream.ID ||
		result.Cards[0].PriorityID != low || result.Cards[1].PriorityID != low || len(result.Changes) != 2 {
		t.Fatal("simultaneous selected demotions did not commit together")
	}
	for _, change := range result.Changes {
		if change.Operation != "patch" || change.Revision != result.BoardRevision || change.ActorID != humanID ||
			snapshotString(change.Field) != "priority_id" || snapshotString(change.Before) != high || snapshotString(change.After) != low {
			t.Fatal("batch priority change audit did not identify the shared commit")
		}
	}
	boardTag = tag
	batch([]string{middle.ID}, high, before.Board.entityTag(), 412)
	batch([]string{middle.ID, strings.ToUpper(middle.ID)}, high, boardTag, 400)
	batch([]string{middle.ID, newID()}, high, boardTag, 404)
	unchanged, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Board.entityTag() != boardTag || len(unchanged.Activity) != len(before.Activity)+2 {
		t.Fatal("refused batch changed durable state")
	}
	result, tag = batch([]string{middle.ID, downstream.ID}, low, boardTag, 200)
	if tag != boardTag || result.BoardRevision != unchanged.Board.Revision || len(result.Changes) != 0 {
		t.Fatal("no-op batch changed revision or audit")
	}
	result, boardTag = batch([]string{upstream.ID}, low, boardTag, 200)
	if len(result.Changes) != 1 || !result.Cards[0].Archived || result.Cards[0].PriorityID != low {
		t.Fatal("archived unfinished selection did not accept its effective demotion")
	}
	result, tag = batch([]string{downstream.ID}, high, boardTag, 200)
	if tag == boardTag || len(result.Changes) != 3 || result.Changes[0].ResourceID != downstream.ID ||
		result.Changes[0].Operation != "patch" || result.Changes[1].ResourceID != middle.ID || result.Changes[1].Operation != "promote" || result.Changes[2].ResourceID != upstream.ID || result.Changes[2].Operation != "promote" {
		t.Fatal("downstream batch did not report selected edit and cascade")
	}
	saved, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSnapshot(saved, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, item := range saved.Cards {
		if item.PriorityID != high {
			t.Fatal("blocking priority floor was not preserved")
		}
	}
	result, noopTag := batch([]string{middle.ID}, low, tag, 200)
	if noopTag != tag || result.Cards[0].PriorityID != high || len(result.Changes) != 0 {
		t.Fatal("external floor did not clamp the selected card without an audit")
	}
	if !relationshipCards(&saved)[upstream.ID].Archived {
		t.Fatal("batch changed archived blocker state")
	}

	// A mixed selection is rejected before applying any override.
	now := time.Now()
	saved.Cards[1].CompletedAt = &now
	original := cloneSnapshot(saved)
	in := priorityBatchInput{cardIDs: []string{saved.Cards[0].ID, saved.Cards[1].ID}, priorityID: low}
	if err := in.apply(&saved, humanID); !errors.Is(err, errPriorityBatchCompleted) {
		t.Fatalf("Completed selection returned %v", err)
	}
	if !reflect.DeepEqual(saved, original) {
		t.Fatal("Completed selection changed part of the batch")
	}
}

func TestPriorityBatchInputValidation(t *testing.T) {
	boardID, cardID, priorityID := newID(), newID(), newID()
	valid := map[string]any{"board_id": boardID, "expected_board_etag": etag(boardID, 1),
		"card_ids": []string{cardID}, "priority_id": priorityID}
	for _, test := range []struct {
		name, field string
		value       any
	}{
		{"empty", "card_ids", []string{}},
		{"null", "card_ids", nil},
		{"not array", "card_ids", cardID},
		{"invalid member", "card_ids", []any{true}},
		{"duplicate", "card_ids", []string{cardID, strings.ToUpper(cardID)}},
		{"too many", "card_ids", make([]string, 1001)},
		{"weak tag", "expected_board_etag", "W/" + etag(boardID, 1)},
		{"protected", "actor_id", humanID},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := make(map[string]any, len(valid)+1)
			for key, value := range valid {
				fields[key] = value
			}
			fields[test.field] = test.value
			raw, _ := json.Marshal(fields)
			object, err := decodeObject(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readPriorityBatchInput(object); err == nil {
				t.Fatal("invalid batch input accepted")
			}
		})
	}
	raw, _ := json.Marshal(valid)
	object, err := decodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	delete(object, "card_ids")
	if _, err := readPriorityBatchInput(object); err == nil {
		t.Fatal("missing selection accepted")
	}
	object["all_unfinished"] = json.RawMessage("true")
	if in, err := readPriorityBatchInput(object); err != nil || !in.allUnfinished || in.cardIDs != nil {
		t.Fatalf("all_unfinished did not select all: %v", err)
	}
	delete(object, "expected_board_etag")
	if _, err := readPriorityBatchInput(object); err == nil {
		t.Fatal("missing precondition accepted")
	}
}

func TestPriorityBatchAllUnfinishedValidation(t *testing.T) {
	base := fmt.Sprintf(`"board_id":%q,"expected_board_etag":%q,"priority_id":%q`, newID(), etag(newID(), 1), newID())
	for _, test := range []struct {
		name, selection string
	}{
		{"missing", ""},
		{"misspelled cards", `,"cards_ids":["ignored"]`},
		{"both", fmt.Sprintf(`,"card_ids":[%q],"all_unfinished":true`, newID())},
		{"null cards with all", `,"card_ids":null,"all_unfinished":true`},
		{"false", `,"all_unfinished":false`},
		{"null", `,"all_unfinished":null`},
		{"string", `,"all_unfinished":"true"`},
		{"number", `,"all_unfinished":1`},
	} {
		t.Run(test.name, func(t *testing.T) {
			object, err := decodeObject([]byte("{" + base + test.selection + "}"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readPriorityBatchInput(object); err == nil {
				t.Fatal("invalid selection accepted")
			}
		})
	}
}

func TestPriorityBatchAllUnfinished(t *testing.T) {
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
	raw, _ := client.call("POST", "/boards", `{"name":"All unfinished priorities"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	boardTag := b.entityTag()
	high, low := b.PriorityIDs[0], b.PriorityIDs[2]
	batch := func(priority, tag string, status int) (priorityBatchResult, string) {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"priority_id":%q,"all_unfinished":true}`, b.ID, tag, priority)
		raw, headers := client.call("PATCH", "/cards", body, "", status)
		if status != 200 {
			return priorityBatchResult{}, ""
		}
		return turnOneDecode[priorityBatchResult](t, raw), headers.Get("X-Board-ETag")
	}
	assertEmpty := func() {
		t.Helper()
		result, tag := batch(low, boardTag, 200)
		if tag != boardTag || result.Cards == nil || len(result.Cards) != 0 || result.Changes == nil || len(result.Changes) != 0 {
			t.Fatal("empty all-unfinished selection must preserve revision and return empty arrays")
		}
		batch(newID(), boardTag, 400)
	}
	assertEmpty()
	create := func(lane, title string, target *card) card {
		t.Helper()
		extra := ""
		if target != nil {
			extra = fmt.Sprintf(`,"relationships":[{"target_card_id":%q,"kind":"blocks"}]`, target.ID)
		}
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q,"priority_id":%q%s}`,
			b.ID, boardTag, lane, title, high, extra)
		raw, headers := client.call("POST", "/cards", body, "", 201)
		boardTag = headers.Get("X-Board-ETag")
		return turnOneDecode[card](t, raw)
	}
	completed := create(b.LaneIDs[0], "Completed", nil)
	for _, laneID := range []string{b.LaneIDs[2], b.LaneIDs[3], b.LaneIDs[4]} {
		body := fmt.Sprintf(`{"lane_id":%q}`, laneID)
		raw, headers := client.call("PATCH", "/cards/"+completed.ID, body, completed.entityTag(), 200)
		completed = turnOneDecode[card](t, raw)
		boardTag = headers.Get("X-Board-ETag")
	}
	assertEmpty()
	downstream := create(b.LaneIDs[0], "Downstream", nil)
	upstream := create(b.LaneIDs[0], "Archived blocker", &downstream)
	_, headers := client.call("PATCH", "/cards/"+upstream.ID, `{"archived":true}`, upstream.entityTag(), 200)
	boardTag = headers.Get("X-Board-ETag")
	before, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch(low, b.entityTag(), 412)
	result, tag := batch(low, boardTag, 200)
	if tag == boardTag || result.BoardRevision != before.Board.Revision+1 || len(result.Cards) != 2 || len(result.Changes) != 2 {
		t.Fatal("all-unfinished selection did not commit all unfinished cards together")
	}
	for _, item := range result.Cards {
		if item.ID == completed.ID || item.PriorityID != low || (item.ID == upstream.ID && !item.Archived) {
			t.Fatal("all-unfinished selection did not preserve completion, archive, or simultaneous floor semantics")
		}
	}
	saved, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*relationshipCards(&saved)[completed.ID], completed) {
		t.Fatal("all-unfinished selection mutated a Completed card")
	}
	for _, change := range result.Changes {
		if change.Revision != result.BoardRevision || change.Operation != "patch" || change.ActorID != humanID {
			t.Fatal("all-unfinished selection audit did not share the batch commit")
		}
	}
	result, noopTag := batch(low, tag, 200)
	if noopTag != tag || len(result.Cards) != 2 || len(result.Changes) != 0 {
		t.Fatal("unchanged all-unfinished selection changed revision or audit")
	}
	if err := validateSnapshot(saved, b.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPriorityBatchAllUnfinishedExceedsExplicitLimit(t *testing.T) {
	now := canonicalTime(time.Now())
	state := snapshot{
		Board:  board{UserIDs: []string{"actor"}, PriorityIDs: []string{"high", "low"}},
		Policy: policy{ResourceGrants: []resourceGrant{{UserID: "actor", Capabilities: []string{"set_priority"}}}},
		Priorities: []priority{
			{entity: newEntity("high", now), Rank: 0},
			{entity: newEntity("low", now), Rank: 1},
		},
	}
	for i := range 1001 {
		state.Cards = append(state.Cards, card{ID: fmt.Sprintf("card-%d", i), PriorityID: "high"})
	}
	in := priorityBatchInput{priorityID: "low", allUnfinished: true}
	if err := in.apply(&state, "actor"); err != nil {
		t.Fatal(err)
	}
	if len(in.cardIDs) != 1001 || len(state.Activity) != 1001 {
		t.Fatal("all-unfinished selection was limited or failed to audit every card")
	}
	for _, item := range state.Cards {
		if item.PriorityID != "low" {
			t.Fatal("all-unfinished selection did not update every card")
		}
	}
}

func TestPriorityBatchNoopPreservesUnrelatedPreFloorGraph(t *testing.T) {
	now := time.Now()
	state := snapshot{
		Board:  board{UserIDs: []string{"actor"}, PriorityIDs: []string{"high", "low"}},
		Policy: policy{ResourceGrants: []resourceGrant{{UserID: "actor", Capabilities: []string{"set_priority"}}}},
		Priorities: []priority{
			{entity: newEntity("high", now), Rank: 0},
			{entity: newEntity("low", now), Rank: 1},
		},
		Cards: []card{
			{ID: "unchanged", PriorityID: "low"},
			{ID: "clamped", PriorityID: "high"},
			{ID: "floor", PriorityID: "high"},
			{ID: "unrelated-blocker", PriorityID: "low"},
			{ID: "unrelated-target", PriorityID: "high"},
		},
		Relationships: []relationship{
			{relationshipLink: relationshipLink{SourceCardID: "clamped", TargetCardID: "floor", Kind: "blocks"}},
			{relationshipLink: relationshipLink{SourceCardID: "unrelated-blocker", TargetCardID: "unrelated-target", Kind: "blocks"}},
		},
	}
	before := cloneSnapshot(state)
	in := priorityBatchInput{cardIDs: []string{"unchanged", "clamped"}, priorityID: "low"}
	if err := in.apply(&state, "actor"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("no-op batch changed the existing graph, card versions, or audit")
	}
}

func TestPriorityBatchLaneInputValidation(t *testing.T) {
	laneID := newID()
	base := fmt.Sprintf(`"board_id":%q,"expected_board_etag":%q,"priority_id":%q`, newID(), etag(newID(), 1), newID())
	for _, test := range []struct {
		name, selection string
	}{
		{"all and lane", fmt.Sprintf(`,"all_unfinished":true,"lane_id":%q`, laneID)},
		{"explicit cards", fmt.Sprintf(`,"card_ids":[%q],"lane_id":%q`, newID(), laneID)},
		{"explicit cards and all", fmt.Sprintf(`,"card_ids":[%q],"all_unfinished":true,"lane_id":%q`, newID(), laneID)},
		{"false all", fmt.Sprintf(`,"all_unfinished":false,"lane_id":%q`, laneID)},
		{"null lane", `,"lane_id":null`},
		{"empty lane", `,"lane_id":""`},
		{"malformed lane", `,"lane_id":"not-a-uuid"`},
		{"nonstring lane", `,"lane_id":42`},
	} {
		t.Run(test.name, func(t *testing.T) {
			object, err := decodeObject([]byte("{" + base + test.selection + "}"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readPriorityBatchInput(object); err == nil {
				t.Fatal("invalid lane selection accepted")
			}
		})
	}
	object, err := decodeObject([]byte("{" + base + fmt.Sprintf(`,"lane_id":%q}`, strings.ToUpper(laneID))))
	if err != nil {
		t.Fatal(err)
	}
	in, err := readPriorityBatchInput(object)
	if err != nil || in.laneID != laneID || in.allUnfinished {
		t.Fatalf("lane identity was not canonicalized: %v", err)
	}
}

func TestPriorityBatchLaneUnfinished(t *testing.T) {
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
	raw, _ := client.call("POST", "/boards", `{"name":"Lane priorities"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	boardTag := b.entityTag()
	high, low := b.PriorityIDs[0], b.PriorityIDs[2]
	create := func(laneID, title string) card {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q,"priority_id":%q}`,
			b.ID, boardTag, laneID, title, high)
		raw, headers := client.call("POST", "/cards", body, "", 201)
		boardTag = headers.Get("X-Board-ETag")
		return turnOneDecode[card](t, raw)
	}
	completed := create(b.LaneIDs[0], "Completed")
	for _, laneID := range []string{b.LaneIDs[2], b.LaneIDs[3], b.LaneIDs[4]} {
		raw, headers := client.call("PATCH", "/cards/"+completed.ID, fmt.Sprintf(`{"lane_id":%q}`, laneID), completed.entityTag(), 200)
		completed = turnOneDecode[card](t, raw)
		boardTag = headers.Get("X-Board-ETag")
	}
	active := create(b.LaneIDs[0], "Selected active")
	archived := create(b.LaneIDs[0], "Selected archived")
	_, headers := client.call("PATCH", "/cards/"+archived.ID, `{"archived":true}`, archived.entityTag(), 200)
	boardTag = headers.Get("X-Board-ETag")
	other := create(b.LaneIDs[1], "Other lane")
	batch := func(laneID, tag string, status int) (priorityBatchResult, string) {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"priority_id":%q,"lane_id":%q}`,
			b.ID, tag, low, laneID)
		raw, headers := client.call("PATCH", "/cards", body, "", status)
		if status != 200 {
			return priorityBatchResult{}, ""
		}
		return turnOneDecode[priorityBatchResult](t, raw), headers.Get("X-Board-ETag")
	}
	before, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = client.call("POST", "/boards", `{"name":"Foreign lane"}`, "", 201)
	foreign := turnOneDecode[board](t, raw)
	batch(foreign.LaneIDs[0], boardTag, 400)
	batch(newID(), boardTag, 400)
	batch("malformed", boardTag, 400)
	batch(b.LaneIDs[0], b.entityTag(), 412)
	unchanged, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, unchanged) {
		t.Fatal("invalid or stale lane batch changed durable state")
	}
	for _, laneID := range []string{b.LaneIDs[2], b.LaneIDs[4]} {
		result, tag := batch(laneID, boardTag, 200)
		if tag != boardTag || result.Cards == nil || len(result.Cards) != 0 || result.Changes == nil || len(result.Changes) != 0 {
			t.Fatal("empty or Completed lane changed revision or returned selected cards")
		}
	}
	result, tag := batch(strings.ToUpper(b.LaneIDs[0]), boardTag, 200)
	if tag == boardTag || result.BoardRevision != before.Board.Revision+1 || len(result.Cards) != 2 || len(result.Changes) != 2 {
		t.Fatal("lane selection did not commit both unfinished cards together")
	}
	selected := map[string]bool{active.ID: false, archived.ID: true}
	for _, item := range result.Cards {
		isArchived, exists := selected[item.ID]
		if !exists || item.PriorityID != low || item.Archived != isArchived {
			t.Fatal("lane selection changed the wrong card or archive state")
		}
	}
	for _, change := range result.Changes {
		if _, exists := selected[change.ResourceID]; !exists || change.Operation != "patch" || change.Revision != result.BoardRevision ||
			change.ActorID != humanID || snapshotString(change.Field) != "priority_id" || snapshotString(change.Before) != high || snapshotString(change.After) != low {
			t.Fatal("lane batch audit did not record the selected priority edit and shared revision")
		}
	}
	saved, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	cards := relationshipCards(&saved)
	if !reflect.DeepEqual(*cards[other.ID], other) || !reflect.DeepEqual(*cards[completed.ID], completed) {
		t.Fatal("lane batch changed other-lane or Completed cards")
	}
	if err := validateSnapshot(saved, b.ID); err != nil {
		t.Fatal(err)
	}
	result, noopTag := batch(b.LaneIDs[0], tag, 200)
	if noopTag != tag || len(result.Cards) != 2 || len(result.Changes) != 0 {
		t.Fatal("unchanged lane selection changed revision or audit")
	}
	batch(b.LaneIDs[2], boardTag, 412)
	afterNoops, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, afterNoops) {
		t.Fatal("no-op or stale empty-lane selection changed durable state")
	}
}

func TestPriorityBatchRetiredLaneIncludesArchivedUnfinished(t *testing.T) {
	now := canonicalTime(time.Now())
	state := snapshot{
		Board:  board{UserIDs: []string{"actor"}, PriorityIDs: []string{"high", "low"}, LaneIDs: []string{"retired", "other"}},
		Policy: policy{ResourceGrants: []resourceGrant{{UserID: "actor", Capabilities: []string{"set_priority"}}}},
		Lanes:  []lane{{ID: "retired", Retired: true}, {ID: "other"}},
		Priorities: []priority{
			{entity: newEntity("high", now), Rank: 0},
			{entity: newEntity("low", now), Rank: 1},
		},
		Cards: []card{
			{ID: "archived", LaneID: "retired", Archived: true, PriorityID: "high"},
			{ID: "completed", LaneID: "retired", CompletedAt: &now, PriorityID: "high"},
			{ID: "other", LaneID: "other", PriorityID: "high"},
		},
	}
	in := priorityBatchInput{priorityID: "low", laneID: "retired"}
	if err := in.apply(&state, "actor"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.cardIDs, []string{"archived"}) || state.Cards[0].PriorityID != "low" || !state.Cards[0].Archived ||
		state.Cards[1].PriorityID != "high" || state.Cards[2].PriorityID != "high" || len(state.Activity) != 1 {
		t.Fatal("retired lane did not preserve archived unfinished eligibility and selection scope")
	}
}
