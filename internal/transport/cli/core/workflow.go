package core

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"strings"
)

func laneText(w *A.Workflow, lane A.Lane) string {
	if w == nil {
		return string(lane)
	}
	for _, l := range w.Lanes {
		if string(l.ID) == string(lane) {
			return fmt.Sprintf("%s [%s; %s]", advisoryText(l.Label), l.Role, l.ID)
		}
	}
	return "Unavailable lane"
}

func readableWorkflowShow(board T.BoardRead) string {
	var out strings.Builder
	b := board.Result
	fmt.Fprintf(&out, "Board %s/%s revision %d; current policy %d\n", b.Identity.BoardID, b.Identity.Incarnation, b.Revision, b.PolicyVersion)
	if len(b.WorkflowPolicies) == 0 {
		out.WriteString("Retained policy 1\n")
		if b.Workflow == nil {
			out.WriteString("Implicit default workflow\n")
		} else {
			writeWorkflow(&out, b.Workflow)
		}
		return out.String()
	}
	for _, policy := range b.WorkflowPolicies {
		fmt.Fprintf(&out, "Retained policy %d\n", policy.PolicyVersion)
		if policy.Workflow == nil {
			out.WriteString("Implicit default workflow\n")
		} else {
			writeWorkflow(&out, policy.Workflow)
		}
	}
	return out.String()
}

// The same complete policy accompanies focused current/history/search output.
func writeWorkflow(out *strings.Builder, w *A.Workflow) {
	if w == nil {
		return
	}
	profile := "configured"
	if w.Profile != "" {
		profile = w.Profile
	}
	fmt.Fprintf(out, "Workflow %s revision %d; profile %s\n", w.WorkflowID, w.Revision, profile)
	for _, l := range w.Lanes {
		fmt.Fprintf(out, "  Lane %s: %s [%s]\n", l.ID, advisoryText(l.Label), l.Role)
	}
	for _, e := range w.Transitions {
		actors := make([]string, len(e.Actors))
		for i, k := range e.Actors {
			actors[i] = string(k)
		}
		fmt.Fprintf(out, "  Edge %s -> %s: %s\n", e.From, e.To, strings.Join(actors, ", "))
	}
	fmt.Fprintf(out, "  Human rights: %s\n  Automation rights: %s\n", strings.Join(w.ActorRights.Human, ", "), strings.Join(w.ActorRights.Automation, ", "))
	assignments := A.ResolveWorkflowAttention(w)
	source := "implicit"
	if w.Attention != nil {
		source = "explicit"
	}
	fmt.Fprintf(out, "  Attention source %s\n", source)
	for _, category := range []struct {
		name  string
		lanes []A.Lane
	}{{"Blocked", assignments.Blocked}, {"Needs Terry", assignments.NeedsTerry}, {"Ready for Review", assignments.ReadyForReview}} {
		ids := make([]string, len(category.lanes))
		for i, lane := range category.lanes {
			ids[i] = string(lane)
		}
		label := "Not configured"
		if len(ids) > 0 {
			label = strings.Join(ids, ", ")
		}
		fmt.Fprintf(out, "  %s: %s\n", category.name, label)
	}
}

func writeAttentionCounts(out *strings.Builder, counts T.CatalogAttention) {
	for _, category := range []struct {
		name  string
		count T.CatalogCount
	}{{"Blocked", counts.Blocked}, {"Needs Terry", counts.NeedsTerry}, {"Ready for Review", counts.ReadyForReview}} {
		if category.count.Value == nil {
			label := "Unavailable"
			if category.count.State == "not_configured" {
				label = "Not configured"
			}
			fmt.Fprintf(out, "  %s: %s\n", category.name, label)
		} else {
			fmt.Fprintf(out, "  %s: %d (%s)\n", category.name, *category.count.Value, category.count.State)
		}
	}
}

func historicalWorkflow(current *A.Workflow, policies []A.WorkflowPolicy, pin *A.PolicyVersion) *A.Workflow {
	version := A.PolicyVersion(1)
	if pin != nil {
		version = *pin
	}
	workflow, _ := A.WorkflowAtVersion(current, policies, version)
	return workflow
}
