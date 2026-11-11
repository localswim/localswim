package v2

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestM21PreviewFullPathsAndSelectedPool(t *testing.T) {
	state := snapshot{Board: board{entity: entity{ID: newID(), Revision: 17}}}
	laneID := newID()
	first := card{ID: newID(), LaneID: laneID, Ticket: 2}
	middle := card{ID: newID(), LaneID: newID(), Ticket: 3, Archived: true}
	last := card{ID: newID(), LaneID: laneID, Ticket: 7}
	free := card{ID: newID(), LaneID: laneID, Ticket: 5}
	completed := time.Now()
	middle.CompletedAt = &completed
	state.Cards = []card{first, middle, last, free}
	state.Relationships = []relationship{
		{SourceCardID: last.ID, TargetCardID: middle.ID, Kind: "blocks"},
		{SourceCardID: middle.ID, TargetCardID: first.ID, Kind: "blocks"},
		{SourceCardID: first.ID, TargetCardID: free.ID, Kind: "references"},
	}
	before := cloneSnapshot(state)
	result, err := previewTicketOrder(&state, laneID, []string{last.ID, first.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []ticketOrderEntry{{last.ID, 2}, {first.ID, 7}}
	if !reflect.DeepEqual(result.Proposed, want) || !result.Changed || result.BoardRevision != 17 {
		t.Fatalf("unexpected selected preview: %+v", result)
	}
	if !reflect.DeepEqual(before, state) {
		t.Fatal("preview mutated the snapshot")
	}
	result, err = previewTicketOrder(&state, laneID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	want = []ticketOrderEntry{{free.ID, 2}, {last.ID, 5}, {first.ID, 7}}
	if !reflect.DeepEqual(result.Proposed, want) {
		t.Fatalf("old-ticket tie break or selected pool changed: %+v", result.Proposed)
	}
	result, err = previewTicketOrder(&state, laneID, nil, false)
	if err != nil || result.Changed || !reflect.DeepEqual(result.Proposed, result.Current) {
		t.Fatalf("current read unexpectedly previewed: %+v, %v", result, err)
	}
}

func TestM21SelectedCycleAndSelectionValidation(t *testing.T) {
	laneID := newID()
	a := card{ID: newID(), LaneID: laneID, Ticket: 1}
	b := card{ID: newID(), LaneID: newID(), Ticket: 2, Archived: true}
	c := card{ID: newID(), LaneID: laneID, Ticket: 3}
	state := snapshot{Cards: []card{a, b, c}, Relationships: []relationship{
		{SourceCardID: a.ID, TargetCardID: b.ID, Kind: "blocks"},
		{SourceCardID: b.ID, TargetCardID: a.ID, Kind: "blocks"},
	}}
	if _, err := previewTicketOrder(&state, laneID, []string{a.ID}, true); err == nil {
		t.Fatal("cycle through an unselected card was accepted")
	}
	if _, err := previewTicketOrder(&state, laneID, []string{c.ID}, true); err != nil {
		t.Fatalf("unrelated cycle refused an acyclic selection: %v", err)
	}
	for _, ids := range [][]string{{newID()}, {b.ID}} {
		if _, err := previewTicketOrder(&state, laneID, ids, true); err == nil {
			t.Fatal("invalid selected membership was accepted")
		}
	}
	empty, err := previewTicketOrder(&state, newID(), nil, true)
	if err != nil || empty.Changed || empty.Current == nil || empty.Proposed == nil || empty.SelectedCardIDs == nil {
		t.Fatalf("empty lane must have explicit empty collections: %+v, %v", empty, err)
	}
}

func TestM21QueryValidation(t *testing.T) {
	id := newID()
	ids, preview, err := ticketOrderQuery(url.Values{"card_id": {strings.ToUpper(id)}, "preview": {"true"}})
	if err != nil || !preview || !reflect.DeepEqual(ids, []string{id}) {
		t.Fatalf("canonical selection failed: %v, %v, %v", ids, preview, err)
	}
	for _, values := range []url.Values{
		{"preview": {"TRUE"}}, {"preview": {"true", "false"}}, {"card_id": {""}},
		{"card_id": {id, strings.ToUpper(id)}}, {"unknown": {"true"}},
		{"card_id": make([]string, 1001)},
	} {
		if _, _, err := ticketOrderQuery(values); err == nil {
			t.Fatalf("invalid query was accepted: %v", values)
		}
	}
}
