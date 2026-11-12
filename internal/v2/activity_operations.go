package v2

type activityOperationGroup uint8

const (
	activityCreated activityOperationGroup = iota
	activityContentsModified
	activityCommentAdded
	activityTicketsReordered
	activityMovedLanes
	activityReviewedApproved
	activityArchived
	activityRestored
)

// The order is fixed across notices, regardless of the order of edits.
var activityOperationLabels = [...]string{
	activityCreated:          "Created",
	activityContentsModified: "Contents modified",
	activityCommentAdded:     "Comment added",
	activityTicketsReordered: "Ticket numbers reordered",
	activityMovedLanes:       "Moved lanes",
	activityReviewedApproved: "Reviewed & approved",
	activityArchived:         "Archived",
	activityRestored:         "Restored",
}

func isApprovalComment(previous, event activity) bool {
	// Verified approval audit always places each receipt comment immediately
	// after its paired move in the same revision, including bulk approval.
	return previous.Revision == event.Revision && previous.ActorID == event.ActorID &&
		previous.ResourceType == "card" && previous.Operation == approvalOperation &&
		event.ResourceType == "comment" && event.Operation == "comment"
}

// classifyActivityOperation is used only for verified events with card references.
func classifyActivityOperation(event activity, approvalComment bool) activityOperationGroup {
	switch event.ResourceType {
	case "comment":
		if approvalComment {
			return activityReviewedApproved
		}
		return activityCommentAdded
	case "card":
		switch event.Operation {
		case "create":
			return activityCreated
		case ticketOrderOperation:
			return activityTicketsReordered
		case approvalOperation:
			return activityReviewedApproved
		case reopenOperation:
			return activityMovedLanes
		case "patch":
			switch snapshotString(event.Field) {
			case "lane_id":
				return activityMovedLanes
			case "archived":
				if snapshotString(event.After) == "true" {
					return activityArchived
				}
				return activityRestored
			}
		}
	}
	return activityContentsModified
}
