package archive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

func testWorkflow() *Workflow {
	return &Workflow{WorkflowID: "50000000-0000-0000-0000-000000000001", Revision: 1,
		Lanes: []WorkflowLane{
			{ID: "60000000-0000-0000-0000-000000000001", Label: "Inbox", Role: "initial"},
			{ID: "60000000-0000-0000-0000-000000000002", Label: "Build", Role: "in_progress"},
			{ID: "60000000-0000-0000-0000-000000000003", Label: "Inspect", Role: "review"},
			{ID: "60000000-0000-0000-0000-000000000004", Label: "Approved", Role: "completed"}},
		Transitions: []WorkflowTransition{
			{From: "60000000-0000-0000-0000-000000000001", To: "60000000-0000-0000-0000-000000000002", Actors: []Kind{"automation", "human"}},
			{From: "60000000-0000-0000-0000-000000000002", To: "60000000-0000-0000-0000-000000000003", Actors: []Kind{"automation", "human"}},
			{From: "60000000-0000-0000-0000-000000000003", To: "60000000-0000-0000-0000-000000000004", Actors: []Kind{"human"}}},
		ActorRights: WorkflowActorRights{Human: []string{"approve", "attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}, Automation: []string{"attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}}}
}

func TestWorkflowValidationAndLegacyCompatibility(t *testing.T) {
	w := testWorkflow()
	if err := ValidateWorkflow(w); err != nil {
		t.Fatal(err)
	}
	if WorkflowInitial(w) != Lane(w.Lanes[0].ID) || WorkflowRole(w, Lane(w.Lanes[3].ID)) != "completed" || WorkflowRole(w, "Completed") != "" {
		t.Fatal("configured role identity")
	}
	if !WorkflowAllows(w, Lane(w.Lanes[0].ID), Lane(w.Lanes[1].ID), "automation") || WorkflowAllows(w, Lane(w.Lanes[2].ID), Lane(w.Lanes[3].ID), "automation") {
		t.Fatal("edge actor authority")
	}
	if WorkflowInitial(nil) != "Ready for Work" || WorkflowRole(nil, "Completed") != "completed" || WorkflowRole(nil, Lane(w.Lanes[0].ID)) != "" {
		t.Fatal("legacy profile")
	}
	if WorkflowAllowsRight(nil, "automation", "approve") || !WorkflowAllowsRight(nil, "human", "approve") || WorkflowAllowsRight(nil, "human", "grant-admin") {
		t.Fatal("legacy ordinary rights")
	}
	if b, err := WorkflowBytes(nil); err != nil || b != nil {
		t.Fatal("legacy bytes changed")
	}
	before, _ := WorkflowBytes(w)
	decoded, err := DecodeWorkflow(before)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := WorkflowBytes(decoded)
	if !bytes.Equal(before, after) {
		t.Fatal("definition changed")
	}
}

func TestWorkflowRejectsInvalidDefinitions(t *testing.T) {
	for name, mutate := range map[string]func(*Workflow){
		"missing role":          func(w *Workflow) { w.Lanes[1].Role = "waiting" },
		"duplicate role":        func(w *Workflow) { w.Lanes[1].Role = "initial" },
		"duplicate ID":          func(w *Workflow) { w.Lanes[1].ID = w.Lanes[0].ID },
		"folded label":          func(w *Workflow) { w.Lanes[0].Label = "K"; w.Lanes[1].Label = "K" },
		"blank label":           func(w *Workflow) { w.Lanes[0].Label = "" },
		"long label":            func(w *Workflow) { w.Lanes[0].Label = strings.Repeat("🌊", 81) },
		"space label":           func(w *Workflow) { w.Lanes[0].Label = "\u2003Inbox" },
		"control label":         func(w *Workflow) { w.Lanes[0].Label = "In\x7fbox" },
		"bad Unicode":           func(w *Workflow) { w.Lanes[0].Label = string([]byte{0xff}) },
		"self edge":             func(w *Workflow) { w.Transitions[0].To = w.Transitions[0].From },
		"edge order":            func(w *Workflow) { w.Transitions[0], w.Transitions[1] = w.Transitions[1], w.Transitions[0] },
		"actor order":           func(w *Workflow) { w.Transitions[0].Actors = []Kind{"human", "automation"} },
		"actor duplicate":       func(w *Workflow) { w.Transitions[0].Actors = []Kind{"human", "human"} },
		"no human reachability": func(w *Workflow) { w.Transitions[0].Actors = []Kind{"automation"} },
		"automated approval":    func(w *Workflow) { w.Transitions[2].Actors = []Kind{"automation", "human"} },
		"rights order": func(w *Workflow) {
			w.ActorRights.Human[0], w.ActorRights.Human[1] = w.ActorRights.Human[1], w.ActorRights.Human[0]
		},
		"admin ceiling":         func(w *Workflow) { w.ActorRights.Human = append(w.ActorRights.Human, "restore") },
		"empty rights":          func(w *Workflow) { w.ActorRights.Automation = []string{} },
		"edge without edit":     func(w *Workflow) { w.ActorRights.Automation = []string{"read", "read-own-outcome", "review-handoff"} },
		"missing handoff right": func(w *Workflow) { w.ActorRights.Human = w.ActorRights.Human[:len(w.ActorRights.Human)-1] },
	} {
		t.Run(name, func(t *testing.T) {
			w := testWorkflow()
			mutate(w)
			if ValidateWorkflow(w) == nil {
				t.Fatal("invalid definition accepted")
			}
		})
	}
}

