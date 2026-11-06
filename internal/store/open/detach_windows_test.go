//go:build windows && amd64

package open

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/store/schema"
)

func legacyTen(t *testing.T) string {
	t.Helper()
	p := legacyNine(t, false)
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 9, 10); err != nil || !changed {
		t.Fatal(err)
	}
	return p
}

func TestDetachUpgradeConservesAllTypedRowsAndExplicitBoundary(t *testing.T) {
	p := legacyTen(t)
	before := supportedDigest(t, p)
	if s, err := Existing(ctx(t), p, identity); err == nil || !strings.Contains(err.Error(), "explicit offline upgrade 10->11") {
		if s != nil {
			s.Close()
		}
		t.Fatal("ten startup", err)
	}
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil || !changed {
		t.Fatal(err)
	}
	if before != supportedDigest(t, p) {
		t.Fatal("retained rows or bytes changed")
	}
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil || changed {
		t.Fatal("repeat", err)
	}
	s, err := Existing(ctx(t), p, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	withWriter(t, s, func(c *sql.Conn) {
		if schema.ValidateV10(ctx(t), c, identity) == nil {
			t.Fatal("ten accepted11")
		}
	})
}

func TestDetachUpgradeRollbackAndUnknown(t *testing.T) {
	for _, stage := range []string{"operation-dropped", "activity-dropped", "operation-copied", "activity-copied", "ddl", "version", "validated", "committed", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			p := legacyTen(t)
			before := supportedDigest(t, p)
			fileBefore := fileDigest(t, p)
			changed, err := upgradeStorageStep(ctx(t), p, identity, 10, 11, func(at string) error {
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
			want := 10
			if post {
				want = 11
			}
			if err != nil || inspection.PhysicalVersion != want {
				t.Fatal("version", inspection, err)
			}
			if supportedDigest(t, p) != before || !post && fileDigest(t, p) != fileBefore {
				t.Fatal("source changed")
			}
		})
	}
}
func TestFrozenTenRejectsDetachHeadersAndNestedOriginal(t *testing.T) {
	for _, location := range []string{"command", "activity", "current-original", "source-original"} {
		t.Run(location, func(t *testing.T) {
			p := legacyTen(t)
			withOfflineLayout(t, p, func(c *sql.Conn) {
				switch location {
				case "command":
					refused(t, c, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at) VALUES(?,?,?,?,1,?,?,'attachment.detach','card',?,2,1,'NOT_COMMITTED','NOT_FOUND','2026-10-25T00:00:00Z')`, incarnation, boardID, incarnation, "b9000000-0000-0000-0000-000000000009", actorID, strings.Repeat("a", 64), cardID)
				case "activity":
					refused(t, c, `INSERT INTO activity VALUES(?,?,?,?,?,?,'attachment.detach',2,'2026-10-25T00:00:00Z','[]')`, incarnation, boardID, incarnation, 99, actorID, "b9000000-0000-0000-0000-000000000009")
				default:
					original := frozenNineWorkflowOriginal(t, false)
					sourceInc := incarnation
					current := 1
					if location == "source-original" {
						sourceInc = "b9000000-0000-0000-0000-000000000088"
						current = 0
					}
					// Deliberately corrupt a nested full original without widening the header.
					execute(t, c, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,expected_revision,terminal_class,code,terminal_at) VALUES(?,?,?,?,?,?,?,'workflow.edit',2,'NOT_COMMITTED','INVALID_INPUT','2026-10-25T00:00:00Z')`, incarnation, boardID, sourceInc, "b9000000-0000-0000-0000-000000000009", current, actorID, original.Fingerprint())
					execute(t, c, `INSERT INTO operation_full VALUES(?,?,?,?,?,'{}')`, incarnation, boardID, sourceInc, "b9000000-0000-0000-0000-000000000009", strings.Replace(string(original.CanonicalBytes()), `"command":"workflow.edit"`, `"command":"attachment.detach"`, 1))
					if schema.ValidateV10(ctx(t), c, identity) == nil {
						t.Fatal("frozen ten accepted nested detach")
					}
				}
			}, func(_ context.Context, _ *sql.Conn, _ schema.Identity) error { return nil })
		})
	}
}
