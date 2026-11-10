package v2

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type workflowFields struct {
	path   string
	fields map[string]json.RawMessage
	err    error
}

func readWorkflowFields(raw json.RawMessage, path string, closed bool, allowed ...string) *workflowFields {
	input := &workflowFields{path: path}
	fields, err := decodeObject(raw)
	if err != nil {
		input.err = invalid(path, "must be an object without duplicate fields")
		return input
	}
	input.fields = fields
	input.err = rejectFields(fields, allowed, closed)
	return input
}

// Each reader leaves the first error in place so a definition is checked before use.
func (input *workflowFields) fail(key, message string) {
	if input.err == nil {
		input.err = invalid(input.path+"."+key, message)
	}
}

func (input *workflowFields) string(key string, minLength, maxLength int, required bool) string {
	raw, present := input.fields[key]
	if !present && !required {
		return ""
	}
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		input.fail(key, "must be a string")
		return ""
	}
	if n := utf8.RuneCountInString(value); n < minLength || n > maxLength {
		input.fail(key, "has an invalid length")
	}
	return value
}

func (input *workflowFields) enum(key string, fallback string, allowed ...string) string {
	if _, present := input.fields[key]; !present && fallback != "" {
		return fallback
	}
	value := input.string(key, 1, 32, true)
	for _, char := range value {
		if char > unicode.MaxASCII {
			input.fail(key, "must be a supported value")
			return ""
		}
	}
	value = strings.ToLower(value)
	if !slices.Contains(allowed, value) {
		input.fail(key, "must be a supported value")
	}
	return value
}

func (input *workflowFields) id(key string) string {
	value := input.string(key, 36, 36, true)
	normalized, ok := normalizeID(value)
	if !ok {
		input.fail(key, "must be a UUIDv4")
	}
	return normalized
}

func (input *workflowFields) integer(key string) int {
	var value int
	raw := input.fields[key]
	if len(raw) == 0 || raw[0] == 'n' || json.Unmarshal(raw, &value) != nil || value < 0 {
		input.fail(key, "must be a nonnegative integer")
	}
	return value
}

func (input *workflowFields) array(key string, maxItems int, required bool) []json.RawMessage {
	raw, present := input.fields[key]
	if !present && !required {
		return nil
	}
	var items []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil || len(items) > maxItems {
		input.fail(key, fmt.Sprintf("must be an array of at most %d items", maxItems))
	}
	return items
}

func (input *workflowFields) referenceList(key string, users map[string]string) []string {
	items := input.array(key, 1000, false)
	ids := make([]string, 0, len(items))
	for index, raw := range items {
		path := fmt.Sprintf("%s[%d]", key, index)
		var value string
		if json.Unmarshal(raw, &value) != nil {
			input.fail(path, "must be a user UUIDv4")
			return nil
		}
		id, ok := normalizeID(value)
		if !ok || users[id] == "" {
			input.fail(path, "must refer to an initial user")
			return nil
		}
		if slices.Contains(ids, id) {
			input.fail(path, "repeats a user")
			return nil
		}
		ids = append(ids, id)
	}
	return ids
}

func workflowItem(path string, index int) string {
	return fmt.Sprintf("%s[%d]", path, index)
}

func initialLaneKey(label, role string) string {
	if role == "completed" {
		return "completed"
	}
	parts := strings.FieldsFunc(strings.ToLower(label), func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsNumber(char)
	})
	key := []rune(strings.Join(parts, "_"))
	if len(key) > 128 {
		key = key[:128]
	}
	return string(key)
}

