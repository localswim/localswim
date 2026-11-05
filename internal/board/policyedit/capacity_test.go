package policyedit

import (
	"context"
	"database/sql"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	_ "modernc.org/sqlite"
	"testing"
)

type testRead struct{ db *sql.DB }

func (r testRead) Scan(q string, args []any, dest ...any) error {
	return r.db.QueryRowContext(context.Background(), q, args...).Scan(dest...)
}
func (r testRead) Query(q string, args ...any) (*sql.Rows, error) {
	return r.db.QueryContext(context.Background(), q, args...)
}

func TestCapacityKeepsNoChangeAvailable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"CREATE TABLE board(singleton INTEGER,board_id TEXT,incarnation TEXT,revision INTEGER)",
		"CREATE TABLE policy(incarnation TEXT,policy_version INTEGER)",
		"CREATE TABLE workflow_policy(incarnation TEXT,policy_version INTEGER,workflow_id TEXT,definition TEXT)",
		"CREATE TABLE card(card_id TEXT,incarnation TEXT,lane TEXT,archived INTEGER,current_approval_id TEXT,workflow_policy_version INTEGER)",
		"INSERT INTO board VALUES(1,'board','inc',127)",
		"INSERT INTO policy VALUES('inc',128)",
		"INSERT INTO workflow_policy VALUES('inc',1,NULL,NULL)",
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var candidate A.Workflow
	for version := 2; version <= 128; version++ {
		candidate = DefaultDefinition("70000000-0000-0000-0000-000000000001", A.PolicyVersion(version))
		candidate.Lanes[0].Label = "Queue A"
		if version%2 != 0 {
			candidate.Lanes[0].Label = "Queue B"
		}
		raw, err := A.WorkflowBytes(&candidate)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO workflow_policy VALUES('inc',?,?,?)", version, string(candidate.WorkflowID), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	candidate.Revision = 129
	request := T.WorkflowPreviewRequest{APIVersion: 1, BoardID: "board", Incarnation: "inc", ExpectedRevision: 127, ExpectedPolicyVersion: 128, Workflow: candidate}
	preview, refusal, err := inspect(testRead{db}, request, true)
	if err != nil || refusal != nil || preview.WouldChange || preview.NextPolicyVersion != 128 || preview.Capacity.PolicyRecordsAfter != 128 {
		t.Fatalf("capacity nochange failed: refusal=%v err=%v", refusal, err)
	}
	request.Workflow.Lanes[0].Label = "Changed"
	_, refusal, err = inspect(testRead{db}, request, true)
	if err != nil || refusal == nil || refusal.Error.Code != "INVALID_INPUT" {
		t.Fatalf("capacity change admitted: refusal=%v err=%v", refusal, err)
	}
}
