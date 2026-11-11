package v2

import (
	"encoding/json"
	"slices"
	"strconv"
)

func (refs snapshotReferences) validateCardChildren(value snapshot) error {
	comments := make(map[string]int)
	attachments := make(map[string]int)
	for _, item := range value.Comments {
		if !refs.claim(entity{ID: item.ID, ETag: item.entityTag(), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}) || item.BoardID != value.Board.ID ||
			refs.cards[item.CardID].ID == "" || refs.users[item.AuthorID].ID == "" ||
			!item.CreatedAt.Equal(item.UpdatedAt) || !snapshotText(item.Text, 1, 4194304) {
			return invalidSnapshot("comments", "Comment identity, references, or content are invalid.")
		}
		refs.comments[item.ID] = item
		comments[item.CardID]++
	}
	for index, item := range value.Attachments {
		if !refs.claim(entity{ID: item.ID, ETag: item.entityTag(), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}) ||
			item.Revision == 0 || item.Revision > value.Board.Revision || item.Revision > maxSnapshotInteger || item.BoardID != value.Board.ID ||
			refs.cards[item.CardID].ID == "" || refs.users[item.AddedBy].ID == "" ||
			!item.Active || !item.CreatedAt.Equal(item.UpdatedAt) {
			return invalidSnapshot("attachments", "Attachment identity, references, or attribution are invalid.")
		}
		if err := validateAttachmentMetadata(item, index); err != nil {
			return err
		}
		refs.attachments[item.ID] = item
		attachments[item.CardID]++
	}
	for _, item := range value.Cards {
		if item.CoverAttachmentID != nil && !refs.validAttachmentCover(item.ID, *item.CoverAttachmentID) {
			return invalidSnapshot("cards.cover_attachment_id", "The cover must be an active raster attachment on this card.")
		}
		if item.CommentCount != comments[item.ID] || item.AttachmentCount != attachments[item.ID] {
			return invalidSnapshot("cards", "Card child counts do not match the native resources.")
		}
	}
	return nil
}

func (refs snapshotReferences) validResourceCreation(event activity, cardCreations map[string]uint64, approvalComment bool) bool {
	if event.Revision < 2 || event.Field != nil || event.Before != nil || (event.ResourceType != "attachment" && event.After != nil) {
		return false
	}
	capabilities := refs.capabilities[event.ActorID]
	switch event.ResourceType {
	case "comment":
		item := refs.comments[event.ResourceID]
		return item.ID != "" && event.Operation == "comment" && (capabilities["comment"] || approvalComment) &&
			item.AuthorID == event.ActorID && item.CreatedAt.Equal(event.At) &&
			cardCreations[item.CardID] > 0 && cardCreations[item.CardID] < event.Revision
	case "attachment":
		item := refs.attachments[event.ResourceID]
		return item.ID != "" && event.Operation == "attach" && capabilities["manage_attachments"] &&
			item.AddedBy == event.ActorID && item.CreatedAt.Equal(event.At) && item.Revision == event.Revision &&
			event.After != nil && *event.After == attachmentAuditValue(item) &&
			cardCreations[item.CardID] > 0 && cardCreations[item.CardID] < event.Revision
	case "lane":
		item := refs.lanes[event.ResourceID]
		return item.ID != "" && event.Operation == "create" && capabilities["manage_workflow"] &&
			item.CreatedAt.Equal(event.At)
	}
	return false
}

func initialAuditedPolicy(value snapshot) policy {
	for _, event := range value.Activity {
		if event.ResourceType == "policy" && snapshotString(event.Field) == policyGrantsField {
			var initial policy
			// The event is validated in replay below, including its exact canonical encoding.
			if event.Before != nil && json.Unmarshal([]byte(*event.Before), &initial) == nil {
				return initial
			}
			break
		}
	}
	return value.Policy
}

// At creation every active lane has a path to Completed. Thus each original
// lane other than Completed appears in at least one initial move grant.
func initialLaneIDs(value snapshot, grants []moveGrant) map[string]bool {
	ids := make(map[string]bool, len(value.Lanes))
	for _, item := range value.Lanes {
		if item.Role == "completed" {
			ids[item.ID] = true
		}
	}
	for _, grant := range grants {
		ids[grant.FromLaneID] = true
		ids[grant.ToLaneID] = true
	}
	return ids
}

