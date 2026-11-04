package cards

import (
	"errors"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

// Generic configured moves are a distinct input from legacy start. Admission
// happens after current authority, identity, revision and terminal checks.
func admitMove(r writer.Read, w *A.Workflow, e envelope, id, lane string, archived int) (string, error) {
	if w == nil || e.Payload.LaneID == nil {
		return "INVALID_INPUT", nil
	}
	if e.Payload.Title != nil || e.Payload.Description != nil || e.Payload.Priority != nil || e.Payload.Lane != nil || e.Payload.Text != nil || e.Payload.Archived != nil || e.Payload.Parent.Present || e.Payload.OwnerID.Present || e.Payload.Relationships != nil {
		return "INVALID_INPUT", nil
	}
	to := A.Lane(*e.Payload.LaneID)
	role := A.WorkflowRole(w, to)
	if role == "" {
		return "INVALID_INPUT", nil
	}
	if archived != 0 || role == "review" || role == "completed" {
		return "INVALID_TRANSITION", nil
	}
	if lane == string(to) {
		return "NO_CHANGE", nil
	}
	var kind string
	if err := r.Scan("SELECT kind FROM actor WHERE incarnation=? AND actor_id=?", []any{e.Incarnation, e.ActorID}, &kind); err != nil {
		return "", err
	}
	if !A.WorkflowAllows(w, A.Lane(lane), to, A.Kind(kind)) {
		return "INVALID_TRANSITION", nil
	}
	if role == "in_progress" {
		ok, err := prerequisitesApproved(r, e.Incarnation, id, string(A.WorkflowLaneForRole(w, "completed")))
		if err != nil {
			return "", err
		}
		if !ok {
			return "PREREQUISITE_NOT_APPROVED", nil
		}
	}
	return "", nil
}

// Only active source-owned blocking links gate entry to in_progress. The
// peer's lane, nonarchive state and current human approval must all agree.
func prerequisitesApproved(r writer.Read, inc, id, completed string) (bool, error) {
	if completed == "" {
		return false, errors.New("completed role unavailable")
	}
	var invalid int
	err := r.Scan(`SELECT count(*) FROM link l LEFT JOIN card c ON c.incarnation=l.incarnation AND c.card_id=l.internal_target_id LEFT JOIN approval a ON a.incarnation=c.incarnation AND a.approval_id=c.current_approval_id AND a.card_id=c.card_id LEFT JOIN actor h ON h.incarnation=a.incarnation AND h.actor_id=a.actor_id JOIN policy p ON p.incarnation=l.incarnation WHERE l.incarnation=? AND l.card_id=? AND l.active=1 AND l.kind='blocking' AND (c.card_id IS NULL OR c.lane<>? OR c.archived<>0 OR a.approval_id IS NULL OR h.kind<>'human' OR a.policy_version<>p.policy_version OR a.reviewed_card_revision<>c.revision-1)`, []any{inc, id, completed}, &invalid)
	return invalid == 0, err
}
