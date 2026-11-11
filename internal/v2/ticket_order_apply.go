package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
)

const ticketOrderOperation = "renumber"

type ticketOrderInput struct {
	boardID, boardTag string
	cardIDs           []string
}

func readTicketOrderInput(fields map[string]json.RawMessage) (ticketOrderInput, error) {
	var in ticketOrderInput
	if err := rejectFields(fields, []string{"board_id", "expected_board_etag", "card_ids"}, true); err != nil {
		return in, err
	}
	var err error
	in.boardID, err = inputID(fields["board_id"], "board_id")
	if err != nil {
		return in, err
	}
	in.boardTag, err = approvalTag(fields["expected_board_etag"], "expected_board_etag")
	if err != nil {
		return in, err
	}
	var rawIDs []json.RawMessage
	if json.Unmarshal(fields["card_ids"], &rawIDs) != nil || rawIDs == nil || len(rawIDs) > 1000 {
		return in, invalid("card_ids", "Expected an array of at most 1000 card identities.")
	}
	in.cardIDs = make([]string, 0, len(rawIDs))
	seen := make(map[string]bool, len(rawIDs))
	for index, raw := range rawIDs {
		field := fmt.Sprintf("card_ids[%d]", index)
		id, err := inputID(raw, field)
		if err != nil {
			return in, err
		}
		if seen[id] {
			return in, invalid(field, "Repeated card identity.")
		}
		seen[id] = true
		in.cardIDs = append(in.cardIDs, id)
	}
	return in, nil
}

func (x *request) applyTicketOrder(laneID string) {
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	in, err := readTicketOrderInput(fields)
	if err != nil {
		var missing approvalPreconditionRequired
		if errors.As(err, &missing) {
			x.problem(http.StatusPreconditionRequired, "precondition_required", missing.Error(), "", nil)
		} else {
			x.fail(err)
		}
		return
	}
	state, exists := x.service.store.accepted(in.boardID)
	if !exists || !slices.Contains(state.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !state.can(x.actor, "reorder_tickets") {
		x.fail(errForbidden)
		return
	}
	var result ticketOrder
	next, err := x.service.store.update(in.boardID, in.boardTag, func(candidate *snapshot) error {
		var err error
		result, err = in.apply(candidate, laneID, x.actor)
		return err
	})
	if err != nil {
		x.fail(err)
		return
	}
	result.BoardRevision = next.Board.Revision
	x.w.Header().Set("X-Board-ETag", next.Board.ETag)
	x.respond(http.StatusOK, result, "")
}

func (in ticketOrderInput) apply(state *snapshot, laneID, actor string) (ticketOrder, error) {
	if !slices.Contains(state.Board.UserIDs, actor) {
		return ticketOrder{}, errMissing
	}
	if !state.can(actor, "reorder_tickets") {
		return ticketOrder{}, errForbidden
	}
	index := slices.IndexFunc(state.Lanes, func(item lane) bool { return item.ID == laneID })
	if index < 0 {
		return ticketOrder{}, errMissing
	}
	if state.Lanes[index].Retired {
		return ticketOrder{}, invalid("lane_id", "Expected an active lane.")
	}
	result, err := previewTicketOrder(state, laneID, in.cardIDs, true)
	if err != nil {
		return ticketOrder{}, err
	}
	for position, assignment := range result.Proposed {
		if assignment.CardID != in.cardIDs[position] {
			return ticketOrder{}, invalid("card_ids", "Expected the exact current dependency-order preview.")
		}
	}
	now := maxCardTime(canonicalTime(time.Now()), state.Board.UpdatedAt)
	cards := relationshipCards(state)
	for _, assignment := range result.Proposed {
		item := cards[assignment.CardID]
		if item.Ticket == assignment.Ticket {
			continue
		}
		before := strconv.FormatUint(item.Ticket, 10)
		after := strconv.FormatUint(assignment.Ticket, 10)
		item.Ticket, item.Revision, item.UpdatedAt = assignment.Ticket, state.Board.Revision+1, now
		recordCardFieldChange(state, item.ID, actor, ticketOrderOperation, "ticket", &before, &after, now)
	}
	return result, nil
}
