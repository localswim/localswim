package v2

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"
)

const (
	approvalOperation  = "approve"
	reviewApprovalText = "Reviewed & approved, moving to Completed."
)

var errApprovalState = errors.New("card is unavailable for approval")

type approvalPreconditionRequired string

func (field approvalPreconditionRequired) Error() string { return string(field) + " is required." }

type approvalInput struct {
	id, boardID, boardTag, cardID, cardTag string
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

func readApprovalInput(fields map[string]json.RawMessage) (approvalInput, error) {
	var in approvalInput
	if err := rejectFields(fields, []string{"id", "board_id", "expected_board_etag", "cards"}, true); err != nil {
		return in, err
	}
	var err error
	if raw, present := fields["id"]; present {
		in.id, err = inputID(raw, "id")
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
	var cards []map[string]json.RawMessage
	if json.Unmarshal(fields["cards"], &cards) != nil || len(cards) != 1 || cards[0] == nil {
		return in, invalid("cards", "Expected exactly one card version object.")
	}
	if err := rejectFields(cards[0], []string{"card_id", "etag"}, true); err != nil {
		return in, err
	}
	in.cardID, err = inputID(cards[0]["card_id"], "cards[0].card_id")
	if err != nil {
		return in, err
	}
	in.cardTag, err = approvalTag(cards[0]["etag"], "cards[0].etag")
	return in, err
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
	if in.id == "" {
		in.id = newID()
	}
	next, err := x.service.store.updateChecked(in.boardID, nil, func(state *snapshot) error {
		return in.apply(state, x.actor)
	})
	if errors.Is(err, errApprovalState) {
		x.problem(http.StatusConflict, "conflict", "Approval requires an active Ready for Review card on an open board.", "", nil)
		return
	}
	if err != nil {
		x.fail(err)
		return
	}
	value := approvalReceipt(next.Activity[len(next.Activity)-2:])
	x.w.Header().Set("Location", Prefix+"/approvals/"+value.ID)
	x.w.Header().Set("X-Board-ETag", etag(value.BoardID, value.BoardRevision))
	x.respond(http.StatusCreated, value, "")
}

func (in approvalInput) apply(state *snapshot, actor string) error {
	if !slices.Contains(state.Board.UserIDs, actor) {
		return errMissing
	}
	if !state.can(actor, "approve") || !slices.ContainsFunc(state.Users, func(u user) bool {
		return u.ID == actor && u.Kind == "human"
	}) {
		return errForbidden
	}
	index := slices.IndexFunc(state.Cards, func(c card) bool { return c.ID == in.cardID })
	if index < 0 {
		return errMissing
	}
	item := &state.Cards[index]
	if !matchesStrongTag(in.boardTag, state.Board.ETag) || !matchesStrongTag(in.cardTag, item.entityTag()) {
		return errStale
	}
	if state.Board.Availability != "open" || item.Archived || !slices.ContainsFunc(state.Lanes, func(l lane) bool {
		return l.ID == item.LaneID && l.Role == "ready_for_review" && !l.Retired
	}) {
		return errApprovalState
	}
	target := slices.IndexFunc(state.Lanes, func(l lane) bool { return l.Role == "completed" && !l.Retired })
	if target < 0 {
		return errInvalidSnapshot
	}
	if snapshotIdentityExists(state, in.id) {
		return errIdentityConflict
	}

	now := maxCardTime(canonicalTime(time.Now()), state.Board.UpdatedAt)
	if _, err := applyCardLane(state, item, state.Lanes[target].ID, actor, now); err != nil {
		return err
	}
	movement := &state.Activity[len(state.Activity)-1]
	movement.ID = in.id
	movement.Operation = approvalOperation

	// The approval grant authorizes the fixed system comment as part of the move.
	note := comment{ID: newID(), CreatedAt: now, UpdatedAt: now, BoardID: state.Board.ID,
		CardID: item.ID, Text: reviewApprovalText, AuthorID: actor}
	state.Comments = append(state.Comments, note)
	item.CommentCount++
	item.Revision = movement.Revision
	item.UpdatedAt = now
	state.Activity = append(state.Activity, activity{ID: newID(), BoardID: state.Board.ID,
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
	return approval{ID: first.ID, BoardID: first.BoardID, CardIDs: []string{first.ResourceID},
		ActorID: first.ActorID, At: first.At, BoardRevision: first.Revision,
		Changes: changes}
}

func findApproval(state snapshot, id string) (approval, bool) {
	index := slices.IndexFunc(state.Activity, func(event activity) bool {
		return event.ID == id && event.Operation == approvalOperation
	})
	if index < 0 {
		return approval{}, false
	}
	// Admitted snapshots verify that the matching move has its comment immediately after it.
	return approvalReceipt(state.Activity[index : index+2]), true
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
