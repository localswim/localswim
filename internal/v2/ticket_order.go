package v2

import (
	"cmp"
	"net/http"
	"net/url"
	"slices"
)

type ticketOrderEntry struct {
	CardID string `json:"card_id"`
	Ticket uint64 `json:"ticket"`
}

type ticketOrder struct {
	BoardID         string             `json:"board_id"`
	LaneID          string             `json:"lane_id"`
	BoardRevision   uint64             `json:"board_revision"`
	SelectedCardIDs []string           `json:"selected_card_ids"`
	Current         []ticketOrderEntry `json:"current"`
	Proposed        []ticketOrderEntry `json:"proposed"`
	Changed         bool               `json:"changed"`
}

func ticketOrderQuery(values url.Values) ([]string, bool, error) {
	for key := range values {
		if key != "card_id" && key != "preview" {
			return nil, false, invalid(key, "Unrecognized query parameter.")
		}
	}
	preview := false
	if raw, present := values["preview"]; present {
		if len(raw) != 1 || (raw[0] != "true" && raw[0] != "false") {
			return nil, false, invalid("preview", "Expected one Boolean value.")
		}
		preview = raw[0] == "true"
	}
	rawIDs := values["card_id"]
	if len(rawIDs) > 1000 {
		return nil, false, invalid("card_id", "Expected at most 1000 card identities.")
	}
	var ids []string
	seen := make(map[string]bool, len(rawIDs))
	for _, raw := range rawIDs {
		id, valid := normalizeID(raw)
		if !valid {
			return nil, false, invalid("card_id", "Expected a UUIDv4 card identity.")
		}
		if seen[id] {
			return nil, false, invalid("card_id", "Repeated card identity.")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, preview, nil
}

func (x *request) ticketOrderResource(id string) {
	values, err := url.ParseQuery(x.r.URL.RawQuery)
	if err != nil {
		x.fail(invalid("query", "Invalid query parameters."))
		return
	}
	ids, preview, err := ticketOrderQuery(values)
	if err != nil {
		x.fail(err)
		return
	}
	for _, state := range x.service.store.list() {
		if !slices.Contains(state.Board.UserIDs, x.actor) {
			continue
		}
		index := slices.IndexFunc(state.Lanes, func(item lane) bool { return item.ID == id })
		if index < 0 {
			continue
		}
		if !state.can(x.actor, "read") {
			x.fail(errForbidden)
			return
		}
		if state.Lanes[index].Retired {
			x.fail(invalid("lane_id", "Expected an active lane."))
			return
		}
		if !x.useBoard(state.Board.ID) {
			return
		}
		value, err := previewTicketOrder(&state, id, ids, preview)
		if err != nil {
			x.fail(err)
			return
		}
		x.w.Header().Set("X-Board-ETag", state.Board.ETag)
		x.respond(http.StatusOK, value, "")
		return
	}
	x.fail(errMissing)
}

func previewTicketOrder(state *snapshot, laneID string, ids []string, preview bool) (ticketOrder, error) {
	selected := make([]card, 0)
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	for _, item := range state.Cards {
		if item.LaneID == laneID && !item.Archived && (ids == nil || wanted[item.ID]) {
			selected = append(selected, item)
		}
	}
	if ids != nil && len(selected) != len(ids) {
		return ticketOrder{}, invalid("card_id", "Expected active cards from the selected lane.")
	}
	slices.SortFunc(selected, func(a, b card) int { return cmp.Compare(a.Ticket, b.Ticket) })
	value := ticketOrder{BoardID: state.Board.ID, LaneID: laneID, BoardRevision: state.Board.Revision,
		SelectedCardIDs: make([]string, 0, len(selected)), Current: make([]ticketOrderEntry, 0, len(selected)),
		Proposed: make([]ticketOrderEntry, 0, len(selected))}
	for _, item := range selected {
		value.SelectedCardIDs = append(value.SelectedCardIDs, item.ID)
		value.Current = append(value.Current, ticketOrderEntry{item.ID, item.Ticket})
	}
	if !preview {
		value.Proposed = slices.Clone(value.Current)
		return value, nil
	}
	order, err := dependencyTicketOrder(state, selected)
	if err != nil {
		return ticketOrder{}, err
	}
	for position, index := range order {
		value.Proposed = append(value.Proposed, ticketOrderEntry{selected[index].ID, selected[position].Ticket})
		value.Changed = value.Changed || index != position
	}
	return value, nil
}

// Paths retain every board card, including unselected and archived intermediates.
// Only the selected cards acquire ordering constraints or new proposed tickets.
func dependencyTicketOrder(state *snapshot, selected []card) ([]int, error) {
	edges := make(map[string][]string)
	for _, link := range state.Relationships {
		if link.Kind == "blocks" {
			edges[link.SourceCardID] = append(edges[link.SourceCardID], link.TargetCardID)
		}
	}
	indices := make(map[string]int, len(selected))
	for index, item := range selected {
		indices[item.ID] = index
	}
	outgoing := make([][]int, len(selected))
	indegree := make([]int, len(selected))
	for source, item := range selected {
		seen := make(map[string]bool)
		pending := slices.Clone(edges[item.ID])
		for len(pending) > 0 {
			last := len(pending) - 1
			id := pending[last]
			pending = pending[:last]
			if seen[id] {
				continue
			}
			seen[id] = true
			if target, exists := indices[id]; exists {
				if source == target {
					return nil, invalid("card_id", "Selected cards contain a blocking cycle.")
				}
				outgoing[source] = append(outgoing[source], target)
				indegree[target]++
			}
			pending = append(pending, edges[id]...)
		}
	}
	order := make([]int, 0, len(selected))
	for len(order) < len(selected) {
		next := -1
		for index, degree := range indegree {
			if degree == 0 {
				next = index
				break
			}
		}
		if next < 0 {
			return nil, invalid("card_id", "Selected cards contain a blocking cycle.")
		}
		indegree[next] = -1
		order = append(order, next)
		for _, target := range outgoing[next] {
			indegree[target]--
		}
	}
	return order, nil
}
