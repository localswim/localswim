package v2

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

func makeV1Workflow(export v1BoardExport, id string, principals []user, now time.Time) (workflow, actorPresentation, map[string]string, error) {
	users, presentation, actors, err := v1Users(export, principals, now)
	if err != nil {
		return workflow{}, actorPresentation{}, nil, err
	}
	lanes := make([]lane, 0, len(export.Policy.Lanes))
	for _, source := range export.Policy.Lanes {
		role := "ordinary"
		switch source.ID {
		case "completed", "blocked", "ready_for_review":
			role = source.ID
		case "needs_terry_action":
			role = "needs_input"
		}
		lanes = append(lanes, lane{Key: source.ID, Label: source.Label, Notes: source.Note, Role: role,
			ApprovalEnabled: source.ApprovalEnabled, ReorderEnabled: source.Reorder})
	}
	priorities := make([]priority, 0, len(export.Policy.Priorities))
	defaultRank := len(export.Policy.Priorities) / 2
	if export.Policy.DefaultPriority != nil {
		defaultRank = slices.IndexFunc(export.Policy.Priorities, func(item v1Priority) bool { return item.ID == *export.Policy.DefaultPriority })
		if defaultRank < 0 {
			return workflow{}, actorPresentation{}, nil, invalid("policy.defaultPriority", "Default priority must name a V1 priority.")
		}
	}
	for _, source := range export.Policy.Priorities {
		priorities = append(priorities, priority{Key: source.ID, Label: source.Label})
	}
	result := newBuiltInWorkflow(id, now, principals, lanes, priorities, defaultRank)
	result.Users = users
	for _, member := range users {
		if member.ID != humanID && member.ID != botID {
			result.Policy.ResourceGrants = append(result.Policy.ResourceGrants, resourceGrant{UserID: member.ID, Capabilities: []string{}})
		}
	}
	if err := translateV1Grants(&result, export.Policy, actors); err != nil {
		return workflow{}, actorPresentation{}, nil, err
	}
	return result, presentation, actors, nil
}

func v1Users(export v1BoardExport, principals []user, now time.Time) ([]user, actorPresentation, map[string]string, error) {
	users := make([]user, 0, len(export.Users))
	var presentation actorPresentation
	actors := make(map[string]string, len(export.Users))
	for _, source := range export.Users {
		if source.ID == "" || actors[source.ID] != "" || source.Label == "" || source.Color == "" ||
			(source.Class != "human" && source.Class != "bot") {
			return nil, actorPresentation{}, nil, fmt.Errorf("V1 user %q has invalid or repeated identity metadata", source.ID)
		}
		member := user{ID: newID(), Revision: 1, CreatedAt: now, Label: source.Label, Kind: source.Class, Color: source.Color}
		switch source.ID {
		case export.BrowserUser:
			if source.Class != "human" {
				return nil, actorPresentation{}, nil, invalid("browserUser", "Expected a Human V1 user.")
			}
			member = principals[0]
			presentation.Human = actorDecoration{Label: source.Label, Accent: source.Color}
		case export.CLIUser:
			if source.Class != "bot" {
				return nil, actorPresentation{}, nil, invalid("cliUser", "Expected a Bot V1 user.")
			}
			member = principals[1]
			presentation.Bot = actorDecoration{Label: source.Label, Accent: source.Color}
		}
		users = append(users, member)
		actors[source.ID] = member.ID
	}
	if actors[export.BrowserUser] != humanID || actors[export.CLIUser] != botID || actors[export.DefaultOwner] == "" {
		return nil, actorPresentation{}, nil, invalid("users", "The V1 browser, CLI and default owner identities must be present.")
	}
	if !presentation.valid() {
		return nil, actorPresentation{}, nil, invalid("users", "V1 principal labels and colors must fit V2 board actor presentation.")
	}
	return users, presentation, actors, nil
}

func translateV1Grants(result *workflow, source v1Policy, actors map[string]string) error {
	laneIDs := make(map[string]string, len(result.Lanes))
	for index, item := range result.Lanes {
		if item.Key == "" || laneIDs[item.Key] != "" || source.Lanes[index].Create == nil {
			return fmt.Errorf("V1 lane %q has an invalid or repeated key or missing creation grants", item.Key)
		}
		laneIDs[item.Key] = item.ID
		seen := make(map[string]bool)
		for _, actor := range source.Lanes[index].Create {
			if actors[actor] == "" {
				return fmt.Errorf("V1 lane %q has a creation grant for unknown user %q", item.Key, actor)
			}
			if !seen[actor] {
				result.Policy.CreateGrants = append(result.Policy.CreateGrants, createGrant{UserID: actors[actor], LaneID: item.ID})
				seen[actor] = true
			}
		}
	}
	retired := make(map[string]bool, len(source.RetiredLanes))
	for _, key := range source.RetiredLanes {
		if key == "completed" || laneIDs[key] == "" || retired[key] {
			return fmt.Errorf("V1 policy has an invalid or repeated retired lane %q", key)
		}
		retired[key] = true
	}
	for _, actor := range slices.Sorted(maps.Keys(source.Edges)) {
		if actors[actor] == "" || source.Edges[actor] == nil {
			return fmt.Errorf("V1 move grants are missing or name unknown user %q", actor)
		}
		for _, edge := range source.Edges[actor] {
			if laneIDs[edge.From] == "" || laneIDs[edge.To] == "" {
				return fmt.Errorf("V1 move grant for %q names an unknown lane", actor)
			}
			result.Policy.MoveGrants = append(result.Policy.MoveGrants, moveGrant{
				UserID: actors[actor], FromLaneID: laneIDs[edge.From], ToLaneID: laneIDs[edge.To],
			})
		}
	}
	return nil
}
