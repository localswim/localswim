//go:build windows && amd64

package workflow

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/policy"
	"github.com/localswim/localswim/internal/board/policyedit"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/writer"
)

func editPolicy(t *testing.T, e *writer.Engine, db *S.Store, g *grants.Service, candidate A.Workflow, want string) writer.Result {
	t.Helper()
	rev := uint64(value(t, db, "SELECT revision FROM board"))
	version := uint64(value(t, db, "SELECT policy_version FROM policy"))
	i := intent(t, "workflow.edit", "", rev, 0, map[string]any{"expectedPolicyVersion": version, "workflow": candidate})
	result := e.Submit(testContext(t), i, policyedit.New(g).Callbacks(policyedit.Route{BoardID: board, Incarnation: inc}))
	if result.Terminal == nil || result.Terminal.Code != want {
		t.Fatalf("policy expected %s, terminal=%v unknown=%v refused=%v", want, result.Terminal, result.Unknown, result.Refused)
	}
	if err := T.Validate(*result.Terminal); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestPolicyEditDefaultNoChangePinsAndIssuer(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "committed")
	issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
	issue(t, e, db, g, "approve", a, map[string]any{"policyVersion": 1}, "committed")
	candidate := policyedit.DefaultDefinition("70000000-0000-0000-0000-000000000001", 2)
	before := value(t, db, "SELECT revision FROM board")
	editPolicy(t, e, db, g, candidate, "NO_CHANGE")
	if value(t, db, "SELECT revision FROM board") != before || value(t, db, "SELECT count(*) FROM workflow_policy") != 1 {
		t.Fatal("nochange mutation")
	}
	candidate.Lanes[0].Label = "Queue"
	editPolicy(t, e, db, g, candidate, "OK")
	if value(t, db, "SELECT count(*) FROM card WHERE workflow_policy_version IS NOT NULL") != 0 || value(t, db, "SELECT count(*) FROM approval WHERE policy_version=1") != 1 {
		t.Fatal("policy edit rewrote card or approval")
	}
	raw(t, db, func(c *sql.Conn) error {
		wf, err := policy.Load(configuredRead{testContext(t), c}, inc)
		if err != nil {
			return err
		}
		valid, err := policy.CurrentApprovalValid(configuredRead{testContext(t), c}, inc, a, wf)
		if err == nil && !valid {
			t.Fatal("retained issuer approval invalidated")
		}
		return err
	})
	b := create(t, e, g, uint64(value(t, db, "SELECT revision FROM board")))
	if value(t, db, "SELECT count(*) FROM card WHERE card_id='"+b+"' AND workflow_policy_version=2") != 1 {
		t.Fatal("new card lacks policy pin")
	}
	issue(t, e, db, g, "card.edit", b, map[string]any{"lane": "In Progress"}, "committed")
	issue(t, e, db, g, "card.edit", b, map[string]any{"title": "Revised"}, "committed")
	if value(t, db, "SELECT count(*) FROM card_revision WHERE card_id='"+b+"' AND json_extract(state,'$.workflowPolicyVersion')=2 AND workflow_policy_version=2") != 2 {
		t.Fatal("prior pin not preserved")
	}
	candidate.Revision = 3
	candidate.Lanes[1].Label = "Doing"
	editPolicy(t, e, db, g, candidate, "OK")
	if value(t, db, "SELECT count(*) FROM card WHERE card_id='"+b+"' AND workflow_policy_version=2") != 1 {
		t.Fatal("policy-only repin")
	}
}
func TestPolicyEditCompatibilityAndPreviewReadonly(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	execSQL(t, db, "INSERT INTO grant_right VALUES(?,?,'read')", inc, grant)
	execSQL(t, db, "UPDATE card SET lane='Completed' WHERE card_id=?", a)
	candidate := policyedit.DefaultDefinition("70000000-0000-0000-0000-000000000001", 2)
	editPolicy(t, e, db, g, candidate, "NO_CHANGE")
	candidate.Lanes[0].Label = "Queue"
	raw(t, db, func(c *sql.Conn) error {
		req := T.WorkflowPreviewRequest{APIVersion: 1, BoardID: board, Incarnation: inc, ExpectedRevision: A.Number(value(t, db, "SELECT revision FROM board")), ExpectedPolicyVersion: 1, Workflow: candidate}
		_, refusal, err := policyedit.New(g).Preview(testContext(t), configuredRead{testContext(t), c}, actor, req)
		if err == nil && (refusal == nil || refusal.Error.Code != "WORKFLOW_INCOMPATIBLE") {
			t.Fatal("legacy compatibility preview accepted")
		}
		return err
	})
	editPolicy(t, e, db, g, candidate, "WORKFLOW_INCOMPATIBLE")
	if value(t, db, "SELECT count(*) FROM workflow_policy") != 1 {
		t.Fatal("incompatible edit published")
	}
}
func TestPolicyEditConfiguredRetirementAndReuse(t *testing.T) {
	e, db := fixture(t, customDefinition(t))
	g := service(t, db)
	execSQL(t, db, "DELETE FROM grant_right WHERE right_name='comment'")
	candidate := *customDefinition(t)
	candidate.Revision = 2
	candidate.Lanes[4].Label = "Waiting"
	first := editPolicy(t, e, db, g, candidate, "OK")
	var result map[string]any
	if err := json.Unmarshal(*first.Terminal.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["policyVersion"] != float64(2) {
		t.Fatal("policy result version")
	}
	candidate.Revision = 3
	candidate.Lanes = candidate.Lanes[:4]
	edges := candidate.Transitions[:0]
	for _, edge := range candidate.Transitions {
		if edge.From != customWaiting && edge.To != customWaiting {
			edges = append(edges, edge)
		}
	}
	candidate.Transitions = edges
	editPolicy(t, e, db, g, candidate, "OK")
	candidate = *customDefinition(t)
	candidate.Revision = 4
	editPolicy(t, e, db, g, candidate, "INVALID_INPUT")
	if value(t, db, "SELECT policy_version FROM policy") != 3 {
		t.Fatal("retired id reused")
	}
}

func TestPolicyEditWriterCASAndRetainedOriginal(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	candidate := policyedit.DefaultDefinition("70000000-0000-0000-0000-000000000001", 2)
	candidate.Lanes[0].Label = "Queue"
	original := intent(t, "workflow.edit", "", 1, 0, map[string]any{"expectedPolicyVersion": 1, "workflow": candidate})
	competing := intent(t, "card.edit", a, 1, 1, map[string]any{"lane": "In Progress"})
	cb := policyedit.New(g).Callbacks(policyedit.Route{BoardID: board, Incarnation: inc})
	first := e.Submit(testContext(t), original, cb)
	if first.Terminal == nil || first.Terminal.Code != "OK" {
		t.Fatal("policy winner failed")
	}
	code(t, submit(t, e, g, competing, a, "not_committed"), "REVISION_CONFLICT")
	candidate.Revision = 3
	candidate.Lanes[0].Label = "Backlog"
	editPolicy(t, e, db, g, candidate, "OK")
	retained := e.Submit(testContext(t), original, cb)
	if retained.Terminal == nil || retained.Terminal.Code != "OK" || *retained.Terminal.Revision != 2 {
		t.Fatal("exact original reevaluated after policy change")
	}
	if value(t, db, "SELECT revision FROM card") != 1 {
		t.Fatal("losing card move changed state")
	}
}

func TestPolicyEditCeilingIntersectionPreviewAndAuthorityOrder(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	execSQL(t, db, "INSERT INTO grant_right VALUES(?,?,'read')", inc, grant)
	candidate := policyedit.DefaultDefinition("70000000-0000-0000-0000-000000000001", 2)
	rights := []string{}
	for _, right := range candidate.ActorRights.Human {
		if right != "comment" {
			rights = append(rights, right)
		}
	}
	candidate.ActorRights.Human = rights
	raw(t, db, func(c *sql.Conn) error {
		preview, refusal, err := policyedit.New(g).Preview(testContext(t), configuredRead{testContext(t), c}, actor, T.WorkflowPreviewRequest{APIVersion: 1, BoardID: board, Incarnation: inc, ExpectedRevision: 0, ExpectedPolicyVersion: 1, Workflow: candidate})
		if err == nil && (refusal != nil || !preview.WouldChange || preview.Capacity.PolicyRecordsAfter != 2 || len(preview.Occupancy) != 4 || len(preview.Changes.Rights.Human.Removed) != 1) {
			t.Fatal("invalid preview")
		}
		return err
	})
	if value(t, db, "SELECT count(*) FROM operation") != 0 || value(t, db, "SELECT revision FROM board") != 0 {
		t.Fatal("preview mutated board")
	}
	editPolicy(t, e, db, g, candidate, "OK")
	raw(t, db, func(c *sql.Conn) error {
		if g.Require(testContext(t), configuredRead{testContext(t), c}, board, inc, actor, "comment") == nil {
			t.Fatal("lowered ceiling still effective")
		}
		return nil
	})
	if value(t, db, "SELECT count(*) FROM grant_right WHERE right_name='comment'") != 1 {
		t.Fatal("ceiling rewrote grant")
	}
	candidate = policyedit.DefaultDefinition(candidate.WorkflowID, 3)
	editPolicy(t, e, db, g, candidate, "OK")
	raw(t, db, func(c *sql.Conn) error {
		if g.Require(testContext(t), configuredRead{testContext(t), c}, board, inc, actor, "comment") != nil {
			t.Fatal("retained grant failed to reexpand")
		}
		if g.Require(testContext(t), configuredRead{testContext(t), c}, board, inc, actor, "attach") == nil {
			t.Fatal("ceiling invented grant")
		}
		return nil
	})
	execSQL(t, db, "DELETE FROM grant_right WHERE right_name='edit'")
	candidate.Revision = 4
	candidate.Lanes[0].Label = "Queue"
	i := intent(t, "workflow.edit", "", 999, 0, map[string]any{"expectedPolicyVersion": 999, "workflow": candidate})
	result := e.Submit(testContext(t), i, policyedit.New(g).Callbacks(policyedit.Route{BoardID: board, Incarnation: inc}))
	if result.Refused == nil || result.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("CAS disclosed before current edit authority")
	}
}

