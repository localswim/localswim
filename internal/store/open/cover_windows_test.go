//go:build windows && amd64

package open

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	"github.com/localswim/localswim/internal/store/schema"
)

const coverFile = "70000000-0000-0000-0000-000000000013"
const coverSource = "70000000-0000-0000-0000-000000000011"

func legacyEleven(t *testing.T) string {
	t.Helper()
	p := legacyTen(t)
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 10, 11); err != nil || !changed {
		t.Fatal("upgrade to predecessor", changed, err)
	}
	return p
}

func advanceCoverLayout(t *testing.T, p string) {
	t.Helper()
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 11, 12); err != nil || !changed {
		t.Fatal("upgrade to cover layout", changed, err)
	}
}

func coverUpgradeDigest(t *testing.T, p string) [32]byte {
	t.Helper()
	var digest [32]byte
	err := inspectExisting(ctx(t), p, identity, func(r InspectionRead) error {
		var err error
		digest, err = recordsDigestUpgrade(ctx(t), r.(*inspectionReader).conn, 11)
		return err
	}, schema.Validate)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestCoverUpgradeBoundaryAndConservation(t *testing.T) {
	p := legacyEleven(t)
	before := supportedDigest(t, p)
	fileBefore := fileDigest(t, p)
	if s, err := Existing(ctx(t), p, identity); err == nil || !strings.Contains(err.Error(), "explicit offline upgrade 11->12") {
		if s != nil {
			s.Close()
		}
		t.Fatal("old startup", err)
	}
	wrong := identity
	wrong.Incarnation = cardTwo
	if changed, err := UpgradeStorageTo(ctx(t), p, wrong, 11, 12); err == nil || changed || fileDigest(t, p) != fileBefore {
		t.Fatal("wrong identity changed source", changed, err)
	}
	owner, err := acquireUpgradePath(p)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := UpgradeStorageTo(ctx(t), p, identity, 11, 12)
	if closeErr := owner.close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err == nil || changed || fileDigest(t, p) != fileBefore {
		t.Fatal("active owner admitted upgrade", changed, err)
	}
	advanceCoverLayout(t, p)
	if coverUpgradeDigest(t, p) != before {
		t.Fatal("retained rows, rowids or payload bytes changed")
	}
	if changed, err := UpgradeStorageTo(ctx(t), p, identity, 11, 12); err != nil || changed {
		t.Fatal("repeat", changed, err)
	}
	s, err := Existing(ctx(t), p, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	withWriter(t, s, func(c *sql.Conn) {
		if schema.ValidateV11(ctx(t), c, identity) == nil {
			t.Fatal("frozen eleven accepted twelve")
		}
	})
}

func TestCoverUpgradeRefusesInvalidLayoutUnchanged(t *testing.T) {
	for _, fixture := range []struct{ name, mutation string }{
		{"extra-object", `CREATE TABLE unexpected(value TEXT)`},
		{"future", `PRAGMA user_version=13`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			p := legacyEleven(t)
			withOfflineLayout(t, p, func(c *sql.Conn) { execute(t, c, fixture.mutation) }, nil)
			before := fileDigest(t, p)
			if changed, err := UpgradeStorageTo(ctx(t), p, identity, 11, 12); err == nil || changed || fileDigest(t, p) != before {
				t.Fatal("invalid source changed", changed, err)
			}
		})
	}
}

func TestCoverUpgradeRollbackAndUnknown(t *testing.T) {
	for _, stage := range []string{"ddl", "version", "validated", "committed", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			p := legacyEleven(t)
			before := supportedDigest(t, p)
			fileBefore := fileDigest(t, p)
			changed, err := upgradeStorageStep(ctx(t), p, identity, 11, 12, func(at string) error {
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
			want := 11
			if post {
				want = 12
			}
			if err != nil || inspection.PhysicalVersion != want || inspection.BaseSchemaVersion != 5 {
				t.Fatal("deliberate inspection", inspection, err)
			}
			if post && coverUpgradeDigest(t, p) != before || !post && (supportedDigest(t, p) != before || fileDigest(t, p) != fileBefore) {
				t.Fatal("retained inventory changed")
			}
		})
	}
}

func TestCoverUpgradeRefusesPredecessorVocabulary(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		version    int
		origin     string
		commit     bool
		state      string
		inputValue string
	}{
		{name: "base5-snapshot", version: 5, origin: incarnation, state: `{"coverAttachmentId":null}`},
		{name: "source-snapshot", version: 11, origin: coverSource, state: `{"coverAttachmentId":"` + coverFile + `"}`},
		{name: "current-commit", version: 11, origin: incarnation, commit: true, inputValue: `"` + coverFile + `"`},
		{name: "current-refusal", version: 11, origin: incarnation, inputValue: `null`},
		{name: "source-commit", version: 11, origin: coverSource, commit: true, inputValue: `"` + coverFile + `"`},
		{name: "source-refusal", version: 11, origin: coverSource, inputValue: `null`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var p string
			if fixture.version == 5 {
				p = legacyFive(t)
			} else {
				p = legacyEleven(t)
			}
			withOfflineLayout(t, p, func(c *sql.Conn) {
				if fixture.state != "" {
					execute(t, c, "UPDATE card SET revision=2 WHERE card_id=?", cardID)
					insertCoverSnapshot(t, c, fixture.origin, 1, fixture.state)
					return
				}
				insertCoverOriginal(t, c, fixture.origin, fixture.commit, fixture.inputValue)
			}, nil)
			before := fileDigest(t, p)
			if _, err := InspectStorage(ctx(t), p, identity); err == nil || !strings.Contains(err.Error(), "predecessor cover vocabulary") {
				t.Fatal("inspection accepted cover-bearing predecessor", err)
			}
			if changed, err := UpgradeStorageTo(ctx(t), p, identity, fixture.version, fixture.version+1); err == nil || changed || fileDigest(t, p) != before {
				t.Fatal("cover-bearing predecessor changed", changed, err)
			}
		})
	}
}

