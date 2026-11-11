package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
)

const (
	approvalOperation  = "approve"
	maxApprovalCards   = 1000
	reviewApprovalText = "Reviewed & approved, moving to Completed."
)

var (
	errApprovalState      = errors.New("card is unavailable for approval")
	errApprovalLane       = errors.New("lane is unavailable for approval")
	errApprovalMembership = errors.New("lane membership has changed")
)

type approvalPreconditionRequired string

func (field approvalPreconditionRequired) Error() string { return string(field) + " is required." }

type approvalInput struct {
	id, boardID, boardTag, laneID string
	cards                         []approvalCardVersion
}

type approvalCardVersion struct {
	id, tag string
}

type approval struct {
	ID            string     `json:"id"`
	BoardID       string     `json:"board_id"`
	CardIDs       []string   `json:"card_ids"`
	ActorID       string     `json:"actor_id"`
	At            time.Time  `json:"at"`
	BoardRevision uint64     `json:"board_revision"`
	Changes       []activity `json:"changes"`
}

type approvalNoop struct {
	BoardID       string   `json:"board_id"`
	BoardRevision uint64   `json:"board_revision"`
	CardIDs       []string `json:"card_ids"`
	Changed       bool     `json:"changed"`
}

func readApprovalInput(fields map[string]json.RawMessage) (approvalInput, error) {
	var in approvalInput
	if err := rejectFields(fields, []string{"id", "board_id", "expected_board_etag", "lane_id", "cards"}, true); err != nil {
		return in, err
	}
	var err error
	if raw, present := fields["id"]; present {
		in.id, err = inputOperationID(raw, "id")
		if err != nil {
			return in, err
		}
	}
	in.boardID, err = inputID(fields["board_id"], "board_id")
	if err != nil {
		return in, err
	}
	in.boardTag, err = approvalTag(fields["expected_board_etag"], "expected_board_etag")
	if err != nil {
		return in, err
	}
	if raw, present := fields["lane_id"]; present {
		in.laneID, err = inputID(raw, "lane_id")
		if err != nil {
			return in, err
		}
	}
	in.cards, err = readApprovalCards(fields["cards"], in.laneID != "")
	return in, err
}

func readApprovalCards(raw json.RawMessage, batch bool) ([]approvalCardVersion, error) {
	var cards []map[string]json.RawMessage
	if json.Unmarshal(raw, &cards) != nil || cards == nil || len(cards) > maxApprovalCards {
		return nil, invalid("cards", "Expected an array of up to 1000 card version objects.")
	}
	if !batch && len(cards) != 1 {
		return nil, invalid("cards", "Expected exactly one card version object.")
	}
	versions := make([]approvalCardVersion, 0, len(cards))
	seen := make(map[string]bool, len(cards))
	for index, fields := range cards {
		path := fmt.Sprintf("cards[%d]", index)
		if fields == nil {
			return nil, invalid(path, "Expected a card version object.")
		}
		if err := rejectFields(fields, []string{"card_id", "etag"}, true); err != nil {
			return nil, invalid(path, "Unrecognized card version member.")
		}
		id, err := inputID(fields["card_id"], path+".card_id")
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, invalid(path+".card_id", "Repeated card identity.")
		}
		tag, err := approvalTag(fields["etag"], path+".etag")
		if err != nil {
			return nil, err
		}
		seen[id] = true
		versions = append(versions, approvalCardVersion{id, tag})
	}
	return versions, nil
}

func approvalTag(raw json.RawMessage, field string) (string, error) {
	if raw == nil {
		return "", approvalPreconditionRequired(field)
	}
	tag, err := textField(raw, field, 18, 130)
	if err != nil {
		return "", err
	}
	if !snapshotETagPattern.MatchString(tag) {
		return "", invalid(field, "Expected one strong entity tag.")
	}
	return tag, nil
}

