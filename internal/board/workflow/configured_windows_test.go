//go:build windows && amd64

package workflow

import (
	"context"
	"database/sql"
	"github.com/localswim/localswim/internal/board/cards"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"testing"
)

const customInitial = "71000000-0000-0000-0000-000000000001"
const customProgress = "71000000-0000-0000-0000-000000000002"
const customReview = "71000000-0000-0000-0000-000000000003"
const customCompleted = "71000000-0000-0000-0000-000000000004"
const customWaiting = "71000000-0000-0000-0000-000000000005"

func customPolicy(t *testing.T, db *S.Store) {
	t.Helper()
	w := A.Workflow{WorkflowID: "70000000-0000-0000-0000-000000000001", Revision: 1,
		Lanes:       []A.WorkflowLane{{ID: customInitial, Label: "Completed", Role: "initial"}, {ID: customProgress, Label: "Build", Role: "in_progress"}, {ID: customReview, Label: "Inspect", Role: "review"}, {ID: customCompleted, Label: "Accepted", Role: "completed"}, {ID: customWaiting, Label: "Queued", Role: "waiting"}},
		ActorRights: A.WorkflowActorRights{Human: []string{"approve", "create", "edit", "read", "read-own-outcome", "review-handoff"}, Automation: []string{"create", "edit", "read", "read-own-outcome", "review-handoff"}}}
	for _, edge := range [][2]string{{customInitial, customProgress}, {customInitial, customWaiting}, {customProgress, customReview}, {customProgress, customWaiting}, {customReview, customProgress}, {customReview, customCompleted}, {customWaiting, customInitial}, {customWaiting, customProgress}} {
		actors := []A.Kind{"human"}
		if edge[1] != customCompleted && edge[1] != customWaiting {
			actors = []A.Kind{"automation", "human"}
		}
		w.Transitions = append(w.Transitions, A.WorkflowTransition{From: A.UUID(edge[0]), To: A.UUID(edge[1]), Actors: actors})
	}
	raw, err := A.WorkflowBytes(&w)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "DELETE FROM grant_right WHERE right_name='comment'")
	execSQL(t, db, "INSERT INTO workflow_definition(incarnation,workflow_id,revision,definition) VALUES(?,?,?,?)", inc, string(w.WorkflowID), 1, string(raw))
}

func TestConfiguredWorkflowMovesApprovalAndTerminalGuards(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	customPolicy(t, db)
	a := create(t, e, g, 0)
	if value(t, db, "SELECT count(*) FROM card WHERE lane='"+customInitial+"' AND priority='P3'") != 1 {
		t.Fatal("create ignored initial role")
	}
	code(t, issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "not_committed"), "INVALID_INPUT")
	code(t, issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customReview}, "not_committed"), "INVALID_TRANSITION")
	code(t, issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": "71000000-0000-0000-0000-000000000099"}, "not_committed"), "INVALID_INPUT")
	issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customWaiting}, "committed")
	before := value(t, db, "SELECT revision FROM board")
	code(t, issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customWaiting}, "not_committed"), "NO_CHANGE")
	if value(t, db, "SELECT revision FROM board") != before {
		t.Fatal("no-op advanced revision")
	}
	issue(t, e, db, g, "card.edit", a, map[string]any{"archived": true}, "committed")
	code(t, issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customWaiting}, "not_committed"), "INVALID_TRANSITION")
	issue(t, e, db, g, "card.edit", a, map[string]any{"archived": false}, "committed")
	issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customProgress}, "committed")
	issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
	issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customProgress}, "committed")
	issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
	reviewed := value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'")
	issue(t, e, db, g, "approve", a, map[string]any{"policyVersion": 1}, "committed")
	if value(t, db, "SELECT count(*) FROM card c JOIN approval a ON a.approval_id=c.current_approval_id WHERE c.lane='"+customCompleted+"' AND a.reviewed_card_revision=c.revision-1") != 1 {
		t.Fatal("approval missing exact reviewed revision")
	}
	if value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'") != reviewed+1 {
		t.Fatal("approval revision count")
	}
	for _, input := range []map[string]any{{"title": "Changed"}, {"archived": false}, {"laneId": customCompleted}, {"parent": nil}, {"relationships": []any{}}} {
		code(t, issue(t, e, db, g, "card.edit", a, input, "not_committed"), "COMPLETED")
	}
	code(t, issue(t, e, db, g, "link.add", a, map[string]any{"kind": "blocking", "targetCardId": a}, "not_committed"), "COMPLETED")
}

