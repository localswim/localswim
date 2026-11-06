//go:build windows && amd64

package open

import (
	"bytes"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
)

func legacyEight(t *testing.T, configured bool) string {
	t.Helper()
	p := legacySeven(t)
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 7, 8); err != nil || !changed {
		t.Fatal("prepare frozen8", err)
	}
	if configured {
		withOfflineLayout(t, p, func(c *sql.Conn) {
			w := workflowFixture()
			definition, err := A.WorkflowBytes(w)
			if err != nil {
				t.Fatal(err)
			}
			execute(t, c, "INSERT INTO workflow_definition VALUES(?,?,1,?)", incarnation, w.WorkflowID, string(definition))
			execute(t, c, "UPDATE card SET lane=CASE lane WHEN 'Completed' THEN ? ELSE ? END", w.Lanes[3].ID, w.Lanes[0].ID)
			state := A.SnapshotState{Ticket: 1, Title: "Old", Description: "", Priority: "P3", Lane: A.Lane(w.Lanes[0].ID)}
			raw, err := A.Canonical(state)
			if err != nil {
				t.Fatal(err)
			}
			execute(t, c, "DROP TRIGGER history_compaction")
			execute(t, c, "UPDATE card_revision SET state=?", string(raw))
			for _, q := range schema.StatementsV8() {
				if strings.HasPrefix(q, "CREATE TRIGGER history_compaction ") {
					execute(t, c, q)
				}
			}
		}, schema.ValidateV8)
	}
	withOfflineLayout(t, p, func(c *sql.Conn) { seedEightInventory(t, c) }, schema.ValidateV8)
	return p
}

