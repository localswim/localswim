// Package policyedit changes complete workflow policies through the single writer.
package policyedit

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/policy"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

type Route struct{ BoardID, Incarnation string }
type Service struct{ grants *grants.Service }

func New(g *grants.Service) *Service { return &Service{grants: g} }
func deny(code string) *T.RefusalResult {
	return &T.RefusalResult{Error: T.Error{Code: code, Message: "Workflow change refused"}}
}

type envelope struct {
	BoardID, Incarnation, ActorID, OpID string
	Command                             contract.Command
	Target                              *contract.Target
	ExpectedRevision                    uint64
	ExpectedCardRevision                *uint64
	Payload                             struct {
		ExpectedPolicyVersion uint64
		Workflow              json.RawMessage
	}
}

// DefaultDefinition is a comparison-only projection of implicit policy1. Its
// supplied draft identity is never allocated or installed by a read.
func DefaultDefinition(id A.UUID, revision A.PolicyVersion) A.Workflow {
	w := A.Workflow{Profile: "default", WorkflowID: id, Revision: revision,
		ActorRights: A.WorkflowActorRights{Human: []string{"approve", "attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}, Automation: []string{"attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}}}
	for _, v := range []struct{ id, role string }{{"Ready for Work", "initial"}, {"In Progress", "in_progress"}, {"Ready for Review", "review"}, {"Completed", "completed"}} {
		w.Lanes = append(w.Lanes, A.WorkflowLane{ID: A.UUID(v.id), Label: v.id, Role: v.role})
	}
	w.Transitions = []A.WorkflowTransition{
		{From: "In Progress", To: "Ready for Review", Actors: []A.Kind{"automation", "human"}},
		{From: "Ready for Review", To: "Completed", Actors: []A.Kind{"human"}},
		{From: "Ready for Work", To: "In Progress", Actors: []A.Kind{"automation", "human"}},
	}
	return w
}

// Preview must run inside one caller-owned authorized reader snapshot.
func (s *Service) Preview(ctx context.Context, r writer.Read, actor string, req T.WorkflowPreviewRequest) (T.WorkflowPreview, *T.RefusalResult, error) {
	if s == nil || s.grants == nil || s.grants.Require(ctx, r, string(req.BoardID), string(req.Incarnation), actor, "read", "edit") != nil {
		return T.WorkflowPreview{}, deny("FORBIDDEN"), nil
	}
	return inspect(r, req, true)
}