func TestConfiguredPrerequisitesAndCeilings(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	customPolicy(t, db)
	a := create(t, e, g, 0)
	b := create(t, e, g, 1)
	issue(t, e, db, g, "link.add", a, map[string]any{"kind": "blocking", "targetCardId": b}, "committed")
	code(t, issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customProgress}, "not_committed"), "PREREQUISITE_NOT_APPROVED")
	issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customWaiting}, "committed")
	code(t, issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customProgress}, "not_committed"), "PREREQUISITE_NOT_APPROVED")
	issue(t, e, db, g, "card.edit", b, map[string]any{"laneId": customProgress}, "committed")
	issue(t, e, db, g, "review-handoff", b, map[string]any{}, "committed")
	issue(t, e, db, g, "approve", b, map[string]any{"policyVersion": 1}, "committed")
	peerRevision := value(t, db, "SELECT revision FROM card WHERE card_id='"+b+"'")
	issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customProgress}, "committed")
	if value(t, db, "SELECT revision FROM card WHERE card_id='"+b+"'") != peerRevision {
		t.Fatal("source move revised prerequisite")
	}
	// A synthetic over-ceiling grant is denied by the runtime read check;
	// exercise that seam directly without claiming corrupt store admission.
	raw(t, db, func(c *sql.Conn) error {
		if _, err := c.ExecContext(testContext(t), "INSERT INTO grant_right VALUES(?,?,'comment')", inc, grant); err != nil {
			return err
		}
		if g.Require(testContext(t), configuredRead{testContext(t), c}, board, inc, actor, "comment") == nil {
			t.Fatal("definition ceiling bypassed")
		}
		_, err := c.ExecContext(testContext(t), "DELETE FROM grant_right WHERE right_name='comment'")
		return err
	})
	issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customWaiting}, "committed")
	// Probe the domain admission seam against synthetic lost-approval evidence.
	// Full store validation correctly rejects such corruption before dispatch.
	rev := uint64(value(t, db, "SELECT revision FROM board"))
	cr := uint64(value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'"))
	attempt := intent(t, "card.edit", a, rev, cr, map[string]any{"laneId": customProgress})
	raw(t, db, func(c *sql.Conn) error {
		var approval string
		if err := c.QueryRowContext(testContext(t), "SELECT current_approval_id FROM card WHERE card_id=?", b).Scan(&approval); err != nil {
			return err
		}
		if _, err := c.ExecContext(testContext(t), "UPDATE card SET current_approval_id=NULL WHERE card_id=?", b); err != nil {
			return err
		}
		refusal, err := cards.New(g).Callbacks(cards.Route{BoardID: board, Incarnation: inc, CardID: a}).Admit(testContext(t), configuredRead{testContext(t), c}, attempt)
		if err != nil {
			return err
		}
		if refusal == nil || refusal.Error.Code != "PREREQUISITE_NOT_APPROVED" {
			t.Fatal("lost current approval qualified")
		}
		_, err = c.ExecContext(testContext(t), "UPDATE card SET current_approval_id=? WHERE card_id=?", approval, b)
		return err
	})
}

type configuredRead struct {
	ctx context.Context
	c   *sql.Conn
}

func (r configuredRead) Scan(q string, args []any, dest ...any) error {
	return r.c.QueryRowContext(r.ctx, q, args...).Scan(dest...)
}
func (r configuredRead) Query(q string, args ...any) (*sql.Rows, error) {
	return r.c.QueryContext(r.ctx, q, args...)
}

func TestConfiguredEdgeActorAndApprovalAuthority(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	customPolicy(t, db)
	a := create(t, e, g, 0)
	execSQL(t, db, "INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES(?,?,'automation','Synthetic automation',?,?)", inc, grantee, board, inc)
	execSQL(t, db, "DELETE FROM grant_right WHERE right_name='approve'")
	execSQL(t, db, "UPDATE grant_record SET actor_id=?", grantee)
	dispatch := func(command string, input map[string]any, want string) {
		t.Helper()
		rev := uint64(value(t, db, "SELECT revision FROM board"))
		cr := uint64(value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'"))
		result := submit(t, e, g, intentFor(t, grantee, command, a, rev, cr, input), a, want)
		if want == "not_committed" {
			expected := "INVALID_TRANSITION"
			if command == "approve" {
				expected = "FORBIDDEN"
			}
			code(t, result, expected)
		}
	}
	dispatch("card.edit", map[string]any{"laneId": customWaiting}, "not_committed")
	dispatch("card.edit", map[string]any{"laneId": customProgress}, "committed")
	dispatch("review-handoff", map[string]any{}, "committed")
	dispatch("approve", map[string]any{"policyVersion": 1}, "not_committed")
	if value(t, db, "SELECT count(*) FROM approval") != 0 {
		t.Fatal("automation created approval")
	}
}
