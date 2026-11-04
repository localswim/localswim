//go:build windows && amd64

package open

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
)

func legacySeven(t *testing.T) string {
	t.Helper()
	p := legacySix(t)
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 6, 7); err != nil || !changed {
		t.Fatal(err)
	}
	withOfflineLayout(t, p, func(c *sql.Conn) {
		execute(t, c, "UPDATE card SET rowid=rowid+41")
		approval := "f0000000-0000-0000-0000-000000000001"
		attachment := "f0000000-0000-0000-0000-000000000002"
		for _, row := range [][2]string{{"approval", approval}, {"attachment", attachment}} {
			execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,?,?,?,?,?)", incarnation, row[0], row[1], cardID, boardID, incarnation)
		}
		execute(t, c, "INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-25T00:00:00Z')", incarnation, approval, cardID, actorID)
		execute(t, c, "UPDATE card SET lane='Completed',current_approval_id=? WHERE card_id=?", approval, cardID)
		execute(t, c, "INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'Synthetic','text/plain',?,0,'2026-10-25T00:00:00Z')", incarnation, attachment, cardID, hash)
		execute(t, c, "INSERT INTO current_reference VALUES(?,?,'attachment',?)", incarnation, cardID, attachment)
		execute(t, c, "INSERT INTO current_attachment_pin VALUES(?,?,?,0)", incarnation, attachment, hash)
	}, schema.ValidateV7)
	return p
}
func workflowFixture() *A.Workflow {
	ids := []A.UUID{"d0000000-0000-0000-0000-000000000001", "d0000000-0000-0000-0000-000000000002", "d0000000-0000-0000-0000-000000000003", "d0000000-0000-0000-0000-000000000004"}
	w := &A.Workflow{WorkflowID: "e0000000-0000-0000-0000-000000000001", Revision: 1, ActorRights: A.WorkflowActorRights{Human: []string{"approve", "create", "edit", "read", "read-own-outcome", "review-handoff"}, Automation: []string{"read", "read-own-outcome"}}}
	for n, role := range []string{"initial", "in_progress", "review", "completed"} {
		w.Lanes = append(w.Lanes, A.WorkflowLane{ID: ids[n], Label: []string{"Intake", "Doing", "Review", "Done"}[n], Role: role})
		if n < 3 {
			w.Transitions = append(w.Transitions, A.WorkflowTransition{From: ids[n], To: ids[n+1], Actors: []A.Kind{"human"}})
		}
	}
	return w
}
func workflowDigest(t *testing.T, p string) [32]byte {
	t.Helper()
	var digest [32]byte
	err := inspectExisting(ctx(t), p, identity, func(r InspectionRead) error {
		var e error
		digest, e = recordsDigestExcluding(ctx(t), r.(*inspectionReader).conn, false, true)
		return e
	}, schema.Validate)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
func TestWorkflowUpgradeConservesExactRowsAndRequiresExplicitStep(t *testing.T) {
	p := legacySeven(t)
	before := supportedDigest(t, p)
	if s, e := Existing(ctx(t), p, identity); e == nil || !strings.Contains(e.Error(), "explicit offline upgrade 7->8") {
		if s != nil {
			s.Close()
		}
		t.Fatal("frozen7 startup", e)
	}
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 6, 8); e == nil || changed {
		t.Fatal("nonadjacent upgrade accepted")
	}
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 7, 8); e != nil || !changed {
		t.Fatal("upgrade", e)
	}
	if workflowDigest(t, p) != before {
		t.Fatal("retained typed rows/rowids/canonical/blob bytes changed")
	}
	x, e := InspectStorage(ctx(t), p, identity)
	if e != nil || x.PhysicalVersion != 8 || x.BaseSchemaVersion != 5 {
		t.Fatal(x, e)
	}
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 7, 8); e != nil || changed {
		t.Fatal("matching8 not no-change", e)
	}
	s, e := Existing(ctx(t), p, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	withWriter(t, s, func(c *sql.Conn) {
		if e := schema.ValidateV7(ctx(t), c, identity); e == nil {
			t.Fatal("frozen7 accepted8")
		}
		w, e := schema.ReadWorkflow(ctx(t), c, identity)
		if e != nil || w != nil {
			t.Fatal("default fabricated workflow", e)
		}
	})
}
func TestWorkflowUpgradeRollbackAndUnknown(t *testing.T) {
	for _, stage := range []string{"card-dropped", "card-copied", "ddl", "version", "validated", "committed", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			p := legacySeven(t)
			before := supportedDigest(t, p)
			bytesBefore := fileDigest(t, p)
			changed, e := upgradeStorageStep(ctx(t), p, identity, 7, 8, func(at string) error {
				if at == stage {
					return errors.New("synthetic fault")
				}
				return nil
			})
			post := stage == "committed" || stage == "cleanup"
			if e == nil || errors.Is(e, ErrUpgradeUnknown) != post || !post && changed {
				t.Fatal("commit knowledge", changed, e)
			}
			x, e := InspectStorage(ctx(t), p, identity)
			want := 7
			if post {
				want = 8
			}
			if e != nil || x.PhysicalVersion != want {
				t.Fatal(x, e)
			}
			if post {
				if workflowDigest(t, p) != before {
					t.Fatal("committed rows changed")
				}
			} else if supportedDigest(t, p) != before || fileDigest(t, p) != bytesBefore {
				t.Fatal("rollback changed exact7")
			}
		})
	}
}
func TestConfiguredWorkflowOpenAndRefusal(t *testing.T) {
	w := workflowFixture()
	p := privateDir(t)
	s, e := CreateWorkflow(ctx(t), p, identity, w)
	if e != nil {
		t.Fatal(e)
	}
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "BEGIN IMMEDIATE")
		seed(t, c)
		execute(t, c, "UPDATE card SET lane=?", string(w.Lanes[0].ID))
		execute(t, c, "COMMIT")
		actual, e := schema.ReadWorkflow(ctx(t), c, identity)
		if e != nil {
			t.Fatal(e)
		}
		a, _ := A.WorkflowBytes(actual)
		b, _ := A.WorkflowBytes(w)
		if string(a) != string(b) {
			t.Fatal("definition changed")
		}
		refused(t, c, "UPDATE workflow_definition SET revision=1")
		refused(t, c, "DELETE FROM workflow_definition")
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal(e)
		}
	})
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	inspection, inspectErr := InspectStorage(ctx(t), p, identity)
	if inspectErr != nil || inspection.Workflow == nil || inspection.Workflow.WorkflowID != w.WorkflowID {
		t.Fatal("configured inspect omitted definition", inspectErr)
	}
	s, e = Existing(ctx(t), p, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, kind := range []string{"legacy-current", "foreign-current", "missing-history-lane", "foreign-history-lane", "grant-ceiling", "approval-missing"} {
		t.Run(kind, func(t *testing.T) {
			withWriter(t, s, func(c *sql.Conn) {
				execute(t, c, "BEGIN IMMEDIATE")
				switch kind {
				case "legacy-current":
					execute(t, c, "UPDATE card SET lane='Ready for Work'")
				case "foreign-current":
					execute(t, c, "UPDATE card SET lane=?", missingID)
				case "missing-history-lane", "foreign-history-lane":
					execute(t, c, "UPDATE card SET revision=2 WHERE card_id=?", cardID)
					state := `{}`
					if kind == "foreign-history-lane" {
						state = `{"ticket":1,"title":"Old","description":"","priority":"P3","lane":"` + missingID + `","archived":false,"currentApprovalId":null}`
					}
					execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z',?)", incarnation, boardID, incarnation, cardID, actorID, state)
				case "grant-ceiling":
					execute(t, c, "INSERT INTO grant_fact VALUES(?,?)", incarnation, credentialGrant)
					execute(t, c, "INSERT INTO grant_record VALUES(?,?,?,1)", incarnation, credentialGrant, actorID)
					execute(t, c, "INSERT INTO grant_right VALUES(?,?,'comment')", incarnation, credentialGrant)
				case "approval-missing":
					execute(t, c, "UPDATE card SET lane=?", string(w.Lanes[3].ID))
				}
				if e := schema.Validate(ctx(t), c, identity); e == nil {
					t.Fatal("invalid configured state accepted")
				}
				execute(t, c, "ROLLBACK")
			})
		})
	}
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "BEGIN IMMEDIATE")
		execute(t, c, "UPDATE card SET revision=2 WHERE card_id=?", cardID)
		state := A.SnapshotState{Ticket: 1, Title: "Old", Description: "", Priority: "P3", Lane: A.Lane(w.Lanes[0].ID)}
		b, _ := json.Marshal(state)
		execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z',?)", incarnation, boardID, incarnation, cardID, actorID, string(b))
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal("valid configured history", e)
		}
		execute(t, c, "ROLLBACK")
	})
}
func TestInvalidWorkflowDoesNotAllocateStorage(t *testing.T) {
	p := privateDir(t)
	w := workflowFixture()
	w.Lanes[1].ID = w.Lanes[0].ID
	if s, e := CreateWorkflow(ctx(t), p, identity, w); e == nil || s != nil {
		t.Fatal("invalid definition created")
	}
	if _, e := os.Stat(filepath.Join(p, "board.sqlite")); !os.IsNotExist(e) {
		t.Fatal("invalid definition allocated storage")
	}
}
func TestDefaultWorkflowRejectsCustomSnapshotAndCurrentLanes(t *testing.T) {
	s, _ := newStore(t)
	for _, source := range []string{"current", "history"} {
		t.Run(source, func(t *testing.T) {
			withWriter(t, s, func(c *sql.Conn) {
				execute(t, c, "BEGIN IMMEDIATE")
				seed(t, c)
				if source == "current" {
					execute(t, c, "UPDATE card SET lane=?", missingID)
				} else {
					execute(t, c, "UPDATE card SET revision=2 WHERE card_id=?", cardID)
					execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z',?)", incarnation, boardID, incarnation, cardID, actorID, `{"lane":"`+missingID+`"}`)
				}
				if e := schema.Validate(ctx(t), c, identity); e == nil {
					t.Fatal("default accepted custom lane")
				}
				execute(t, c, "ROLLBACK")
			})
		})
	}
}
