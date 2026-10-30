//go:build windows && amd64

package open

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"github.com/localswim/localswim/internal/store/schema"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyFive(t *testing.T) string {
	p := privateDir(t)
	g, e := acquirePath(p, true)
	if e != nil {
		t.Fatal(e)
	}
	defer g.close()
	db, e := pool(g.path, false)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c, e := db.Conn(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = configure(ctx(t), c, false, true); e != nil {
		t.Fatal(e)
	}
	execute(t, c, "BEGIN IMMEDIATE")
	for _, q := range schema.StatementsV5() {
		execute(t, c, q)
	}
	execute(t, c, "INSERT INTO board(singleton,board_id,incarnation) VALUES(1,?,?)", boardID, incarnation)
	execute(t, c, "INSERT INTO policy(incarnation) VALUES(?)", incarnation)
	execute(t, c, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=5", schema.ApplicationID))
	seed(t, c)
	execute(t, c, "INSERT INTO grant_fact VALUES(?,?)", incarnation, credentialGrant)
	execute(t, c, "INSERT INTO grant_record VALUES(?,?,?,1)", incarnation, credentialGrant, actorID)
	execute(t, c, credentialInsert, credentialArgs()...)
	if e = schema.ValidateV5(ctx(t), c, identity); e != nil {
		t.Fatal(e)
	}
	execute(t, c, "COMMIT")
	return p
}
func legacyDigest(t *testing.T, p string) [32]byte {
	var digest [32]byte
	e := inspectExisting(ctx(t), p, identity, func(r InspectionRead) error {
		v := r.(*inspectionReader)
		var e error
		digest, e = recordsDigest(ctx(t), v.conn)
		return e
	}, schema.ValidateV5)
	if e != nil {
		t.Fatal(e)
	}
	return digest
}
func TestHierarchyUpgradeRollbackConservationAndUnknown(t *testing.T) {
	for _, stage := range []string{"ddl", "version", "validated"} {
		t.Run(stage, func(t *testing.T) {
			p := legacyFive(t)
			before := legacyDigest(t, p)
			changed, e := upgradeStorage(ctx(t), p, identity, func(at string) error {
				if at == stage {
					return errors.New("synthetic fault")
				}
				return nil
			})
			if e == nil || changed {
				t.Fatal("fault did not refuse")
			}
			if after := legacyDigest(t, p); after != before {
				t.Fatal("rollback changed baseline rows")
			}
			x, e := InspectStorage(ctx(t), p, identity)
			if e != nil || x.PhysicalVersion != 5 || x.BaseSchemaVersion != 5 {
				t.Fatal("rollback not valid5", e)
			}
		})
	}
	p := legacyFive(t)
	if s, e := Existing(ctx(t), p, identity); e == nil {
		if s != nil {
			s.Close()
		}
		t.Fatal("startup migrated5")
	}
	if changed, e := UpgradeStorage(ctx(t), p, identity); e != nil || !changed {
		t.Fatal("upgrade", e)
	}
	x, e := InspectStorage(ctx(t), p, identity)
	if e != nil || x.PhysicalVersion != 6 || x.BaseSchemaVersion != 5 || !x.Valid {
		t.Fatal("inspection", e)
	}
	if changed, e := UpgradeStorage(ctx(t), p, identity); e != nil || changed {
		t.Fatal("valid6 no-change", e)
	}
	s, e := Existing(ctx(t), p, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "BEGIN IMMEDIATE")
		execute(t, c, "INSERT INTO card_parent VALUES(?,?,?)", incarnation, cardTwo, cardID)
		execute(t, c, "COMMIT")
		if e := schema.Validate(context.Background(), c, identity); e != nil {
			t.Fatal(e)
		}
	})
	if _, e := InspectStorage(ctx(t), p, identity); e == nil {
		t.Fatal("held lease inspect accepted")
	}
	q := legacyFive(t)
	changed, e := upgradeStorage(ctx(t), q, identity, func(at string) error {
		if at == "committed" {
			return errors.New("reply lost")
		}
		return nil
	})
	if changed || !errors.Is(e, ErrUpgradeUnknown) {
		t.Fatal("reply loss guessed", e)
	}
	x, e = InspectStorage(ctx(t), q, identity)
	if e != nil || x.PhysicalVersion != 6 {
		t.Fatal("deliberate reconciliation", e)
	}
}
func TestHierarchyUpgradePostCommitCleanupIsUnknown(t *testing.T) {
	p := legacyFive(t)
	changed, err := upgradeStorage(ctx(t), p, identity, func(stage string) error {
		if stage == "cleanup" {
			return errors.New("synthetic cleanup failure")
		}
		return nil
	})
	if !changed || !errors.Is(err, ErrUpgradeUnknown) {
		t.Fatal("cleanup failure became definite", changed, err)
	}
	x, e := InspectStorage(ctx(t), p, identity)
	if e != nil || x.PhysicalVersion != 6 {
		t.Fatal("deliberate inspect", e)
	}
}
func TestHierarchyUpgradeRefusesUnknownAndMissingInventory(t *testing.T) {
	for _, name := range []string{"unknown.txt", "board.sqlite-journal"} {
		t.Run(name, func(t *testing.T) {
			p := legacyFive(t)
			before := legacyDigest(t, p)
			if e := os.WriteFile(filepath.Join(p, name), []byte("synthetic"), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := UpgradeStorage(ctx(t), p, identity); e == nil {
				t.Fatal("unknown inventory accepted")
			}
			if e := os.Remove(filepath.Join(p, name)); e != nil {
				t.Fatal(e)
			}
			if legacyDigest(t, p) != before {
				t.Fatal("refusal changed data")
			}
		})
	}
	p := legacyFive(t)
	if e := os.Remove(filepath.Join(p, "board.lock")); e != nil {
		t.Fatal(e)
	}
	if _, e := UpgradeStorage(ctx(t), p, identity); e == nil {
		t.Fatal("missing lock accepted")
	}
	if _, e := os.Stat(filepath.Join(p, "board.lock")); !os.IsNotExist(e) {
		t.Fatal("missing lock created")
	}
}

// Layout5 deliberately accepted opaque valid historical JSON. The additive
// upgrade preserves all absent-parent records without demanding a newer DTO.
func TestHierarchyUpgradePreservesOpaqueLegacyStates(t *testing.T) {
	states := []string{`{}`, `{"legacy":"opaque","title":17}`, `["legacy",{"parent":"nested-only"}]`, `{"legacy":1,"legacy":2}`, fmt.Sprintf(`{"legacy":"opaque","parent":{"boardId":%q,"incarnation":%q,"cardId":%q}}`, boardID, incarnation, cardTwo)}
	for _, state := range states {
		t.Run(fmt.Sprintf("state-%x", sha256.Sum256([]byte(state)))[:20], func(t *testing.T) {
			p := legacyFive(t)
			withOfflineFive(t, p, func(c *sql.Conn) {
				execute(t, c, "UPDATE card SET revision=2 WHERE card_id=?", cardID)
				execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z',?)", incarnation, boardID, incarnation, cardID, actorID, state)
			})
			before := legacyDigest(t, p)
			changed, e := UpgradeStorage(ctx(t), p, identity)
			if e != nil || !changed {
				t.Fatal("legacy upgrade refused", e)
			}
			// Compare the entire baseline row digest after excluding only the new empty
			// adjacency table from the same typed row stream.
			e = InspectExisting(ctx(t), p, identity, func(r InspectionRead) error {
				var actual string
				if err := r.Scan("SELECT state FROM card_revision WHERE card_id=?", []any{cardID}, &actual); err != nil {
					return err
				}
				if actual != state {
					t.Fatal("opaque bytes rewritten")
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			// Reconstruct a baseline5 validation in a rolled-back writer snapshot only;
			// no committed downgrade occurs, and every original row is conserved.
			s, e := Existing(ctx(t), p, identity)
			if e != nil {
				t.Fatal(e)
			}
			withWriter(t, s, func(c *sql.Conn) {
				execute(t, c, "BEGIN IMMEDIATE")
				execute(t, c, "DROP TRIGGER card_parent_no_update")
				execute(t, c, "DROP INDEX card_parent_by_parent")
				execute(t, c, "DROP TABLE card_parent")
				execute(t, c, "PRAGMA user_version=5")
				if e := schema.ValidateV5(ctx(t), c, identity); e != nil {
					t.Fatal(e)
				}
				after, e := recordsDigest(ctx(t), c)
				if e != nil || before != after {
					t.Fatal("baseline rows changed", e)
				}
				execute(t, c, "ROLLBACK")
			})
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			if _, e = InspectStorage(ctx(t), p, identity); e != nil {
				t.Fatal("fresh6 reopen", e)
			}
		})
	}
}
func withOfflineFive(t *testing.T, p string, fn func(*sql.Conn)) {
	t.Helper()
	g, e := acquireUpgradePath(p)
	if e != nil {
		t.Fatal(e)
	}
	defer g.close()
	db, e := pool(g.path, false)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c, e := db.Conn(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = configure(ctx(t), c, false, false); e != nil {
		t.Fatal(e)
	}
	execute(t, c, "BEGIN IMMEDIATE")
	fn(c)
	if e = schema.ValidateV5(ctx(t), c, identity); e != nil {
		t.Fatal(e)
	}
	execute(t, c, "COMMIT")
}
func TestHierarchyUpgradeRejectsMalformedHistoricalParentWithoutRewrite(t *testing.T) {
	good := fmt.Sprintf(`{"boardId":%q,"incarnation":%q,"cardId":%q}`, boardID, incarnation, cardTwo)
	parents := []string{`null`, `"bare-id"`, `{}`, `{"boardId":"wrong"}`, strings.Replace(good, cardTwo, missingID, 1), strings.Replace(good, incarnation, "90000000-0000-0000-0000-000000000001", 1), strings.Replace(good, cardTwo, cardID, 1), strings.TrimSuffix(good, "}") + `,"extra":true}`}
	states := []string{}
	for _, parent := range parents {
		states = append(states, `{"legacy":"opaque","parent":`+parent+`}`)
	}
	states = append(states, `{"\u0070arent":null}`, `{"parent":`+good+`,"parent":null}`, `{"parent":null,"\u0070arent":`+good+`}`)
	for index, state := range states {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			p := legacyFive(t)
			withOfflineFive(t, p, func(c *sql.Conn) {
				execute(t, c, "UPDATE card SET revision=2 WHERE card_id=?", cardID)
				execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z',?)", incarnation, boardID, incarnation, cardID, actorID, state)
			})
			before := legacyDigest(t, p)
			if changed, e := UpgradeStorage(ctx(t), p, identity); e == nil || changed {
				t.Fatal("malformed parent upgraded")
			}
			if legacyDigest(t, p) != before {
				t.Fatal("refused extension changed baseline")
			}
			x, e := InspectStorage(ctx(t), p, identity)
			if e != nil || x.PhysicalVersion != 5 {
				t.Fatal("not valid unchanged5", e)
			}
		})
	}
}
