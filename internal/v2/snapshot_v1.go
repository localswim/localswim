package v2

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Version 1 used camelCase member names. This map applies only to closed native
// snapshots, never to arbitrary API objects or string values.
var snapshotV1Names = map[string]string{
	"actorId":           "actor_id",
	"approvalEnabled":   "approval_enabled",
	"attachmentCount":   "attachment_count",
	"blockedCardCount":  "blocked_card_count",
	"blockingDepth":     "blocking_depth",
	"boardId":           "board_id",
	"childIds":          "child_ids",
	"commentCount":      "comment_count",
	"completedAt":       "completed_at",
	"coverAttachmentId": "cover_attachment_id",
	"createGrants":      "create_grants",
	"createdAt":         "created_at",
	"defaultPriorityId": "default_priority_id",
	"errorCode":         "error_code",
	"formatVersion":     "format_version",
	"fromLaneId":        "from_lane_id",
	"laneId":            "lane_id",
	"laneIds":           "lane_ids",
	"lastCardEditAt":    "last_card_edit_at",
	"lastSuccessAt":     "last_success_at",
	"moveGrants":        "move_grants",
	"needsInput":        "needs_input",
	"ownerId":           "owner_id",
	"parentId":          "parent_id",
	"policyId":          "policy_id",
	"priorityId":        "priority_id",
	"priorityIds":       "priority_ids",
	"readyForReview":    "ready_for_review",
	"reorderEnabled":    "reorder_enabled",
	"resourceGrants":    "resource_grants",
	"resourceId":        "resource_id",
	"resourceType":      "resource_type",
	"targetThreadId":    "target_thread_id",
	"toLaneId":          "to_lane_id",
	"triageScore":       "triage_score",
	"updatedAt":         "updated_at",
	"userCss":           "user_css",
	"userId":            "user_id",
	"userIds":           "user_ids",
}

func convertSnapshotV1Names(data []byte, toLegacy bool) ([]byte, error) {
	names := snapshotV1Names
	if toLegacy {
		names = make(map[string]string, len(snapshotV1Names))
		for old, current := range snapshotV1Names {
			names[current] = old
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	converted, err := renameSnapshotV1Fields(value, names)
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func renameSnapshotV1Fields(value any, names map[string]string) (any, error) {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			if renamed, ok := names[key]; ok {
				key = renamed
			}
			if _, exists := result[key]; exists {
				return nil, errors.New("conflicting native snapshot member names")
			}
			converted, err := renameSnapshotV1Fields(child, names)
			if err != nil {
				return nil, err
			}
			result[key] = converted
		}
		return result, nil
	case []any:
		for index, child := range value {
			converted, err := renameSnapshotV1Fields(child, names)
			if err != nil {
				return nil, err
			}
			value[index] = converted
		}
	}
	return value, nil
}

// Preserve historical audit strings while comparing their field identities.
func canonicalAuditFieldName(name string) string {
	switch name {
	case "parentId":
		return "parent_id"
	case "notifications.targetThreadId":
		return "notifications.target_thread_id"
	default:
		return name
	}
}
