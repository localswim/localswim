package policy

import (
	"database/sql"
	"errors"
	A "github.com/localswim/localswim/internal/contract/archive"
	"testing"
)

type policyRow struct {
	id         string
	revision   uint64
	definition string
	err        error
}

func (r policyRow) Scan(_ string, _ []any, dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = r.id
	*dest[1].(*uint64) = r.revision
	*dest[2].(*string) = r.definition
	return nil
}
func validDefinition(t *testing.T) (*A.Workflow, string) {
	t.Helper()
	w := &A.Workflow{WorkflowID: "70000000-0000-0000-0000-000000000001", Revision: 1,
		ActorRights: A.WorkflowActorRights{Human: []string{"approve", "create", "edit", "read", "read-own-outcome", "review-handoff"}, Automation: []string{"read", "read-own-outcome"}}}
	for i, role := range []string{"initial", "in_progress", "review", "completed"} {
		ids := []A.UUID{"71000000-0000-0000-0000-000000000001", "71000000-0000-0000-0000-000000000002", "71000000-0000-0000-0000-000000000003", "71000000-0000-0000-0000-000000000004"}
		w.Lanes = append(w.Lanes, A.WorkflowLane{ID: ids[i], Label: role, Role: role})
		if i > 0 {
			w.Transitions = append(w.Transitions, A.WorkflowTransition{From: ids[i-1], To: ids[i], Actors: []A.Kind{"human"}})
		}
	}
	raw, err := A.WorkflowBytes(w)
	if err != nil {
		t.Fatal(err)
	}
	return w, string(raw)
}
func TestLoadExactProfileAndIdentity(t *testing.T) {
	w, raw := validDefinition(t)
	if got, err := Load(policyRow{err: sql.ErrNoRows}, "inc"); err != nil || got != nil {
		t.Fatal("absent profile is not exact default")
	}
	if got, err := Load(policyRow{id: string(w.WorkflowID), revision: 1, definition: raw}, "inc"); err != nil || got == nil {
		t.Fatal(err)
	}
	for _, r := range []policyRow{
		{id: string(w.WorkflowID), revision: 2, definition: raw},
		{id: "70000000-0000-0000-0000-000000000002", revision: 1, definition: raw},
		{id: string(w.WorkflowID), revision: 1, definition: "null"},
		{err: errors.New("unavailable")},
	} {
		if got, err := Load(r, "inc"); err == nil || got != nil {
			t.Fatal("invalid policy silently defaulted")
		}
	}
}
func TestOrdinaryCeilingLeavesSeparateAdministration(t *testing.T) {
	w, _ := validDefinition(t)
	if AllowsRights(w, "human", "comment") || AllowsRights(w, "automation", "edit") || AllowsRights(nil, "automation", "approve") {
		t.Fatal("ordinary ceiling bypassed")
	}
	if !AllowsRights(w, "human", "approve", "grant-admin", "history-admin") || !AllowsRights(nil, "human", "comment") {
		t.Fatal("legacy/native authority changed")
	}
}
