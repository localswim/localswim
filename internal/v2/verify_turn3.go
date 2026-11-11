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
			item.Active && !item.CreatedAt.Equal(item.UpdatedAt) {
			return invalidSnapshot("attachments", "Attachment identity, references, or attribution are invalid.")
		}
		if err := validateAttachmentMetadata(item, index); err != nil {
			return err
		}
		refs.attachments[item.ID] = item
		if item.Active {
			attachments[item.CardID]++
		}
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
		original := item
		original.Active = true
		original.Revision = event.Revision
		original.UpdatedAt = original.CreatedAt
		return item.ID != "" && event.Operation == "attach" && capabilities["manage_attachments"] &&
			item.AddedBy == event.ActorID && item.CreatedAt.Equal(event.At) &&
			event.After != nil && *event.After == attachmentAuditValue(original) &&
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

func initialLaneIDs(lanes []lane, original policy) map[string]bool {
	ids := make(map[string]bool, len(lanes))
	for _, item := range lanes {
		if item.Role == "completed" {
			ids[item.ID] = true
		}
	}
	for _, grant := range original.MoveGrants {
		ids[grant.FromLaneID] = true
		ids[grant.ToLaneID] = true
	}
	for _, grant := range original.CreateGrants {
		ids[grant.LaneID] = true
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

func (refs snapshotReferences) approvalAuditEnd(value snapshot, start int, latest map[snapshotAuditField]*string, occupancy map[string]int) int {
	first := value.Activity[start]
	source, target := snapshotString(first.Before), snapshotString(first.After)
	if value.Activity[start-1].Revision == first.Revision ||
		refs.users[first.ActorID].Kind != "human" || !refs.capabilities[first.ActorID]["approve"] ||
		refs.lanes[source].Role != "ready_for_review" || refs.lanes[target].Role != "completed" ||
		snapshotString(latest[snapshotAuditField{"lane", source, "retired"}]) != "false" {
		return 0
	}
	text := approvalCommentText(first.BulkApprovalComment)
	end := start
	members := make(map[string]bool)
	for end < len(value.Activity) && value.Activity[end].Revision == first.Revision {
		move := value.Activity[end]
		if end+1 >= len(value.Activity) || move.ActorID != first.ActorID || !move.At.Equal(first.At) ||
			snapshotString(move.Before) != source || snapshotString(move.After) != target ||
			snapshotString(latest[snapshotAuditField{"card", move.ResourceID, "lane_id"}]) != source ||
			snapshotString(latest[snapshotAuditField{"card", move.ResourceID, "archived"}]) != "false" ||
			members[move.ResourceID] || (end != start && move.BulkApprovalComment != "") {
			return 0
		}
		note := value.Activity[end+1]
		if !validApprovalPair(move, note, refs.comments[note.ResourceID], text) {
			return 0
		}
		members[move.ResourceID] = true
		end += 2
	}
	bulk := first.BulkApprovalComment != "" || len(members) > 1
	configured := snapshotString(latest[snapshotAuditField{"lane", source, "bulk_approval_comment"}])
	if len(members) > maxApprovalCards || bulk && (len(members) != occupancy[source] ||
		text != approvalCommentText(configured) ||
		snapshotString(latest[snapshotAuditField{"lane", source, "approval_enabled"}]) != "true") {
		return 0
	}
	return end
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
		if value == nil {
			return capabilities["manage_attachments"]
		}
		item := refs.attachments[*value]
		return capabilities["manage_attachments"] && item.CardID == field.resourceID && item.Previewable
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
	case "notes":
		return snapshotText(*value, 0, maxLaneNotes)
	case "key":
		return refs.lanes[field.resourceID].Role != "completed" && *value != "completed" && snapshotText(*value, 1, 128)
	case "position":
		position, err := strconv.Atoi(*value)
		return err == nil && position >= 0 && strconv.Itoa(position) == *value
	case "retired":
		return refs.lanes[field.resourceID].Role != "completed" && (*value == "false" || *value == "true")
	case "reorder_enabled":
		return *value == "false" || *value == "true"
	case "approval_enabled":
		return refs.lanes[field.resourceID].Role == "ready_for_review" && (*value == "false" || *value == "true")
	case "bulk_approval_comment":
		return refs.lanes[field.resourceID].Role == "ready_for_review" && snapshotText(*value, 0, maxBulkApprovalComment)
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
	case "ticket":
		ticket := strconv.FormatUint(item.Ticket, 10)
		return &ticket
	case "priority_id":
		return &item.PriorityID
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
	case "notes":
		return &item.Notes
	case "key":
		return &item.Key
	case "retired":
		return archiveAuditValue(item.Retired)
	case "reorder_enabled":
		return archiveAuditValue(item.ReorderEnabled)
	case "approval_enabled":
		return archiveAuditValue(item.ApprovalEnabled)
	case "bulk_approval_comment":
		return &item.BulkApprovalComment
	case "position":
		position := strconv.Itoa(item.Position)
		return &position
	}
	return nil
}

func originalCardLanes(value snapshot) map[string]string {
	origins := make(map[string]string, len(value.Cards))
	for _, item := range value.Cards {
		origins[item.ID] = item.LaneID
	}
	for i := len(value.Activity) - 1; i >= 0; i-- {
		event := value.Activity[i]
		if event.ResourceType == "card" && snapshotString(event.Field) == "lane_id" && event.Before != nil {
			origins[event.ResourceID] = *event.Before
		}
	}
	return origins
}

func validDetachAuditShape(events []activity, index int, item attachment) bool {
	detach := events[index]
	if index+1 < len(events) && events[index+1].Revision == detach.Revision {
		return false
	}
	clear := events[index-1]
	if clear.Revision != detach.Revision {
		return true
	}
	return (index < 2 || events[index-2].Revision != detach.Revision) &&
		clear.At.Equal(detach.At) && clear.Operation == "patch" && clear.ResourceType == "card" &&
		clear.ResourceID == item.CardID && snapshotString(clear.Field) == "cover_attachment_id" &&
		snapshotString(clear.Before) == item.ID && clear.After == nil
}

func validHistoricalRetirement(lanes []lane, rules policy, retiring string, exists func(string) bool,
	latest map[snapshotAuditField]*string) bool {
	historical := make([]lane, 0, len(lanes))
	for _, item := range lanes {
		if exists(item.ID) {
			item.Retired = item.ID == retiring ||
				snapshotString(latest[snapshotAuditField{"lane", item.ID, "retired"}]) == "true"
			historical = append(historical, item)
		}
	}
	return validateWorkflowPaths(historical, rules) == nil
}

func validRetirementAuditShape(events []activity, index int) bool {
	retired := events[index]
	if index+1 < len(events) && events[index+1].Revision == retired.Revision {
		return false
	}
	for i := index - 1; i >= 0 && events[i].Revision == retired.Revision; i-- {
		edit := events[i]
		field := snapshotString(edit.Field)
		if !edit.At.Equal(retired.At) || edit.ResourceType != "lane" || edit.ResourceID != retired.ResourceID ||
			edit.Operation != "patch" || (field != "label" && field != "key" && field != "notes" &&
			field != "approval_enabled" && field != "reorder_enabled") {
			return false
		}
	}
	return true
}
