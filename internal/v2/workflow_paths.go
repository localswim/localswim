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
	starts := make([]string, 0)
	for _, grant := range rules.CreateGrants {
		if creators[grant.UserID] && active[grant.LaneID] {
			starts = append(starts, grant.LaneID)
		}
	}
	if len(starts) == 0 {
		return &invalidField{"policy.create_grants", "At least one active lane must allow card creation."}
	}

	forward := make(map[string][]string)
	reverse := make(map[string][]string)
	for _, grant := range rules.MoveGrants {
		if active[grant.FromLaneID] && active[grant.ToLaneID] {
			forward[grant.FromLaneID] = append(forward[grant.FromLaneID], grant.ToLaneID)
			reverse[grant.ToLaneID] = append(reverse[grant.ToLaneID], grant.FromLaneID)
		}
	}
	reachable := reachableWorkflowLanes(starts, forward)
	canComplete := reachableWorkflowLanes([]string{completed}, reverse)
	for _, lane := range lanes {
		if reachable[lane.ID] && !canComplete[lane.ID] {
			return &invalidField{"policy.move_grants", fmt.Sprintf("Lane %q has no transition path to the completed lane.", lane.Label)}
		}
	}
	return nil
}

func reachableWorkflowLanes(starts []string, edges map[string][]string) map[string]bool {
	visited := make(map[string]bool)
	queue := make([]string, 0, len(starts))
	for _, id := range starts {
		if !visited[id] {
			visited[id] = true
			queue = append(queue, id)
		}
	}
	for head := 0; head < len(queue); head++ {
		for _, next := range edges[queue[head]] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return visited
}