func TestWorkflowStrictJSONAndSubmittedSize(t *testing.T) {
	b, _ := json.Marshal(testWorkflow())
	for _, bad := range [][]byte{[]byte("null"), append([]byte(`{"unknown":true,`), b[1:]...), bytes.Replace(b, []byte(`"revision":1`), []byte(`"revision":1,"revision":1`), 1), bytes.Replace(b, []byte(`"lanes":[`), []byte(`"lanes":null,"ignored":[`), 1)} {
		if _, err := DecodeWorkflow(bad); err == nil {
			t.Fatal("invalid workflow JSON accepted")
		}
	}
	boundary := append(bytes.Repeat([]byte(" "), 65536-len(b)), b...)
	if _, err := DecodeWorkflow(boundary); err != nil {
		t.Fatal("exact byte boundary", err)
	}
	if _, err := DecodeWorkflow(append(boundary, ' ')); err == nil {
		t.Fatal("excess submitted bytes accepted")
	}
	if _, err := Decode[*Workflow]([]byte("null")); err == nil {
		t.Fatal("pointer decode accepted explicit null")
	}
}

func TestWorkflowBoundsAndHumanGraph(t *testing.T) {
	w := testWorkflow()
	w.Lanes[0].Label = strings.Repeat("🌊", 80)
	for i := 5; i <= 16; i++ {
		id := UUID(fmt.Sprintf("60000000-0000-0000-0000-%012d", i))
		w.Lanes = append(w.Lanes, WorkflowLane{ID: id, Label: fmt.Sprintf("Wait %d", i), Role: "waiting"})
		w.Transitions = append(w.Transitions, WorkflowTransition{From: w.Lanes[0].ID, To: id, Actors: []Kind{"human"}}, WorkflowTransition{From: id, To: w.Lanes[1].ID, Actors: []Kind{"human"}})
	}
	// Fill to the exact edge ceiling with harmless nonterminal cycles.
	for _, from := range w.Lanes {
		for _, to := range w.Lanes {
			if len(w.Transitions) == 64 {
				break
			}
			if from.Role == "completed" || to.Role == "completed" || to.Role == "review" || from.ID == to.ID || WorkflowAllows(w, Lane(from.ID), Lane(to.ID), "human") {
				continue
			}
			w.Transitions = append(w.Transitions, WorkflowTransition{From: from.ID, To: to.ID, Actors: []Kind{"human"}})
		}
	}
	sort.Slice(w.Transitions, func(i, j int) bool {
		a, b := w.Transitions[i], w.Transitions[j]
		return a.From < b.From || (a.From == b.From && a.To < b.To)
	})
	if len(w.Lanes) != 16 || len(w.Transitions) != 64 {
		t.Fatal("invalid boundary fixture")
	}
	if err := ValidateWorkflow(w); err != nil {
		t.Fatal("valid boundary/cycles", err)
	}
	w.Lanes = append(w.Lanes, WorkflowLane{ID: "60000000-0000-0000-0000-000000000099", Label: "Extra", Role: "waiting"})
	if ValidateWorkflow(w) == nil {
		t.Fatal("17 lanes accepted")
	}
	w.Lanes = w.Lanes[:16]
	w.Transitions = append(w.Transitions, w.Transitions[0])
	if ValidateWorkflow(w) == nil {
		t.Fatal("65 edges accepted")
	}
	w = testWorkflow()
	wait := WorkflowLane{ID: "60000000-0000-0000-0000-000000000005", Label: "Wait", Role: "waiting"}
	w.Lanes = append(w.Lanes, wait)
	w.Transitions = append(w.Transitions, WorkflowTransition{From: w.Lanes[0].ID, To: wait.ID, Actors: []Kind{"human"}}, WorkflowTransition{From: wait.ID, To: w.Lanes[1].ID, Actors: []Kind{"automation"}})
	sort.Slice(w.Transitions, func(i, j int) bool {
		a, b := w.Transitions[i], w.Transitions[j]
		return a.From < b.From || (a.From == b.From && a.To < b.To)
	})
	if ValidateWorkflow(w) == nil {
		t.Fatal("lane with no human path to completion accepted")
	}
}