func customWorkflow(raw json.RawMessage, boardID string, now time.Time) (workflow, error) {
	root := readWorkflowFields(raw, "workflow", true, "users", "lanes", "priorities", "policy")
	userInputs := root.array("users", 100, true)
	laneInputs := root.array("lanes", 100, true)
	priorityInputs := root.array("priorities", 100, true)
	if root.err != nil {
		return workflow{}, root.err
	}

	result := workflow{
		Users:      make([]user, 0, len(userInputs)),
		Lanes:      make([]lane, 0, len(laneInputs)),
		Priorities: make([]priority, 0, len(priorityInputs)),
	}
	ids := map[string]bool{boardID: true}
	users := make(map[string]string, len(userInputs))
	for index, raw := range userInputs {
		path := workflowItem("workflow.users", index)
		input := readWorkflowFields(raw, path, true, "id", "label", "kind", "color")
		id := input.id("id")
		definition := user{
			entity: newEntity(id, now),
			Label:  input.string("label", 1, 256, true),
			Kind:   input.enum("kind", "", "human", "bot"),
			Color:  input.string("color", 0, 64, false),
		}
		if ids[id] {
			input.fail("id", "repeats an entity UUID")
		}
		if input.err != nil {
			return workflow{}, input.err
		}
		ids[id], users[id] = true, definition.Kind
		result.Users = append(result.Users, definition)
	}

	lanes := make(map[string]string, len(laneInputs))
	laneKeys := make(map[string]bool, len(laneInputs))
	laneCreate := make([][]string, len(laneInputs))
	laneConnect := make([][]string, len(laneInputs))
	completedCount := 0
	for index, raw := range laneInputs {
		path := workflowItem("workflow.lanes", index)
		input := readWorkflowFields(raw, path, false, "id", "key", "label", "role", "createFor", "connectFor")
		id := input.id("id")
		label := input.string("label", 1, 256, true)
		role := input.enum("role", "ordinary", "ordinary", "completed", "blocked", "needs_input", "ready_for_review")
		key := input.string("key", 1, 128, false)
		if _, present := input.fields["key"]; !present {
			key = initialLaneKey(label, role)
			if key == "" {
				input.fail("key", "is required when the label does not yield a key")
			}
		}
		if ids[id] {
			input.fail("id", "repeats an entity UUID")
		}
		if laneKeys[key] {
			input.fail("key", "repeats a lane key")
		}
		if (key == "completed") != (role == "completed") {
			input.fail("key", "the completed key is reserved for the completed lane")
		}
		if _, before := input.fields["beforeId"]; before {
			if _, after := input.fields["afterId"]; after {
				input.fail("beforeId", "conflicts with afterId")
			}
		}
		laneCreate[index] = input.referenceList("createFor", users)
		laneConnect[index] = input.referenceList("connectFor", users)
		if input.err != nil {
			return workflow{}, input.err
		}
		if role == "completed" {
			completedCount++
		}
		ids[id], lanes[id], laneKeys[key] = true, role, true
		result.Lanes = append(result.Lanes, lane{
			entity:   newEntity(id, now),
			BoardID:  boardID,
			Key:      key,
			Label:    label,
			Role:     role,
			Position: index,
		})
	}
	if completedCount != 1 {
		return workflow{}, invalid("workflow.lanes", "must define exactly one completed lane")
	}

	priorities := make(map[string]bool, len(priorityInputs))
	priorityKeys := make(map[string]bool, len(priorityInputs))
	ranks := make(map[int]bool, len(priorityInputs))
	for index, raw := range priorityInputs {
		path := workflowItem("workflow.priorities", index)
		input := readWorkflowFields(raw, path, false, "id", "key", "label", "rank", "color")
		id := input.id("id")
		definition := priority{
			entity:  newEntity(id, now),
			BoardID: boardID,
			Key:     input.string("key", 1, 128, true),
			Label:   input.string("label", 1, 256, true),
			Rank:    input.integer("rank"),
			Color:   input.string("color", 0, 64, false),
		}
		if ids[id] {
			input.fail("id", "repeats an entity UUID")
		}
		if priorityKeys[definition.Key] {
			input.fail("key", "repeats a priority key")
		}
		if ranks[definition.Rank] {
			input.fail("rank", "repeats a priority rank")
		}
		if input.err != nil {
			return workflow{}, input.err
		}
		ids[id], priorities[id], priorityKeys[definition.Key], ranks[definition.Rank] = true, true, true, true
		result.Priorities = append(result.Priorities, definition)
	}

	resolvedPolicy, err := readInitialPolicy(root.fields["policy"], boardID, now, users, lanes, priorities)
	if err != nil {
		return workflow{}, err
	}
	addInitialLaneGrants(&resolvedPolicy, result.Lanes, laneCreate, laneConnect)
	result.Policy = resolvedPolicy
	return result, nil
}

