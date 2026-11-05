package policyedit

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func attentionFixture(t *testing.T, current *A.Workflow) (testRead, T.WorkflowPreviewRequest) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"CREATE TABLE board(singleton INTEGER,board_id TEXT,incarnation TEXT,revision INTEGER)",
		"CREATE TABLE policy(incarnation TEXT,policy_version INTEGER)",
		"CREATE TABLE workflow_policy(incarnation TEXT,policy_version INTEGER,workflow_id TEXT,definition TEXT)",
		"CREATE TABLE card(card_id TEXT,incarnation TEXT,lane TEXT,archived INTEGER,current_approval_id TEXT,workflow_policy_version INTEGER)",
		"INSERT INTO board VALUES(1,'board','inc',7)",
		"INSERT INTO policy VALUES('inc',1)",
		"INSERT INTO workflow_policy VALUES('inc',1,NULL,NULL)",
		"INSERT INTO card VALUES('review','inc','Ready for Review',0,NULL,NULL)",
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	candidate := DefaultDefinition("70000000-0000-0000-0000-000000000001", 2)
	version := A.PolicyVersion(1)
	if current != nil {
		raw, err := A.WorkflowBytes(current)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO workflow_policy VALUES('inc',?,?,?)", uint64(current.Revision), string(current.WorkflowID), string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("UPDATE policy SET policy_version=?", uint64(current.Revision)); err != nil {
			t.Fatal(err)
		}
		candidate = *current
		candidate.Revision++
		version = current.Revision
	}
	return testRead{db}, T.WorkflowPreviewRequest{APIVersion: 1, BoardID: "board", Incarnation: "inc", ExpectedRevision: 7, ExpectedPolicyVersion: version, Workflow: candidate}
}

func emptyAttention() *A.AttentionAssignments {
	return &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{}}
}

func TestAttentionPreviewPresenceAndExactNoChange(t *testing.T) {
	r, req := attentionFixture(t, nil)
	req.Workflow.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{"Ready for Review"}}
	preview, refusal, err := inspect(r, req, true)
	if err != nil || refusal != nil {
		t.Fatalf("preview failed: %v %v", refusal, err)
	}
	if !preview.WouldChange || !preview.Changes.Attention.Changed || preview.Changes.Attention.BeforeSource != "implicit" || preview.Changes.Attention.AfterSource != "explicit" || !reflect.DeepEqual(preview.Changes.Attention.Before, preview.Changes.Attention.After) {
		t.Fatal("explicit presence was not a policy change")
	}
	if preview.CurrentWorkflow != nil || preview.Candidate.Attention == nil || *preview.CurrentAttention.ReadyForReview.Value != 1 || *preview.CandidateAttention.ReadyForReview.Value != 1 {
		t.Fatal("preview lost exact representation or current occupants")
	}
	current := req.Workflow
	r, req = attentionFixture(t, &current)
	preview, refusal, err = inspect(r, req, true)
	if err != nil || refusal != nil || preview.WouldChange || preview.Changes.Attention.Changed || preview.NextPolicyVersion != 2 {
		t.Fatalf("exact category nochange failed: %v %v", refusal, err)
	}
	req.Workflow.Attention = nil
	preview, refusal, err = inspect(r, req, true)
	if err != nil || refusal != nil || !preview.WouldChange || !preview.Changes.Attention.Changed || preview.Changes.Attention.AfterSource != "implicit" {
		t.Fatalf("restore implicit failed: %v %v", refusal, err)
	}
	var policies, revision int
	if err = r.Scan("SELECT count(*) FROM workflow_policy", nil, &policies); err != nil {
		t.Fatal(err)
	}
	if err = r.Scan("SELECT revision FROM board", nil, &revision); err != nil {
		t.Fatal(err)
	}
	if policies != 2 || revision != 7 {
		t.Fatal("preview mutated state")
	}
}

func TestAttentionPreviewMixedNewLaneAndDisabled(t *testing.T) {
	r, req := attentionFixture(t, nil)
	waiting := A.UUID("70000000-0000-0000-0000-000000000002")
	req.Workflow.Lanes = append(req.Workflow.Lanes, A.WorkflowLane{ID: waiting, Label: "External", Role: "waiting"})
	req.Workflow.Transitions = append(req.Workflow.Transitions, A.WorkflowTransition{From: "In Progress", To: waiting, Actors: []A.Kind{"automation", "human"}}, A.WorkflowTransition{From: waiting, To: "In Progress", Actors: []A.Kind{"automation", "human"}})
	sort.Slice(req.Workflow.Transitions, func(i, j int) bool {
		return string(req.Workflow.Transitions[i].From)+"/"+string(req.Workflow.Transitions[i].To) < string(req.Workflow.Transitions[j].From)+"/"+string(req.Workflow.Transitions[j].To)
	})
	req.Workflow.Attention = emptyAttention()
	req.Workflow.Attention.Blocked = []A.Lane{A.Lane(waiting)}
	preview, refusal, err := inspect(r, req, true)
	if err != nil || refusal != nil {
		t.Fatalf("mixed preview failed: %v %v", refusal, err)
	}
	if len(preview.Changes.Added) != 1 || !preview.Changes.Attention.Changed || !preview.Changes.TransitionsChanged || preview.CandidateAttention.Blocked.State != "known" || *preview.CandidateAttention.Blocked.Value != 0 || preview.CandidateAttention.ReadyForReview.State != "not_configured" {
		t.Fatal("mixed preview lost new known-zero lane or disable")
	}
}

func TestAttentionPreviewCompatibilityAndPreconditionOrder(t *testing.T) {
	r, req := attentionFixture(t, nil)
	req.Workflow.Attention = emptyAttention()
	if _, err := r.db.Exec("UPDATE card SET lane='Unknown legacy lane'"); err != nil {
		t.Fatal(err)
	}
	_, refusal, err := inspect(r, req, true)
	if err != nil || refusal == nil || refusal.Error.Code != "WORKFLOW_INCOMPATIBLE" {
		t.Fatalf("first category edit bypassed compatibility: %v %v", refusal, err)
	}
	req.ExpectedRevision = 6
	_, refusal, err = inspect(r, req, true)
	if err != nil || refusal == nil || refusal.Error.Code != "REVISION_CONFLICT" {
		t.Fatalf("stale category edit bypassed revision: %v %v", refusal, err)
	}
	req.ExpectedRevision = 7
	req.ExpectedPolicyVersion = 2
	_, refusal, err = inspect(r, req, true)
	if err != nil || refusal == nil || refusal.Error.Code != "POLICY_CONFLICT" {
		t.Fatalf("stale category edit bypassed policy: %v %v", refusal, err)
	}
}
