package archive

import (
	"encoding/json"
	"strings"
	"testing"
)

func defaultEditedWorkflow() *Workflow {
	w := testWorkflow()
	w.Profile = "default"
	w.Revision = 2
	ids := map[UUID]UUID{}
	for i := range w.Lanes {
		old := w.Lanes[i].ID
		w.Lanes[i].ID = UUID(WorkflowLaneForRole(nil, w.Lanes[i].Role))
		ids[old] = w.Lanes[i].ID
	}
	for i := range w.Transitions {
		w.Transitions[i].From = ids[w.Transitions[i].From]
		w.Transitions[i].To = ids[w.Transitions[i].To]
	}
	w.Transitions = []WorkflowTransition{w.Transitions[1], w.Transitions[2], w.Transitions[0]}
	return w
}
func TestPolicyVersionDomainAndDefaultProfile(t *testing.T) {
	w := defaultEditedWorkflow()
	raw, _ := json.Marshal(w)
	if _, err := DecodeWorkflow(raw); err != nil {
		t.Fatal(err)
	}
	if ValidateInitialWorkflow(w) == nil {
		t.Fatal("edited default accepted at setup")
	}
	if _, err := Decode[Version]([]byte("2")); err == nil {
		t.Fatal("fixed version widened")
	}
	if _, err := Decode[PolicyVersion]([]byte("2")); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWorkflow([]byte(strings.Replace(string(raw), `"default"`, `"configured"`, 1))); err == nil {
		t.Fatal("explicit configured profile accepted")
	}
	w.Lanes[0].ID = "60000000-0000-0000-0000-000000000001"
	if ValidateWorkflow(w) == nil {
		t.Fatal("default required identity changed")
	}
}
func TestPolicyHistoryConservesRetiredIdentity(t *testing.T) {
	first := testWorkflow()
	if ValidateWorkflowPolicies(1, first, []WorkflowPolicy{}) == nil {
		t.Fatal("explicit empty policy history accepted")
	}
	second := testWorkflow()
	second.Revision = 2
	second.Lanes[0].Label = "Renamed"
	policies := []WorkflowPolicy{{PolicyVersion: 1, Workflow: first}, {PolicyVersion: 2, Workflow: second}}
	if err := ValidateWorkflowPolicies(2, second, policies); err != nil {
		t.Fatal(err)
	}
	third := testWorkflow()
	third.Revision = 3
	third.Lanes[0].ID = "60000000-0000-0000-0000-000000000099"
	if err := ValidateWorkflowPolicies(3, third, append(policies, WorkflowPolicy{PolicyVersion: 3, Workflow: third})); err == nil {
		t.Fatal("required identity replacement accepted")
	}
	if _, err := WorkflowAtVersion(second, policies, 3); err == nil {
		t.Fatal("missing policy resolved")
	}
	defaultW := defaultEditedWorkflow()
	if err := ValidateWorkflowPolicies(2, defaultW, []WorkflowPolicy{{PolicyVersion: 1}, {PolicyVersion: 2, Workflow: defaultW}}); err != nil {
		t.Fatal(err)
	}
}
func TestCurrentApprovalUsesIssuingPolicyAcrossEdit(t *testing.T) {
	first := testWorkflow()
	second := testWorkflow()
	second.Revision = 2
	second.Lanes[2].Label = "Inspection"
	policies := []WorkflowPolicy{{PolicyVersion: 1, Workflow: first}, {PolicyVersion: 2, Workflow: second}}
	actor := Actor{ActorID: "00000000-0000-0000-0000-000000000001", Kind: "human"}
	approval := Approval{ApprovalID: "00000000-0000-0000-0000-000000000002", CardID: "00000000-0000-0000-0000-000000000003", ActorID: actor.ActorID, PolicyVersion: 1, ReviewedCardRevision: 2}
	card := Card{CardID: approval.CardID, Revision: 3, Lane: WorkflowLaneForRole(first, "completed"), CurrentApprovalID: &approval.ApprovalID}
	if err := ValidateWorkflowPolicyFacts(2, second, policies, []Card{card}, nil, []Actor{actor}, []Approval{approval}); err != nil {
		t.Fatal(err)
	}
	approval.ReviewedCardRevision = 1
	if err := ValidateWorkflowPolicyFacts(2, second, policies, []Card{card}, nil, []Actor{actor}, []Approval{approval}); err == nil {
		t.Fatal("wrong reviewed revision accepted")
	}
}
func TestEditedDefaultCurrentAndHistoricalBranches(t *testing.T) {
	w := defaultEditedWorkflow()
	policies := []WorkflowPolicy{{PolicyVersion: 1}, {PolicyVersion: 2, Workflow: w}}
	card := Card{CardID: "00000000-0000-0000-0000-000000000003", Revision: 3, Lane: "Completed"}
	if err := ValidateWorkflowPolicyFacts(2, w, policies, []Card{card}, nil, nil, nil); err == nil {
		t.Fatal("current completed accepted without approval")
	}
	history := Snapshot{CardID: card.CardID, CardRevision: 2, State: &SnapshotState{Lane: "Completed"}}
	if err := ValidateWorkflowPolicyFacts(2, w, policies, nil, []Snapshot{history}, nil, nil); err != nil {
		t.Fatal("legacy historical state reinterpreted", err)
	}
}
