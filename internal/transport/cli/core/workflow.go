package core

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
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

// The same complete policy accompanies focused current/history/search output.
func writeWorkflow(out *strings.Builder, w *A.Workflow) {
	if w == nil {
		return
	}
	fmt.Fprintf(out, "Workflow %s revision %d (immutable):\n", w.WorkflowID, w.Revision)
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
}
