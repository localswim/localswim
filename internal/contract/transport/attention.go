package transport

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	"reflect"
	"strings"
	"unicode/utf8"
)

type AttentionLane struct {
	ID    A.Lane `json:"id"`
	Label string `json:"label"`
	Role  string `json:"role"`
}
type AttentionContext struct {
	PolicyVersion A.PolicyVersion        `json:"policyVersion"`
	WorkflowID    *A.UUID                `json:"workflowId"`
	Profile       string                 `json:"profile"`
	Source        string                 `json:"source"`
	Assignments   A.AttentionAssignments `json:"assignments"`
	Lanes         []AttentionLane        `json:"lanes"`
}
type BoardAttention struct {
	APIVersion       A.Version        `json:"apiVersion"`
	BoardID          A.UUID           `json:"boardId"`
	Incarnation      A.UUID           `json:"incarnation"`
	Revision         A.Number         `json:"revision"`
	AttentionContext AttentionContext `json:"attentionContext"`
	Attention        CatalogAttention `json:"attention"`
}
type WorkflowAttentionChange struct {
	BeforeSource string                 `json:"beforeSource"`
	AfterSource  string                 `json:"afterSource"`
	Before       A.AttentionAssignments `json:"before"`
	After        A.AttentionAssignments `json:"after"`
	Changed      bool                   `json:"changed"`
}

func attentionSource(w *A.Workflow) string {
	if w != nil && w.Attention != nil {
		return "explicit"
	}
	return "implicit"
}

func validateAttentionContext(c AttentionContext) error {
	fail := func() error { return fmt.Errorf("invalid attention context") }
	if (c.Profile != "default" && c.Profile != "configured") || (c.Source != "implicit" && c.Source != "explicit") {
		return fail()
	}
	if c.WorkflowID == nil {
		if c.Profile != "default" || c.Source != "implicit" || c.PolicyVersion != 1 || !reflect.DeepEqual(c.Assignments, A.ResolveWorkflowAttention(nil)) || !reflect.DeepEqual(c.Lanes, []AttentionLane{{ID: "Ready for Review", Label: "Ready for Review", Role: "review"}}) {
			return fail()
		}
		return nil
	}
	if (c.Profile == "default" && c.PolicyVersion < 2) || len(c.Assignments.Blocked)+len(c.Assignments.NeedsTerry) > 12 {
		return fail()
	}
	w := &A.Workflow{}
	for i, lane := range c.Lanes {
		if (i > 0 && c.Lanes[i-1].ID >= lane.ID) || (lane.Role != "waiting" && lane.Role != "review") || !utf8.ValidString(lane.Label) || utf8.RuneCountInString(lane.Label) < 1 || utf8.RuneCountInString(lane.Label) > 80 || strings.TrimSpace(lane.Label) != lane.Label {
			return fail()
		}
		for _, r := range lane.Label {
			if r < 32 || r == 127 {
				return fail()
			}
		}
		if c.Profile == "default" && lane.Role == "review" && lane.ID != "Ready for Review" {
			return fail()
		}
		for _, previous := range c.Lanes[:i] {
			if strings.EqualFold(previous.Label, lane.Label) {
				return fail()
			}
		}
		if err := A.Validate(A.UUID(lane.ID)); err != nil && !(c.Profile == "default" && lane.ID == "Ready for Review" && lane.Role == "review") {
			return fail()
		}
		w.Lanes = append(w.Lanes, A.WorkflowLane{ID: A.UUID(lane.ID), Label: lane.Label, Role: lane.Role})
	}
	if err := A.ValidateAttentionAssignments(&c.Assignments, w); err != nil {
		return err
	}
	if len(c.Lanes) != len(c.Assignments.Blocked)+len(c.Assignments.NeedsTerry)+len(c.Assignments.ReadyForReview) {
		return fail()
	}
	if c.Source == "implicit" && (len(c.Assignments.Blocked) != 0 || len(c.Assignments.NeedsTerry) != 0 || len(c.Assignments.ReadyForReview) != 1) {
		return fail()
	}
	return nil
}

func validateMappedAttention(a CatalogAttention, assignments A.AttentionAssignments) error {
	counts := []CatalogCount{a.Blocked, a.NeedsTerry, a.ReadyForReview}
	for i, lanes := range [][]A.Lane{assignments.Blocked, assignments.NeedsTerry, assignments.ReadyForReview} {
		if (len(lanes) == 0 && counts[i].State != "not_configured") || (len(lanes) > 0 && counts[i].State != "known") {
			return fmt.Errorf("attention count mapping mismatch")
		}
	}
	return nil
}

func previewAttention(a A.AttentionAssignments, occupancy []WorkflowOccupancy) CatalogAttention {
	counts := []CatalogCount{{State: "not_configured"}, {State: "not_configured"}, {State: "not_configured"}}
	for i, lanes := range [][]A.Lane{a.Blocked, a.NeedsTerry, a.ReadyForReview} {
		if len(lanes) == 0 {
			continue
		}
		value := A.Number(0)
		for _, lane := range lanes {
			for _, row := range occupancy {
				if row.LaneID == lane {
					value += row.CurrentCards - row.ArchivedCards
				}
			}
		}
		counts[i] = CatalogCount{State: "known", Value: &value}
	}
	return CatalogAttention{Blocked: counts[0], NeedsTerry: counts[1], ReadyForReview: counts[2]}
}

func attentionSchema(t reflect.Type, m map[string]any) {
	p := m["properties"].(map[string]any)
	if t == reflect.TypeFor[AttentionContext]() {
		p["profile"] = map[string]any{"enum": []string{"default", "configured"}}
		p["source"] = map[string]any{"enum": []string{"implicit", "explicit"}}
	}
	if t == reflect.TypeFor[WorkflowAttentionChange]() {
		for _, key := range []string{"beforeSource", "afterSource"} {
			p[key] = map[string]any{"enum": []string{"implicit", "explicit"}}
		}
	}
	if t == reflect.TypeFor[AttentionLane]() {
		p["role"] = map[string]any{"enum": []string{"waiting", "review"}}
		p["label"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 80, "pattern": `^[^\u0000-\u001f\u007f]+$`}
	}
}
