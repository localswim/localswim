package v2

import (
	"fmt"
	"slices"
)

// Validate paths after lane identities, references, and the completed role.
func validateWorkflowPaths(lanes []lane, rules policy) *invalidField {
	active := make(map[string]bool, len(lanes))
	var completed string
	for _, lane := range lanes {
		if !lane.Retired {
			active[lane.ID] = true
			if lane.Role == "completed" {
				completed = lane.ID
			}
		}
	}
	creators := make(map[string]bool)
	for _, grant := range rules.ResourceGrants {
		if slices.Contains(grant.Capabilities, "create_card") {
			creators[grant.UserID] = true
		}
	}
	hasCreation := false
	for _, grant := range rules.CreateGrants {
		if creators[grant.UserID] && active[grant.LaneID] {
			hasCreation = true
			break
		}
	}
	if !hasCreation {
		return &invalidField{"policy.create_grants", "At least one active lane must allow card creation."}
	}

	reverse := make(map[string][]string)
	for _, grant := range rules.MoveGrants {
		if active[grant.FromLaneID] && active[grant.ToLaneID] {
			reverse[grant.ToLaneID] = append(reverse[grant.ToLaneID], grant.FromLaneID)
		}
	}
	canComplete := map[string]bool{completed: true}
	queue := []string{completed}
	for head := 0; head < len(queue); head++ {
		for _, from := range reverse[queue[head]] {
			if !canComplete[from] {
				canComplete[from] = true
				queue = append(queue, from)
			}
		}
	}

	for _, lane := range lanes {
		if !lane.Retired && !canComplete[lane.ID] {
			return &invalidField{"policy.move_grants", fmt.Sprintf("Lane %q has no transition path to the completed lane.", lane.Label)}
		}
	}
	return nil
}
