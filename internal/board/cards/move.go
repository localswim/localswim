package cards

import (
	"errors"
	"github.com/localswim/localswim/internal/board/policy"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

// Explicit workflow moves are a distinct input from legacy start. Admission
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
	rows, err := r.Query("SELECT coalesce(c.card_id,''),coalesce(c.lane,''),coalesce(c.archived,0) FROM link l LEFT JOIN card c ON c.incarnation=l.incarnation AND c.card_id=l.internal_target_id WHERE l.incarnation=? AND l.card_id=? AND l.active=1 AND l.kind='blocking'", inc, id)
	if err != nil {
		return false, err
	}
	type peer struct {
		id, lane string
		archived int
	}
	var peers []peer
	for rows.Next() {
		var p peer
		if err = rows.Scan(&p.id, &p.lane, &p.archived); err != nil {
			rows.Close()
			return false, err
		}
		peers = append(peers, p)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	wf, err := policy.Load(r, inc)
	if err != nil {
		return false, err
	}
	for _, p := range peers {
		if p.lane != completed || p.archived != 0 {
			return false, nil
		}
		valid, err := policy.CurrentApprovalValid(r, inc, p.id, wf)
		if err != nil || !valid {
			return false, err
		}
	}
	return true, nil
}
