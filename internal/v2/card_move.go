package v2

import (
	"slices"
	"time"
)

const reopenOperation = "reopen"

// applyCardLane uses the current directed lane grant for the requesting actor.
func applyCardLane(s *snapshot, value *card, laneID, actorID string, now time.Time) (bool, error) {
	index := slices.IndexFunc(s.Lanes, func(l lane) bool { return l.ID == laneID })
	if index < 0 || s.Lanes[index].Retired {
		return false, invalid("lane_id", "Expected an active lane on this board.")
	}
	from := value.LaneID
	if !slices.Contains(s.Policy.MoveGrants, moveGrant{UserID: actorID, FromLaneID: from, ToLaneID: laneID}) {
		return false, errForbidden
	}
	value.LaneID = laneID
	operation := "patch"
	if s.Lanes[index].Role == "completed" {
		value.CompletedAt = &now
	} else {
		if value.CompletedAt != nil {
			operation = reopenOperation
		}
		value.CompletedAt = nil
	}
	recordCardFieldChange(s, value.ID, actorID, operation, "lane_id", &from, &laneID, now)
	return true, nil
}

// applyCardOwner checks edit permission independently of any lane movement.
func applyCardOwner(s *snapshot, value *card, ownerID string, actorID string, now time.Time) (bool, error) {
	if !s.can(actorID, "edit_card") {
		return false, errForbidden
	}
	if !slices.Contains(s.Board.UserIDs, ownerID) {
		return false, invalid("owner_id", "Expected a user on this board.")
	}
	if value.OwnerID == ownerID {
		return false, nil
	}
	old := value.OwnerID
	value.OwnerID = ownerID
	recordCardAssignment(s, value.ID, actorID, "owner_id", &old, &ownerID, now)
	return true, nil
}

func recordCardAssignment(s *snapshot, cardID, actorID, field string, before, after *string, now time.Time) {
	recordCardFieldChange(s, cardID, actorID, "patch", field, before, after, now)
}

func recordCardFieldChange(s *snapshot, cardID, actorID, operation, field string, before, after *string, now time.Time) {
	s.Activity = append(s.Activity, activity{
		ID: newID(), BoardID: s.Board.ID, Revision: s.Board.Revision + 1, At: now,
		ActorID: actorID, Operation: operation, ResourceType: "card", ResourceID: cardID, Field: &field,
		Before: cloneSnapshotPointer(before), After: cloneSnapshotPointer(after),
	})
}
