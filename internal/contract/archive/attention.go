package archive

import "fmt"

type AttentionAssignments struct {
	Blocked        []Lane `json:"blocked"`
	NeedsTerry     []Lane `json:"needsTerry"`
	ReadyForReview []Lane `json:"readyForReview"`
}

// ResolveWorkflowAttention returns owned arrays without changing policy presence.
// Callers validate the complete policy before resolving it.
func ResolveWorkflowAttention(w *Workflow) AttentionAssignments {
	out := AttentionAssignments{Blocked: []Lane{}, NeedsTerry: []Lane{}, ReadyForReview: []Lane{}}
	if w != nil && w.Attention != nil {
		out.Blocked = append(out.Blocked, w.Attention.Blocked...)
		out.NeedsTerry = append(out.NeedsTerry, w.Attention.NeedsTerry...)
		out.ReadyForReview = append(out.ReadyForReview, w.Attention.ReadyForReview...)
		return out
	}
	if w == nil {
		out.ReadyForReview = append(out.ReadyForReview, "Ready for Review")
		return out
	}
	for _, lane := range w.Lanes {
		if lane.Role == "review" {
			out.ReadyForReview = append(out.ReadyForReview, Lane(lane.ID))
		}
	}
	return out
}

// ValidateAttentionAssignments checks exact membership against this policy.
func ValidateAttentionAssignments(a *AttentionAssignments, w *Workflow) error {
	if a == nil {
		return nil
	}
	if err := validateAttentionShape(*a); err != nil {
		return err
	}
	for index, lanes := range [][]Lane{a.Blocked, a.NeedsTerry, a.ReadyForReview} {
		role := "waiting"
		if index == 2 {
			role = "review"
		}
		for _, lane := range lanes {
			if WorkflowRole(w, lane) != role {
				return fmt.Errorf("invalid attention lane")
			}
		}
	}
	return nil
}

func validateAttentionShape(a AttentionAssignments) error {
	seen := map[Lane]bool{}
	for index, lanes := range [][]Lane{a.Blocked, a.NeedsTerry, a.ReadyForReview} {
		limit := 12
		if index == 2 {
			limit = 1
		}
		if lanes == nil || len(lanes) > limit {
			return fmt.Errorf("invalid attention assignments")
		}
		for i, lane := range lanes {
			if seen[lane] || (i > 0 && lanes[i-1] >= lane) {
				return fmt.Errorf("invalid attention order or overlap")
			}
			seen[lane] = true
		}
	}
	return nil
}
