package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func readWorkflow() *A.Workflow {
	w := &A.Workflow{WorkflowID: "50000000-0000-0000-0000-000000000001", Revision: 1,
		ActorRights: A.WorkflowActorRights{Human: []string{"approve", "create", "edit", "read", "read-own-outcome", "review-handoff"}, Automation: []string{"read", "read-own-outcome"}}}
	for i, role := range []string{"initial", "in_progress", "review", "completed"} {
		id := A.UUID(fmt.Sprintf("60000000-0000-0000-0000-%012d", i+1))
		w.Lanes = append(w.Lanes, A.WorkflowLane{ID: id, Label: role, Role: role})
		if i > 0 {
			w.Transitions = append(w.Transitions, A.WorkflowTransition{From: w.Lanes[i-1].ID, To: id, Actors: []A.Kind{"human"}})
		}
	}
	return w
}

func TestReadWorkflowMustMatchEveryLaneAndQuery(t *testing.T) {
	page := summaryPage()
	page.Result.Workflow = readWorkflow()
	page.Result.Items[0].Lane = A.WorkflowInitial(page.Result.Workflow)
	if err := Validate(page); err != nil {
		t.Fatal(err)
	}
	raw, err := Readable(page)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode[CardSearchRead](raw); err != nil {
		t.Fatal(err)
	}
	page.Result.Workflow = nil
	if Validate(page) == nil {
		t.Fatal("missing workflow accepted")
	}
	page.Result.Workflow = readWorkflow()
	page.Result.Items[0].Lane = "Ready for Work"
	if Validate(page) == nil {
		t.Fatal("legacy lane in configured profile accepted")
	}
	page.Result.Items = []CardSummary{}
	foreign := A.Lane("60000000-0000-0000-0000-000000000099")
	page.Result.Query.Lane = &foreign
	if Validate(page) == nil {
		t.Fatal("foreign query accepted on empty page")
	}
	legacy, _ := Readable(summaryPage())
	if bytes.Contains(legacy, []byte(`"workflow"`)) {
		t.Fatal("legacy read changed")
	}
}

func TestConfiguredWorkspaceRequestStrictScope(t *testing.T) {
	request := ConfiguredWorkspaceBoardRequest{APIVersion: 1, WorkspaceID: "00000000-0000-4000-8000-000000000011", ExpectedConfigRevision: 1, ProjectKey: "project", BoardKey: "board", Label: "Board", Workflow: *readWorkflow()}
	b, err := Readable(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode[ConfiguredWorkspaceBoardRequest](b); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]json.RawMessage{"root": json.RawMessage(`"C:/private"`), "actor": json.RawMessage(`"human"`), "workflow": json.RawMessage(`null`)} {
		var object map[string]json.RawMessage
		json.Unmarshal(b, &object)
		object[name] = value
		bad, _ := json.Marshal(object)
		if _, err := Decode[ConfiguredWorkspaceBoardRequest](bad); err == nil {
			t.Fatal("accepted invalid field", name)
		}
	}
	status := ConfiguredWorkspaceStatusRequest{APIVersion: 1, WorkspaceID: request.WorkspaceID, ProjectKey: request.ProjectKey, BoardKey: request.BoardKey, Workflow: request.Workflow}
	b, err = Readable(status)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("expectedIdentity")) {
		t.Fatal("absent identity emitted")
	}
	var object map[string]json.RawMessage
	json.Unmarshal(b, &object)
	object["expectedIdentity"] = json.RawMessage(`null`)
	bad, _ := json.Marshal(object)
	if _, err := Decode[ConfiguredWorkspaceStatusRequest](bad); err == nil {
		t.Fatal("explicit null expected identity accepted")
	}
	register := WorkspaceBoardRequest{APIVersion: 1, ExpectedConfigRevision: 1, ProjectKey: "p", BoardKey: "b", Label: "B", DataDir: "C:/b", Action: "register", Workflow: readWorkflow()}
	if Validate(register) == nil {
		t.Fatal("register can supply workflow")
	}
}