func policyUpgradeDigest(t *testing.T, p string) [32]byte {
	t.Helper()
	var digest [32]byte
	err := inspectExisting(ctx(t), p, identity, func(r InspectionRead) error {
		var err error
		digest, err = recordsDigestUpgrade(ctx(t), r.(*inspectionReader).conn, false, false, true)
		return err
	}, validateSupported)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestPolicyUpgradeConservesEveryTypedRowAndRequiresExplicitStep(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "configured"}[configured], func(t *testing.T) {
			p := legacyEight(t, configured)
			before := supportedDigest(t, p)
			if s, err := Existing(ctx(t), p, identity); err == nil || !strings.Contains(err.Error(), "explicit offline upgrade 8->9") {
				if s != nil {
					s.Close()
				}
				t.Fatal("frozen8 startup", err)
			}
			if changed, err := UpgradeStorageTo(ctx(t), p, identity, 7, 9); err == nil || changed {
				t.Fatal("nonadjacent accepted")
			}
			if changed, err := UpgradeStorageTo(ctx(t), p, identity, 8, 9); err != nil || !changed {
				t.Fatal("upgrade", err)
			}
			if policyUpgradeDigest(t, p) != before {
				t.Fatal("old typed rows, rowids, canonical originals or opaque bytes changed")
			}
			if changed, err := UpgradeStorageTo(ctx(t), p, identity, 8, 9); err != nil || changed {
				t.Fatal("repeat was not no-change", err)
			}
			if _, err := UpgradeStorageTo(ctx(t), p, identity, 9, 10); err != nil {
				t.Fatal(err)
			}
			if _, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil {
				t.Fatal(err)
			}
			s, err := Existing(ctx(t), p, identity)
			if err != nil {
				t.Fatal(err)
			}
			withWriter(t, s, func(c *sql.Conn) {
				if err := schema.ValidateV8(ctx(t), c, identity); err == nil {
					t.Fatal("frozen8 accepted9")
				}
				policies, err := schema.ReadWorkflowPolicies(ctx(t), c, identity)
				if err != nil || len(policies) != 1 || (policies[0].Workflow != nil) != configured {
					t.Fatal("initial policy", err)
				}
				var pins int
				executeErr := c.QueryRowContext(ctx(t), "SELECT count(*) FROM card WHERE workflow_policy_version IS NOT NULL").Scan(&pins)
				if executeErr != nil || pins != 0 {
					t.Fatal("upgrade repinned existing state", executeErr)
				}
			})
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPolicyUpgradeRollbackAndUnknown(t *testing.T) {
	for _, stage := range []string{"card-dropped", "approval-copied", "policy-copied", "operation-copied", "ddl", "version", "validated", "committed", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			p := legacyEight(t, false)
			before := supportedDigest(t, p)
			fileBefore := fileDigest(t, p)
			changed, err := upgradeStorageStep(ctx(t), p, identity, 8, 9, func(at string) error {
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
			want := 8
			if post {
				want = 9
			}
			if err != nil || inspection.PhysicalVersion != want {
				t.Fatal("version", inspection, err)
			}
			if post {
				if policyUpgradeDigest(t, p) != before {
					t.Fatal("committed original rows changed")
				}
			} else if before != supportedDigest(t, p) || fileBefore != fileDigest(t, p) {
				t.Fatal("refusal changed frozen8")
			}
		})
	}
}

func defaultEditedWorkflow(version A.PolicyVersion) *A.Workflow {
	w := workflowFixture()
	w.Profile = "default"
	w.Revision = version
	for i, id := range []A.UUID{"Ready for Work", "In Progress", "Ready for Review", "Completed"} {
		w.Lanes[i].ID = id
	}
	w.Lanes[2].Label = "Approval queue"
	w.Transitions = nil
	for i := 0; i < 3; i++ {
		w.Transitions = append(w.Transitions, A.WorkflowTransition{From: w.Lanes[i].ID, To: w.Lanes[i+1].ID, Actors: []A.Kind{"human"}})
	}
	sort.Slice(w.Transitions, func(i, j int) bool {
		return string(w.Transitions[i].From)+"/"+string(w.Transitions[i].To) < string(w.Transitions[j].From)+"/"+string(w.Transitions[j].To)
	})
	return w
}

func TestEditedDefaultPreservesOpaqueHistoryAndStrictCurrentPinOne(t *testing.T) {
	p := legacyEight(t, false)
	// This key was ordinary opaque JSON in physical8. The upgrade must retain it
	// verbatim without treating its arbitrary value as a new policy pin.
	withOfflineLayout(t, p, func(c *sql.Conn) {
		execute(t, c, "DROP TRIGGER history_compaction")
		execute(t, c, "UPDATE card_revision SET state=?", `{ "legacyValue": "synthetic", "workflowPolicyVersion": {"opaque":"old"} }`)
		for _, q := range schema.StatementsV8() {
			if strings.HasPrefix(q, "CREATE TRIGGER history_compaction ") {
				execute(t, c, q)
			}
		}
	}, schema.ValidateV8)
	if _, err := UpgradeStorageTo(ctx(t), p, identity, 8, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := UpgradeStorageTo(ctx(t), p, identity, 9, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil {
		t.Fatal(err)
	}
	var before []byte
	withOfflineLayout(t, p, func(c *sql.Conn) {
		if err := c.QueryRowContext(ctx(t), "SELECT state FROM card_revision LIMIT 1").Scan(&before); err != nil {
			t.Fatal(err)
		}
		policies := []A.WorkflowPolicy{{PolicyVersion: 1}, {PolicyVersion: 2, Workflow: defaultEditedWorkflow(2)}}
		if err := schema.InstallWorkflowPolicies(ctx(t), c, identity, policies); err != nil {
			t.Fatal(err)
		}
	}, schema.Validate)
	if _, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil {
		t.Fatal(err)
	}
	s, err := Existing(ctx(t), p, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	withWriter(t, s, func(c *sql.Conn) {
		var after []byte
		if err := c.QueryRowContext(ctx(t), "SELECT state FROM card_revision LIMIT 1").Scan(&after); err != nil || !bytes.Equal(before, after) {
			t.Fatal("opaque history changed", err)
		}
		if err := schema.ValidateSnapshotPolicyPin(after, sql.NullInt64{}); err == nil {
			t.Fatal("opaque reserved key became public state")
		}
		refused(t, c, "UPDATE workflow_policy SET definition=definition")
		refused(t, c, "DELETE FROM workflow_policy WHERE policy_version=1")
		for _, q := range []string{"UPDATE card SET current_approval_id=NULL WHERE lane='Completed'", "UPDATE card SET workflow_policy_version=3", "UPDATE card SET lane='Ready for Work' WHERE lane='Completed'"} {
			execute(t, c, "BEGIN IMMEDIATE")
			execute(t, c, q)
			if err := schema.Validate(ctx(t), c, identity); err == nil {
				t.Fatal("invalid implicit1 current accepted")
			}
			execute(t, c, "ROLLBACK")
		}
		execute(t, c, "BEGIN IMMEDIATE")
		execute(t, c, "INSERT INTO grant_fact VALUES(?,?)", incarnation, "a9000000-0000-0000-0000-000000000099")
		execute(t, c, "INSERT INTO grant_record VALUES(?,?,?,0)", incarnation, "a9000000-0000-0000-0000-000000000099", actorID)
		execute(t, c, "INSERT INTO grant_right VALUES(?,?,'comment')", incarnation, "a9000000-0000-0000-0000-000000000099")
		if err := schema.Validate(ctx(t), c, identity); err != nil {
			t.Fatal("retained disabled grant above ceiling rejected", err)
		}
		execute(t, c, "ROLLBACK")
	})
}

func TestPolicyUpgradeRacingOwnersAndBadSchemaRefuseWithoutChanges(t *testing.T) {
	p := legacyEight(t, false)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := UpgradeStorageTo(ctx(t), p, identity, 8, 9); results <- err }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes < 1 {
		t.Fatal("neither racing upgrade succeeded")
	}
	if inspection, err := InspectStorage(ctx(t), p, identity); err != nil || inspection.PhysicalVersion != 9 {
		t.Fatal("race published invalid layout", err)
	}
	if _, err := UpgradeStorageTo(ctx(t), p, identity, 9, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil {
		t.Fatal(err)
	}
	s, err := Existing(ctx(t), p, identity)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 8, 9); err == nil || changed {
		t.Fatal("active owner admitted upgrade")
	}
	s.Close()
	q := legacyEight(t, false)
	withOfflineLayout(t, q, func(c *sql.Conn) { execute(t, c, "CREATE TABLE extra(value TEXT)") }, nil)
	before := fileDigest(t, q)
	if changed, err := UpgradeStorageTo(ctx(t), q, identity, 8, 9); err == nil || changed {
		t.Fatal("extra schema admitted")
	}
	if fileDigest(t, q) != before {
		t.Fatal("invalid source changed")
	}
}

func TestStrictSnapshotPolicyMetadataAndPruning(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "BEGIN IMMEDIATE")
		seed(t, c)
		if err := schema.InstallWorkflowPolicies(ctx(t), c, identity, []A.WorkflowPolicy{{PolicyVersion: 1}, {PolicyVersion: 2, Workflow: defaultEditedWorkflow(2)}}); err != nil {
			t.Fatal(err)
		}
		execute(t, c, "UPDATE card SET revision=2,workflow_policy_version=2 WHERE card_id=?", cardTwo)
		execute(t, c, "COMMIT")
		version := A.PolicyVersion(2)
		good := A.SnapshotState{Ticket: 2, Title: "Before", Description: "", Priority: "P3", Lane: "Ready for Work", WorkflowPolicyVersion: &version}
		for _, test := range []struct {
			name  string
			pin   int64
			state A.SnapshotState
			valid bool
		}{
			{"matching", 2, good, true},
			{"mismatched", 1, good, false},
			{"future", 3, good, false},
			{"missing", 2, A.SnapshotState{Ticket: 2, Title: "Before", Description: "", Priority: "P3", Lane: "Ready for Work"}, false},
		} {
			t.Run(test.name, func(t *testing.T) {
				execute(t, c, "BEGIN IMMEDIATE")
				raw, err := A.Canonical(test.state)
				if err != nil {
					t.Fatal(err)
				}
				execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state,workflow_policy_version) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z',?,?)", incarnation, boardID, incarnation, cardTwo, actorID, string(raw), test.pin)
				err = schema.Validate(ctx(t), c, identity)
				if (err == nil) != test.valid {
					t.Fatal("snapshot pin validation", err)
				}
				if test.valid {
					execute(t, c, "UPDATE card_revision SET state=NULL,workflow_policy_version=NULL,removed_at='2026-10-25T01:00:00Z',removal_reason='expired',removal_revision=2")
					if err := schema.Validate(ctx(t), c, identity); err != nil {
						t.Fatal("pruned metadata", err)
					}
				}
				execute(t, c, "ROLLBACK")
			})
		}
	})
}

