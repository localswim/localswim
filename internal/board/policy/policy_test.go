package policy

import (
	"context"
	"database/sql"
	"errors"
	A "github.com/localswim/localswim/internal/contract/archive"
	_ "modernc.org/sqlite"
	"strings"
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
	if got, err := Load(policyRow{err: sql.ErrNoRows}, "inc"); err == nil || got != nil {
		t.Fatal("missing retained policy became default")
	}
	if got, err := Load(policyRow{revision: 1}, "inc"); err != nil || got != nil {
		t.Fatal("implicit policy1 not recognized")
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

type historyRead struct{ db *sql.DB }

func (r historyRead) Query(q string, args ...any) (*sql.Rows, error) {
	return r.db.QueryContext(context.Background(), q, args...)
}
func (r historyRead) Scan(q string, args []any, dest ...any) error {
	return r.db.QueryRowContext(context.Background(), q, args...).Scan(dest...)
}

func TestRetainedHistoryStrictDecode(t *testing.T) {
	w, valid := validDefinition(t)
	w.Revision = 2
	current, err := A.WorkflowBytes(w)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(valid, "{", "{\"unknown\":1,", 1), strings.Replace(valid, "{", "{\"revision\":1,", 1), " " + valid} {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err = db.Exec("CREATE TABLE workflow_policy(incarnation TEXT,policy_version INTEGER,workflow_id TEXT,definition TEXT)"); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO workflow_policy VALUES('inc',1,?,?),('inc',2,?,?)", string(w.WorkflowID), bad, string(w.WorkflowID), string(current)); err != nil {
			t.Fatal(err)
		}
		if _, err = LoadHistory(historyRead{db}, "inc"); err == nil {
			t.Fatal("malformed historical policy was projected")
		}
		db.Close()
	}
}