func TestEmbeddedWorkflowRawByteBoundary(t *testing.T) {
	w := readWorkflow()
	wireWorkflow, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	create := ConfiguredWorkspaceBoardRequest{APIVersion: 1, WorkspaceID: "00000000-0000-4000-8000-000000000011", ExpectedConfigRevision: 1, ProjectKey: "project", BoardKey: "board", Label: "Board", Workflow: *w}
	status := ConfiguredWorkspaceStatusRequest{APIVersion: 1, WorkspaceID: create.WorkspaceID, ProjectKey: create.ProjectKey, BoardKey: create.BoardKey, Workflow: *w}
	native := WorkspaceBoardRequest{APIVersion: 1, ExpectedConfigRevision: 1, ProjectKey: "project", BoardKey: "board", Label: "Board", DataDir: "C:/synthetic", Action: "create", Workflow: w}
	view := BoardView{Workflow: w, Identity: summaryPage().Result.Identity, PolicyVersion: 1, Retention: A.Retention{HistoryDays: 1, ActivityDays: 1, OutcomeDays: 1}, RetentionVersion: 1, Coverage: A.Coverage{Gaps: []A.Gap{}}, Actors: []A.Actor{}, Cards: []A.Card{}, Comments: []A.Comment{}, Links: []A.Link{}, Attachments: []A.Attachment{}, Approvals: []A.Approval{}}
	for _, tc := range []struct {
		name   string
		value  any
		decode func([]byte) error
	}{
		{"root", w, func(b []byte) error { _, err := A.Decode[A.Workflow](b); return err }},
		{"create", create, func(b []byte) error { _, err := Decode[ConfiguredWorkspaceBoardRequest](b); return err }},
		{"status", status, func(b []byte) error { _, err := Decode[ConfiguredWorkspaceStatusRequest](b); return err }},
		{"native optional", native, func(b []byte) error { _, err := Decode[WorkspaceBoardRequest](b); return err }},
		{"wrapped board read", BoardRead{APIVersion: 1, Scope: "board", Result: view}, func(b []byte) error { _, err := Decode[BoardRead](b); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(raw, wireWorkflow) != 1 {
				t.Fatal("fixture workflow subtree not unique")
			}
			for _, size := range []int{65536, 65537} {
				padded := append([]byte{'{'}, bytes.Repeat([]byte{' '}, size-len(wireWorkflow))...)
				padded = append(padded, wireWorkflow[1:]...)
				candidate := bytes.Replace(raw, wireWorkflow, padded, 1)
				err := tc.decode(candidate)
				if (err == nil) != (size == 65536) {
					t.Fatalf("raw workflow size %d: %v", size, err)
				}
			}
			duplicate := bytes.Replace(wireWorkflow, []byte(`"revision":1`), []byte(`"revision":1,"revision":1`), 1)
			if err := tc.decode(bytes.Replace(raw, wireWorkflow, duplicate, 1)); err == nil {
				t.Fatal("duplicate workflow key accepted")
			}
		})
	}
}

func TestWorkflowRawBoundDoesNotInspectOpaqueContent(t *testing.T) {
	large := append([]byte(`{"workflow":{`), bytes.Repeat([]byte{' '}, 65537)...)
	large = append(large, []byte(`}}`)...)
	if _, err := A.Decode[struct {
		Payload json.RawMessage `json:"payload"`
	}](append(append([]byte(`{"payload":`), large...), '}')); err != nil {
		t.Fatal("opaque raw content inspected", err)
	}
	prose, _ := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: string(large)})
	if _, err := A.Decode[struct {
		Text string `json:"text"`
	}](prose); err != nil {
		t.Fatal("prose inspected", err)
	}
}

func TestWorkflowPreviewRejectsContradictoryFacts(t *testing.T) {
	current := readWorkflow()
	candidate := readWorkflow()
	candidate.Revision = 2
	candidate.Lanes[0].Label = "Renamed"
	preview := WorkflowPreview{APIVersion: 1, BoardID: "00000000-0000-4000-8000-000000000001", Incarnation: "00000000-0000-4000-8000-000000000002", Revision: 5, PolicyVersion: 1, CurrentWorkflow: current, Candidate: *candidate, WouldChange: true, NextPolicyVersion: 2,
		Changes:  WorkflowChanges{Renamed: []WorkflowRename{{LaneID: A.Lane(current.Lanes[0].ID), Before: current.Lanes[0].Label, After: "Renamed"}}, Added: []A.WorkflowLane{}, Retired: []A.WorkflowLane{}, Rights: WorkflowActorRightsChange{Human: WorkflowRightsChange{Added: []string{}, Removed: []string{}}, Automation: WorkflowRightsChange{Added: []string{}, Removed: []string{}}}},
		Approval: WorkflowApproval{Rule: "retained-issuing-policy"}, Capacity: WorkflowCapacity{PolicyRecords: 1, PolicyRecordsAfter: 2, CanonicalPolicyBytesAfter: 1000, MaxPolicyRecords: 128, MaxCanonicalPolicyBytes: 8388608}}
	for _, lane := range current.Lanes {
		preview.Occupancy = append(preview.Occupancy, WorkflowOccupancy{LaneID: A.Lane(lane.ID)})
	}
	if err := Validate(preview); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(preview)
	for name, mutate := range map[string]func(*WorkflowPreview){"missing occupancy": func(v *WorkflowPreview) { v.Occupancy = []WorkflowOccupancy{} }, "wrong next": func(v *WorkflowPreview) { v.NextPolicyVersion = 99 }, "wrong record count": func(v *WorkflowPreview) { v.Capacity.PolicyRecordsAfter = 128 }, "wrong current count": func(v *WorkflowPreview) { v.Capacity.PolicyRecords = 2 }, "missing differences": func(v *WorkflowPreview) { v.Changes.Renamed = []WorkflowRename{} }, "changed identity": func(v *WorkflowPreview) { v.Candidate.WorkflowID = "00000000-0000-4000-8000-000000000099" }} {
		t.Run(name, func(t *testing.T) {
			var v WorkflowPreview
			json.Unmarshal(raw, &v)
			mutate(&v)
			if Validate(v) == nil {
				t.Fatal("contradictory preview accepted")
			}
		})
	}
}