func (x *request) createApproval() {
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	in, err := readApprovalInput(fields)
	if err != nil {
		var missing approvalPreconditionRequired
		if errors.As(err, &missing) {
			x.problem(http.StatusPreconditionRequired, "precondition_required", missing.Error(), "", nil)
		} else {
			x.fail(err)
		}
		return
	}
	current, exists := x.service.store.accepted(in.boardID)
	if !exists || !slices.Contains(current.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !canApprove(&current, x.actor) {
		x.fail(errForbidden)
		return
	}
	if in.id == "" && len(in.cards) != 0 {
		in.id = newOperationID()
	}
	next, err := x.updateBoardChecked(in.boardID, nil, func(state *snapshot) error {
		return in.apply(state, x.actor)
	})
	switch {
	case errors.Is(err, errApprovalState):
		x.problem(http.StatusConflict, "conflict", "Approval requires an active Ready for Review card on an open board.", "", nil)
		return
	case errors.Is(err, errApprovalLane):
		x.problem(http.StatusConflict, "conflict", "Lane approval requires an enabled, active Ready for Review lane on an open board.", "", nil)
		return
	case errors.Is(err, errApprovalMembership):
		x.problem(http.StatusConflict, "conflict", "The supplied cards must match the active Ready for Review lane membership.", "", nil)
		return
	case err != nil:
		x.fail(err)
		return
	}
	x.w.Header().Set("X-Board-ETag", next.Board.entityTag())
	if len(in.cards) == 0 {
		x.respond(http.StatusOK, approvalNoop{
			BoardID: next.Board.ID, BoardRevision: next.Board.Revision, CardIDs: []string{},
		}, "")
		return
	}
	value := approvalReceipt(next.Activity[len(next.Activity)-2*len(in.cards):])
	x.w.Header().Set("Location", Prefix+"/approvals/"+value.ID)
	x.respond(http.StatusCreated, value, "")
}

func canApprove(state *snapshot, actor string) bool {
	return state.can(actor, "approve") && slices.ContainsFunc(state.Users, func(u user) bool {
		return u.ID == actor && u.Kind == "human"
	})
}

func (in approvalInput) apply(state *snapshot, actor string) error {
	if !slices.Contains(state.Board.UserIDs, actor) {
		return errMissing
	}
	if !canApprove(state, actor) {
		return errForbidden
	}
	indices, err := in.selectCards(state)
	if err != nil {
		return err
	}
	target := slices.IndexFunc(state.Lanes, func(l lane) bool { return l.Role == "completed" && !l.Retired })
	if target < 0 {
		return errInvalidSnapshot
	}
	if len(indices) == 0 {
		grant := moveGrant{UserID: actor, FromLaneID: in.laneID, ToLaneID: state.Lanes[target].ID}
		if !slices.Contains(state.Policy.MoveGrants, grant) {
			return errForbidden
		}
		return nil
	}
	if snapshotIdentityExists(state, in.id) {
		return errIdentityConflict
	}

	now := maxCardTime(canonicalTime(time.Now()), state.Board.UpdatedAt)
	start := len(state.Activity)
	for _, index := range indices {
		if err := approveCard(state, &state.Cards[index], state.Lanes[target].ID, actor, now); err != nil {
			return err
		}
	}
	state.Activity[start].ID = in.id
	return nil
}

func (in approvalInput) selectCards(state *snapshot) ([]int, error) {
	if in.laneID == "" {
		version := in.cards[0]
		index := slices.IndexFunc(state.Cards, func(c card) bool { return c.ID == version.id })
		if index < 0 {
			return nil, errMissing
		}
		item := state.Cards[index]
		if !matchesStrongTag(in.boardTag, state.Board.entityTag()) || !matchesStrongTag(version.tag, item.entityTag()) {
			return nil, errStale
		}
		if state.Board.Availability != "open" || item.Archived || !slices.ContainsFunc(state.Lanes, func(l lane) bool {
			return l.ID == item.LaneID && l.Role == "ready_for_review" && !l.Retired
		}) {
			return nil, errApprovalState
		}
		return []int{index}, nil
	}
	if !matchesStrongTag(in.boardTag, state.Board.entityTag()) {
		return nil, errStale
	}
	laneIndex := slices.IndexFunc(state.Lanes, func(l lane) bool { return l.ID == in.laneID })
	if laneIndex < 0 {
		return nil, errMissing
	}
	source := state.Lanes[laneIndex]
	if state.Board.Availability != "open" || source.Role != "ready_for_review" || source.Retired || !source.ApprovalEnabled {
		return nil, errApprovalLane
	}

	active := make(map[string]int, len(in.cards))
	for index, item := range state.Cards {
		if item.LaneID == source.ID && !item.Archived {
			active[item.ID] = index
		}
	}
	if len(active) != len(in.cards) {
		return nil, errApprovalMembership
	}
	indices := make([]int, 0, len(in.cards))
	for _, version := range in.cards {
		index, exists := active[version.id]
		if !exists {
			return nil, errApprovalMembership
		}
		indices = append(indices, index)
	}
	for position, index := range indices {
		if !matchesStrongTag(in.cards[position].tag, state.Cards[index].entityTag()) {
			return nil, errStale
		}
	}
	return indices, nil
}

func approveCard(state *snapshot, item *card, targetID, actor string, now time.Time) error {
	if _, err := applyCardLane(state, item, targetID, actor, now); err != nil {
		return err
	}
	movement := &state.Activity[len(state.Activity)-1]
	movement.Operation = approvalOperation

	// The approval grant authorizes the fixed system comment as part of the move.
	note := comment{ID: newID(), CreatedAt: now, UpdatedAt: now, BoardID: state.Board.ID,
		CardID: item.ID, Text: reviewApprovalText, AuthorID: actor}
	state.Comments = append(state.Comments, note)
	item.CommentCount++
	item.Revision = movement.Revision
	item.UpdatedAt = now
	state.Activity = append(state.Activity, activity{ID: newOperationID(), BoardID: state.Board.ID,
		Revision: movement.Revision, At: now, ActorID: actor, Operation: "comment",
		ResourceType: "comment", ResourceID: note.ID})
	return nil
}

func validApprovalPair(approved, added activity, note comment) bool {
	return approved.Operation == approvalOperation && approved.ResourceType == "card" &&
		snapshotString(approved.Field) == "lane_id" &&
		added.Operation == "comment" && added.ResourceType == "comment" &&
		added.Field == nil && added.Before == nil && added.After == nil &&
		added.BoardID == approved.BoardID && added.Revision == approved.Revision &&
		added.ActorID == approved.ActorID && added.At.Equal(approved.At) &&
		note.ID != "" && note.ID == added.ResourceID && note.BoardID == approved.BoardID &&
		note.CardID == approved.ResourceID && note.AuthorID == approved.ActorID &&
		note.Text == reviewApprovalText && note.CreatedAt.Equal(approved.At)
}

func approvalReceipt(changes []activity) approval {
	first := changes[0]
	cardIDs := make([]string, 0, len(changes)/2)
	for index := 0; index < len(changes); index += 2 {
		cardIDs = append(cardIDs, changes[index].ResourceID)
	}
	return approval{ID: first.ID, BoardID: first.BoardID, CardIDs: cardIDs,
		ActorID: first.ActorID, At: first.At, BoardRevision: first.Revision,
		Changes: changes}
}

func findApproval(state snapshot, id string) (approval, bool) {
	index := slices.IndexFunc(state.Activity, func(event activity) bool {
		return event.ID == id && event.Operation == approvalOperation
	})
	if index < 0 || (index > 0 && state.Activity[index-1].Revision == state.Activity[index].Revision) {
		return approval{}, false
	}
	// Admitted snapshots verify that the revision contains only approval and comment pairs.
	end := index + 2
	for end < len(state.Activity) && state.Activity[end].Revision == state.Activity[index].Revision {
		end += 2
	}
	return approvalReceipt(state.Activity[index:end]), true
}

func (x *request) approvalResource(id string) {
	for _, state := range x.service.store.list() {
		if !slices.Contains(state.Board.UserIDs, x.actor) {
			continue
		}
		if value, found := findApproval(state, id); found {
			if !state.can(x.actor, "read") {
				x.fail(errForbidden)
				return
			}
			x.respond(http.StatusOK, value, etag(value.ID, 1))
			return
		}
	}
	// An uncertain commit can have reached disk before the receipt reached accepted memory.
	if boardID := x.service.store.fencedBoard(); boardID != "" {
		if known, exists := x.service.store.accepted(boardID); exists && known.can(x.actor, "read") {
			state, err := x.service.store.readback(boardID)
			if err != nil {
				x.fail(err)
				return
			}
			if value, found := findApproval(state, id); found && state.can(x.actor, "read") {
				x.respond(http.StatusOK, value, etag(value.ID, 1))
				return
			}
		}
	}
	x.fail(errMissing)
}
