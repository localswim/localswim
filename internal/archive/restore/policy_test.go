package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func editedPolicyMembers(t *testing.T, defaultProfile bool) map[string][]byte {
	t.Helper()
	members := configuredMembers(t)
	b, err := A.DecodeReadable[A.Board](members["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	h, err := A.DecodeReadable[A.History](members["history.json"])
	if err != nil {
		t.Fatal(err)
	}
	original := *b.Workflow
	if defaultProfile {
		original.Profile = "default"
		ids := map[A.UUID]A.UUID{}
		for i, lane := range original.Lanes {
			if lane.Role != "waiting" {
				ids[lane.ID] = A.UUID(A.WorkflowLaneForRole(nil, lane.Role))
				original.Lanes[i].ID = ids[lane.ID]
			}
		}
		for i, edge := range original.Transitions {
			if id, ok := ids[edge.From]; ok {
				original.Transitions[i].From = id
			}
			if id, ok := ids[edge.To]; ok {
				original.Transitions[i].To = id
			}
		}
		sort.Slice(original.Transitions, func(i, j int) bool {
			x, y := original.Transitions[i], original.Transitions[j]
			return string(x.From)+"/"+string(x.To) < string(y.From)+"/"+string(y.To)
		})
		for i := range b.Cards {
			b.Cards[i].Lane = "Ready for Work"
		}
		for i := range h.Records {
			if h.Records[i].State != nil {
				h.Records[i].State.Lane = "Ready for Work"
			}
		}
		b.WorkflowPolicies = []A.WorkflowPolicy{{PolicyVersion: 1}}
	} else {
		b.WorkflowPolicies = []A.WorkflowPolicy{{PolicyVersion: 1, Workflow: &original}}
	}
	// Deep-copy arrays so each retained definition remains independently exact.
	raw, _ := A.Canonical(original)
	var second A.Workflow
	if err = json.Unmarshal(raw, &second); err != nil {
		t.Fatal(err)
	}
	second.Revision = 2
	second.Lanes[0].Label = "Renamed intake"
	b.WorkflowPolicies = append(b.WorkflowPolicies, A.WorkflowPolicy{PolicyVersion: 2, Workflow: &second})
	raw, _ = A.Canonical(second)
	var third A.Workflow
	if err = json.Unmarshal(raw, &third); err != nil {
		t.Fatal(err)
	}
	third.Revision = 3
	third.Lanes[0].Label = "Current intake"
	retired := third.Lanes[1].ID
	third.Lanes = append(third.Lanes[:1], third.Lanes[2:]...)
	edges := []A.WorkflowTransition{}
	for _, edge := range third.Transitions {
		if edge.From != retired && edge.To != retired {
			edges = append(edges, edge)
		}
	}
	third.Transitions = edges
	b.WorkflowPolicies = append(b.WorkflowPolicies, A.WorkflowPolicy{PolicyVersion: 3, Workflow: &third})
	b.Workflow = &third
	b.PolicyVersion = 3
	members["board.json"], err = A.Readable(b)
	if err != nil {
		t.Fatal(err)
	}
	members["history.json"], err = A.Readable(h)
	if err != nil {
		t.Fatal(err)
	}
	return members
}

func TestEditedPoliciesRepeatedPrepareConservesHistoryPinsAndOriginals(t *testing.T) {
	for _, defaultProfile := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "default"}[defaultProfile], func(t *testing.T) {
			source := sourcePackage(t, editedPolicyMembers(t, defaultProfile))
			originalBoard, _ := decode[A.Board](source, "board.json")
			originalHistory, _ := decode[A.History](source, "history.json")
			originalOutcomes, _ := decode[A.Outcomes](source, "outcomes.json")
			first, err := prepare(context.Background(), source, testTime, testInc)
			if err != nil {
				t.Fatal(err)
			}
			members := map[string][]byte{}
			for _, name := range source.MemberNames() {
				members[name], _ = source.Member(name)
			}
			for name, raw := range first.documents {
				members[name] = raw
			}
			manifest := first.manifest
			manifest.Source = first.board.Identity
			manifest.Lineage = first.lineage
			manifest.Coverage = first.board.Coverage
			members["manifest.json"], _ = A.Readable(manifest)
			second, err := prepare(context.Background(), sourcePackage(t, members), testTime, "99999999-9999-9999-9999-999999999998")
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range []*Prepared{first, second} {
				if !reflect.DeepEqual(result.board.WorkflowPolicies, originalBoard.WorkflowPolicies) || !reflect.DeepEqual(result.board.Cards, originalBoard.Cards) || !reflect.DeepEqual(result.history.Records, originalHistory.Records) {
					t.Fatal("retained policy/state facts changed")
				}
				found := false
				for _, group := range result.outcomes.SourceArchives {
					if group.Identity == originalOutcomes.Identity {
						actual, _ := A.Canonical(group.Operations)
						expected, _ := A.Canonical(originalOutcomes.Operations)
						found = bytes.Equal(actual, expected)
					}
				}
				if !found {
					t.Fatal("canonical originals or terminal records changed")
				}
				if result.board.Cards[0].WorkflowPolicyVersion != nil {
					t.Fatal("implicit original pin was rewritten")
				}
			}
		})
	}
}