func TestWorkflowArchiveMembershipIsCommittedContext(t *testing.T) {
	m := fixture[Manifest](t, "package/manifest.json")
	b := fixture[Board](t, "package/board.json")
	h := fixture[History](t, "package/history.json")
	a := fixture[Activity](t, "package/activity.json")
	o := fixture[Outcomes](t, "package/outcomes.json")
	w := testWorkflow()
	b.Workflow = w
	for i := range b.Cards {
		b.Cards[i].Lane = WorkflowInitial(w)
		if b.Cards[i].CurrentApprovalID != nil {
			b.Cards[i].Lane = WorkflowLaneForRole(w, "completed")
		}
	}
	for i := range h.Records {
		if h.Records[i].State != nil {
			h.Records[i].State.Lane = WorkflowInitial(w)
			if h.Records[i].State.CurrentApprovalID != nil {
				h.Records[i].State.Lane = WorkflowLaneForRole(w, "completed")
			}
		}
	}
	// A refused original's well-shaped attempted UUID is evidence, not a
	// committed lane fact. Re-freezing it must retain that foreign target.
	for i := range o.Operations {
		op := &o.Operations[i]
		if op.TerminalClass != "NOT_COMMITTED" || op.Full == nil {
			continue
		}
		op.Command = "card.edit"
		op.ByteFacts = []ByteFact{}
		cardID := b.Cards[0].CardID
		op.Target = &Target{CardID: &cardID}
		rev := Positive(1)
		op.ExpectedCardRevision = &rev
		wire, _ := json.Marshal(map[string]any{"opId": op.OpID, "incarnation": op.Origin.Incarnation, "target": op.Target, "expectedRevision": op.ExpectedRevision, "expectedCardRevision": rev, "input": map[string]any{"laneId": "60000000-0000-0000-0000-000000000099"}})
		intent, err := contract.Freeze(wire, contract.ResolvedRoute{Command: "card.edit", BoardID: string(op.Origin.BoardID), TargetID: string(cardID)}, contract.AuthenticatedActor{ActorID: string(op.ActorID)})
		if err != nil {
			t.Fatal(err)
		}
		op.Fingerprint = Digest(intent.Fingerprint())
		op.Full.Intent = intent.CanonicalBytes()
		break
	}
	m = refreshProjectionInventory(t, m, b, h, a, o)
	if _, err := ValidateDocuments(m, b, h, a, o); err != nil {
		t.Fatal(err)
	}
	h.Records[0].State.Lane = "60000000-0000-0000-0000-000000000099"
	if _, err := ValidateDocuments(m, b, h, a, o); err == nil {
		t.Fatal("foreign snapshot lane accepted")
	}
	b.Workflow = nil
	if Validate(b) == nil {
		t.Fatal("UUID lane accepted without workflow")
	}
}