// Seed every preexisting physical table with retained synthetic evidence. The
// upgrade digest then covers complete typed records and all SQLite rowids.
func seedEightInventory(t *testing.T, c *sql.Conn) {
	const comment = "b9000000-0000-0000-0000-000000000001"
	const removed = "b9000000-0000-0000-0000-000000000002"
	const attachment = "f0000000-0000-0000-0000-000000000002"
	const operation = "b9000000-0000-0000-0000-000000000003"
	const oldOperation = "b9000000-0000-0000-0000-000000000004"
	const source = "b9000000-0000-0000-0000-000000000005"
	const at = "2026-10-25T00:00:00Z"
	execute(t, c, "INSERT INTO grant_right VALUES(?,?,'read')", incarnation, credentialGrant)
	execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'comment',?,?,?,?)", incarnation, comment, cardID, boardID, incarnation)
	execute(t, c, "INSERT INTO comment(incarnation,comment_id,card_id,actor_id,text,created_at) VALUES(?,?,?,?,'Synthetic',?)", incarnation, comment, cardID, actorID, at)
	execute(t, c, "INSERT INTO current_reference VALUES(?,?,'comment',?)", incarnation, cardID, comment)
	execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,removed_at,removal_reason) VALUES(?,'comment',?,?,?,?,?,'expired')", incarnation, removed, cardID, boardID, incarnation, at)
	execute(t, c, "INSERT INTO revision_reference VALUES(?,?,?,?,1,'attachment',?)", incarnation, boardID, incarnation, cardID, attachment)
	execute(t, c, "INSERT INTO history_blob_pin VALUES(?,?,?,?,1,?,?,0)", incarnation, boardID, incarnation, cardID, attachment, hash)
	execute(t, c, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision) VALUES(?,?,?,?,1,?,?,'attachment.add','card',?,1,1,'COMMITTED','OK',?,2,2)`, incarnation, boardID, incarnation, operation, actorID, hash, cardID, at)
	execute(t, c, "INSERT INTO operation_full VALUES(?,?,?,?,'{\"synthetic\":\"attachment original\"}','{\"synthetic\":\"terminal result\"}')", incarnation, boardID, incarnation, operation)
	execute(t, c, "INSERT INTO operation_reference(incarnation,origin_board,origin_incarnation,op_id,kind,entity_id,revision) VALUES(?,?,?,?,'attachment',?,NULL)", incarnation, boardID, incarnation, operation, attachment)
	execute(t, c, "INSERT INTO byte_fact VALUES(?,?,?,?,?,0)", incarnation, boardID, incarnation, operation, hash)
	execute(t, c, "INSERT INTO operation_blob_pin VALUES(?,?,?,?,?,?,0)", incarnation, boardID, incarnation, operation, attachment, hash)
	execute(t, c, "UPDATE board SET next_event=2")
	execute(t, c, "INSERT INTO activity VALUES(?,?,?,1,?,?,'attachment.add',2,?,'[]')", incarnation, boardID, incarnation, actorID, operation, at)
	execute(t, c, "INSERT INTO coverage_gap(incarnation,origin_board,origin_incarnation,class,reason,through_inclusive,recorded_at,board_revision) VALUES(?,?,?,'history','expired',?,?,2)", incarnation, boardID, incarnation, at, at)
	execute(t, c, "INSERT INTO recovery_preview VALUES(?,?,?,2,1,'history.clear','{}',?,'2026-10-25T00:05:00Z')", incarnation, hash, actorID, at)
	execute(t, c, "INSERT INTO source_archive_group VALUES(?,?,?)", incarnation, boardID, source)
	execute(t, c, "INSERT INTO lineage VALUES(?,?,?,?,1)", incarnation, boardID, source, hash)
	execute(t, c, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,expected_revision,terminal_class,code,terminal_at,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'card.create',0,'NOT_COMMITTED','INVALID_INPUT',?,?,'expired',2)`, incarnation, boardID, source, oldOperation, actorID, hash, at, at)
}