func inspect(r writer.Read, req T.WorkflowPreviewRequest, checkRevision bool) (T.WorkflowPreview, *T.RefusalResult, error) {
	var out T.WorkflowPreview
	var board, inc string
	var revision, version uint64
	if err := r.Scan("SELECT b.board_id,b.incarnation,b.revision,p.policy_version FROM board b JOIN policy p ON p.incarnation=b.incarnation WHERE b.singleton=1", nil, &board, &inc, &revision, &version); err != nil {
		return out, nil, err
	}
	if board != string(req.BoardID) || inc != string(req.Incarnation) {
		return out, deny("INVALID_INPUT"), nil
	}
	if checkRevision && revision != uint64(req.ExpectedRevision) {
		return out, deny("REVISION_CONFLICT"), nil
	}
	if checkRevision && revision >= contract.MaxInteger {
		return out, deny("COUNTER_OVERFLOW"), nil
	}
	if version != uint64(req.ExpectedPolicyVersion) {
		return out, deny("POLICY_CONFLICT"), nil
	}
	if version >= contract.MaxInteger {
		return out, deny("COUNTER_OVERFLOW"), nil
	}
	if req.APIVersion != 1 || uint64(req.Workflow.Revision) != version+1 || A.ValidateWorkflow(&req.Workflow) != nil {
		return out, deny("INVALID_INPUT"), nil
	}
	current, err := policy.Load(r, inc)
	if err != nil {
		return out, nil, err
	}
	history, err := policy.LoadHistory(r, inc)
	if err != nil {
		return out, nil, err
	}
	if uint64(len(history)) != version {
		return out, nil, errors.New("invalid current policy pointer")
	}
	if current != nil {
		if err = policy.ValidateCurrent(r, inc, current); err != nil {
			return out, nil, err
		}
	}
	baseline := DefaultDefinition(req.Workflow.WorkflowID, A.PolicyVersion(version))
	if current != nil {
		baseline = *current
	}
	if req.Workflow.Profile != baseline.Profile || current != nil && req.Workflow.WorkflowID != baseline.WorkflowID {
		return out, deny("INVALID_INPUT"), nil
	}
	oldLanes := map[A.UUID]A.WorkflowLane{}
	newLanes := map[A.UUID]A.WorkflowLane{}
	for _, lane := range baseline.Lanes {
		oldLanes[lane.ID] = lane
	}
	for _, lane := range req.Workflow.Lanes {
		newLanes[lane.ID] = lane
	}
	for _, lane := range baseline.Lanes {
		replacement, ok := newLanes[lane.ID]
		if !ok && lane.Role != "waiting" || ok && replacement.Role != lane.Role {
			return out, deny("INVALID_INPUT"), nil
		}
	}
	ever := map[A.UUID]bool{}
	for _, record := range history {
		if record.Workflow != nil {
			for _, lane := range record.Workflow.Lanes {
				ever[lane.ID] = true
			}
		}
	}
	changes := T.WorkflowChanges{Renamed: []T.WorkflowRename{}, Added: []A.WorkflowLane{}, Retired: []A.WorkflowLane{}}
	for _, lane := range req.Workflow.Lanes {
		old, exists := oldLanes[lane.ID]
		if !exists {
			if ever[lane.ID] || lane.Role != "waiting" {
				return out, deny("INVALID_INPUT"), nil
			}
			changes.Added = append(changes.Added, lane)
		} else if old.Label != lane.Label {
			changes.Renamed = append(changes.Renamed, T.WorkflowRename{LaneID: A.Lane(lane.ID), Before: old.Label, After: lane.Label})
		}
	}
	for _, lane := range baseline.Lanes {
		if _, ok := newLanes[lane.ID]; !ok {
			changes.Retired = append(changes.Retired, lane)
		}
	}
	sort.Slice(changes.Renamed, func(i, j int) bool { return changes.Renamed[i].LaneID < changes.Renamed[j].LaneID })
	oldOrder, newOrder := []A.UUID{}, []A.UUID{}
	for _, lane := range baseline.Lanes {
		oldOrder = append(oldOrder, lane.ID)
	}
	for _, lane := range req.Workflow.Lanes {
		newOrder = append(newOrder, lane.ID)
	}
	changes.OrderChanged = !reflect.DeepEqual(oldOrder, newOrder)
	changes.TransitionsChanged = !reflect.DeepEqual(baseline.Transitions, req.Workflow.Transitions)
	changes.Rights = T.WorkflowActorRightsChange{Human: rightChanges(baseline.ActorRights.Human, req.Workflow.ActorRights.Human), Automation: rightChanges(baseline.ActorRights.Automation, req.Workflow.ActorRights.Automation)}
	occupancy := []T.WorkflowOccupancy{}
	for _, lane := range baseline.Lanes {
		var all, archived uint64
		if err = r.Scan("SELECT count(*),coalesce(sum(archived),0) FROM card WHERE incarnation=? AND lane=?", []any{inc, string(lane.ID)}, &all, &archived); err != nil {
			return out, nil, err
		}
		if _, ok := newLanes[lane.ID]; !ok && all != 0 {
			return out, deny("INVALID_INPUT"), nil
		}
		occupancy = append(occupancy, T.WorkflowOccupancy{LaneID: A.Lane(lane.ID), CurrentCards: A.Number(all), ArchivedCards: A.Number(archived)})
	}
	sort.Slice(occupancy, func(i, j int) bool { return occupancy[i].LaneID < occupancy[j].LaneID })
	proposed := req.Workflow
	proposed.Revision = baseline.Revision
	changed := !reflect.DeepEqual(baseline, proposed)
	after := history
	next := version
	if changed {
		next++
		after = append(append([]A.WorkflowPolicy(nil), history...), A.WorkflowPolicy{PolicyVersion: A.PolicyVersion(next), Workflow: &req.Workflow})
	}
	bytes, err := A.Canonical(after)
	if err != nil {
		return out, nil, err
	}
	if changed && (len(after) > 128 || len(bytes) > 8388608) {
		return out, deny("INVALID_INPUT"), nil
	}
	if changed && current == nil {
		if err = policy.ValidateCurrent(r, inc, nil); err != nil {
			return out, deny("WORKFLOW_INCOMPATIBLE"), nil
		}
	}
	var preserved uint64
	if err = r.Scan("SELECT count(*) FROM card WHERE incarnation=? AND current_approval_id IS NOT NULL", []any{inc}, &preserved); err != nil {
		return out, nil, err
	}
	out = T.WorkflowPreview{APIVersion: 1, BoardID: req.BoardID, Incarnation: req.Incarnation, Revision: A.Number(revision), PolicyVersion: A.PolicyVersion(version), CurrentWorkflow: current, Candidate: req.Workflow, WouldChange: changed, NextPolicyVersion: A.PolicyVersion(next), Changes: changes, Occupancy: occupancy, Approval: T.WorkflowApproval{PreservedCurrentCount: A.Number(preserved), Rule: "retained-issuing-policy"}, Capacity: T.WorkflowCapacity{PolicyRecords: A.Number(len(history)), PolicyRecordsAfter: A.Number(len(after)), CanonicalPolicyBytesAfter: A.Number(len(bytes)), MaxPolicyRecords: 128, MaxCanonicalPolicyBytes: 8388608}}
	return out, nil, nil
}
func rightChanges(before, after []string) T.WorkflowRightsChange {
	out := T.WorkflowRightsChange{Added: []string{}, Removed: []string{}}
	b, a := map[string]bool{}, map[string]bool{}
	for _, v := range before {
		b[v] = true
	}
	for _, v := range after {
		a[v] = true
		if !b[v] {
			out.Added = append(out.Added, v)
		}
	}
	for _, v := range before {
		if !a[v] {
			out.Removed = append(out.Removed, v)
		}
	}
	return out
}