func TestWorkflowApprovalFacts(t *testing.T) {
	w := testWorkflow()
	cardID := UUID("70000000-0000-0000-0000-000000000001")
	actorID := UUID("70000000-0000-0000-0000-000000000002")
	approvalID := UUID("70000000-0000-0000-0000-000000000003")
	for name, mutate := range map[string]func(*Card, *Snapshot, []Actor, []Approval){
		"valid":                           func(*Card, *Snapshot, []Actor, []Approval) {},
		"current missing":                 func(c *Card, _ *Snapshot, _ []Actor, _ []Approval) { c.CurrentApprovalID = nil },
		"history missing":                 func(_ *Card, s *Snapshot, _ []Actor, _ []Approval) { s.State.CurrentApprovalID = nil },
		"current nonterminal approval":    func(c *Card, _ *Snapshot, _ []Actor, _ []Approval) { c.Lane = WorkflowInitial(w) },
		"historical nonterminal approval": func(_ *Card, s *Snapshot, _ []Actor, _ []Approval) { s.State.Lane = WorkflowInitial(w) },
		"unknown approval": func(c *Card, _ *Snapshot, _ []Actor, _ []Approval) {
			id := UUID("70000000-0000-0000-0000-000000000099")
			c.CurrentApprovalID = &id
		},
		"wrong reviewed revision": func(_ *Card, _ *Snapshot, _ []Actor, a []Approval) { a[0].ReviewedCardRevision = 1 },
		"wrong card": func(_ *Card, _ *Snapshot, _ []Actor, a []Approval) {
			a[0].CardID = "70000000-0000-0000-0000-000000000099"
		},
		"automation actor": func(_ *Card, _ *Snapshot, a []Actor, _ []Approval) { a[0].Kind = "automation" },
		"wrong policy":     func(_ *Card, _ *Snapshot, _ []Actor, a []Approval) { a[0].PolicyVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			c := Card{CardID: cardID, Revision: 3, Lane: WorkflowLaneForRole(w, "completed"), CurrentApprovalID: &approvalID}
			s := Snapshot{CardID: cardID, CardRevision: 3, State: &SnapshotState{Lane: c.Lane, CurrentApprovalID: &approvalID}}
			actors := []Actor{{ActorID: actorID, Kind: "human"}}
			approvals := []Approval{{ApprovalID: approvalID, CardID: cardID, ActorID: actorID, ReviewedCardRevision: 2, PolicyVersion: 1}}
			mutate(&c, &s, actors, approvals)
			err := ValidateWorkflowFacts(w, []Card{c}, []Snapshot{s}, actors, approvals)
			if (err == nil) != (name == "valid") {
				t.Fatalf("configured approval validation=%v", err)
			}
			if err := ValidateWorkflowFacts(nil, []Card{c}, []Snapshot{s}, actors, approvals); err != nil {
				t.Fatal("changed legacy historical interpretation", err)
			}
		})
	}
}

func TestWorkflowDocumentsRejectTerminalFactsBeforeRestore(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprint("history=", history), func(t *testing.T) {
			m := fixture[Manifest](t, "package/manifest.json")
			b := fixture[Board](t, "package/board.json")
			h := fixture[History](t, "package/history.json")
			a := fixture[Activity](t, "package/activity.json")
			o := fixture[Outcomes](t, "package/outcomes.json")
			b.Workflow = testWorkflow()
			for i := range b.Cards {
				b.Cards[i].Lane = WorkflowInitial(b.Workflow)
				if b.Cards[i].CurrentApprovalID != nil {
					b.Cards[i].Lane = WorkflowLaneForRole(b.Workflow, "completed")
				}
			}
			for i := range h.Records {
				if h.Records[i].State != nil {
					h.Records[i].State.Lane = WorkflowInitial(b.Workflow)
					if h.Records[i].State.CurrentApprovalID != nil {
						h.Records[i].State.Lane = WorkflowLaneForRole(b.Workflow, "completed")
					}
				}
			}
			m = refreshProjectionInventory(t, m, b, h, a, o)
			if _, err := ValidateDocuments(m, b, h, a, o); err != nil {
				t.Fatal("valid configured baseline", err)
			}
			if history {
				h.Records[0].State.Lane = WorkflowLaneForRole(b.Workflow, "completed")
				h.Records[0].State.CurrentApprovalID = nil
			} else {
				b.Cards[0].Lane = WorkflowLaneForRole(b.Workflow, "completed")
				b.Cards[0].CurrentApprovalID = nil
			}
			m = refreshProjectionInventory(t, m, b, h, a, o)
			if _, err := ValidateDocuments(m, b, h, a, o); err == nil {
				t.Fatal("unapproved terminal fact accepted")
			}
		})
	}
}