func TestPolicyEditConcurrentWriters(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	candidate := policyedit.DefaultDefinition("70000000-0000-0000-0000-000000000001", 2)
	candidate.Lanes[0].Label = "Queue one"
	first := intent(t, "workflow.edit", "", 0, 0, map[string]any{"expectedPolicyVersion": 1, "workflow": candidate})
	candidate.Lanes[0].Label = "Queue two"
	second := intent(t, "workflow.edit", "", 0, 0, map[string]any{"expectedPolicyVersion": 1, "workflow": candidate})
	cb := policyedit.New(g).Callbacks(policyedit.Route{BoardID: board, Incarnation: inc})
	start := make(chan struct{})
	results := make(chan writer.Result, 2)
	ctx := testContext(t)
	go func() { <-start; results <- e.Submit(ctx, first, cb) }()
	go func() { <-start; results <- e.Submit(ctx, second, cb) }()
	close(start)
	codes := map[string]int{}
	for range 2 {
		result := <-results
		if result.Terminal == nil {
			t.Fatal("racing writer lost terminal result")
		}
		codes[result.Terminal.Code]++
	}
	if codes["OK"] != 1 || codes["REVISION_CONFLICT"] != 1 || value(t, db, "SELECT revision FROM board") != 1 || value(t, db, "SELECT count(*) FROM workflow_policy") != 2 || value(t, db, "SELECT count(*) FROM activity") != 1 {
		t.Fatal("policy race was not atomic")
	}
}

