//go:build windows && amd64

package workflow

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/localswim/localswim/internal/board/attention"
	"github.com/localswim/localswim/internal/board/policy"
	"github.com/localswim/localswim/internal/board/policyedit"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
)

// Capture complete domain rows so category edits cannot hide a change to a pin,
// approval, archived flag, snapshot, relationship or stored grant.
func attentionConservedRows(t *testing.T, db *S.Store) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	raw(t, db, func(c *sql.Conn) error {
		for _, table := range []string{"card", "card_revision", "approval", "comment", "link", "attachment", "card_parent", "entity", "grant_record", "grant_right"} {
			rows, err := c.QueryContext(testContext(t), "SELECT rowid,* FROM "+table+" ORDER BY rowid")
			if err != nil {
				return err
			}
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				return err
			}
			for rows.Next() {
				values := make([]any, len(columns))
				dest := make([]any, len(columns))
				for i := range values {
					dest[i] = &values[i]
				}
				if err = rows.Scan(dest...); err != nil {
					rows.Close()
					return err
				}
				encoded, err := json.Marshal(values)
				if err != nil {
					rows.Close()
					return err
				}
				out[table] = append(out[table], string(encoded))
			}
			err = rows.Err()
			closeErr := rows.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
	return out
}

func TestAttentionCategoryEditPreservesDomainAndOriginal(t *testing.T) {
	e, db := fixture(t, customDefinition(t))
	g := service(t, db)
	execSQL(t, db, "DELETE FROM grant_right WHERE right_name='comment'")
	a := create(t, e, g, 0)
	issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customWaiting}, "committed")
	b := create(t, e, g, uint64(value(t, db, "SELECT revision FROM board")))
	issue(t, e, db, g, "card.edit", b, map[string]any{"laneId": customWaiting}, "committed")
	issue(t, e, db, g, "card.edit", b, map[string]any{"archived": true}, "committed")
	c := create(t, e, g, uint64(value(t, db, "SELECT revision FROM board")))
	issue(t, e, db, g, "card.edit", c, map[string]any{"laneId": customProgress}, "committed")
	issue(t, e, db, g, "review-handoff", c, map[string]any{}, "committed")
	issue(t, e, db, g, "approve", c, map[string]any{"policyVersion": 1}, "committed")
	before := attentionConservedRows(t, db)
	cardEvents := value(t, db, "SELECT count(*) FROM activity WHERE type<>'workflow.edit'")
	candidate := *customDefinition(t)
	candidate.Revision = 2
	candidate.Attention = &A.AttentionAssignments{Blocked: []A.Lane{customWaiting}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{customReview}}
	original := intent(t, "workflow.edit", "", uint64(value(t, db, "SELECT revision FROM board")), 0, map[string]any{"expectedPolicyVersion": 1, "workflow": candidate})
	callbacks := policyedit.New(g).Callbacks(policyedit.Route{BoardID: board, Incarnation: inc})
	first := e.Submit(testContext(t), original, callbacks)
	if first.Terminal == nil || first.Terminal.Code != "OK" {
		t.Fatalf("category write failed: %#v", first.Terminal)
	}
	if !reflect.DeepEqual(before, attentionConservedRows(t, db)) || value(t, db, "SELECT count(*) FROM activity WHERE type<>'workflow.edit'") != cardEvents {
		t.Fatal("category write changed card evidence or grants")
	}
	raw(t, db, func(c *sql.Conn) error {
		read := configuredRead{testContext(t), c}
		out, err := attention.ReadCurrent(read, board, inc)
		if err != nil {
			return err
		}
		if err = T.Validate(out); err != nil {
			return err
		}
		if out.AttentionContext.Profile != "configured" || out.AttentionContext.Source != "explicit" || out.Attention.Blocked.Value == nil || *out.Attention.Blocked.Value != 1 || out.Attention.NeedsTerry.State != "not_configured" || *out.Attention.ReadyForReview.Value != 0 {
			t.Fatal("native count did not resolve committed unarchived cards")
		}
		return nil
	})
	candidate.Revision = 3
	candidate.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{}}
	editPolicy(t, e, db, g, candidate, "OK")
	revision := value(t, db, "SELECT revision FROM board")
	events := value(t, db, "SELECT count(*) FROM activity")
	originals := value(t, db, "SELECT count(*) FROM operation")
	replay := e.Submit(testContext(t), original, callbacks)
	if replay.Terminal == nil || replay.Terminal.Code != "OK" || *replay.Terminal.Revision != *first.Terminal.Revision {
		t.Fatal("exact original was reevaluated under newer assignments")
	}
	candidate.Revision = 4
	editPolicy(t, e, db, g, candidate, "NO_CHANGE")
	if revision != value(t, db, "SELECT revision FROM board") || events != value(t, db, "SELECT count(*) FROM activity") || value(t, db, "SELECT count(*) FROM workflow_policy") != 3 || value(t, db, "SELECT count(*) FROM operation") != originals+1 || !reflect.DeepEqual(before, attentionConservedRows(t, db)) {
		t.Fatal("category nochange or replay mutated retained state")
	}
	raw(t, db, func(c *sql.Conn) error {
		history, err := policy.LoadHistory(configuredRead{testContext(t), c}, inc)
		if err != nil {
			return err
		}
		if len(history) != 3 || history[0].Workflow.Attention != nil || !reflect.DeepEqual(history[1].Workflow.Attention.Blocked, []A.Lane{customWaiting}) || history[2].Workflow.Attention == nil || len(history[2].Workflow.Attention.Blocked) != 0 {
			t.Fatal("retained assignment presence or values changed")
		}
		return nil
	})
}

func TestAttentionLaneRetirementRequiresUnassignmentAndNoArchivedOccupants(t *testing.T) {
	for _, occupied := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "archived"}[occupied], func(t *testing.T) {
			initial := customDefinition(t)
			initial.Attention = &A.AttentionAssignments{Blocked: []A.Lane{customWaiting}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{customReview}}
			e, db := fixture(t, initial)
			g := service(t, db)
			execSQL(t, db, "DELETE FROM grant_right WHERE right_name='comment'")
			if occupied {
				a := create(t, e, g, 0)
				issue(t, e, db, g, "card.edit", a, map[string]any{"laneId": customWaiting}, "committed")
				issue(t, e, db, g, "card.edit", a, map[string]any{"archived": true}, "committed")
			}
			candidate := *initial
			candidate.Revision = 2
			candidate.Lanes = candidate.Lanes[:4]
			candidate.Transitions = nil
			for _, edge := range initial.Transitions {
				if edge.From != customWaiting && edge.To != customWaiting {
					candidate.Transitions = append(candidate.Transitions, edge)
				}
			}
			editPolicy(t, e, db, g, candidate, "INVALID_INPUT")
			candidate.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{customReview}}
			if occupied {
				editPolicy(t, e, db, g, candidate, "INVALID_INPUT")
				if value(t, db, "SELECT count(*) FROM workflow_policy") != 1 {
					t.Fatal("archived retirement changed history")
				}
			} else {
				editPolicy(t, e, db, g, candidate, "OK")
				resurrect := *initial
				resurrect.Revision = 3
				editPolicy(t, e, db, g, resurrect, "INVALID_INPUT")
			}
		})
	}
}
