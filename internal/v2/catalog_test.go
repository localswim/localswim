package v2

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestDeriveAttentionCountsAndAuditedRecency(t *testing.T) {
	newest := time.Date(2026, 11, 10, 18, 30, 0, 0, time.FixedZone("offset", 3600))
	older := newest.Add(-time.Second)
	state := snapshot{
		Board: board{entity: entity{UpdatedAt: newest.Add(time.Hour)}},
		Lanes: []lane{
			{ID: "a", Role: "blocked"},
			{ID: "b", Role: "needs_input"},
			{ID: "c", Role: "ready_for_review"},
			{ID: "d", Role: "ordinary"},
		},
		Cards: []card{
			{ID: "archived", LaneID: "a", Archived: true},
			{LaneID: "a"},
			{LaneID: "b"},
			{LaneID: "b"},
			{LaneID: "c"},
			{LaneID: "d"},
		},
		Activity: []activity{
			{ResourceType: "board", At: newest.Add(time.Hour)},
			{ResourceType: "card", ResourceID: "archived", At: newest},
			{ResourceType: "card", At: older},
			{ResourceType: "lane", At: newest.Add(time.Minute)},
		},
	}
	counts, latest := deriveAttention(state)
	if want := (attention{Blocked: 1, NeedsInput: 2, ReadyForReview: 1}); counts != want {
		t.Errorf("attention = %+v, want %+v", counts, want)
	}
	if latest == nil || *latest != newest.UTC() {
		t.Errorf("latest edit = %v, want %v", latest, newest.UTC())
	}
}

func TestDeriveAttentionCountsCardOwnedAudit(t *testing.T) {
	for _, resourceType := range []string{"comment", "attachment", "relationship"} {
		t.Run(resourceType, func(t *testing.T) {
			at := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
			_, latest := deriveAttention(snapshot{
				Activity: []activity{{ResourceType: resourceType, At: at}},
			})
			if latest == nil || *latest != at {
				t.Errorf("latest edit = %v, want %v", latest, at)
			}
		})
	}
}

func TestSummarizeDistinguishesZeroAttentionFromUnknown(t *testing.T) {
	state := snapshot{
		Board: board{
			entity:       entity{ID: "board", Revision: 1},
			Name:         "Accepted name",
			Availability: "open",
		},
		Activity: []activity{{ResourceType: "board", At: time.Now()}},
	}
	available := summarize(state, nil)
	if available.Availability != "open" || available.Attention == nil || *available.Attention != (attention{}) ||
		available.LastCardEditAt != nil || available.ErrorCode != nil {
		t.Fatalf("unexpected available summary: %+v", available)
	}

	failed := summarize(state, errors.New("private disk error"))
	if failed.ID != state.Board.ID || failed.Name == nil || *failed.Name != state.Board.Name ||
		failed.Revision == nil || *failed.Revision != state.Board.Revision || failed.Availability != "unhealthy" ||
		failed.Attention != nil || failed.LastCardEditAt != nil || failed.ErrorCode == nil {
		t.Fatalf("unexpected unhealthy summary: %+v", failed)
	}
	wire, err := marshalCanonical(failed)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]string{
		"attention": "null", "last_card_edit_at": "null", "error_code": "\"durable_state_unavailable\"",
	} {
		if got := string(fields[key]); got != expected {
			t.Errorf("%s = %s, want %s", key, got, expected)
		}
	}

	at := time.Date(2026, 11, 10, 12, 0, 0, 120000000, time.FixedZone("offset", 3600))
	state.Activity = []activity{{ResourceType: "card", At: at}}
	wire, err = marshalCanonical(summarize(state, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if got := string(fields["last_card_edit_at"]); got != `"2026-11-10 11:00:00+00:00"` {
		t.Errorf("last_card_edit_at = %s", got)
	}
}

func TestSortSummariesByExactRecencyAttentionNameAndID(t *testing.T) {
	first := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	second := first.Add(time.Second)
	otherZone := first.In(time.FixedZone("offset", 3600))
	alpha, mixedAlpha, zulu := "alpha", "aLpHa", "Zulu"
	boards := []boardSummary{
		{ID: "unknown"},
		{ID: "by-id-z", Name: &alpha, LastCardEditAt: &first, Attention: &attention{Blocked: 1}},
		{ID: "by-name-z", Name: &zulu, LastCardEditAt: &first, Attention: &attention{NeedsInput: 1}},
		{ID: "latest", Name: &zulu, LastCardEditAt: &second},
		{ID: "by-count", Name: &zulu, LastCardEditAt: &first, Attention: &attention{ReadyForReview: 2}},
		{ID: "by-id-a", Name: &mixedAlpha, LastCardEditAt: &otherZone, Attention: &attention{Blocked: 1}},
	}
	sortSummaries(boards)
	var ids []string
	for _, board := range boards {
		ids = append(ids, board.ID)
	}
	want := []string{"latest", "by-count", "by-id-a", "by-id-z", "by-name-z", "unknown"}
	if !slices.Equal(ids, want) {
		t.Errorf("order = %v, want %v", ids, want)
	}
}