func readInitialPolicy(raw json.RawMessage, boardID string, now time.Time, users, lanes map[string]string, priorities map[string]bool) (policy, error) {
	const path = "workflow.policy"
	root := readWorkflowFields(raw, path, true, "defaultPriorityId", "createGrants", "moveGrants", "resourceGrants")
	defaultPriority := root.id("defaultPriorityId")
	createInputs := root.array("createGrants", 1000, true)
	moveInputs := root.array("moveGrants", 1000, true)
	resourceInputs := root.array("resourceGrants", 1000, true)
	if !priorities[defaultPriority] {
		root.fail("defaultPriorityId", "must refer to an initial priority")
	}
	if root.err != nil {
		return policy{}, root.err
	}
	result := policy{
		entity:            newEntity(newID(), now),
		BoardID:           boardID,
		DefaultPriorityID: defaultPriority,
		CreateGrants:      make([]createGrant, 0, len(createInputs)),
		MoveGrants:        make([]moveGrant, 0, len(moveInputs)),
		ResourceGrants:    make([]resourceGrant, 0, len(resourceInputs)),
	}
	createSeen := make(map[createGrant]bool, len(createInputs))
	for index, raw := range createInputs {
		input := readWorkflowFields(raw, workflowItem(path+".createGrants", index), true, "userId", "laneId")
		grant := createGrant{UserID: input.id("userId"), LaneID: input.id("laneId")}
		if users[grant.UserID] == "" {
			input.fail("userId", "must refer to an initial user")
		}
		if lanes[grant.LaneID] == "" {
			input.fail("laneId", "must refer to an initial lane")
		}
		if createSeen[grant] {
			input.fail("laneId", "repeats a creation grant")
		}
		if input.err != nil {
			return policy{}, input.err
		}
		createSeen[grant] = true
		result.CreateGrants = append(result.CreateGrants, grant)
	}
	moveSeen := make(map[moveGrant]bool, len(moveInputs))
	for index, raw := range moveInputs {
		input := readWorkflowFields(raw, workflowItem(path+".moveGrants", index), true, "userId", "fromLaneId", "toLaneId")
		grant := moveGrant{UserID: input.id("userId"), FromLaneID: input.id("fromLaneId"), ToLaneID: input.id("toLaneId")}
		if users[grant.UserID] == "" {
			input.fail("userId", "must refer to an initial user")
		}
		if lanes[grant.FromLaneID] == "" {
			input.fail("fromLaneId", "must refer to an initial lane")
		}
		if lanes[grant.ToLaneID] == "" {
			input.fail("toLaneId", "must refer to an initial lane")
		}
		if grant.FromLaneID == grant.ToLaneID {
			input.fail("toLaneId", "must differ from fromLaneId")
		}
		if moveSeen[grant] {
			input.fail("toLaneId", "repeats a move grant")
		}
		if input.err != nil {
			return policy{}, input.err
		}
		moveSeen[grant] = true
		result.MoveGrants = append(result.MoveGrants, grant)
	}
	resourceSeen := make(map[string]bool, len(resourceInputs))
	for index, raw := range resourceInputs {
		input := readWorkflowFields(raw, workflowItem(path+".resourceGrants", index), true, "userId", "capabilities")
		id := input.id("userId")
		if users[id] == "" {
			input.fail("userId", "must refer to an initial user")
		}
		if resourceSeen[id] {
			input.fail("userId", "repeats a resource grant")
		}
		capabilityInputs := input.array("capabilities", 18, true)
		capabilities := make([]string, 0, len(capabilityInputs))
		for i, raw := range capabilityInputs {
			var value string
			key := workflowItem("capabilities", i)
			if json.Unmarshal(raw, &value) != nil || !isInitialCapability(value) {
				input.fail(key, "must be a supported capability")
			}
			if slices.Contains(capabilities, value) {
				input.fail(key, "repeats a capability")
			}
			if value == "approve" && users[id] != "human" {
				input.fail(key, "approval is Human-only")
			}
			capabilities = append(capabilities, value)
		}
		if input.err != nil {
			return policy{}, input.err
		}
		resourceSeen[id] = true
		result.ResourceGrants = append(result.ResourceGrants, resourceGrant{UserID: id, Capabilities: capabilities})
	}
	return result, nil
}

func isInitialCapability(value string) bool {
	switch value {
	case "read", "edit_card", "comment", "archive_card", "manage_relationships", "manage_attachments",
		"set_priority", "approve", "manage_board", "manage_workflow", "manage_permissions",
		"manage_notifications", "backup", "restore", "publish", "close_board", "create_card", "reorder_tickets":
		return true
	default:
		return false
	}
}

// Shorthand permissions are expanded across the initial lanes and merged with explicit grants.
func addInitialLaneGrants(result *policy, lanes []lane, createFor, connectFor [][]string) {
	createSeen := make(map[createGrant]bool, len(result.CreateGrants))
	for _, grant := range result.CreateGrants {
		createSeen[grant] = true
	}
	moveSeen := make(map[moveGrant]bool, len(result.MoveGrants))
	for _, grant := range result.MoveGrants {
		moveSeen[grant] = true
	}

	for index, current := range lanes {
		for _, id := range createFor[index] {
			grant := createGrant{UserID: id, LaneID: current.ID}
			if !createSeen[grant] {
				createSeen[grant] = true
				result.CreateGrants = append(result.CreateGrants, grant)
			}
		}
		for _, id := range connectFor[index] {
			for _, other := range lanes {
				if current.ID == other.ID {
					continue
				}
				for _, grant := range [...]moveGrant{
					{UserID: id, FromLaneID: current.ID, ToLaneID: other.ID},
					{UserID: id, FromLaneID: other.ID, ToLaneID: current.ID},
				} {
					if !moveSeen[grant] {
						moveSeen[grant] = true
						result.MoveGrants = append(result.MoveGrants, grant)
					}
				}
			}
		}
	}
}
