package v2

import "time"

func terryWorkflow(boardID string, now time.Time, principals []user) workflow {
	const (
		backlog = iota
		ready
		inProgress
		blocked
		needsTerry
		review
		completed
	)
	lanes := []lane{
		{Key: "backlog", Label: "Backlog", Role: "ordinary", ReorderEnabled: true},
		{Key: "ready_for_work", Label: "Ready For Work", Role: "ordinary"},
		{Key: "in_progress", Label: "In Progress", Role: "ordinary"},
		{Key: "blocked", Label: "Blocked", Role: "blocked"},
		{Key: "needs_terry_action", Label: "Needs Terry", Role: "needs_input"},
		{Key: "ready_for_review", Label: "Ready For Review", Role: "ready_for_review", ApprovalEnabled: true},
		{Key: "completed", Label: "Completed", Role: "completed"},
	}
	priorities := []priority{
		{Key: "P0", Label: "On fire"},
		{Key: "P1", Label: "Urgent"},
		{Key: "P2", Label: "High"},
		{Key: "P3", Label: "Normal"},
		{Key: "P4", Label: "Low"},
		{Key: "P5", Label: "Only if idle"},
	}
	humanMoves := [][2]int{
		{backlog, ready}, {backlog, inProgress}, {backlog, needsTerry}, {backlog, blocked},
		{ready, backlog}, {ready, inProgress}, {ready, needsTerry},
		{inProgress, ready}, {inProgress, blocked}, {inProgress, needsTerry}, {inProgress, review},
		{blocked, ready}, {blocked, inProgress}, {blocked, needsTerry},
		{needsTerry, backlog}, {needsTerry, ready}, {needsTerry, inProgress}, {needsTerry, review},
		{review, backlog}, {review, ready}, {review, inProgress}, {review, completed},
	}
	botMoves := [][2]int{
		{backlog, ready}, {backlog, needsTerry}, {backlog, blocked},
		{ready, backlog}, {ready, inProgress}, {ready, blocked}, {ready, needsTerry},
		{inProgress, backlog}, {inProgress, ready}, {inProgress, blocked}, {inProgress, needsTerry}, {inProgress, review},
		{blocked, backlog}, {blocked, ready}, {blocked, inProgress}, {blocked, needsTerry}, {blocked, review},
		{needsTerry, backlog}, {needsTerry, ready}, {needsTerry, inProgress}, {needsTerry, blocked}, {needsTerry, review},
		{review, backlog}, {review, ready}, {review, inProgress}, {review, blocked}, {review, needsTerry},
	}
	result := newBuiltInWorkflow(boardID, now, principals, lanes, priorities, 3)
	for _, actor := range principals {
		moves := botMoves
		if actor.Kind == "human" {
			moves = humanMoves
		}
		result.addLaneGrants(actor.ID, []int{backlog, ready, blocked, needsTerry}, moves)
	}
	return result
}
