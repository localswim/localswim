//go:build windows && amd64

package recovery

import (
	"database/sql"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
)

func TestPruneClearsSnapshotPolicyPinWithState(t *testing.T) {
	f := newFixture(t)
	at := A.Timestamp(baseTime.Add(-24 * time.Hour).Format(stamp))
	f.seed(t, at, false)
	definition := &A.Workflow{Profile: "default", WorkflowID: "70000000-0000-0000-0000-000000000001", Revision: 2,
		Lanes:       []A.WorkflowLane{{ID: "Ready for Work", Label: "Intake", Role: "initial"}, {ID: "In Progress", Label: "Active", Role: "in_progress"}, {ID: "Ready for Review", Label: "Review", Role: "review"}, {ID: "Completed", Label: "Done", Role: "completed"}},
		Transitions: []A.WorkflowTransition{{From: "In Progress", To: "Ready for Review", Actors: []A.Kind{"human"}}, {From: "Ready for Review", To: "Completed", Actors: []A.Kind{"human"}}, {From: "Ready for Work", To: "In Progress", Actors: []A.Kind{"human"}}},
		ActorRights: A.WorkflowActorRights{Human: []string{"approve", "create", "edit", "read", "read-own-outcome", "review-handoff"}, Automation: []string{"read", "read-own-outcome"}}}
	f.raw(t, func(c *sql.Conn) error {
		return schema.InstallWorkflowPolicies(tc(t), c, schema.Identity{BoardID: bid, Incarnation: inc}, []A.WorkflowPolicy{{PolicyVersion: 1}, {PolicyVersion: 2, Workflow: definition}})
	})
	pin := A.PolicyVersion(2)
	state, err := A.Canonical(A.SnapshotState{Ticket: 1, Title: "Pinned prior state", Description: "", Priority: "P3", Lane: "In Progress", WorkflowPolicyVersion: &pin})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state,workflow_policy_version) VALUES(?,?,?,?,?,?,?, ?,2)", inc, bid, inc, card, 3, human, at, string(state))
	preview := f.preview(t, "retention.set", A.Retention{HistoryDays: 1, ActivityDays: 1, OutcomeDays: 1})
	terminal(t, confirm(t, f, preview), "OK")
	if f.value(t, "SELECT count(*) FROM card_revision WHERE state IS NOT NULL OR workflow_policy_version IS NOT NULL") != 0 {
		t.Fatal("compacted snapshot retained state or policy pin")
	}
	if f.value(t, "SELECT count(*) FROM card_revision WHERE removed_at IS NOT NULL AND removal_reason='expired'") != 3 {
		t.Fatal("snapshot provenance or removal metadata lost")
	}
	if f.value(t, "SELECT count(*) FROM workflow_policy") != 2 {
		t.Fatal("retention changed workflow policy history")
	}
}