func TestEditedPolicyArchiveRejectsBrokenHistoryAndPins(t *testing.T) {
	for _, mode := range []string{"missing-history", "future-pin", "current-retired-lane", "reused-lane", "mismatched-current"} {
		t.Run(mode, func(t *testing.T) {
			members := editedPolicyMembers(t, false)
			b, err := A.DecodeReadable[A.Board](members["board.json"])
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing-history":
				b.WorkflowPolicies = nil
			case "future-pin":
				v := A.PolicyVersion(4)
				b.Cards[0].WorkflowPolicyVersion = &v
			case "current-retired-lane":
				b.Cards[0].Lane = A.Lane(b.WorkflowPolicies[0].Workflow.Lanes[1].ID)
			case "reused-lane":
				raw, _ := A.Canonical(b.WorkflowPolicies[1].Workflow)
				var reused A.Workflow
				json.Unmarshal(raw, &reused)
				reused.Revision = 4
				b.Workflow = &reused
				b.WorkflowPolicies = append(b.WorkflowPolicies, A.WorkflowPolicy{PolicyVersion: 4, Workflow: &reused})
				b.PolicyVersion = 4
			case "mismatched-current":
				b.Workflow.Lanes[0].Label = "Mismatch"
			}
			members["board.json"], _ = A.Readable(b)
			if pack, err := V.Read(context.Background(), bytes.NewReader(sourceArchiveBytes(t, members))); err == nil || pack != nil {
				t.Fatal("invalid policy package accepted")
			}
		})
	}
}

func TestEditedDefaultHistoricalApprovalKeepsLegacyMeaning(t *testing.T) {
	members := editedPolicyMembers(t, true)
	b, err := A.DecodeReadable[A.Board](members["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	h, err := A.DecodeReadable[A.History](members["history.json"])
	if err != nil {
		t.Fatal(err)
	}
	id := A.UUID("99000000-0000-4000-8000-000000000001")
	approval := A.Approval{ApprovalID: id, CardID: b.Cards[0].CardID, ActorID: b.Actors[0].ActorID, ReviewedCardRevision: 1, ReviewedBoardRevision: 1, PolicyVersion: 1, CreatedAt: testTime, CreatedIn: b.Identity}
	b.Approvals = append(b.Approvals, approval)
	h.Records[0].State.CurrentApprovalID = &id
	h.Records[0].References.ApprovalIDs = []A.UUID{id}
	members["board.json"], _ = A.Readable(b)
	members["history.json"], _ = A.Readable(h)
	source := sourcePackage(t, members)
	prepared, err := prepare(context.Background(), source, testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.board.Approvals, b.Approvals) || !reflect.DeepEqual(prepared.history.Records, h.Records) {
		t.Fatal("legacy approval facts changed")
	}
	// That preserved historical association cannot authorize a current state.
	b.Cards[0].CurrentApprovalID = &id
	members["board.json"], _ = A.Readable(b)
	if pack, err := V.Read(context.Background(), bytes.NewReader(sourceArchiveBytes(t, members))); err == nil || pack != nil {
		t.Fatal("historical association acquired current authority")
	}
}

func TestArchiveRejectsPolicyPinsBeforeDestinationPreparation(t *testing.T) {
	for _, mode := range []string{"default-current-one", "configured-current-one", "default-history-one", "configured-history-one", "default-future-approval", "configured-future-approval"} {
		t.Run(mode, func(t *testing.T) {
			defaultProfile := mode[:7] == "default"
			members := configuredMembers(t)
			if defaultProfile {
				members = fixture(t)
			}
			futureApproval := mode == "default-future-approval" || mode == "configured-future-approval"
			if futureApproval {
				members = editedPolicyMembers(t, defaultProfile)
			}
			b, err := A.DecodeReadable[A.Board](members["board.json"])
			if err != nil {
				t.Fatal(err)
			}
			h, err := A.DecodeReadable[A.History](members["history.json"])
			if err != nil {
				t.Fatal(err)
			}
			pin := A.PolicyVersion(1)
			switch mode {
			case "default-current-one", "configured-current-one":
				b.Cards[0].WorkflowPolicyVersion = &pin
			case "default-history-one", "configured-history-one":
				h.Records[0].State.WorkflowPolicyVersion = &pin
			default:
				pin = 2
				id := A.UUID("99000000-0000-4000-8000-000000000003")
				b.Approvals = []A.Approval{{ApprovalID: id, CardID: b.Cards[0].CardID, ActorID: b.Actors[0].ActorID, ReviewedCardRevision: 2, ReviewedBoardRevision: 2, PolicyVersion: 3, CreatedAt: testTime, CreatedIn: b.Identity}}
				h.Records[0].State.WorkflowPolicyVersion = &pin
				h.Records[0].State.Lane = A.WorkflowLaneForRole(b.WorkflowPolicies[1].Workflow, "completed")
				h.Records[0].State.CurrentApprovalID = &id
				h.Records[0].References.ApprovalIDs = []A.UUID{id}
			}
			members["board.json"], _ = A.Readable(b)
			members["history.json"], _ = A.Readable(h)
			if source, err := V.Read(context.Background(), bytes.NewReader(sourceArchiveBytes(t, members))); err == nil || source != nil {
				t.Fatal("invalid policy pin survived preallocation archive validation")
			}
		})
	}
}
