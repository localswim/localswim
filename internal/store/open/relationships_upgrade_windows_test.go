//go:build windows && amd64

package open

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/localswim/localswim/internal/store/schema"
)

func legacySix(t *testing.T) string {
	t.Helper()
	p := legacyFive(t)
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 5, 6); e != nil || !changed {
		t.Fatal(e)
	}
	withOfflineLayout(t, p, func(c *sql.Conn) {
		execute(t, c, "INSERT INTO card_parent VALUES(?,?,?)", incarnation, cardTwo, cardID)
		execute(t, c, "UPDATE card SET revision=2 WHERE card_id=?", cardID)
		execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z',?)", incarnation, boardID, incarnation, cardID, actorID, `{"legacy":"opaque","linkIds":["history-only"]}`)
		for n, v := range []struct {
			kind, board, inc, target string
			active                   int
			internal                 any
		}{
			{"blocking", boardID, incarnation, cardTwo, 1, cardTwo},
			{"informational", boardID, incarnation, cardTwo, 1, cardTwo},
			{"blocking", "90000000-0000-0000-0000-000000000001", "90000000-0000-0000-0000-000000000002", missingID, 0, nil},
			{"informational", boardID, "90000000-0000-0000-0000-000000000003", missingID, 0, nil},
		} {
			id := fmt.Sprintf("a0000000-0000-0000-0000-%012d", n+1)
			execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", incarnation, id, cardID, boardID, incarnation)
			execute(t, c, "INSERT INTO link(rowid,incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,'2026-10-25T00:00:00Z')", n*10+7, incarnation, id, cardID, v.kind, v.board, v.inc, v.target, v.internal, v.active)
			if v.active == 1 {
				execute(t, c, "INSERT INTO current_reference VALUES(?,?,'link',?)", incarnation, cardID, id)
			} else {
				execute(t, c, "INSERT INTO revision_reference VALUES(?,?,?,?,1,'link',?)", incarnation, boardID, incarnation, cardID, id)
			}
		}
		execute(t, c, "INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,expected_revision,terminal_class,code,terminal_at) VALUES(?,?,?,?,1,?,?,'card.create',0,'NOT_COMMITTED','INVALID_INPUT','2026-10-25T00:00:00Z')", incarnation, boardID, incarnation, opID, actorID, hash)
		execute(t, c, "INSERT INTO operation_full VALUES(?,?,?,?,?,?)", incarnation, boardID, incarnation, opID, `{"old":"original","omitted":true}`, `{"outcome":"NOT_COMMITTED"}`)
		execute(t, c, "INSERT INTO blob VALUES(?,?,0,?)", incarnation, hash, []byte{})
	}, schema.ValidateV6)
	return p
}
func withOfflineLayout(t *testing.T, p string, fn func(*sql.Conn), validate func(context.Context, *sql.Conn, schema.Identity) error) {
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
	if validate != nil {
		if e = validate(ctx(t), c, identity); e != nil {
			t.Fatal(e)
		}
	}
	execute(t, c, "COMMIT")
}
func supportedDigest(t *testing.T, p string) [32]byte {
	t.Helper()
	var sum [32]byte
	e := inspectExisting(ctx(t), p, identity, func(r InspectionRead) error {
		var e error
		sum, e = recordsDigest(ctx(t), r.(*inspectionReader).conn)
		return e
	}, validateSupported)
	if e != nil {
		t.Fatal(e)
	}
	return sum
}
func fileDigest(t *testing.T, p string) [32]byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	return sha256.Sum256(b)
}
func TestRelationshipUpgradeConservesCompleteTypedRows(t *testing.T) {
	p := legacySix(t)
	before := supportedDigest(t, p)
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 6, 7); e != nil || !changed {
		t.Fatal("upgrade", e)
	}
	if supportedDigest(t, p) != before {
		t.Fatal("typed rows or rowids changed")
	}
	x, e := InspectStorage(ctx(t), p, identity)
	if e != nil || x.PhysicalVersion != 7 || x.BaseSchemaVersion != 5 {
		t.Fatal(x, e)
	}
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 6, 7); e != nil || changed {
		t.Fatal("matching7 not no-change", e)
	}
	s, e := Existing(ctx(t), p, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	withWriter(t, s, func(c *sql.Conn) {
		if e := schema.ValidateV6(ctx(t), c, identity); e == nil {
			t.Fatal("old6 validator accepted7")
		}
		execute(t, c, "BEGIN IMMEDIATE")
		insert := func(id, owner, target, kind string, active int) {
			execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", incarnation, id, owner, boardID, incarnation)
			execute(t, c, "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,?,?,?,?,?,?,'2026-10-25T00:00:00Z')", incarnation, id, owner, kind, boardID, incarnation, target, target, active)
		}
		insert("b0000000-0000-0000-0000-000000000001", cardID, cardTwo, "related", 1)
		id := "b0000000-0000-0000-0000-000000000002"
		execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", incarnation, id, cardTwo, boardID, incarnation)
		refused(t, c, "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,'related',?,?,?,?,1,'2026-10-25T00:00:00Z')", incarnation, id, cardTwo, boardID, incarnation, cardID, cardID)
		execute(t, c, "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,'related',?,?,?,?,0,'2026-10-25T00:00:00Z')", incarnation, id, cardTwo, boardID, incarnation, cardID, cardID)
		insert("b0000000-0000-0000-0000-000000000003", cardTwo, cardID, "related", 0)
		insert("b0000000-0000-0000-0000-000000000004", cardID, cardTwo, "reference", 1)
		insert("b0000000-0000-0000-0000-000000000005", cardID, cardTwo, "duplicate", 1)

		// These are distinct unordered exact endpoints: each foreign endpoint is
		// unavailable evidence, not the other local card with the same UUID.
		foreignBoard := "90000000-0000-0000-0000-000000000001"
		foreignInc := "90000000-0000-0000-0000-000000000002"
		for n, pair := range [][2]string{{cardID, cardTwo}, {cardTwo, cardID}} {
			id := fmt.Sprintf("c0000000-0000-0000-0000-%012d", n+1)
			execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", incarnation, id, pair[0], boardID, incarnation)
			execute(t, c, "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,active,created_at) VALUES(?,?,?,'related',?,?,?,1,'2026-10-25T00:00:00Z')", incarnation, id, pair[0], foreignBoard, foreignInc, pair[1])
			if n == 0 {
				execute(t, c, "INSERT INTO revision_reference VALUES(?,?,?,?,1,'link',?)", incarnation, boardID, incarnation, pair[0], id)
			}
			if n == 0 {
				if e := schema.Validate(ctx(t), c, identity); e != nil {
					t.Fatal("historical foreign evidence refused", e)
				}
				execute(t, c, "INSERT INTO current_reference VALUES(?,?,'link',?)", incarnation, pair[0], id)
				if e := schema.Validate(ctx(t), c, identity); e == nil {
					t.Fatal("foreign current new kind accepted")
				}
				execute(t, c, "DELETE FROM current_reference WHERE entity_id=?", id)
			}
		}
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal("foreign exact-pair distinction refused", e)
		}
		execute(t, c, "ROLLBACK")
	})
}
func TestRelationshipUpgradeRollbackAndUnknown(t *testing.T) {
	for _, stage := range []string{"link-dropped", "link-copied", "ddl", "version", "validated", "committed", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			p := legacySix(t)
			before := supportedDigest(t, p)
			bytesBefore := fileDigest(t, p)
			changed, e := upgradeStorageStep(ctx(t), p, identity, 6, 7, func(at string) error {
				if at == stage {
					return errors.New("synthetic fault")
				}
				return nil
			})
			post := stage == "committed" || stage == "cleanup"
			if e == nil || errors.Is(e, ErrUpgradeUnknown) != post {
				t.Fatal("commit knowledge", changed, e)
			}
			if post && stage == "cleanup" && !changed {
				t.Fatal("lost successful commit knowledge")
			}
			if !post && changed {
				t.Fatal("precommit reports change")
			}
			x, e := InspectStorage(ctx(t), p, identity)
			want := 6
			if post {
				want = 7
			}
			if e != nil || x.PhysicalVersion != want {
				t.Fatal(x, e)
			}
			if supportedDigest(t, p) != before {
				t.Fatal("fault changed retained rows")
			}
			if !post && fileDigest(t, p) != bytesBefore {
				t.Fatal("rollback changed database bytes")
			}
		})
	}
}
func TestRelationshipUpgradeRefusesUnsafeSourcesUnchanged(t *testing.T) {
	for _, kind := range []string{"wrong-identity", "future", "extra-object", "corrupt-check", "unknown-file", "dirty-sidecar", "held-exclusion", "wrong-step"} {
		t.Run(kind, func(t *testing.T) {
			p := legacySix(t)
			id := identity
			switch kind {
			case "wrong-identity":
				id.BoardID = missingID
			case "future":
				withOfflineLayout(t, p, func(c *sql.Conn) { execute(t, c, "PRAGMA user_version=8") }, nil)
			case "extra-object":
				withOfflineLayout(t, p, func(c *sql.Conn) { execute(t, c, "CREATE TABLE unexpected(value TEXT)") }, nil)
			case "corrupt-check":
				withOfflineLayout(t, p, func(c *sql.Conn) {
					execute(t, c, "PRAGMA ignore_check_constraints=ON")
					execute(t, c, "UPDATE board SET schema_version=4")
				}, nil)
			case "unknown-file", "dirty-sidecar":
				name := "extra.txt"
				if kind == "dirty-sidecar" {
					name = "board.sqlite-journal"
				}
				if e := os.WriteFile(filepath.Join(p, name), []byte("synthetic"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			before := fileDigest(t, p)
			var held *pathGuard
			if kind == "held-exclusion" {
				var e error
				held, e = acquireUpgradePath(p)
				if e != nil {
					t.Fatal(e)
				}
				defer held.close()
			}
			from, to := 6, 7
			if kind == "wrong-step" {
				from, to = 7, 8
			}
			if changed, e := UpgradeStorageTo(ctx(t), p, id, from, to); e == nil || changed || errors.Is(e, ErrUpgradeUnknown) {
				t.Fatal("unsafe upgrade accepted or unknown", changed, e)
			}
			if fileDigest(t, p) != before {
				t.Fatal("refusal changed bytes")
			}
		})
	}
	// Two separately guarded operations: a refused second step retains the successful first.
	p := legacyFive(t)
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 6, 7); e == nil || changed {
		t.Fatal("wrong source5 admitted as6")
	}
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 5, 7); e == nil || changed {
		t.Fatal("direct5->7 admitted")
	}
	if changed, e := UpgradeStorageTo(ctx(t), p, identity, 5, 6); e != nil || !changed {
		t.Fatal(e)
	}
	before := fileDigest(t, p)
	if changed, e := upgradeStorageStep(ctx(t), p, identity, 6, 7, func(at string) error {
		if at == "link-copied" {
			return errors.New("stop")
		}
		return nil
	}); e == nil || changed {
		t.Fatal("second step not refused")
	}
	x, e := InspectStorage(ctx(t), p, identity)
	if e != nil || x.PhysicalVersion != 6 || fileDigest(t, p) != before {
		t.Fatal("first step lost", e)
	}
}

func TestRelationshipUpgradeRefusesLiveWriter(t *testing.T) {
	s, p := newStore(t)
	before := fileDigest(t, p)
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "BEGIN IMMEDIATE")
		if changed, e := UpgradeStorageTo(ctx(t), p, identity, 6, 7); e == nil || changed || errors.Is(e, ErrUpgradeUnknown) {
			t.Fatal("live writer upgrade admitted", changed, e)
		}
		execute(t, c, "ROLLBACK")
	})
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if fileDigest(t, p) != before {
		t.Fatal("live writer refusal changed bytes")
	}
}
