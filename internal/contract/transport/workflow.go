package transport

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	"reflect"
	"sort"
)

type WorkflowPreviewRequest struct {
	APIVersion            A.Version       `json:"apiVersion"`
	BoardID               A.UUID          `json:"boardId"`
	Incarnation           A.UUID          `json:"incarnation"`
	ExpectedRevision      A.Number        `json:"expectedRevision"`
	ExpectedPolicyVersion A.PolicyVersion `json:"expectedPolicyVersion"`
	Workflow              A.Workflow      `json:"workflow"`
}
type WorkflowRename struct {
	LaneID A.Lane `json:"laneId"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type WorkflowRightsChange struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}
type WorkflowActorRightsChange struct {
	Human      WorkflowRightsChange `json:"human"`
	Automation WorkflowRightsChange `json:"automation"`
}
type WorkflowChanges struct {
	Renamed            []WorkflowRename          `json:"renamed"`
	OrderChanged       bool                      `json:"orderChanged"`
	Added              []A.WorkflowLane          `json:"added"`
	Retired            []A.WorkflowLane          `json:"retired"`
	TransitionsChanged bool                      `json:"transitionsChanged"`
	Rights             WorkflowActorRightsChange `json:"rights"`
}
type WorkflowOccupancy struct {
	LaneID        A.Lane   `json:"laneId"`
	CurrentCards  A.Number `json:"currentCards"`
	ArchivedCards A.Number `json:"archivedCards"`
}
type WorkflowApproval struct {
	PreservedCurrentCount A.Number `json:"preservedCurrentCount"`
	Rule                  string   `json:"rule"`
}
type WorkflowCapacity struct {
	PolicyRecords             A.Number `json:"policyRecords"`
	PolicyRecordsAfter        A.Number `json:"policyRecordsAfter"`
	CanonicalPolicyBytesAfter A.Number `json:"canonicalPolicyBytesAfter"`
	MaxPolicyRecords          A.Number `json:"maxPolicyRecords"`
	MaxCanonicalPolicyBytes   A.Number `json:"maxCanonicalPolicyBytes"`
}
type WorkflowPreview struct {
	APIVersion        A.Version           `json:"apiVersion"`
	BoardID           A.UUID              `json:"boardId"`
	Incarnation       A.UUID              `json:"incarnation"`
	Revision          A.Number            `json:"revision"`
	PolicyVersion     A.PolicyVersion     `json:"policyVersion"`
	CurrentWorkflow   *A.Workflow         `json:"currentWorkflow,omitempty"`
	Candidate         A.Workflow          `json:"candidate"`
	WouldChange       bool                `json:"wouldChange"`
	NextPolicyVersion A.PolicyVersion     `json:"nextPolicyVersion"`
	Changes           WorkflowChanges     `json:"changes"`
	Occupancy         []WorkflowOccupancy `json:"occupancy"`
	Approval          WorkflowApproval    `json:"approval"`
	Capacity          WorkflowCapacity    `json:"capacity"`
}

func DecodeWorkflowPreviewRequest(data []byte) (WorkflowPreviewRequest, error) {
	if len(data) > 800000 {
		return WorkflowPreviewRequest{}, fmt.Errorf("workflow preview exceeds limit")
	}
	return Decode[WorkflowPreviewRequest](data)
}

func readPolicyVersion(w *A.Workflow) A.PolicyVersion {
	if w == nil {
		return 1
	}
	return w.Revision
}
func validateHistoryWorkflow(w *A.Workflow, policies []A.WorkflowPolicy, records []A.Snapshot) error {
	for _, r := range records {
		if r.State != nil {
			v := A.PolicyVersion(1)
			if r.State.WorkflowPolicyVersion != nil {
				v = *r.State.WorkflowPolicyVersion
			}
			policy, err := A.WorkflowAtVersion(w, policies, v)
			if err != nil {
				return err
			}
			if err = A.ValidateWorkflowLanes(policy, r.State.Lane); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateWorkflowPreview(v WorkflowPreview) error {
	if v.Approval.Rule != "retained-issuing-policy" || v.Capacity.MaxPolicyRecords != 128 || v.Capacity.MaxCanonicalPolicyBytes != 8388608 || v.Capacity.PolicyRecords != A.Number(v.PolicyVersion) || v.Capacity.CanonicalPolicyBytesAfter == 0 || v.Capacity.PolicyRecords < 1 || v.Capacity.PolicyRecords > 128 || v.Capacity.PolicyRecordsAfter < v.Capacity.PolicyRecords || v.Capacity.PolicyRecordsAfter > 128 || v.Capacity.CanonicalPolicyBytesAfter > 8388608 {
		return fmt.Errorf("invalid workflow preview capacity")
	}
	if readPolicyVersion(v.CurrentWorkflow) != v.PolicyVersion || v.Candidate.Revision != v.PolicyVersion+1 {
		return fmt.Errorf("workflow preview version mismatch")
	}
	baseline := workflowPreviewBaseline(v.CurrentWorkflow, v.Candidate.WorkflowID)
	if baseline.Profile != v.Candidate.Profile || baseline.WorkflowID != v.Candidate.WorkflowID {
		return fmt.Errorf("workflow preview identity mismatch")
	}
	expected := WorkflowChanges{Renamed: []WorkflowRename{}, Added: []A.WorkflowLane{}, Retired: []A.WorkflowLane{}}
	old := map[A.UUID]A.WorkflowLane{}
	newer := map[A.UUID]A.WorkflowLane{}
	oldOrder, newOrder := []A.UUID{}, []A.UUID{}
	for _, lane := range baseline.Lanes {
		old[lane.ID] = lane
		oldOrder = append(oldOrder, lane.ID)
	}
	for _, lane := range v.Candidate.Lanes {
		newer[lane.ID] = lane
		newOrder = append(newOrder, lane.ID)
		previous, ok := old[lane.ID]
		if !ok {
			if lane.Role != "waiting" {
				return fmt.Errorf("new required lane")
			}
			expected.Added = append(expected.Added, lane)
		} else {
			if previous.Role != lane.Role {
				return fmt.Errorf("workflow role changed")
			}
			if previous.Label != lane.Label {
				expected.Renamed = append(expected.Renamed, WorkflowRename{LaneID: A.Lane(lane.ID), Before: previous.Label, After: lane.Label})
			}
		}
	}
	for _, lane := range baseline.Lanes {
		if _, ok := newer[lane.ID]; !ok {
			if lane.Role != "waiting" {
				return fmt.Errorf("required lane removed")
			}
			expected.Retired = append(expected.Retired, lane)
		}
	}
	sort.Slice(expected.Renamed, func(i, j int) bool { return expected.Renamed[i].LaneID < expected.Renamed[j].LaneID })
	expected.OrderChanged = !reflect.DeepEqual(oldOrder, newOrder)
	expected.TransitionsChanged = !reflect.DeepEqual(baseline.Transitions, v.Candidate.Transitions)
	expected.Rights = WorkflowActorRightsChange{Human: previewRights(baseline.ActorRights.Human, v.Candidate.ActorRights.Human), Automation: previewRights(baseline.ActorRights.Automation, v.Candidate.ActorRights.Automation)}
	if !reflect.DeepEqual(expected, v.Changes) {
		return fmt.Errorf("workflow preview changes mismatch")
	}
	proposed := v.Candidate
	proposed.Revision = baseline.Revision
	if v.WouldChange == reflect.DeepEqual(*baseline, proposed) {
		return fmt.Errorf("workflow preview equality mismatch")
	}
	next := v.PolicyVersion
	count := v.Capacity.PolicyRecords
	if v.WouldChange {
		next++
		count++
	}
	if v.NextPolicyVersion != next || v.Capacity.PolicyRecordsAfter != count {
		return fmt.Errorf("workflow preview progression")
	}
	if len(v.Occupancy) != len(workflowPreviewLanes(v.CurrentWorkflow)) {
		return fmt.Errorf("workflow occupancy incomplete")
	}
	for i, row := range v.Occupancy {
		if row.ArchivedCards > row.CurrentCards || A.WorkflowRole(v.CurrentWorkflow, row.LaneID) == "" || (i > 0 && v.Occupancy[i-1].LaneID >= row.LaneID) {
			return fmt.Errorf("invalid workflow occupancy")
		}
	}
	for i, row := range v.Changes.Renamed {
		if row.Before == row.After || (i > 0 && v.Changes.Renamed[i-1].LaneID >= row.LaneID) {
			return fmt.Errorf("invalid workflow rename")
		}
	}
	for _, diff := range []WorkflowRightsChange{v.Changes.Rights.Human, v.Changes.Rights.Automation} {
		for _, values := range [][]string{diff.Added, diff.Removed} {
			for i, value := range values {
				if i > 0 && values[i-1] >= value {
					return fmt.Errorf("invalid rights difference")
				}
			}
		}
	}
	return nil
}
func workflowPreviewLanes(w *A.Workflow) []A.WorkflowLane {
	if w != nil {
		return w.Lanes
	}
	return []A.WorkflowLane{{ID: "Ready for Work"}, {ID: "In Progress"}, {ID: "Ready for Review"}, {ID: "Completed"}}
}

func workflowPreviewSchema(t reflect.Type, m map[string]any) {
	p := m["properties"].(map[string]any)
	if t == reflect.TypeFor[WorkflowApproval]() {
		p["rule"] = map[string]any{"const": "retained-issuing-policy"}
	}
	if t == reflect.TypeFor[WorkflowCapacity]() {
		p["maxPolicyRecords"] = map[string]any{"const": 128}
		p["maxCanonicalPolicyBytes"] = map[string]any{"const": 8388608}
	}
}

func previewRights(before, after []string) WorkflowRightsChange {
	out := WorkflowRightsChange{Added: []string{}, Removed: []string{}}
	contains := func(values []string, target string) bool {
		for _, value := range values {
			if value == target {
				return true
			}
		}
		return false
	}
	for _, value := range after {
		if !contains(before, value) {
			out.Added = append(out.Added, value)
		}
	}
	for _, value := range before {
		if !contains(after, value) {
			out.Removed = append(out.Removed, value)
		}
	}
	return out
}
func workflowPreviewBaseline(current *A.Workflow, id A.UUID) *A.Workflow {
	if current != nil {
		return current
	}
	ordinary := []string{"attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}
	return &A.Workflow{WorkflowID: id, Revision: 1, Profile: "default", Lanes: []A.WorkflowLane{{ID: "Ready for Work", Label: "Ready for Work", Role: "initial"}, {ID: "In Progress", Label: "In Progress", Role: "in_progress"}, {ID: "Ready for Review", Label: "Ready for Review", Role: "review"}, {ID: "Completed", Label: "Completed", Role: "completed"}}, Transitions: []A.WorkflowTransition{{From: "In Progress", To: "Ready for Review", Actors: []A.Kind{"automation", "human"}}, {From: "Ready for Review", To: "Completed", Actors: []A.Kind{"human"}}, {From: "Ready for Work", To: "In Progress", Actors: []A.Kind{"automation", "human"}}}, ActorRights: A.WorkflowActorRights{Human: append([]string{"approve"}, ordinary...), Automation: ordinary}}
}
