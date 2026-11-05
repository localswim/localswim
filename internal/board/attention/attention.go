// Package attention projects current category assignments and committed card
// counts inside a caller-owned, authorized read transaction.
package attention

import (
	"errors"
	"sort"

	"github.com/localswim/localswim/internal/board/policy"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

// Context describes an already validated policy without allocating an identity
// or changing the exact optional representation of that policy.
func Context(w *A.Workflow, version A.PolicyVersion) T.AttentionContext {
	out := T.AttentionContext{PolicyVersion: version, Profile: "default", Source: "implicit", Assignments: A.ResolveWorkflowAttention(w), Lanes: []T.AttentionLane{}}
	if w != nil {
		id := w.WorkflowID
		out.WorkflowID = &id
		out.Profile = w.Profile
		if out.Profile == "" {
			out.Profile = "configured"
		}
		if w.Attention != nil {
			out.Source = "explicit"
		}
	}
	assigned := map[A.Lane]bool{}
	for _, lanes := range [][]A.Lane{out.Assignments.Blocked, out.Assignments.NeedsTerry, out.Assignments.ReadyForReview} {
		for _, lane := range lanes {
			assigned[lane] = true
		}
	}
	if w == nil {
		out.Lanes = append(out.Lanes, T.AttentionLane{ID: "Ready for Review", Label: "Ready for Review", Role: "review"})
	} else {
		for _, lane := range w.Lanes {
			if assigned[A.Lane(lane.ID)] {
				out.Lanes = append(out.Lanes, T.AttentionLane{ID: A.Lane(lane.ID), Label: lane.Label, Role: lane.Role})
			}
		}
	}
	sort.Slice(out.Lanes, func(i, j int) bool { return out.Lanes[i].ID < out.Lanes[j].ID })
	return out
}

// Count uses current committed, unarchived identities only. A disabled measure
// stays not_configured; a configured measure with no occupants is known zero.
func Count(r writer.Read, inc string, w *A.Workflow) (T.CatalogAttention, error) {
	var out T.CatalogAttention
	if err := A.ValidateWorkflow(w); err != nil {
		return out, err
	}
	rows, err := r.Query("SELECT lane,count(DISTINCT card_id) FROM card WHERE incarnation=? AND archived=0 GROUP BY lane", inc)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	counts := map[A.Lane]uint64{}
	for rows.Next() {
		var lane A.Lane
		var count uint64
		if err = rows.Scan(&lane, &count); err != nil {
			return out, errors.Join(err, rows.Close())
		}
		counts[lane] = count
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	assignments := A.ResolveWorkflowAttention(w)
	for _, category := range []struct {
		lanes []A.Lane
		count *T.CatalogCount
	}{{assignments.Blocked, &out.Blocked}, {assignments.NeedsTerry, &out.NeedsTerry}, {assignments.ReadyForReview, &out.ReadyForReview}} {
		category.count.State = "not_configured"
		if len(category.lanes) == 0 {
			continue
		}
		var total uint64
		for _, lane := range category.lanes {
			if counts[lane] > contract.MaxInteger-total {
				return T.CatalogAttention{}, errors.New("attention count overflow")
			}
			total += counts[lane]
		}
		value := A.Number(total)
		category.count.State = "known"
		category.count.Value = &value
	}
	return out, nil
}

// ReadCurrent requires the caller to establish current board-channel read
// authority and keep the supplied read transaction open through this call.
func ReadCurrent(r writer.Read, boardID, inc string) (T.BoardAttention, error) {
	var out T.BoardAttention
	var actualBoard, actualInc string
	var revision, version uint64
	if err := r.Scan("SELECT b.board_id,b.incarnation,b.revision,p.policy_version FROM board b JOIN policy p ON p.incarnation=b.incarnation WHERE b.singleton=1", nil, &actualBoard, &actualInc, &revision, &version); err != nil {
		return out, err
	}
	if actualBoard != boardID || actualInc != inc || revision > contract.MaxInteger || version < 1 || version > contract.MaxInteger {
		return out, errors.New("attention board identity or revision mismatch")
	}
	w, err := policy.Load(r, inc)
	if err != nil {
		return out, err
	}
	if w != nil {
		if err = policy.ValidateCurrent(r, inc, w); err != nil {
			return out, err
		}
	}
	counts, err := Count(r, inc, w)
	if err != nil {
		return out, err
	}
	return T.BoardAttention{APIVersion: 1, BoardID: A.UUID(boardID), Incarnation: A.UUID(inc), Revision: A.Number(revision), AttentionContext: Context(w, A.PolicyVersion(version)), Attention: counts}, nil
}