func insertCoverOriginal(t *testing.T, c *sql.Conn, origin string, committed bool, inputValue string) {
	t.Helper()
	opID := "b9000000-0000-0000-0000-000000000042"
	wire := fmt.Sprintf(`{"opId":%q,"incarnation":%q,"target":{"cardId":%q},"expectedRevision":1,"expectedCardRevision":1,"input":{"coverAttachmentId":%s}}`, opID, origin, cardID, inputValue)
	original, err := contract.Freeze([]byte(wire), contract.ResolvedRoute{Command: "card.edit", BoardID: boardID, TargetID: cardID}, contract.AuthenticatedActor{ActorID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	class, code := "NOT_COMMITTED", "NOT_FOUND"
	var revision any
	if committed {
		class, code, revision = "COMMITTED", "OK", 2
	}
	execute(t, c, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision) VALUES(?,?,?,?,?,?,?,'card.edit','card',?,1,1,?,?,'2026-10-25T00:00:00Z',?,?)`, incarnation, boardID, origin, opID, origin == incarnation, actorID, original.Fingerprint(), cardID, class, code, revision, revision)
	execute(t, c, `INSERT INTO operation_full VALUES(?,?,?,?,?,'{}')`, incarnation, boardID, origin, opID, string(original.CanonicalBytes()))
}

func insertCoverSnapshot(t *testing.T, c *sql.Conn, origin string, revision int, state string) {
	t.Helper()
	execute(t, c, `INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,?,?,'2026-10-25T00:00:00Z',?)`, incarnation, boardID, origin, cardID, revision, actorID, state)
}

func TestCoverCurrentCascadeAndHistoricalScope(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		const at = "2026-10-25T00:00:00Z"
		const digest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		execute(t, c, `UPDATE card SET revision=2 WHERE card_id=?`, cardID)
		execute(t, c, `INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at) VALUES(?,'attachment',?,?,?,?,?,?)`, incarnation, coverFile, cardID, boardID, incarnation, actorID, at)
		execute(t, c, `INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'file','',?,0,?)`, incarnation, coverFile, cardID, digest, at)
		execute(t, c, `INSERT INTO blob VALUES(?,?,0,x'')`, incarnation, digest)
		execute(t, c, `INSERT INTO current_attachment_pin VALUES(?,?,?,0)`, incarnation, coverFile, digest)
		const selectCover = `INSERT INTO card_cover(incarnation,card_id,attachment_id) VALUES(?,?,?)`
		refused(t, c, selectCover, incarnation, cardID, coverFile)
		execute(t, c, `INSERT INTO current_reference VALUES(?,?,'attachment',?)`, incarnation, cardID, coverFile)
		refused(t, c, selectCover, incarnation, cardTwo, coverFile)
		execute(t, c, selectCover, incarnation, cardID, coverFile)
		refused(t, c, selectCover, incarnation, cardID, coverFile)

		state := `{"coverAttachmentId":"` + coverFile + `"}`
		insertCoverSnapshot(t, c, coverSource, 1, state)
		insertCoverSnapshot(t, c, incarnation, 1, `{}`)
		addMembership := func(origin string) {
			execute(t, c, `INSERT INTO revision_reference VALUES(?,?,?,?,1,'attachment',?)`, incarnation, boardID, origin, cardID, coverFile)
			execute(t, c, `INSERT INTO history_blob_pin VALUES(?,?,?,?,1,?,?,0)`, incarnation, boardID, origin, cardID, coverFile, digest)
		}
		addMembership(incarnation)
		if err := schema.Validate(ctx(t), c, identity); err == nil || !strings.Contains(err.Error(), "historical cover membership") {
			t.Fatal("other origin or current membership substituted", err)
		}
		addMembership(coverSource)
		execute(t, c, `DELETE FROM current_reference WHERE incarnation=? AND card_id=? AND kind='attachment' AND entity_id=?`, incarnation, cardID, coverFile)
		execute(t, c, `DELETE FROM current_attachment_pin WHERE incarnation=? AND attachment_id=?`, incarnation, coverFile)
		var n int
		if err := c.QueryRowContext(ctx(t), `SELECT count(*) FROM card_cover`).Scan(&n); err != nil || n != 0 {
			t.Fatal("detach failed to clear selection", n, err)
		}
		if err := schema.Validate(ctx(t), c, identity); err != nil {
			t.Fatal("source membership invalid after detach", err)
		}
	})
}
