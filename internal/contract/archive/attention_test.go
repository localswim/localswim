package archive

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestWorkflowAttentionPresenceAndResolution(t *testing.T) {
	w := testWorkflow()
	old, err := WorkflowBytes(w)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(old, []byte(`"attention"`)) {
		t.Fatal("absent attention changed old shape")
	}
	parsed, err := DecodeWorkflow(old)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := WorkflowBytes(parsed)
	if !bytes.Equal(old, again) || parsed.Attention != nil {
		t.Fatal("old canonical bytes changed")
	}
	a := ResolveWorkflowAttention(w)
	if !reflect.DeepEqual(a, AttentionAssignments{Blocked: []Lane{}, NeedsTerry: []Lane{}, ReadyForReview: []Lane{Lane(w.Lanes[2].ID)}}) {
		t.Fatal("implicit mapping")
	}
	w.Attention = &AttentionAssignments{Blocked: []Lane{}, NeedsTerry: []Lane{}, ReadyForReview: []Lane{}}
	empty, err := WorkflowBytes(w)
	if err != nil || !bytes.Contains(empty, []byte(`"attention":{"blocked":[],"needsTerry":[],"readyForReview":[]}`)) {
		t.Fatal("empty configuration lost", err)
	}
	if len(ResolveWorkflowAttention(w).ReadyForReview) != 0 {
		t.Fatal("explicit empty inherited")
	}
	w.Attention.ReadyForReview = []Lane{Lane(w.Lanes[2].ID)}
	a = ResolveWorkflowAttention(w)
	a.ReadyForReview[0] = "changed"
	if w.Attention.ReadyForReview[0] != Lane(w.Lanes[2].ID) {
		t.Fatal("resolver aliases policy")
	}
}

func TestWorkflowAttentionRejectsInvalidInput(t *testing.T) {
	w := testWorkflow()
	raw, _ := json.Marshal(w)
	for _, attention := range []string{`null`, `{}`, `{"blocked":null,"needsTerry":[],"readyForReview":[]}`, `{"blocked":[],"needsTerry":[],"readyForReview":[],"extra":[]}`, `{"blocked":["60000000-0000-0000-0000-000000000001"],"needsTerry":[],"readyForReview":[]}`, `{"blocked":[],"needsTerry":[],"readyForReview":["60000000-0000-0000-0000-000000000099"]}`} {
		input := append([]byte(`{"attention":`+attention+`,`), raw[1:]...)
		if _, err := DecodeWorkflow(input); err == nil {
			t.Fatalf("accepted invalid attention %s", attention)
		}
	}
	for _, a := range []AttentionAssignments{
		{Blocked: []Lane{"b", "a"}, NeedsTerry: []Lane{}, ReadyForReview: []Lane{}},
		{Blocked: []Lane{"a", "a"}, NeedsTerry: []Lane{}, ReadyForReview: []Lane{}},
		{Blocked: []Lane{"a"}, NeedsTerry: []Lane{"a"}, ReadyForReview: []Lane{}},
		{Blocked: []Lane{}, NeedsTerry: []Lane{}, ReadyForReview: []Lane{"a", "b"}},
	} {
		if Validate(a) == nil {
			t.Fatal("accepted malformed assignment set")
		}
	}
}

func TestWorkflowAttentionWaitingMembership(t *testing.T) {
	w := testWorkflow()
	for _, lane := range []WorkflowLane{{ID: "60000000-0000-0000-0000-000000000005", Label: "External", Role: "waiting"}, {ID: "60000000-0000-0000-0000-000000000006", Label: "Input", Role: "waiting"}} {
		w.Lanes = append(w.Lanes, lane)
		w.Transitions = append(w.Transitions, WorkflowTransition{From: w.Lanes[0].ID, To: lane.ID, Actors: []Kind{"human"}}, WorkflowTransition{From: lane.ID, To: w.Lanes[1].ID, Actors: []Kind{"human"}})
	}
	sort.Slice(w.Transitions, func(i, j int) bool {
		a, b := w.Transitions[i], w.Transitions[j]
		return a.From < b.From || (a.From == b.From && a.To < b.To)
	})
	w.Attention = &AttentionAssignments{Blocked: []Lane{Lane(w.Lanes[4].ID)}, NeedsTerry: []Lane{Lane(w.Lanes[5].ID)}, ReadyForReview: []Lane{Lane(w.Lanes[2].ID)}}
	if err := ValidateWorkflow(w); err != nil {
		t.Fatal(err)
	}
	w.Attention.NeedsTerry = append(w.Attention.NeedsTerry, Lane(w.Lanes[4].ID))
	if ValidateWorkflow(w) == nil {
		t.Fatal("accepted overlap")
	}
	w.Attention.NeedsTerry = []Lane{}
	w.Attention.Blocked = []Lane{Lane(w.Lanes[5].ID), Lane(w.Lanes[4].ID)}
	if ValidateWorkflow(w) == nil {
		t.Fatal("accepted unsorted membership")
	}
	w.Attention.Blocked = []Lane{Lane(w.Lanes[4].ID), Lane(w.Lanes[4].ID)}
	if ValidateWorkflow(w) == nil {
		t.Fatal("accepted duplicate membership")
	}
}