func request(i contract.Intent) (envelope, T.WorkflowPreviewRequest, error) {
	var e envelope
	err := json.Unmarshal(i.CanonicalBytes(), &e)
	req := T.WorkflowPreviewRequest{APIVersion: 1, BoardID: A.UUID(e.BoardID), Incarnation: A.UUID(e.Incarnation), ExpectedRevision: A.Number(e.ExpectedRevision), ExpectedPolicyVersion: A.PolicyVersion(e.Payload.ExpectedPolicyVersion)}
	if err == nil {
		err = json.Unmarshal(e.Payload.Workflow, &req.Workflow)
	}
	return e, req, err
}

func (s *Service) Callbacks(route Route) writer.Callbacks {
	if s == nil || s.grants == nil {
		return writer.Callbacks{}
	}
	authority := func(ctx context.Context, r writer.Read, req writer.AuthorityRequest, purpose writer.Purpose) error {
		if err := s.grants.Authority(ctx, r, req, purpose); err != nil {
			return err
		}
		if purpose == writer.Submission {
			return s.grants.Require(ctx, r, req.Identity.BoardID, req.Identity.Incarnation, req.Identity.ActorID, "edit")
		}
		return nil
	}
	admit := func(ctx context.Context, r writer.Read, i contract.Intent) (*T.RefusalResult, error) {
		e, req, err := request(i)
		if err != nil || e.Command != "workflow.edit" || e.BoardID != route.BoardID || e.Incarnation != route.Incarnation || e.Target != nil || e.ExpectedCardRevision != nil {
			return deny("INVALID_INPUT"), nil
		}
		if d, err := s.grants.Admit(ctx, r, i); d != nil || err != nil {
			return d, err
		}
		out, d, err := inspect(r, req, false)
		if d != nil || err != nil {
			return d, err
		}
		if !out.WouldChange {
			return deny("NO_CHANGE"), nil
		}
		var next uint64
		if err = r.Scan("SELECT next_event FROM board WHERE singleton=1", nil, &next); err != nil {
			return nil, err
		}
		if next >= contract.MaxInteger {
			return deny("COUNTER_OVERFLOW"), nil
		}
		return nil, nil
	}
	apply := func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		if d, err := admit(ctx, w, i); d != nil || err != nil {
			return writer.Effect{}, errors.New("workflow admission changed")
		}
		e, req, err := request(i)
		if err != nil {
			return writer.Effect{}, err
		}
		raw, err := A.WorkflowBytes(&req.Workflow)
		if err != nil {
			return writer.Effect{}, err
		}
		if _, err = w.Exec("INSERT INTO workflow_policy(incarnation,policy_version,workflow_id,definition) VALUES(?,?,?,?)", e.Incarnation, uint64(req.Workflow.Revision), string(req.Workflow.WorkflowID), string(raw)); err != nil {
			return writer.Effect{}, err
		}
		if _, err = w.Exec("UPDATE policy SET policy_version=? WHERE incarnation=?", uint64(req.Workflow.Revision), e.Incarnation); err != nil {
			return writer.Effect{}, err
		}
		var next, revision uint64
		if err = w.Scan("SELECT next_event,revision FROM board WHERE singleton=1", nil, &next, &revision); err != nil {
			return writer.Effect{}, err
		}
		refRevision := A.Number(req.Workflow.Revision)
		refs := []A.FactRef{{Kind: "workflow", ID: req.Workflow.WorkflowID, Revision: &refRevision}}
		affected, err := json.Marshal(refs)
		if err != nil {
			return writer.Effect{}, err
		}
		if _, err = w.Exec("UPDATE board SET next_event=next_event+1 WHERE singleton=1"); err != nil {
			return writer.Effect{}, err
		}
		if _, err = w.Exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,?,?,?,?,?,?,?)", e.Incarnation, e.BoardID, e.Incarnation, next, e.ActorID, e.OpID, "workflow.edit", revision, string(at), string(affected)); err != nil {
			return writer.Effect{}, err
		}
		result, err := json.Marshal(struct {
			WorkflowID    A.UUID          `json:"workflowId"`
			PolicyVersion A.PolicyVersion `json:"policyVersion"`
		}{req.Workflow.WorkflowID, req.Workflow.Revision})
		return writer.Effect{Result: result, References: refs}, err
	}
	return writer.Callbacks{Authority: authority, Admit: admit, Apply: apply}
}
