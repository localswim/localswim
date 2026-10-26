//go:build windows && amd64

package open

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/store/schema"
)

func TestTitleUnicodeScalarStorage(t *testing.T) {
	// NUL replacement belongs only to the CHECK expression, never the stored text.
	mixed := strings.Repeat("\x00\u96ea\U0001f600e\u0301", 40)
	cases := []struct {
		name, title string
		valid       bool
	}{
		{"nul-alone", "\x00", true},
		{"nul-prefix", "\x00Snow", true},
		{"nul-middle", "Sn\x00ow", true},
		{"nul-suffix", "Snow\x00", true},
		{"mixed-200", mixed, true},
		{"astral-200", strings.Repeat("\U0001f600", 200), true},
		{"nul-200", strings.Repeat("\x00", 200), true},
		{"json-escapes-200", strings.Repeat("\\\"\b\f\n\r\t\x01\x1f\x00", 20), true},
		{"literal-escapes-200", strings.Repeat(`\u0000`, 33) + `\x`, true},
		{"backslash-nul", "\\\x00\\u0000", true},
		{"empty", "", false},
		{"mixed-201", mixed + "\u96ea", false},
		{"astral-201", strings.Repeat("\U0001f600", 201), false},
		{"nul-201", strings.Repeat("\x00", 201), false},
		{"nul-hidden-overflow", "Snow\x00" + strings.Repeat("\u96ea", 196), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, p := newStore(t)
			withWriter(t, s, func(c *sql.Conn) {
				seed(t, c)
				if tc.valid {
					execute(t, c, "UPDATE card SET title=? WHERE card_id=?", tc.title, cardID)
				} else {
					constraint(t, c, 275, "CHECK constraint failed", "UPDATE card SET title=? WHERE card_id=?", tc.title, cardID)
				}
				var text string
				var raw []byte
				if err := c.QueryRowContext(ctx(t), "SELECT title,CAST(title AS BLOB) FROM card WHERE card_id=?", cardID).Scan(&text, &raw); err != nil {
					t.Fatal(err)
				}
				want := "Synthetic"
				if tc.valid {
					want = tc.title
				}
				if text != want || !bytes.Equal(raw, []byte(want)) {
					t.Fatal("stored title bytes changed")
				}
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
			withWriter(t, reopened, func(c *sql.Conn) {
				var raw []byte
				if err := c.QueryRowContext(ctx(t), "SELECT CAST(title AS BLOB) FROM card WHERE card_id=?", cardID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				want := "Synthetic"
				if tc.valid {
					want = tc.title
				}
				if !bytes.Equal(raw, []byte(want)) {
					t.Fatal("reopened title bytes changed")
				}
			})
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTitleUnicodeScalarAudit(t *testing.T) {
	s, p := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		execute(t, c, "PRAGMA ignore_check_constraints=ON")
		execute(t, c, "UPDATE card SET title=? WHERE card_id=?", "Snow\x00"+strings.Repeat("\u96ea", 196), cardID)
		execute(t, c, "PRAGMA ignore_check_constraints=OFF")
		if err := schema.Validate(ctx(t), c, identity); err == nil || err.Error() != "storage integrity check failed" {
			t.Fatalf("title audit refusal: %v", err)
		}
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if reopened, err := Existing(ctx(t), p, identity); err == nil {
		reopened.Close()
		t.Fatal("invalid scalar count accepted on reopen")
	}
	after, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed title audit changed database bytes")
	}
}

func TestPhysicalLayoutTwoRefusedWithoutWrites(t *testing.T) {
	// Undo only this unreleased correction to build the preceding exact layout.
	// Neither Existing nor any production path translates an existing database.
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
	if err := configure(ctx(t), c, false, true); err != nil {
		t.Fatal(err)
	}
	execute(t, c, "BEGIN IMMEDIATE")
	for _, statement := range schema.Statements() {
		statement = strings.ReplaceAll(statement, "schema_version INTEGER NOT NULL DEFAULT 3 CHECK(schema_version=3)", "schema_version INTEGER NOT NULL DEFAULT 2 CHECK(schema_version=2)")
		statement = previousTitleLength(statement)
		execute(t, c, statement)
	}
	execute(t, c, "INSERT INTO board(singleton,board_id,incarnation) VALUES(1,?,?)", boardID, incarnation)
	execute(t, c, "INSERT INTO policy(incarnation) VALUES(?)", incarnation)
	execute(t, c, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=2", schema.ApplicationID))
	execute(t, c, "COMMIT")
	c.Close()
	db.Close()
	g.close()
	before, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := directoryNames(p)
	if err != nil {
		t.Fatal(err)
	}
	if reopened, err := Existing(ctx(t), p, identity); err == nil {
		reopened.Close()
		t.Fatal("layout two accepted")
	} else if err.Error() != "unsupported storage user_version" {
		t.Fatalf("layout two refusal: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	afterInventory, err := directoryNames(p)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) || !reflect.DeepEqual(inventory, afterInventory) {
		t.Fatal("layout two refusal changed bytes or directory inventory")
	}
}

// previousTitleLength is used only to reconstruct unsupported synthetic layouts.
func previousTitleLength(statement string) string {
	for _, current := range schema.Statements() {
		if !strings.HasPrefix(current, "CREATE TABLE card (") {
			continue
		}
		start := strings.Index(current, "title TEXT NOT NULL CHECK(") + len("title TEXT NOT NULL CHECK(")
		end := strings.Index(current[start:], " BETWEEN 1 AND 200)") + start
		return strings.ReplaceAll(statement, current[start:end], "length(title)")
	}
	panic("card title CHECK missing")
}
