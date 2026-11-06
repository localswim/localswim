//go:build windows && amd64

package open

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/localswim/localswim/internal/contract"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
)

func legacyNine(t *testing.T, configured bool) string {
	t.Helper()
	p := legacyEight(t, configured)
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 8, 9); err != nil || !changed {
		t.Fatal(err)
	}
	return p
}

func TestAttentionUpgradeConservesEveryTypedRowAndRequiresExplicitStep(t *testing.T) {
	for _, configured := range []bool{false, true} {
		p := legacyNine(t, configured)
		before := supportedDigest(t, p)
		if s, err := Existing(ctx(t), p, identity); err == nil || !strings.Contains(err.Error(), "explicit offline upgrade 9->10") {
			if s != nil {
				s.Close()
			}
			t.Fatal("legacy startup", err)
		}
		if changed, err := UpgradeStorageTo(ctx(t), p, identity, 9, 10); err != nil || !changed {
			t.Fatal(err)
		}
		if before != supportedDigest(t, p) {
			t.Fatal("retained row or byte changed")
		}
		if changed, err := UpgradeStorageTo(ctx(t), p, identity, 9, 10); err != nil || changed {
			t.Fatal("repeat", err)
		}
		assertPredecessorRefuses(t, p, schema.ValidateV9, schema.ValidateV10)
		if changed, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil || !changed {
			t.Fatal(err)
		}
		advanceCoverLayout(t, p)
		s, err := Existing(ctx(t), p, identity)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAttentionUpgradeRollbackAndUnknown(t *testing.T) {
	for _, stage := range []string{"ddl", "version", "validated", "committed", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			p := legacyNine(t, false)
			before := supportedDigest(t, p)
			fileBefore := fileDigest(t, p)
			changed, err := upgradeStorageStep(ctx(t), p, identity, 9, 10, func(at string) error {
				if at == stage {
					return errors.New("synthetic fault")
				}
				return nil
			})
			post := stage == "committed" || stage == "cleanup"
			if err == nil || errors.Is(err, ErrUpgradeUnknown) != post || !post && changed {
				t.Fatal("knowledge class", changed, err)
			}
			inspection, err := InspectStorage(ctx(t), p, identity)
			want := 9
			if post {
				want = 10
			}
			if err != nil || inspection.PhysicalVersion != want {
				t.Fatal("version", inspection, err)
			}
			if supportedDigest(t, p) != before || !post && fileDigest(t, p) != fileBefore {
				t.Fatal("retained source changed")
			}
		})
	}
}

func TestFrozenNineRejectsAttentionDefinitionsAndOriginals(t *testing.T) {
	for _, location := range []string{"definition", "policy", "original"} {
		t.Run(location, func(t *testing.T) {
			p := legacyNine(t, true)
			withOfflineLayout(t, p, func(c *sql.Conn) {
				switch location {
				case "definition":
					execute(t, c, "DROP TRIGGER workflow_definition_immutable")
					execute(t, c, `UPDATE workflow_definition SET definition=json_set(definition,'$.attention',json('{"blocked":[],"needsTerry":[],"readyForReview":[]}'))`)
				case "policy":
					execute(t, c, "DROP TRIGGER workflow_policy_immutable")
					execute(t, c, `UPDATE workflow_policy SET definition=json_set(definition,'$.attention',json('{"blocked":[],"needsTerry":[],"readyForReview":[]}'))`)
				case "original":
					original := frozenNineWorkflowOriginal(t, true)
					insertFrozenNineWorkflowOriginal(t, c, original)
				}
				if location != "original" {
					for _, q := range schema.StatementsV9() {
						if strings.HasPrefix(q, "CREATE TRIGGER workflow_"+location+"_immutable ") {
							execute(t, c, q)
						}
					}
				}
				if schema.ValidateV9(ctx(t), c, identity) == nil {
					t.Fatal("legacy accepted attention")
				}
			}, func(_ context.Context, _ *sql.Conn, _ schema.Identity) error { return nil })
		})
	}
}

func TestCurrentAttentionDefinitionSurvivesReopen(t *testing.T) {
	p := privateDir(t)
	w := workflowFixture()
	w.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{A.Lane(w.Lanes[2].ID)}}
	s, err := CreateWorkflow(ctx(t), p, identity, w)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectStorage(ctx(t), p, identity)
	if err != nil || inspection.Workflow == nil || inspection.Workflow.Attention == nil {
		t.Fatal("attention lost", err)
	}
}

// Use the real public request freezer so the regression follows the stored
// semantic envelope, including payload rather than the wire request's input.
func frozenNineWorkflowOriginal(t *testing.T, attention bool) contract.Intent {
	t.Helper()
	w := workflowFixture()
	w.Revision = 2
	if attention {
		w.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{A.Lane(w.Lanes[2].ID)}}
	}
	wire, err := A.Canonical(map[string]any{
		"opId": "b9000000-0000-0000-0000-000000000009", "incarnation": incarnation,
		"target": nil, "expectedRevision": 2, "expectedCardRevision": nil,
		"input": map[string]any{"expectedPolicyVersion": 1, "workflow": w},
	})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := contract.Freeze(wire, contract.ResolvedRoute{Command: "workflow.edit", BoardID: boardID}, contract.AuthenticatedActor{ActorID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func insertFrozenNineWorkflowOriginal(t *testing.T, c *sql.Conn, original contract.Intent) {
	t.Helper()
	execute(t, c, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,expected_revision,terminal_class,code,terminal_at) VALUES(?,?,?,?,1,?,?,'workflow.edit',2,'NOT_COMMITTED','INVALID_INPUT','2026-10-25T00:00:00Z')`, incarnation, boardID, incarnation, "b9000000-0000-0000-0000-000000000009", actorID, original.Fingerprint())
	execute(t, c, `INSERT INTO operation_full VALUES(?,?,?,?,?, '{}')`, incarnation, boardID, incarnation, "b9000000-0000-0000-0000-000000000009", string(original.CanonicalBytes()))
}

func TestFrozenNineRealOriginalConservation(t *testing.T) {
	p := legacyNine(t, true)
	original := frozenNineWorkflowOriginal(t, false)
	withOfflineLayout(t, p, func(c *sql.Conn) { insertFrozenNineWorkflowOriginal(t, c, original) }, schema.ValidateV9)
	before := supportedDigest(t, p)
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 9, 10); err != nil || !changed {
		t.Fatal("upgrade", err)
	}
	if before != supportedDigest(t, p) {
		t.Fatal("legacy original row bytes changed")
	}
	if _, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil {
		t.Fatal(err)
	}
	advanceCoverLayout(t, p)
	if err := InspectExisting(ctx(t), p, identity, func(r InspectionRead) error {
		var raw []byte
		var fingerprint string
		err := r.Scan(`SELECT f.intent,o.fingerprint FROM operation_full f JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.op_id=?`, []any{"b9000000-0000-0000-0000-000000000009"}, &raw, &fingerprint)
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, original.CanonicalBytes()) || fingerprint != original.Fingerprint() {
			t.Fatal("legacy canonical original or fingerprint changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
