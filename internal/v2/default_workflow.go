package v2

import (
	"slices"
	"time"
)

func defaultWorkflow(boardID string, now time.Time, principals []user) workflow {
	now = canonicalTime(now)
	lanes := []lane{
		{Key: "backlog", Label: "Backlog", Role: "ordinary"},
		{Key: "ready_for_work", Label: "Ready For Work", Role: "ordinary"},
		{Key: "in_progress", Label: "In Progress", Role: "ordinary"},
		{Key: "ready_for_review", Label: "Ready For Review", Role: "ready_for_review"},
		{Key: "completed", Label: "Completed", Role: "completed"},
	}
	for index := range lanes {
		lanes[index].ID = newID()
		lanes[index].Revision = 1
		lanes[index].CreatedAt, lanes[index].UpdatedAt = now, now
		lanes[index].BoardID = boardID
		lanes[index].Position = index
	}
	priorities := []priority{
		{Key: "P1", Label: "Urgent"},
		{Key: "P2", Label: "Normal"},
		{Key: "P3", Label: "Low"},
	}
	for index := range priorities {
		priorities[index].entity = newEntity(newID(), now)
		priorities[index].BoardID = boardID
		priorities[index].Rank = index
	}
	p := policy{
		entity:            newEntity(newID(), now),
		BoardID:           boardID,
		DefaultPriorityID: priorities[1].ID,
		CreateGrants:      make([]createGrant, 0, len(principals)*2),
		MoveGrants:        make([]moveGrant, 0, len(principals)*8),
		ResourceGrants:    make([]resourceGrant, 0, len(principals)),
	}
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
		capabilities := []string{
			"read", "edit_card", "comment", "archive_card", "manage_relationships", "manage_attachments",
			"set_priority", "manage_board", "manage_workflow", "manage_permissions", "manage_notifications",
			"backup", "restore", "publish", "close_board", "create_card", "reorder_tickets",
		}
		moves := commonMoves
		if actor.Kind == "human" {
			moves = append(slices.Clone(commonMoves), humanMoves...)
			capabilities = append(capabilities, "approve")
		}
		p.ResourceGrants = append(p.ResourceGrants, resourceGrant{UserID: actor.ID, Capabilities: capabilities})
		for _, target := range []int{backlog, ready} {
			p.CreateGrants = append(p.CreateGrants, createGrant{UserID: actor.ID, LaneID: lanes[target].ID})
		}
		for _, edge := range moves {
			p.MoveGrants = append(p.MoveGrants, moveGrant{
				UserID: actor.ID, FromLaneID: lanes[edge[0]].ID, ToLaneID: lanes[edge[1]].ID,
			})
		}
	}
	return workflow{Users: slices.Clone(principals), Lanes: lanes, Priorities: priorities, Policy: p}
}
