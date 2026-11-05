package attention_test

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"testing"

	"github.com/localswim/localswim/internal/board/attention"
	"github.com/localswim/localswim/internal/board/policyedit"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	_ "modernc.org/sqlite"
)

type reader struct{ db *sql.DB }

func (r reader) Scan(q string, args []any, dest ...any) error {
	return r.db.QueryRowContext(context.Background(), q, args...).Scan(dest...)
}
func (r reader) Query(q string, args ...any) (*sql.Rows, error) {
	return r.db.QueryContext(context.Background(), q, args...)
}

const waiting A.Lane = "70000000-0000-0000-0000-000000000002"
const input A.Lane = "70000000-0000-0000-0000-000000000003"

func fixture(t *testing.T) reader {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"CREATE TABLE card(card_id TEXT,incarnation TEXT,lane TEXT,archived INTEGER,current_approval_id TEXT,workflow_policy_version INTEGER)",
		"CREATE TABLE board(singleton INTEGER,board_id TEXT,incarnation TEXT,revision INTEGER)",
		"CREATE TABLE policy(incarnation TEXT,policy_version INTEGER)",
		"CREATE TABLE workflow_policy(incarnation TEXT,policy_version INTEGER,workflow_id TEXT,definition TEXT)",
		"INSERT INTO board VALUES(1,'board','inc',11)",
		"INSERT INTO policy VALUES('inc',1)",
		"INSERT INTO workflow_policy VALUES('inc',1,NULL,NULL)",
		"INSERT INTO card(card_id,incarnation,lane,archived) VALUES('review','inc','Ready for Review',0),('archived','inc','Ready for Review',1),('foreign','other','Ready for Review',0)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return reader{db}
}

func workflow() A.Workflow {
	w := policyedit.DefaultDefinition("70000000-0000-0000-0000-000000000001", 2)
	w.Lanes = append(w.Lanes, A.WorkflowLane{ID: A.UUID(waiting), Label: "External", Role: "waiting"}, A.WorkflowLane{ID: A.UUID(input), Label: "Owner input", Role: "waiting"})
	for _, lane := range []A.Lane{waiting, input} {
		w.Transitions = append(w.Transitions, A.WorkflowTransition{From: "In Progress", To: A.UUID(lane), Actors: []A.Kind{"automation", "human"}}, A.WorkflowTransition{From: A.UUID(lane), To: "In Progress", Actors: []A.Kind{"automation", "human"}})
	}
	sort.Slice(w.Transitions, func(i, j int) bool {
		return string(w.Transitions[i].From)+"/"+string(w.Transitions[i].To) < string(w.Transitions[j].From)+"/"+string(w.Transitions[j].To)
	})
	w.Attention = &A.AttentionAssignments{Blocked: []A.Lane{waiting}, NeedsTerry: []A.Lane{input}, ReadyForReview: []A.Lane{"Ready for Review"}}
	return w
}

func known(t *testing.T, count T.CatalogCount, want A.Number) {
	t.Helper()
	if count.State != "known" || count.Value == nil || *count.Value != want {
		t.Fatalf("expected known %d, got %#v", want, count)
	}
}

func TestCountCurrentDistinctAndDisabled(t *testing.T) {
	r := fixture(t)
	w := workflow()
	for _, row := range []struct {
		id       string
		lane     A.Lane
		archived int
	}{{"wait", waiting, 0}, {"wait", waiting, 0}, {"archived-wait", waiting, 1}, {"input", input, 0}} {
		if _, err := r.db.Exec("INSERT INTO card(card_id,incarnation,lane,archived) VALUES(?,'inc',?,?)", row.id, string(row.lane), row.archived); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := attention.Count(r, "inc", &w)
	if err != nil {
		t.Fatal(err)
	}
	known(t, counts.Blocked, 1)
	known(t, counts.NeedsTerry, 1)
	known(t, counts.ReadyForReview, 1)
	if _, err := r.db.Exec("DELETE FROM card WHERE lane=?", string(input)); err != nil {
		t.Fatal(err)
	}
	counts, err = attention.Count(r, "inc", &w)
	if err != nil {
		t.Fatal(err)
	}
	known(t, counts.NeedsTerry, 0)
	w.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{}}
	counts, err = attention.Count(r, "inc", &w)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []T.CatalogCount{counts.Blocked, counts.NeedsTerry, counts.ReadyForReview} {
		if c.State != "not_configured" || c.Value != nil {
			t.Fatal("disabled category became a count")
		}
	}
}

func TestContextUsesSortedAssignedCurrentLanes(t *testing.T) {
	w := workflow()
	w.Lanes[2].Label = "Owner review"
	c := attention.Context(&w, 2)
	if c.Source != "explicit" || c.Profile != "default" || c.WorkflowID == nil || *c.WorkflowID != w.WorkflowID || len(c.Lanes) != 3 {
		t.Fatalf("invalid context %#v", c)
	}
	if c.Lanes[0].ID != waiting || c.Lanes[1].ID != input || c.Lanes[2].ID != "Ready for Review" || c.Lanes[2].Label != "Owner review" {
		t.Fatal("context lanes not sorted or current")
	}
	w.Attention = nil
	c = attention.Context(&w, 2)
	if c.Source != "implicit" || len(c.Lanes) != 1 || c.Lanes[0].Label != "Owner review" {
		t.Fatal("implicit configured context lost current label")
	}
}

func TestReadCurrentUsesExactPolicyAndFailsClosed(t *testing.T) {
	r := fixture(t)
	out, err := attention.ReadCurrent(r, "board", "inc")
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != 11 || out.AttentionContext.PolicyVersion != 1 || out.AttentionContext.WorkflowID != nil || out.AttentionContext.Source != "implicit" {
		t.Fatal("implicit current context changed")
	}
	known(t, out.Attention.ReadyForReview, 1)
	w := workflow()
	raw, err := A.WorkflowBytes(&w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec("INSERT INTO workflow_policy VALUES('inc',2,?,?)", string(w.WorkflowID), string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec("UPDATE policy SET policy_version=2"); err != nil {
		t.Fatal(err)
	}
	out, err = attention.ReadCurrent(r, "board", "inc")
	if err != nil || !reflect.DeepEqual(out.AttentionContext.Assignments, *w.Attention) {
		t.Fatalf("current policy not resolved: %v", err)
	}
	if _, err = r.db.Exec("UPDATE card SET workflow_policy_version=3 WHERE incarnation='inc'"); err != nil {
		t.Fatal(err)
	}
	if _, err = attention.ReadCurrent(r, "board", "inc"); err == nil {
		t.Fatal("invalid configured current pin became an observation")
	}
	if _, err = r.db.Exec("UPDATE card SET workflow_policy_version=NULL WHERE incarnation='inc'"); err != nil {
		t.Fatal(err)
	}
	if _, err = attention.ReadCurrent(r, "other", "inc"); err == nil {
		t.Fatal("foreign identity disclosed")
	}
	if _, err = r.db.Exec("UPDATE workflow_policy SET definition='{}' WHERE policy_version=2"); err != nil {
		t.Fatal(err)
	}
	if _, err = attention.ReadCurrent(r, "board", "inc"); err == nil {
		t.Fatal("invalid policy became an observation")
	}
}

func TestCountReadFailureDoesNotManufactureZero(t *testing.T) {
	r := fixture(t)
	if _, err := r.db.Exec("DROP TABLE card"); err != nil {
		t.Fatal(err)
	}
	out, err := attention.Count(r, "inc", nil)
	if err == nil || out.ReadyForReview.Value != nil || out.ReadyForReview.State == "known" {
		t.Fatal("failed read manufactured zero")
	}
}
