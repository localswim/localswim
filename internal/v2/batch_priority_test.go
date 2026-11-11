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
	delete(object, "expected_board_etag")
	if _, err := readPriorityBatchInput(object); err == nil {
		t.Fatal("missing precondition accepted")
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
