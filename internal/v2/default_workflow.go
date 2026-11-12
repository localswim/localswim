package v2

import (
	"slices"
	"time"
)

func defaultWorkflow(boardID string, now time.Time, principals []user) workflow {
	lanes := []lane{
		{Key: "backlog", Label: "Backlog", Role: "ordinary"},
		{Key: "ready_for_work", Label: "Ready For Work", Role: "ordinary"},
		{Key: "in_progress", Label: "In Progress", Role: "ordinary"},
		{Key: "ready_for_review", Label: "Ready For Review", Role: "ready_for_review"},
		{Key: "completed", Label: "Completed", Role: "completed"},
	}
	priorities := []priority{
		{Key: "P1", Label: "Urgent"},
		{Key: "P2", Label: "Normal"},
		{Key: "P3", Label: "Low"},
	}
	result := newBuiltInWorkflow(boardID, now, principals, lanes, priorities, 1)
	const (
		backlog = iota
		ready
		inProgress
		review
		completed
	)
	commonMoves := [][2]int{
		{backlog, ready}, {ready, backlog}, {ready, inProgress}, {inProgress, ready}, {inProgress, review},
	}
	humanMoves := [][2]int{{backlog, inProgress}, {review, inProgress}, {review, completed}}
	for _, actor := range principals {
		moves := commonMoves
		if actor.Kind == "human" {
			moves = append(slices.Clone(commonMoves), humanMoves...)
		}
		result.addLaneGrants(actor.ID, []int{backlog, ready}, moves)
	}
	return result
}

func newBuiltInWorkflow(boardID string, now time.Time, principals []user, lanes []lane, priorities []priority, defaultRank int) workflow {
	now = canonicalTime(now)
	for index := range lanes {
		lanes[index].ID = newID()
		lanes[index].Revision = 1
		lanes[index].CreatedAt, lanes[index].UpdatedAt = now, now
		lanes[index].BoardID = boardID
		lanes[index].Position = index
	}
	for index := range priorities {
		priorities[index].entity = newEntity(newID(), now)
		priorities[index].BoardID = boardID
		priorities[index].Rank = index
	}
	p := policy{
		entity:            newEntity(newID(), now),
		BoardID:           boardID,
		DefaultPriorityID: priorities[defaultRank].ID,
		CreateGrants:      []createGrant{},
		MoveGrants:        []moveGrant{},
		ResourceGrants:    make([]resourceGrant, 0, len(principals)),
	}
	for _, actor := range principals {
		capabilities := []string{
			"read", "edit_card", "comment", "archive_card", "manage_relationships", "manage_attachments",
			"set_priority", "manage_board", "manage_workflow", "manage_permissions", "manage_notifications",
			"backup", "restore", "publish", "close_board", "create_card", "reorder_tickets",
		}
		if actor.Kind == "human" {
			capabilities = append(capabilities, "approve")
		}
		p.ResourceGrants = append(p.ResourceGrants, resourceGrant{UserID: actor.ID, Capabilities: capabilities})
	}
	return workflow{Users: slices.Clone(principals), Lanes: lanes, Priorities: priorities, Policy: p}
}

func (w *workflow) addLaneGrants(actorID string, create []int, moves [][2]int) {
	for _, target := range create {
		w.Policy.CreateGrants = append(w.Policy.CreateGrants, createGrant{UserID: actorID, LaneID: w.Lanes[target].ID})
	}
	for _, edge := range moves {
		w.Policy.MoveGrants = append(w.Policy.MoveGrants, moveGrant{
			UserID: actorID, FromLaneID: w.Lanes[edge[0]].ID, ToLaneID: w.Lanes[edge[1]].ID,
		})
	}
}
