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
	ownerID, parentID   *string
	hasOwner, hasParent bool
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
	allowed := []string{"title", "description", "lane_id", "owner_id", "parent_id"}
	if err := rejectFields(fields, allowed, false); err != nil {
		return patch, err
	}
	for _, field := range []string{"priority_id", "archived", "cover_attachment_id"} {
		if _, exists := fields[field]; exists {
			return patch, invalid(field, "This card edit is unavailable.")
		}
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
	if raw, present := fields["owner_id"]; present {
		patch.hasOwner = true
		var id string
		id, err = inputID(raw, "owner_id")
		patch.ownerID = &id
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
	updated, err := x.service.store.updateChecked(state.Board.ID, nil, func(candidate *snapshot) error {
		return patch.apply(candidate, id, tag, x.actor, canonicalTime(time.Now()))
	})
	if err != nil {
		x.fail(err)
		return
	}
	for _, item := range updated.Cards {
		if item.ID == id {
			x.w.Header().Set("X-Board-ETag", updated.Board.ETag)
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
	if patch.hasParent {
		if _, err := patchParent(s, item.ID, tag, patch.parentID, actor, now); err != nil {
			return err
		}
	}
	if changed {
		item.Revision = s.Board.Revision + 1
		item.UpdatedAt = maxCardTime(item.UpdatedAt, now)
	}
	return nil
}
