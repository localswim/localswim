//go:build windows && amd64

package open

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/store/schema"
)

// Captured from schema.Statements at physical layout 3, before the layout-4
// edits, from source commit 70f87b48cca85029924d2a72bba114d281b5c4a1.
// This is the complete preceding DDL, not a current-layout header substitution.
func physicalThreeStatements(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("testdata/layout3-statements.json")
	if err != nil {
		t.Fatal(err)
	}
	var statements []string
	if err = json.Unmarshal(raw, &statements); err != nil {
		t.Fatal(err)
	}
	return statements
}

func legacyFiles(t *testing.T, p string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	entries, err := os.ReadDir(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			t.Fatal("unexpected legacy fixture object")
		}
		raw, err := os.ReadFile(filepath.Join(p, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = sha256.Sum256(raw)
	}
	return out
}

func TestPhysicalLayoutThreeRefusedWithoutWrites(t *testing.T) {
	p := privateDir(t)
	g, err := acquirePath(p, true)
	if err != nil {
		t.Fatal(err)
	}
	db, err := pool(g.path, false)
	if err != nil {
		g.close()
		t.Fatal(err)
	}
	c, err := db.Conn(ctx(t))
	if err != nil {
		db.Close()
		g.close()
		t.Fatal(err)
	}
	if err = configure(ctx(t), c, false, true); err != nil {
		t.Fatal(err)
	}
	execute(t, c, "BEGIN IMMEDIATE")
	for _, statement := range physicalThreeStatements(t) {
		execute(t, c, statement)
	}
	execute(t, c, "INSERT INTO board(singleton,board_id,incarnation) VALUES(1,?,?)", boardID, incarnation)
	execute(t, c, "INSERT INTO policy(incarnation) VALUES(?)", incarnation)
	execute(t, c, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=3", schema.ApplicationID))
	execute(t, c, "COMMIT")
	var oldGap string
	if err = c.QueryRowContext(ctx(t), "SELECT sql FROM sqlite_schema WHERE name='coverage_gap'").Scan(&oldGap); err != nil || strings.Contains(oldGap, "gap_id") || !strings.Contains(oldGap, "PRIMARY KEY(incarnation,origin_board,origin_incarnation,class,reason,through_inclusive,board_revision)") {
		t.Fatal("legacy fixture is not layout 3", err)
	}
	c.Close()
	db.Close()
	g.close()
	before := legacyFiles(t, p)
	if reopened, err := Existing(ctx(t), p, identity); err == nil {
		reopened.Close()
		t.Fatal("layout three accepted")
	} else if err.Error() != "unsupported storage user_version" {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, legacyFiles(t, p)) {
		t.Fatal("layout three refusal changed a file or inventory")
	}
}

func TestPhysicalFourGapAndGroupPersistence(t *testing.T) {
	s, p := newStore(t)
	oldOne := "20000000-0000-0000-0000-000000000002"
	oldTwo := "20000000-0000-0000-0000-000000000003"
	insert := "INSERT INTO coverage_gap(incarnation,origin_board,origin_incarnation,class,reason,from_exclusive,through_inclusive,recorded_at,board_revision) VALUES(?,?,?,'activity','expired',?,'2026-10-20T00:00:00Z',?,1)"
	withWriter(t, s, func(c *sql.Conn) {
		for _, old := range []string{oldOne, oldTwo} {
			execute(t, c, "INSERT INTO source_archive_group VALUES(?,?,?)", incarnation, boardID, old)
		}
		refused(t, c, "INSERT INTO source_archive_group VALUES(?,?,?)", incarnation, boardID, oldOne)
		refused(t, c, "INSERT INTO source_archive_group VALUES(?,?,?)", incarnation, boardID, incarnation)
		refused(t, c, "INSERT INTO source_archive_group VALUES(?,?,?)", incarnation, cardID, oldOne)
		refused(t, c, "INSERT INTO source_archive_group VALUES(?,?,?)", incarnation, boardID, strings.ToUpper("aaaaaaaa-0000-0000-0000-000000000001"))
		refused(t, c, "UPDATE source_archive_group SET origin_incarnation=?", missingID)
		refused(t, c, "DELETE FROM source_archive_group")
		for _, row := range []struct {
			from any
			at   string
		}{{nil, "2026-10-21T00:00:00Z"}, {nil, "2026-10-22T00:00:00Z"}, {"2026-10-19T00:00:00Z", "2026-10-21T00:00:00Z"}, {"2026-10-18T00:00:00Z", "2026-10-21T00:00:00Z"}} {
			execute(t, c, insert, incarnation, boardID, oldOne, row.from, row.at)
			refused(t, c, insert, incarnation, boardID, oldOne, row.from, row.at)
		}
		refused(t, c, insert, incarnation, boardID, oldOne, "", "2026-10-21T00:00:00Z")
		refused(t, c, insert, incarnation, boardID, oldOne, nil, "")
		refused(t, c, insert, missingID, boardID, oldOne, nil, "2026-10-21T00:00:00Z")
		if err := schema.Validate(ctx(t), c, identity); err != nil {
			t.Fatal(err)
		}
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Existing(ctx(t), p, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	withWriter(t, reopened, func(c *sql.Conn) {
		var groups, gaps, nulls int
		if err := c.QueryRowContext(ctx(t), "SELECT (SELECT count(*) FROM source_archive_group),(SELECT count(*) FROM coverage_gap),(SELECT count(*) FROM coverage_gap WHERE from_exclusive IS NULL)").Scan(&groups, &gaps, &nulls); err != nil || groups != 2 || gaps != 4 || nulls != 2 {
			t.Fatal("persisted group/gap tuples changed", err)
		}
		if err := schema.Validate(ctx(t), c, identity); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPhysicalFourExactLayoutAndForeignKeyAudit(t *testing.T) {
	for _, mutation := range []string{"DROP INDEX coverage_gap_complete_tuple", "DROP TRIGGER source_archive_group_origin", "CREATE TABLE unsupported_object(value TEXT) STRICT"} {
		t.Run(strings.Fields(mutation)[1], func(t *testing.T) {
			s, _ := newStore(t)
			withWriter(t, s, func(c *sql.Conn) {
				execute(t, c, mutation)
				if err := schema.Validate(ctx(t), c, identity); err == nil {
					t.Fatal("changed exact layout accepted")
				}
			})
		})
	}
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "PRAGMA foreign_keys=OFF")
		execute(t, c, "INSERT INTO coverage_gap(incarnation,origin_board,origin_incarnation,class,reason,through_inclusive,recorded_at,board_revision) VALUES(?,?,?,'activity','expired','2026-10-20T00:00:00Z','2026-10-21T00:00:00Z',1)", missingID, boardID, incarnation)
		execute(t, c, "PRAGMA foreign_keys=ON")
		if err := schema.Validate(ctx(t), c, identity); err == nil || err.Error() != "storage foreign key check failed" {
			t.Fatal("orphan gap accepted", err)
		}
	})
}

func TestPhysicalFourSourceGroupAudit(t *testing.T) {
	for _, origin := range []schema.Identity{{BoardID: boardID, Incarnation: incarnation}, {BoardID: cardID, Incarnation: missingID}} {
		t.Run(origin.Incarnation, func(t *testing.T) {
			s, _ := newStore(t)
			withWriter(t, s, func(c *sql.Conn) {
				var guard string
				if err := c.QueryRowContext(ctx(t), "SELECT sql FROM sqlite_schema WHERE name='source_archive_group_origin'").Scan(&guard); err != nil {
					t.Fatal(err)
				}
				execute(t, c, "DROP TRIGGER source_archive_group_origin")
				execute(t, c, "INSERT INTO source_archive_group VALUES(?,?,?)", incarnation, origin.BoardID, origin.Incarnation)
				execute(t, c, guard)
				if err := schema.Validate(ctx(t), c, identity); err == nil || err.Error() != "storage relational consistency check failed" {
					t.Fatal("invalid stored source group accepted", err)
				}
			})
		})
	}
}
