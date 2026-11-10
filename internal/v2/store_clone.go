package v2

import (
	"encoding/json"
	"slices"
)

// cloneSnapshot detaches every mutable field so a failed mutation or a
// caller-held read result cannot change the accepted snapshot.
func cloneSnapshot(value snapshot) snapshot {
	value.Board.UserIDs = slices.Clone(value.Board.UserIDs)
	value.Board.LaneIDs = slices.Clone(value.Board.LaneIDs)
	value.Board.PriorityIDs = slices.Clone(value.Board.PriorityIDs)
	value.Board.LastCardEditAt = cloneSnapshotPointer(value.Board.LastCardEditAt)
	value.Board.Notifications.TargetThreadID = cloneSnapshotPointer(value.Board.Notifications.TargetThreadID)
	value.Board.Publishing.Repository = cloneSnapshotPointer(value.Board.Publishing.Repository)
	value.Board.Publishing.LastSuccessAt = cloneSnapshotPointer(value.Board.Publishing.LastSuccessAt)
	value.Board.Publishing.ErrorCode = cloneSnapshotPointer(value.Board.Publishing.ErrorCode)
	value.Users = slices.Clone(value.Users)
	value.Lanes = slices.Clone(value.Lanes)
	value.Priorities = slices.Clone(value.Priorities)
	value.Policy.CreateGrants = slices.Clone(value.Policy.CreateGrants)
	value.Policy.MoveGrants = slices.Clone(value.Policy.MoveGrants)
	value.Policy.ResourceGrants = slices.Clone(value.Policy.ResourceGrants)
	for index := range value.Policy.ResourceGrants {
		grant := &value.Policy.ResourceGrants[index]
		grant.Capabilities = slices.Clone(grant.Capabilities)
	}
	value.Cards = slices.Clone(value.Cards)
	for index := range value.Cards {
		card := &value.Cards[index]
		card.OwnerID = cloneSnapshotPointer(card.OwnerID)
		card.ParentID = cloneSnapshotPointer(card.ParentID)
		card.ChildIDs = slices.Clone(card.ChildIDs)
		card.CoverAttachmentID = cloneSnapshotPointer(card.CoverAttachmentID)
		card.CompletedAt = cloneSnapshotPointer(card.CompletedAt)
	}
	value.Comments = slices.Clone(value.Comments)
	value.Relationships = cloneSnapshotMessages(value.Relationships)
	value.Attachments = slices.Clone(value.Attachments)
	value.Activity = slices.Clone(value.Activity)
	for index := range value.Activity {
		event := &value.Activity[index]
		event.Field = cloneSnapshotPointer(event.Field)
		event.Before = cloneSnapshotPointer(event.Before)
		event.After = cloneSnapshotPointer(event.After)
	}
	return value
}

func cloneSnapshotPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	detached := *value
	return &detached
}

func cloneSnapshotMessages(values []json.RawMessage) []json.RawMessage {
	values = slices.Clone(values)
	for index := range values {
		values[index] = slices.Clone(values[index])
	}
	return values
}