func TestPolicyEditRejectsCorruptCurrentPinOneBeforeMutation(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "committed")
	issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
	issue(t, e, db, g, "approve", a, map[string]any{"policyVersion": 1}, "committed")
	candidate := policyedit.DefaultDefinition("70000000-0000-0000-0000-000000000001", 2)
	candidate.Lanes[0].Label = "Queue"
	editPolicy(t, e, db, g, candidate, "OK")
	execSQL(t, db, "UPDATE card SET lane='In Progress' WHERE card_id=?", a)
	revision := value(t, db, "SELECT revision FROM board")
	cardRevision := value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'")
	history := value(t, db, "SELECT count(*) FROM card_revision")
	operations := value(t, db, "SELECT count(*) FROM operation")
	for _, test := range []struct {
		command string
		input   map[string]any
	}{
		{"card.edit", map[string]any{"title": "Changed"}},
		{"comment.add", map[string]any{"text": "Comment"}},
		{"review-handoff", map[string]any{}},
	} {
		original := intent(t, test.command, a, uint64(revision), uint64(cardRevision), test.input)
		cb := New(g).Callbacks(Route{BoardID: board, Incarnation: inc, CardID: a})
		if test.command == "card.edit" || test.command == "comment.add" {
			cb = cards.New(g).Callbacks(cards.Route{BoardID: board, Incarnation: inc, CardID: a})
		}
		result := e.Submit(testContext(t), original, cb)
		if result.Unknown == nil {
			t.Fatalf("%s did not fail closed on corrupt current state", test.command)
		}
	}
	if value(t, db, "SELECT revision FROM board") != revision || value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'") != cardRevision || value(t, db, "SELECT count(*) FROM card_revision") != history || value(t, db, "SELECT count(*) FROM operation") != operations || value(t, db, "SELECT count(*) FROM card WHERE workflow_policy_version IS NULL AND current_approval_id IS NOT NULL AND lane='In Progress'") != 1 {
		t.Fatal("corrupt current state was laundered into history or mutated")
	}
}
