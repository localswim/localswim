package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"time"
)

type cardPatch struct {
	text                cardTextPatch
	laneID              *string
	priorityID          *string
	ownerID, parentID   *string
	hasOwner, hasParent bool
	archived            *bool
	coverID             *string
	hasCover            bool
}

func nullableCardID(raw json.RawMessage, field string) (*string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	id, err := inputID(raw, field)
	return &id, err
}

func readCardPatch(fields map[string]json.RawMessage) (cardPatch, error) {
	var patch cardPatch
	allowed := []string{"title", "description", "lane_id", "owner_id", "parent_id", "archived", "cover_attachment_id", "priority_id"}
	if err := rejectFields(fields, allowed, false); err != nil {
		return patch, err
	}
	recognized := false
	for _, field := range allowed {
		_, present := fields[field]
		recognized = recognized || present
	}
	if !recognized {
		return patch, invalid("body", "A card edit is required.")
	}

	var err error
	patch.text, err = readCardText(fields)
	if err != nil {
		return patch, err
	}
	if raw, present := fields["lane_id"]; present {
		id, err := inputID(raw, "lane_id")
		if err != nil {
			return patch, err
		}
		patch.laneID = &id
	}
	if raw, present := fields["priority_id"]; present {
		id, err := inputID(raw, "priority_id")
		if err != nil {
			return patch, err
		}
		patch.priorityID = &id
	}
	if raw, present := fields["owner_id"]; present {
		patch.hasOwner = true
		var id string
		id, err = inputID(raw, "owner_id")
		patch.ownerID = &id
		if err != nil {
			return patch, err
		}
	}
	if raw, present := fields["archived"]; present {
		patch.archived, err = readArchived(raw)
		if err != nil {
			return patch, err
		}
	}
	if raw, present := fields["cover_attachment_id"]; present {
		patch.hasCover = true
		patch.coverID, err = nullableCardID(raw, "cover_attachment_id")
		if err != nil {
			return patch, err
		}
	}
	if raw, present := fields["parent_id"]; present {
		patch.hasParent = true
		patch.parentID, err = nullableCardID(raw, "parent_id")
	}
	return patch, err
}

func (x *request) patchCard(id string) {
	state, _, ok := x.findCard(id)
	if !ok {
		x.fail(errMissing)
		return
	}
	tag, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	const boardHeader = "X-Expected-Board-ETag"
	boardTags := x.r.Header.Values(boardHeader)
	var boardMatches func(snapshot) bool
	if len(boardTags) != 0 {
		if len(boardTags) != 1 || !snapshotETagPattern.MatchString(boardTags[0]) {
			x.fail(invalid(boardHeader, "Expected one canonical entity tag."))
			return
		}
		boardMatches = func(current snapshot) bool {
			return matchesStrongTag(boardTags[0], current.Board.entityTag())
		}
	}
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	patch, err := readCardPatch(fields)
	if err != nil {
		x.fail(err)
		return
	}
	updated, err := x.updateBoardChecked(state.Board.ID, boardMatches, func(candidate *snapshot) error {
		now := maxCardTime(canonicalTime(time.Now()), candidate.Board.UpdatedAt)
		return patch.apply(candidate, id, tag, x.actor, now)
	})
	if err != nil {
		x.fail(err)
		return
	}
	for _, item := range updated.Cards {
		if item.ID == id {
			x.w.Header().Set("X-Board-ETag", updated.Board.entityTag())
			x.respond(http.StatusOK, item, item.entityTag())
			return
		}
	}
	x.fail(errMissing)
}

func (patch cardPatch) apply(s *snapshot, id, tag, actor string, now time.Time) error {
	if !slices.Contains(s.Board.UserIDs, actor) {
		return errMissing
	}
	for i := range s.Cards {
		item := &s.Cards[i]
		if item.ID != id {
			continue
		}
		return patch.applyToCard(s, item, tag, actor, now)
	}
	return errMissing
}

func (patch cardPatch) applyToCard(s *snapshot, item *card, tag, actor string, now time.Time) error {
	if !matchesStrongTag(tag, item.entityTag()) {
		return errStale
	}
	wasCompleted := item.CompletedAt != nil
	changed, err := patch.text.apply(s, item, actor, now)
	if err != nil {
		return err
	}
	if patch.laneID != nil {
		moved, err := applyCardLane(s, item, *patch.laneID, actor, now)
		if err != nil {
			return err
		}
		changed = changed || moved
	}
	if patch.hasOwner {
		assigned, err := applyCardOwner(s, item, *patch.ownerID, actor, now)
		if err != nil {
			return err
		}
		changed = changed || assigned
	}
	if patch.archived != nil {
		archived, err := applyCardArchive(s, item, *patch.archived, actor, now)
		if err != nil {
			return err
		}
		changed = changed || archived
	}
	if patch.hasCover {
		if !s.can(actor, "manage_attachments") {
			return errForbidden
		}
		if err := validateAttachmentCover(s, item.ID, patch.coverID); err != nil {
			return err
		}
		if !equalSnapshotStrings(item.CoverAttachmentID, patch.coverID) {
			recordCardAssignment(s, item.ID, actor, "cover_attachment_id", item.CoverAttachmentID, patch.coverID, now)
			item.CoverAttachmentID = cloneSnapshotPointer(patch.coverID)
			changed = true
		}
	}
	if patch.hasParent {
		if _, err := patchParent(s, item.ID, item.Revision, patch.parentID, actor, now); err != nil {
			return err
		}
	}
	if changed {
		item.Revision = s.Board.Revision + 1
		item.UpdatedAt = maxCardTime(item.UpdatedAt, now)
	}
	reopened := wasCompleted && item.CompletedAt == nil
	if patch.priorityID != nil {
		return applyCardPriority(s, item, *patch.priorityID, actor, reopened, now)
	}
	if reopened {
		promotePriorityFloor(s, actor, now)
	}
	return nil
}