func policyReferencesKnownLanes(p policy, exists func(string) bool) bool {
	for _, grant := range p.CreateGrants {
		if !exists(grant.LaneID) {
			return false
		}
	}
	for _, grant := range p.MoveGrants {
		if !exists(grant.FromLaneID) || !exists(grant.ToLaneID) {
			return false
		}
	}
	return true
}

func validCardLaneAudit(grants []moveGrant, event activity) bool {
	if event.Before == nil || event.After == nil {
		return false
	}
	return slices.Contains(grants, moveGrant{
		UserID: event.ActorID, FromLaneID: *event.Before, ToLaneID: *event.After,
	})
}

func (refs snapshotReferences) approvalAuditComment(value snapshot, index int, latest map[snapshotAuditField]*string) (string, bool) {
	event := value.Activity[index]
	if index+1 >= len(value.Activity) || value.Activity[index-1].Revision == event.Revision ||
		(index+2 < len(value.Activity) && value.Activity[index+2].Revision == event.Revision) {
		return "", false
	}
	if refs.users[event.ActorID].Kind != "human" || !refs.capabilities[event.ActorID]["approve"] ||
		refs.lanes[snapshotString(event.Before)].Role != "ready_for_review" ||
		refs.lanes[snapshotString(event.After)].Role != "completed" ||
		snapshotString(latest[snapshotAuditField{"card", event.ResourceID, "archived"}]) != "false" {
		return "", false
	}
	noteEvent := value.Activity[index+1]
	return noteEvent.ResourceID, validApprovalPair(event, noteEvent, refs.comments[noteEvent.ResourceID])
}

func (refs snapshotReferences) validAttachmentCover(cardID, attachmentID string) bool {
	item := refs.attachments[attachmentID]
	return item.CardID == cardID && item.Active && item.Previewable
}

func (refs snapshotReferences) validCardAuditValue(actor string, field snapshotAuditField, value *string) bool {
	if refs.cards[field.resourceID].ID == "" {
		return false
	}
	capabilities := refs.capabilities[actor]
	switch field.name {
	case "parent_id":
		return capabilities["manage_relationships"] &&
			(value == nil || *value != field.resourceID && refs.cards[*value].ID != "")
	case "title":
		return capabilities["edit_card"] && value != nil && snapshotText(*value, 1, 1024)
	case "description":
		return capabilities["edit_card"] && value != nil && snapshotText(*value, 0, 4194304)
	case "cover_attachment_id":
		return capabilities["manage_attachments"] && (value == nil || refs.validAttachmentCover(field.resourceID, *value))
	case "archived":
		return capabilities["archive_card"] && (snapshotString(value) == "true" || snapshotString(value) == "false")
	case "owner_id":
		return capabilities["edit_card"] && value != nil && refs.users[*value].ID != ""
	}
	return false
}

func (refs snapshotReferences) validLaneAuditValue(actor string, field snapshotAuditField, value *string) bool {
	if refs.lanes[field.resourceID].ID == "" || !refs.capabilities[actor]["manage_workflow"] || value == nil {
		return false
	}
	switch field.name {
	case "label":
		return snapshotText(*value, 1, 256)
	case "position":
		position, err := strconv.Atoi(*value)
		return err == nil && position >= 0 && strconv.Itoa(position) == *value
	}
	return false
}

func (refs snapshotReferences) currentCardAuditValue(field snapshotAuditField) *string {
	item := refs.cards[field.resourceID]
	switch field.name {
	case "parent_id":
		return item.ParentID
	case "title":
		return &item.Title
	case "description":
		return &item.Description
	case "owner_id":
		return &item.OwnerID
	case "lane_id":
		return &item.LaneID
	case "archived":
		return archiveAuditValue(item.Archived)
	case "cover_attachment_id":
		return item.CoverAttachmentID
	}
	return nil
}

func (refs snapshotReferences) currentLaneAuditValue(field snapshotAuditField) *string {
	item := refs.lanes[field.resourceID]
	switch field.name {
	case "label":
		return &item.Label
	case "position":
		position := strconv.Itoa(item.Position)
		return &position
	}
	return nil
}
