package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
)

var errPriorityBatchCompleted = errors.New("Completed cards cannot be selected")

type priorityBatchInput struct {
	boardID, boardTag, priorityID string
	cardIDs                       []string
	allUnfinished                 bool
}

type priorityBatchResult struct {
	BoardID       string     `json:"board_id"`
	BoardRevision uint64     `json:"board_revision"`
	Cards         []card     `json:"cards"`
	Changes       []activity `json:"changes"`
}

func readPriorityBatchInput(fields map[string]json.RawMessage) (priorityBatchInput, error) {
	var in priorityBatchInput
	if err := rejectFields(fields, []string{"board_id", "expected_board_etag", "card_ids", "all_unfinished", "priority_id"}, false); err != nil {
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
	in.priorityID, err = inputID(fields["priority_id"], "priority_id")
	if err != nil {
		return in, err
	}
	_, explicit := fields["card_ids"]
	all, hasAll := fields["all_unfinished"]
	if explicit == hasAll {
		return in, invalid("card_ids", "Provide either card_ids or all_unfinished: true.")
	}
	if hasAll {
		if json.Unmarshal(all, &in.allUnfinished) != nil || !in.allUnfinished {
			return in, invalid("all_unfinished", "Expected true.")
		}
		return in, nil
	}
	var rawIDs []json.RawMessage
	if json.Unmarshal(fields["card_ids"], &rawIDs) != nil || len(rawIDs) < 1 || len(rawIDs) > 1000 {
		return in, invalid("card_ids", "Expected an array of 1 to 1000 card identities.")
	}
	seen := make(map[string]bool, len(rawIDs))
	in.cardIDs = make([]string, 0, len(rawIDs))
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

func (x *request) batchPriorities() {
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	in, err := readPriorityBatchInput(fields)
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
	if !state.can(x.actor, "set_priority") {
		x.fail(errForbidden)
		return
	}
	start := 0
	next, err := x.updateBoard(in.boardID, in.boardTag, func(candidate *snapshot) error {
		start = len(candidate.Activity)
		return in.apply(candidate, x.actor)
	})
	if errors.Is(err, errPriorityBatchCompleted) {
		x.problem(http.StatusConflict, "conflict", "Completed cards cannot be selected.", "", nil)
		return
	}
	if err != nil {
		x.fail(err)
		return
	}
	byID := relationshipCards(&next)
	result := priorityBatchResult{
		BoardID: next.Board.ID, BoardRevision: next.Board.Revision,
		Cards: make([]card, 0, len(in.cardIDs)), Changes: append([]activity{}, next.Activity[start:]...),
	}
	for _, id := range in.cardIDs {
		result.Cards = append(result.Cards, *byID[id])
	}
	x.w.Header().Set("X-Board-ETag", next.Board.entityTag())
	x.respond(http.StatusOK, result, "")
}

func (in *priorityBatchInput) apply(state *snapshot, actor string) error {
	if !slices.Contains(state.Board.UserIDs, actor) {
		return errMissing
	}
	if !state.can(actor, "set_priority") {
		return errForbidden
	}
	if !slices.Contains(state.Board.PriorityIDs, in.priorityID) {
		return invalid("priority_id", "Expected a priority on this board.")
	}
	if in.allUnfinished {
		in.cardIDs = make([]string, 0, len(state.Cards))
		for _, item := range state.Cards {
			if item.CompletedAt == nil {
				in.cardIDs = append(in.cardIDs, item.ID)
			}
		}
	}
	cards := relationshipCards(state)
	overrides := make(map[string]string, len(in.cardIDs))
	for _, id := range in.cardIDs {
		item := cards[id]
		if item == nil {
			return errMissing
		}
		if item.CompletedAt != nil {
			return errPriorityBatchCompleted
		}
		overrides[id] = in.priorityID
	}
	floor := calculatePriorityFloor(state, overrides)
	now := maxCardTime(canonicalTime(time.Now()), state.Board.UpdatedAt)
	changed := false
	for index := range state.Cards {
		item := &state.Cards[index]
		if _, selected := overrides[item.ID]; selected && item.PriorityID != floor.ids[index] {
			id := floor.ids[index]
			recordCardPriority(state, item, id, floor.ranks[id], actor, "patch", now)
			changed = true
		}
	}
	if changed {
		floor.apply(state, actor, now)
	}
	return nil
}
