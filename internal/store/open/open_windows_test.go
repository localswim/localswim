//go:build windows && amd64

package open

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/localswim/localswim/internal/store/schema"
	"golang.org/x/sys/windows"
	"modernc.org/sqlite"
)

const boardID = "10000000-0000-0000-0000-000000000001"
const incarnation = "20000000-0000-0000-0000-000000000001"
const actorID = "30000000-0000-0000-0000-000000000001"
const cardID = "40000000-0000-0000-0000-000000000001"
const cardTwo = "40000000-0000-0000-0000-000000000002"
const missingID = "40000000-0000-0000-0000-000000000099"
const opID = "50000000-0000-0000-0000-000000000001"
const hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
const credentialGrant = "60000000-0000-0000-0000-000000000001"
const credentialInstallation = "70000000-0000-4000-8000-000000000001"
const credentialID = "80000000-0000-4000-8000-000000000001"
const credentialOrigin = "https://0123456789abcdef0123456789abcdef.localhost:47443"

// Only synthetic digest-shaped fixtures are stored; no bearer is generated here.
const credentialInsert = `INSERT INTO cli_credential(incarnation,board_id,grant_id,actor_id,installation_id,origin,credential_id,credential_hash,issued_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`

func credentialArgs() []any {
	return []any{incarnation, boardID, credentialGrant, actorID, credentialInstallation, credentialOrigin, credentialID, strings.Repeat("a", 64), "2026-10-26T12:00:00Z", "2026-11-25T12:00:00Z"}
}

func seedCredentialGrant(t *testing.T, c *sql.Conn) {
	t.Helper()
	execute(t, c, "INSERT INTO actor VALUES(?,?,?,?,?,?)", incarnation, actorID, "human", "Synthetic credential actor", boardID, incarnation)
	execute(t, c, "INSERT INTO grant_fact VALUES(?,?)", incarnation, credentialGrant)
	execute(t, c, "INSERT INTO grant_record VALUES(?,?,?,1)", incarnation, credentialGrant, actorID)
}

var identity = schema.Identity{BoardID: boardID, Incarnation: incarnation}

func privateDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("S01_TEST_ROOT")
	if root == "" {
		t.Skip("native synthetic fixtures require explicit private S01_TEST_ROOT")
	}
	root, err := filepath.Abs(root)
	if err != nil || !safePath(root) {
		t.Fatal("unsupported synthetic runtime root")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.EqualFold(root, repo) || strings.HasPrefix(strings.ToLower(root), strings.ToLower(repo)+`\`) {
		t.Fatal("synthetic runtime must be outside public repository")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")+"-"+hex.EncodeToString(nonce[:]))
	if err = windows.CreateDirectory(windows.StringToUTF16Ptr(p), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); err != nil {
		t.Fatal(err)
	}
	// Retain only synthetic artifacts; do not recursively clean caller paths.
	return p
}
func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}
func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := privateDir(t)
	s, err := Create(ctx(t), p, identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, p
}
func withWriter(t *testing.T, s *Store, fn func(*sql.Conn)) {
	t.Helper()
	l, e := s.Writer(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e = l.Close(); e != nil {
			t.Error(e)
		}
	}()
	if e = l.Do(ctx(t), func(c *sql.Conn) error { fn(c); return nil }); e != nil {
		t.Fatal(e)
	}
}
func execute(t *testing.T, c *sql.Conn, q string, args ...any) {
	t.Helper()
	if _, e := c.ExecContext(ctx(t), q, args...); e != nil {
		t.Fatal(e)
	}
}
func refused(t *testing.T, c *sql.Conn, q string, args ...any) {
	t.Helper()
	if _, e := c.ExecContext(ctx(t), q, args...); e == nil {
		t.Fatal("invalid database mutation was accepted")
	}
}

func constraint(t *testing.T, c *sql.Conn, code int, reason, q string, args ...any) {
	t.Helper()
	_, err := c.ExecContext(ctx(t), q, args...)
	var actual *sqlite.Error
	if !errors.As(err, &actual) || actual.Code() != code || !strings.Contains(err.Error(), reason) {
		t.Fatalf("wanted SQLite constraint %d (%s), got %v", code, reason, err)
	}
}
func seed(t *testing.T, c *sql.Conn) {
	execute(t, c, "INSERT INTO actor VALUES(?,?,?,?,?,?)", incarnation, actorID, "human", "Synthetic actor", boardID, incarnation)
	execute(t, c, "UPDATE board SET next_ticket=3,revision=2")
	for n, id := range []string{cardID, cardTwo} {
		execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'card',?,?,?,?)", incarnation, id, id, boardID, incarnation)
		execute(t, c, "INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,?,?)", incarnation, n+1, id)
		execute(t, c, "INSERT INTO card(incarnation,card_id,revision,title,description) VALUES(?,?,1,'Synthetic','')", incarnation, id)
	}
}

func TestCreateOpenSettingsAndIdentity(t *testing.T) {
	s, p := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		var layout, user int
		if e := c.QueryRowContext(ctx(t), "SELECT schema_version FROM board").Scan(&layout); e != nil {
			t.Fatal(e)
		}
		if e := c.QueryRowContext(ctx(t), "PRAGMA user_version").Scan(&user); e != nil || layout != schema.Version || user != schema.Version || schema.Version != 3 {
			t.Fatal("physical layout markers mismatch")
		}
		if e := configure(ctx(t), c, false, false); e != nil {
			t.Fatal(e)
		}
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal(e)
		}
	})
	if _, e := Existing(ctx(t), p, identity); e == nil {
		t.Fatal("second owner admitted")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := Create(ctx(t), p, identity); e == nil {
		t.Fatal("existing DB overwritten")
	}
	wrong := identity
	wrong.Incarnation = cardTwo
	if _, e := Existing(ctx(t), p, wrong); e == nil {
		t.Fatal("wrong incarnation admitted")
	}
	s2, e := Existing(ctx(t), p, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.Close()
	r, e := s2.Reader(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if e = r.Do(ctx(t), func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx(t), "UPDATE board SET revision=1"); e == nil {
			t.Fatal("read connection mutated")
		}
		return configure(ctx(t), c, true, false)
	}); e != nil {
		t.Fatal(e)
	}
}

func TestRelationalConstraintsAndOverflow(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		for _, q := range []string{"UPDATE board SET revision=9007199254740992", "UPDATE board SET revision=1.5", "UPDATE board SET next_ticket=2", "UPDATE board SET next_ticket=9007199254740991+1", "UPDATE card SET revision=0", "UPDATE card SET priority='P6'", "UPDATE card SET lane='Blocked'", "UPDATE card SET title=''", "UPDATE card SET description=printf('%065537d',0)", "UPDATE board SET history_days=0", "UPDATE board SET incarnation='20000000-0000-0000-0000-000000000002'"} {
			refused(t, c, q)
		}
		refused(t, c, "INSERT INTO card(incarnation,card_id,revision,title,description) VALUES(?,?,1,'X','')", incarnation, missingID)
		refused(t, c, "INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,1,?)", incarnation, missingID)
		refused(t, c, "DELETE FROM ticket WHERE ticket=1")
		refused(t, c, "INSERT INTO actor VALUES(?,?,?,?,?,?)", cardTwo, missingID, "human", "wrong incarnation", boardID, incarnation)
		var p, l string
		if e := c.QueryRowContext(ctx(t), "SELECT priority,lane FROM card WHERE card_id=?", cardID).Scan(&p, &l); e != nil || p != "P3" || l != "Ready for Work" {
			t.Fatal("creation defaults mismatch")
		}
		execute(t, c, "UPDATE board SET revision=9007199254740991")
		refused(t, c, "UPDATE board SET revision=revision+1")
	})
}

func TestCredentialMetadataConstraints(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seedCredentialGrant(t, c)
		for _, tc := range []struct {
			name   string
			column int
			value  any
			code   int
			reason string
		}{
			{"wrong-incarnation", 0, cardTwo, 1811, "credential current board mismatch"},
			{"wrong-board", 1, cardTwo, 1811, "credential current board mismatch"},
			{"missing-grant", 2, missingID, 1811, "credential requires enabled current grant"},
			{"wrong-actor", 3, cardTwo, 1811, "credential requires enabled current grant"},
			{"installation-v1", 4, actorID, 275, "CHECK constraint failed"},
			{"installation-variant", 4, "70000000-0000-4000-7000-000000000001", 275, "CHECK constraint failed"},
			{"installation-uppercase", 4, "AAAAAAAA-0000-4000-8000-000000000001", 275, "CHECK constraint failed"},
			{"installation-nul", 4, credentialInstallation + "\x00suffix", 275, "CHECK constraint failed"},
			{"origin-http", 5, strings.Replace(credentialOrigin, "https:", "http:", 1), 275, "CHECK constraint failed"},
			{"origin-alternate-host", 5, strings.Replace(credentialOrigin, ".localhost:", ".examplexx:", 1), 275, "CHECK constraint failed"},
			{"origin-path", 5, credentialOrigin + "/", 275, "CHECK constraint failed"},
			{"origin-nul", 5, credentialOrigin + "\x00suffix", 275, "CHECK constraint failed"},
			{"origin-port-leading-zero", 5, strings.Replace(credentialOrigin, "47443", "047443", 1), 275, "CHECK constraint failed"},
			{"origin-port-low", 5, strings.Replace(credentialOrigin, "47443", "1023", 1), 275, "CHECK constraint failed"},
			{"origin-port-high", 5, strings.Replace(credentialOrigin, "47443", "65536", 1), 275, "CHECK constraint failed"},
			{"credential-v1", 6, actorID, 275, "CHECK constraint failed"},
			{"credential-variant", 6, "80000000-0000-4000-7000-000000000001", 275, "CHECK constraint failed"},
			{"credential-nul", 6, credentialID + "\x00suffix", 275, "CHECK constraint failed"},
			{"hash-short", 7, strings.Repeat("a", 63), 275, "CHECK constraint failed"},
			{"hash-uppercase", 7, strings.Repeat("A", 64), 275, "CHECK constraint failed"},
			{"hash-not-hex", 7, strings.Repeat("z", 64), 275, "CHECK constraint failed"},
			{"hash-nul", 7, strings.Repeat("a", 64) + "\x00suffix", 275, "CHECK constraint failed"},
			{"issued-null", 8, nil, 1299, "NOT NULL constraint failed"},
			{"issued-nul", 8, "2026-10-26T12:00:00Z\x00suffix", 275, "CHECK constraint failed"},
			{"issued-subseconds", 8, "2026-10-26T12:00:00.1Z", 275, "CHECK constraint failed"},
			{"issued-invalid-date", 8, "2026-02-30T12:00:00Z", 275, "CHECK constraint failed"},
			{"issued-offset", 8, "2026-10-26T12:00:00+00:00", 275, "CHECK constraint failed"},
			{"expiry-short", 9, "2026-11-25T11:59:59Z", 275, "CHECK constraint failed"},
			{"expiry-long", 9, "2026-11-25T12:00:01Z", 275, "CHECK constraint failed"},
			{"expiry-backward", 9, "2026-10-25T12:00:00Z", 275, "CHECK constraint failed"},
			{"expiry-invalid", 9, "2026-11-31T12:00:00Z", 275, "CHECK constraint failed"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				a := credentialArgs()
				a[tc.column] = tc.value
				constraint(t, c, tc.code, tc.reason, credentialInsert, a...)
			})
		}
		// A historical existence fact is never current grant authority.
		execute(t, c, "INSERT INTO grant_fact VALUES(?,?)", incarnation, missingID)
		a := credentialArgs()
		a[2] = missingID
		constraint(t, c, 1811, "credential requires enabled current grant", credentialInsert, a...)
		execute(t, c, "UPDATE grant_record SET enabled=0")
		constraint(t, c, 1811, "credential requires enabled current grant", credentialInsert, credentialArgs()...)
		execute(t, c, "UPDATE grant_record SET enabled=1")
		execute(t, c, credentialInsert, credentialArgs()...)
		// A second credential cannot overlap, even with a different installation.
		a = credentialArgs()
		a[4], a[6], a[7] = "70000000-0000-4000-8000-000000000002", "80000000-0000-4000-8000-000000000002", strings.Repeat("b", 64)
		constraint(t, c, 1555, "UNIQUE constraint failed", credentialInsert, a...)
		// Current metadata is immutable; replacement/revocation must be explicit.
		for _, q := range []string{"UPDATE cli_credential SET credential_hash=credential_hash", "UPDATE cli_credential SET actor_id='" + cardTwo + "'", "UPDATE cli_credential SET origin=origin", "UPDATE cli_credential SET expires_at='2026-11-26T12:00:00Z'"} {
			constraint(t, c, 1811, "replace credential through explicit delete and insert", q)
		}
		execute(t, c, "INSERT INTO actor VALUES(?,?,?,?,?,?)", incarnation, cardTwo, "automation", "Synthetic other actor", boardID, incarnation)
		constraint(t, c, 787, "FOREIGN KEY constraint failed", "UPDATE grant_record SET actor_id=?", cardTwo)
		constraint(t, c, 787, "FOREIGN KEY constraint failed", "DELETE FROM grant_record")
		// Unique public ID and hash also hold across different grants.
		execute(t, c, "INSERT INTO grant_record VALUES(?,?,?,1)", incarnation, missingID, cardTwo)
		a = credentialArgs()
		a[2], a[3], a[7] = missingID, cardTwo, strings.Repeat("b", 64)
		constraint(t, c, 2067, "UNIQUE constraint failed", credentialInsert, a...)
		a[6], a[7] = "80000000-0000-4000-8000-000000000002", strings.Repeat("a", 64)
		constraint(t, c, 2067, "UNIQUE constraint failed", credentialInsert, a...)
		// Revocation preserves the record and permits a normal restart audit.
		execute(t, c, "UPDATE grant_record SET enabled=0 WHERE grant_id=?", credentialGrant)
		if err := schema.Validate(ctx(t), c, identity); err != nil {
			t.Fatal("disabled grant metadata must remain valid")
		}
	})
}

func TestCredentialRestartReplacementAndRevocation(t *testing.T) {
	s, p := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seedCredentialGrant(t, c)
		execute(t, c, credentialInsert, credentialArgs()...)
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Existing(ctx(t), p, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	withWriter(t, s2, func(c *sql.Conn) {
		var a [10]string
		if err := c.QueryRowContext(ctx(t), "SELECT incarnation,board_id,grant_id,actor_id,installation_id,origin,credential_id,credential_hash,issued_at,expires_at FROM cli_credential").Scan(&a[0], &a[1], &a[2], &a[3], &a[4], &a[5], &a[6], &a[7], &a[8], &a[9]); err != nil {
			t.Fatal(err)
		}
		for n, want := range credentialArgs() {
			if a[n] != want {
				t.Fatal("restart lost exact credential metadata")
			}
		}
		// Rollback after deleting the old credential cannot lose that credential.
		execute(t, c, "BEGIN IMMEDIATE")
		execute(t, c, "DELETE FROM cli_credential WHERE incarnation=? AND grant_id=?", incarnation, credentialGrant)
		bad := credentialArgs()
		bad[9] = "2026-11-26T12:00:00Z"
		constraint(t, c, 275, "CHECK constraint failed", credentialInsert, bad...)
		execute(t, c, "ROLLBACK")
		var n int
		if err := c.QueryRowContext(ctx(t), "SELECT count(*) FROM cli_credential WHERE credential_id=?", credentialID).Scan(&n); err != nil || n != 1 {
			t.Fatal("rollback lost predecessor")
		}
		execute(t, c, "BEGIN IMMEDIATE")
		execute(t, c, "DELETE FROM cli_credential WHERE incarnation=? AND grant_id=?", incarnation, credentialGrant)
		a2 := credentialArgs()
		a2[6], a2[7] = "80000000-0000-4000-8000-000000000002", strings.Repeat("b", 64)
		execute(t, c, credentialInsert, a2...)
		execute(t, c, "COMMIT")
		if err := c.QueryRowContext(ctx(t), "SELECT count(*) FROM cli_credential WHERE credential_id=?", credentialID).Scan(&n); err != nil || n != 0 {
			t.Fatal("replacement retained predecessor")
		}
		// Authentication metadata must not create domain revisions or history.
		for _, q := range []string{"SELECT revision FROM board", "SELECT next_ticket-1 FROM board", "SELECT next_event-1 FROM board", "SELECT count(*) FROM card", "SELECT count(*) FROM operation", "SELECT count(*) FROM activity"} {
			if err := c.QueryRowContext(ctx(t), q).Scan(&n); err != nil || n != 0 {
				t.Fatal("credential metadata changed domain state")
			}
		}
		execute(t, c, "DELETE FROM cli_credential WHERE credential_id=?", a2[6])
		if err := c.QueryRowContext(ctx(t), "SELECT count(*) FROM cli_credential").Scan(&n); err != nil || n != 0 {
			t.Fatal("credential revocation did not remove active metadata")
		}
		for _, q := range []string{"SELECT count(*) FROM actor", "SELECT count(*) FROM grant_fact", "SELECT count(*) FROM grant_record WHERE enabled=1"} {
			if err := c.QueryRowContext(ctx(t), q).Scan(&n); err != nil || n != 1 {
				t.Fatal("credential revocation changed grant attribution")
			}
		}
		if err := schema.Validate(ctx(t), c, identity); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCredentialAuditCurrentBinding(t *testing.T) {
	s, p := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seedCredentialGrant(t, c)
		// Synthetic corruption bypasses the insert guard, then restores its exact
		// inventory. FKs remain satisfied: open must also audit current board scope.
		var guard string
		if err := c.QueryRowContext(ctx(t), "SELECT sql FROM sqlite_schema WHERE name='cli_credential_current'").Scan(&guard); err != nil {
			t.Fatal(err)
		}
		execute(t, c, "DROP TRIGGER cli_credential_current")
		a := credentialArgs()
		a[1] = cardTwo
		execute(t, c, credentialInsert, a...)
		execute(t, c, guard)
		if err := schema.Validate(ctx(t), c, identity); err == nil || err.Error() != "storage relational consistency check failed" {
			t.Fatalf("current credential binding audit: %v", err)
		}
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Existing(ctx(t), p, identity); err == nil || err.Error() != "storage relational consistency check failed" {
		t.Fatalf("existing credential binding audit: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("credential audit changed database bytes")
	}
}

func TestPhysicalLayoutOneRefusedWithoutWrites(t *testing.T) {
	// Construct the exact immediately preceding S01 physical layout by undoing
	// only S04's bounded DDL additions. This is a synthetic fixture, never migration.
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
		if strings.HasPrefix(statement, "CREATE TABLE cli_credential ") || strings.HasPrefix(statement, "CREATE TRIGGER cli_credential_") {
			continue
		}
		statement = strings.ReplaceAll(statement, "schema_version INTEGER NOT NULL DEFAULT 3 CHECK(schema_version=3)", "schema_version INTEGER NOT NULL DEFAULT 1 CHECK(schema_version=1)")
		statement = previousTitleLength(statement)
		statement = strings.ReplaceAll(statement, " UNIQUE(incarnation,grant_id,actor_id),", "")
		execute(t, c, statement)
	}
	execute(t, c, "INSERT INTO board(singleton,board_id,incarnation) VALUES(1,?,?)", boardID, incarnation)
	execute(t, c, "INSERT INTO policy(incarnation) VALUES(?)", incarnation)
	execute(t, c, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=1", schema.ApplicationID))
	execute(t, c, "COMMIT")
	c.Close()
	db.Close()
	g.close()
	before, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Existing(ctx(t), p, identity); err == nil || err.Error() != "unsupported storage user_version" {
		t.Fatalf("layout one refusal: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("layout one refusal changed database bytes")
	}
}

func TestTypedLinkUniqueness(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		for n, kind := range []string{"blocking", "informational", "blocking"} {
			id := []string{"60000000-0000-0000-0000-000000000001", "60000000-0000-0000-0000-000000000002", "60000000-0000-0000-0000-000000000003"}[n]
			execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", incarnation, id, cardID, boardID, incarnation)
			q := "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,?,?,?,?,?,1,'2026-10-25T00:00:00Z')"
			a := []any{incarnation, id, cardID, kind, boardID, incarnation, cardTwo, cardTwo}
			if n == 2 {
				refused(t, c, q, a...)
				execute(t, c, "UPDATE link SET active=0 WHERE kind='blocking'")
				execute(t, c, q, a...)
			} else {
				execute(t, c, q, a...)
			}
		}
		refused(t, c, "UPDATE link SET internal_target_id=card_id,target_card_id=card_id")
	})
}

func TestRefusedAttemptNeedsNoInventedEntityOrBytes(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		q := "INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at) VALUES(?,?,?,?,1,?,?,'attachment.add','card',?,2,1,'NOT_COMMITTED','NOT_FOUND','2026-10-25T00:00:00Z')"
		execute(t, c, q, incarnation, boardID, incarnation, opID, actorID, hash, missingID)
		execute(t, c, "INSERT INTO byte_fact VALUES(?,?,?,?,?,0)", incarnation, boardID, incarnation, opID, hash)
		execute(t, c, "INSERT INTO operation_full VALUES(?,?,?,?,'{}','{}')", incarnation, boardID, incarnation, opID)
		refused(t, c, "INSERT INTO operation_reference VALUES(?,?,?,?,'card',?,1)", incarnation, boardID, incarnation, opID, missingID)
		refused(t, c, "UPDATE operation SET fingerprint=?", strings.Repeat("a", 64))
		refused(t, c, "DELETE FROM operation")
		var n int
		if e := c.QueryRowContext(ctx(t), "SELECT (SELECT count(*) FROM blob)+(SELECT count(*) FROM entity WHERE entity_id=?)", missingID).Scan(&n); e != nil || n != 0 {
			t.Fatal("attempt fabricated entity or byte pin")
		}
		refused(t, c, q, incarnation, boardID, incarnation, opID, actorID, hash, missingID)
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal(e)
		}
	})
}

func TestLeaseCleanupReaderBlocksCommitAndFence(t *testing.T) {
	s, _ := newStore(t)
	r, e := s.Reader(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Do(ctx(t), func(c *sql.Conn) error {
		execute(t, c, "BEGIN")
		var n int
		return c.QueryRowContext(ctx(t), "SELECT revision FROM board").Scan(&n)
	}); e != nil {
		t.Fatal(e)
	}
	w, e := s.Writer(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Do(ctx(t), func(c *sql.Conn) error {
		execute(t, c, "BEGIN IMMEDIATE")
		execute(t, c, "UPDATE board SET revision=1")
		refused(t, c, "COMMIT")
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	withWriter(t, s, func(c *sql.Conn) {
		var rev int
		if e := c.QueryRowContext(ctx(t), "SELECT revision FROM board").Scan(&rev); e != nil || rev != 0 {
			t.Fatal("failed commit leaked effects")
		}
	})
	w, e = s.Writer(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	deadline, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, e = s.Writer(deadline); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("writer lease bound failed")
	}
	if e = s.Close(); e == nil {
		t.Fatal("closed with active lease")
	}
	w.Close()
	s.Fence()
	if _, e = s.Writer(ctx(t)); !errors.Is(e, ErrFenced) {
		t.Fatal("fenced writer admitted")
	}
}

func TestExistingUnknownVersionAndCorruptionFailClosed(t *testing.T) {
	for _, kind := range []string{"version", "layout", "wal", "corrupt", "journal", "digest"} {
		t.Run(kind, func(t *testing.T) {
			s, p := newStore(t)
			if kind == "version" {
				withWriter(t, s, func(c *sql.Conn) { execute(t, c, "PRAGMA user_version=9") })
			}
			if kind == "layout" {
				withWriter(t, s, func(c *sql.Conn) { execute(t, c, "CREATE TABLE unknown(x)") })
			}
			if kind == "digest" {
				withWriter(t, s, func(c *sql.Conn) {
					execute(t, c, "INSERT INTO blob VALUES(?,?,0,x'')", incarnation, strings.Repeat("a", 64))
				})
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			if kind == "corrupt" {
				if e := os.WriteFile(filepath.Join(p, "board.sqlite"), []byte("synthetic invalid database"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "journal" {
				if e := os.WriteFile(filepath.Join(p, "board.sqlite-journal"), []byte("synthetic uncertain journal"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "wal" {
				db, e := pool(filepath.Join(p, "board.sqlite"), false)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = db.Exec("PRAGMA journal_mode=WAL"); e != nil {
					t.Fatal(e)
				}
				db.Close()
			}
			before, _ := os.ReadFile(filepath.Join(p, "board.sqlite"))
			if _, e := Existing(ctx(t), p, identity); e == nil {
				t.Fatal("unsafe existing DB admitted")
			}
			after, _ := os.ReadFile(filepath.Join(p, "board.sqlite"))
			if string(before) != string(after) {
				t.Fatal("failed open changed database bytes")
			}
		})
	}
}

func TestNativePathAndACLRefusals(t *testing.T) {
	for _, p := range []string{`C:relative`, `\\server\share\x`, `C:\x\..\x`, `C:\x.`, `C:\NUL`, `C:\x:stream`, `C:\PROGRA~1\x`} {
		if _, e := Create(ctx(t), p, identity); e == nil {
			t.Fatal("unsupported path accepted")
		}
	}
	p := privateDir(t)
	if e := os.WriteFile(filepath.Join(p, "foreign"), []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Create(ctx(t), p, identity); e == nil {
		t.Fatal("foreign inventory adopted")
	}
	p = privateDir(t)
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := Create(ctx(t), p, identity); e == nil {
		t.Fatal("broad ACL accepted")
	}
}

func TestCrossProcessExclusion(t *testing.T) {
	if p := os.Getenv("S01_CHILD_DIR"); p != "" {
		s, e := Existing(ctx(t), p, identity)
		if os.Getenv("S01_CHILD_EXPECT_OPEN") == "1" {
			if e != nil {
				t.Fatal("released OS exclusion not available")
			}
			s.Close()
		} else {
			if e == nil {
				s.Close()
				t.Fatal("competing process acquired store")
			}
		}
		return
	}
	s, p := newStore(t)
	child := func(open bool) {
		t.Helper()
		cmd := exec.CommandContext(ctx(t), os.Args[0], "-test.run=^TestCrossProcessExclusion$", "-test.v")
		cmd.Env = append(os.Environ(), "S01_CHILD_DIR="+p, "S01_CHILD_EXPECT_OPEN="+map[bool]string{false: "0", true: "1"}[open])
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("synthetic exclusion child failed: %s", out)
		}
	}
	child(false)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	child(true)
}

func TestTransientJournalPrivateAtCreation(t *testing.T) {
	s, p := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "BEGIN IMMEDIATE")
		execute(t, c, "UPDATE board SET revision=1")
		h, e := openHandle(filepath.Join(p, "board.sqlite-journal"), windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.OPEN_EXISTING, nil, false)
		if e != nil {
			t.Fatal("active journal unavailable for ACL inspection")
		}
		defer func() {
			if h != windows.InvalidHandle {
				windows.CloseHandle(h)
			}
		}()
		token, e := windows.OpenCurrentProcessToken()
		if e != nil {
			t.Fatal(e)
		}
		defer token.Close()
		u, e := token.GetTokenUser()
		if e != nil {
			t.Fatal(e)
		}
		if e = verifyACL(h, u.User.Sid, false, true); e != nil {
			t.Fatal(e)
		}
		// No protected-descendant claim: SQLite-created journals inherit the
		// verified directory's exact private ACEs. Main DB has explicit protection.
		windows.CloseHandle(h)
		h = windows.InvalidHandle
		execute(t, c, "ROLLBACK")
	})
}

func TestTombstonesFullHistoryAndCommittedReferences(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		id := "70000000-0000-0000-0000-000000000001"
		execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at) VALUES(?,'comment',?,?,?,?,?,'2026-10-25T00:00:00Z')", incarnation, id, cardID, boardID, incarnation, actorID)
		execute(t, c, "INSERT INTO comment(incarnation,comment_id,card_id,actor_id,text,created_at) VALUES(?,?,?,?,'Synthetic retained text','2026-10-25T00:00:00Z')", incarnation, id, cardID, actorID)
		execute(t, c, "INSERT INTO current_reference VALUES(?,?,'comment',?)", incarnation, cardID, id)
		refused(t, c, "UPDATE entity SET removed_at='2026-10-25T01:00:00Z',removal_reason='cleared' WHERE entity_id=?", id)
		execute(t, c, "UPDATE card SET revision=2 WHERE card_id=?", cardID)
		execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z','{}')", incarnation, boardID, incarnation, cardID, actorID)
		execute(t, c, "INSERT INTO revision_reference VALUES(?,?,?,?,1,'comment',?)", incarnation, boardID, incarnation, cardID, id)
		execute(t, c, "DELETE FROM current_reference WHERE entity_id=?", id)
		refused(t, c, "UPDATE entity SET removed_at='2026-10-25T01:00:00Z',removal_reason='cleared' WHERE entity_id=?", id)
		execute(t, c, "UPDATE card_revision SET state=NULL,removed_at='2026-10-25T01:00:00Z',removal_reason='cleared',removal_revision=2")
		execute(t, c, "UPDATE entity SET removed_at='2026-10-25T01:00:00Z',removal_reason='cleared' WHERE entity_id=?", id)
		execute(t, c, "DELETE FROM comment WHERE comment_id=?", id)
		refused(t, c, "INSERT INTO current_reference VALUES(?,?,'comment',?)", incarnation, cardID, id)
		refused(t, c, "DELETE FROM entity WHERE entity_id=?", id)
		refused(t, c, "UPDATE card_revision SET state='{}',removed_at=NULL,removal_reason=NULL,removal_revision=NULL")
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal(e)
		}
	})
}

func TestSourceLedgerCannotMasqueradeAsCurrent(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		q := "INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,expected_revision,terminal_class,code,terminal_at,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,?,?,?,'card.create',0,'NOT_COMMITTED','INVALID_INPUT','2026-10-25T00:00:00Z','2026-10-25T01:00:00Z','cleared',2)"
		refused(t, c, q, incarnation, boardID, cardTwo, opID, 1, actorID, hash)
		execute(t, c, q, incarnation, boardID, cardTwo, opID, 0, actorID, hash)
		execute(t, c, q, incarnation, boardID, incarnation, opID, 1, actorID, hash)
		var n int
		if e := c.QueryRowContext(ctx(t), "SELECT count(*) FROM operation WHERE current_ledger=1 AND op_id=?", opID).Scan(&n); e != nil || n != 1 {
			t.Fatal("source operation entered current ledger")
		}
		refused(t, c, "UPDATE operation SET current_ledger=1 WHERE origin_incarnation=?", cardTwo)
		committed := "INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,1,?,?,'card.edit','card',?,1,1,'COMMITTED','OK','2026-10-25T00:00:00Z',2,2,'2026-10-25T01:00:00Z','cleared',2)"
		secondOp := "50000000-0000-0000-0000-000000000002"
		refused(t, c, committed, incarnation, boardID, incarnation, secondOp, actorID, hash, missingID)
		execute(t, c, committed, incarnation, boardID, incarnation, secondOp, actorID, hash, cardID)
		execute(t, c, "INSERT INTO operation_reference(incarnation,origin_board,origin_incarnation,op_id,kind,entity_id,revision) VALUES(?,?,?,?,'card',?,2)", incarnation, boardID, incarnation, secondOp, cardID)
		refused(t, c, "DELETE FROM operation_reference WHERE op_id=?", secondOp)
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal(e)
		}
	})
}

func TestBlobBoundsAndPins(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		refused(t, c, "INSERT INTO blob VALUES(?,?,1,x'')", incarnation, hash)
		refused(t, c, "INSERT INTO blob VALUES(?,?,10485761,zeroblob(10485761))", incarnation, hash)
		execute(t, c, "INSERT INTO blob VALUES(?,?,0,x'')", incarnation, hash)
		id := "80000000-0000-0000-0000-000000000001"
		execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'attachment',?,?,?,?)", incarnation, id, cardID, boardID, incarnation)
		execute(t, c, "INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'empty.txt','text/plain',?,0,'2026-10-25T00:00:00Z')", incarnation, id, cardID, hash)
		execute(t, c, "INSERT INTO current_reference VALUES(?,?,'attachment',?)", incarnation, cardID, id)
		execute(t, c, "INSERT INTO current_attachment_pin VALUES(?,?,?,0)", incarnation, id, hash)
		refused(t, c, "DELETE FROM blob")
		refused(t, c, "UPDATE blob SET bytes=x'00',byte_length=1")
		refused(t, c, "UPDATE entity SET removed_at='2026-10-25T01:00:00Z',removal_reason='cleared' WHERE entity_id=?", id)
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal(e)
		}
	})
}

func TestNativeReparseAliasAndRenameRefusals(t *testing.T) {
	// A directory junction is created with the native API under synthetic roots;
	// it requires no symlink privilege, shell command, ACL repair or host setting.
	base, target := privateDir(t), privateDir(t)
	junction := filepath.Join(base, "redirect")
	if e := os.Mkdir(junction, 0700); e != nil {
		t.Fatal(e)
	}
	h, e := openHandle(junction, windows.GENERIC_WRITE, 0, windows.OPEN_EXISTING, nil, true)
	if e != nil {
		t.Fatal(e)
	}
	sub, printName := utf16.Encode([]rune(`\??\`+target)), utf16.Encode([]rune(target))
	paths := append(append(append(sub, 0), printName...), 0)
	buf := make([]byte, 16+len(paths)*2)
	binary.LittleEndian.PutUint32(buf, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:], uint16(len(sub)*2))
	binary.LittleEndian.PutUint16(buf[12:], uint16((len(sub)+1)*2))
	binary.LittleEndian.PutUint16(buf[14:], uint16(len(printName)*2))
	for n, v := range paths {
		binary.LittleEndian.PutUint16(buf[16+n*2:], v)
	}
	var returned uint32
	e = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
	windows.CloseHandle(h)
	if e != nil {
		t.Fatalf("synthetic junction creation failed: %v", e)
	}
	if _, e := Create(ctx(t), junction, identity); e == nil {
		t.Fatal("reparse directory accepted")
	}
	s, p := newStore(t)
	if e = os.Rename(p, p+"-renamed"); e == nil {
		t.Fatal("leased storage directory renamed")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	alias := privateDir(t)
	if e = os.Link(filepath.Join(p, "board.sqlite"), filepath.Join(alias, "board.sqlite")); e != nil {
		t.Fatal("synthetic hardlink creation failed")
	}
	if _, e = Existing(ctx(t), alias, identity); e == nil {
		t.Fatal("hardlink alias accepted")
	}
}

func TestDistinctProtectedByteQuota(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		seed(t, c)
		var firstDigest string
		for n, size := range []int{10, 10, 10, 10, 10, 10, 4, 1} {
			bytes := make([]byte, size*1048576)
			bytes[0] = byte(n)
			digest := fmt.Sprintf("%x", sha256.Sum256(bytes))
			if n == 0 {
				firstDigest = digest
			}
			execute(t, c, "INSERT INTO blob VALUES(?,?,?,?)", incarnation, digest, len(bytes), bytes)
			id := fmt.Sprintf("80000000-0000-0000-0000-%012d", n+1)
			execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'attachment',?,?,?,?)", incarnation, id, cardID, boardID, incarnation)
			execute(t, c, "INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'synthetic.bin','application/octet-stream',?,?,'2026-10-25T00:00:00Z')", incarnation, id, cardID, digest, len(bytes))
			if n == 7 {
				refused(t, c, "INSERT INTO current_attachment_pin VALUES(?,?,?,?)", incarnation, id, digest, len(bytes))
			} else {
				execute(t, c, "INSERT INTO current_attachment_pin VALUES(?,?,?,?)", incarnation, id, digest, len(bytes))
				execute(t, c, "INSERT INTO current_reference VALUES(?,?,'attachment',?)", incarnation, cardID, id)
			}
		}
		id := "80000000-0000-0000-0000-000000000099"
		execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'attachment',?,?,?,?)", incarnation, id, cardID, boardID, incarnation)
		execute(t, c, "INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'duplicate.bin','application/octet-stream',?,10485760,'2026-10-25T00:00:00Z')", incarnation, id, cardID, firstDigest)
		execute(t, c, "INSERT INTO current_attachment_pin VALUES(?,?,?,10485760)", incarnation, id, firstDigest)
		execute(t, c, "INSERT INTO current_reference VALUES(?,?,'attachment',?)", incarnation, cardID, id)
		if e := schema.Validate(ctx(t), c, identity); e != nil {
			t.Fatal(e)
		}
	})
}

func TestExactSchemaLookalike(t *testing.T) {
	s, p := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "CREATE TABLE sqliteXunexpected(x INTEGER)")
		if err := schema.Validate(ctx(t), c, identity); err == nil || err.Error() != "storage layout mismatch" {
			t.Fatalf("legal lookalike must fail exact layout audit: %v", err)
		}
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Existing(ctx(t), p, identity); err == nil || err.Error() != "storage layout mismatch" {
		t.Fatalf("existing lookalike must fail exact layout audit: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(p, "board.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("failed lookalike open changed database bytes")
	}
}

func TestHistoryOwner(t *testing.T) {
	for _, kind := range []string{"comment", "link", "attachment", "approval"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := newStore(t)
			withWriter(t, s, func(c *sql.Conn) {
				seed(t, c)
				execute(t, c, "UPDATE card SET revision=2")
				id := "90000000-0000-0000-0000-000000000001"
				execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,?,?,?,?,?)", incarnation, kind, id, cardTwo, boardID, incarnation)
				switch kind {
				case "comment":
					execute(t, c, "INSERT INTO comment(incarnation,comment_id,card_id,actor_id,text,created_at) VALUES(?,?,?,?,'Synthetic','2026-10-25T00:00:00Z')", incarnation, id, cardTwo, actorID)
				case "link":
					execute(t, c, "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,'informational',?,?,?,?,1,'2026-10-25T00:00:00Z')", incarnation, id, cardTwo, boardID, incarnation, cardID, cardID)
				case "attachment":
					execute(t, c, "INSERT INTO blob VALUES(?,?,0,x'')", incarnation, hash)
					execute(t, c, "INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'empty.txt','text/plain',?,0,'2026-10-25T00:00:00Z')", incarnation, id, cardTwo, hash)
				case "approval":
					execute(t, c, "INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-25T00:00:00Z')", incarnation, id, cardTwo, actorID)
				}
				for _, card := range []string{cardID, cardTwo} {
					execute(t, c, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,'2026-10-25T00:00:00Z','{}')", incarnation, boardID, incarnation, card, actorID)
				}
				q := "INSERT INTO revision_reference VALUES(?,?,?,?,1,?,?)"
				constraint(t, c, 787, "FOREIGN KEY constraint failed", q, incarnation, boardID, incarnation, cardID, kind, id)
				execute(t, c, q, incarnation, boardID, incarnation, cardTwo, kind, id)
				if kind == "attachment" {
					execute(t, c, "INSERT INTO history_blob_pin VALUES(?,?,?,?,1,?,?,0)", incarnation, boardID, incarnation, cardTwo, id, hash)
				}
				if err := schema.Validate(ctx(t), c, identity); err != nil {
					t.Fatal(err)
				}
				if kind == "attachment" {
					execute(t, c, "DELETE FROM history_blob_pin")
				}
				execute(t, c, "UPDATE card_revision SET state=NULL,removed_at='2026-10-25T01:00:00Z',removal_reason='cleared',removal_revision=2")
				if kind != "approval" {
					execute(t, c, "UPDATE entity SET removed_at='2026-10-25T01:00:00Z',removal_reason='cleared' WHERE entity_id=?", id)
					execute(t, c, "DELETE FROM "+kind+" WHERE "+map[string]string{"comment": "comment_id", "link": "link_id", "attachment": "attachment_id"}[kind]+"=?", id)
					constraint(t, c, 787, "FOREIGN KEY constraint failed", q, incarnation, boardID, incarnation, cardID, kind, id)
				}
				if err := schema.Validate(ctx(t), c, identity); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestOperationFactPin(t *testing.T) {
	for _, source := range []bool{false, true} {
		for _, factFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("source%t-first%t", source, factFirst), func(t *testing.T) {
				s, _ := newStore(t)
				withWriter(t, s, func(c *sql.Conn) {
					seed(t, c)
					id := "90000000-0000-0000-0000-000000000002"
					execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'attachment',?,?,?,?)", incarnation, id, cardID, boardID, incarnation)
					execute(t, c, "INSERT INTO blob VALUES(?,?,0,x'')", incarnation, hash)
					execute(t, c, "INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'empty.txt','text/plain',?,0,'2026-10-25T00:00:00Z')", incarnation, id, cardID, hash)
					origin, live := incarnation, 1
					if source {
						origin, live = cardTwo, 0
					}
					operation := func() {
						execute(t, c, "INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision) VALUES(?,?,?,?,?,?,?,'attachment.add','card',?,1,1,'COMMITTED','OK','2026-10-25T00:00:00Z',2,2)", incarnation, boardID, origin, opID, live, actorID, hash, cardID)
						execute(t, c, "INSERT INTO operation_full VALUES(?,?,?,?,'{}','{}')", incarnation, boardID, origin, opID)
					}
					fact := "INSERT INTO byte_fact VALUES(?,?,?,?,?,0)"
					pin := "INSERT INTO operation_blob_pin VALUES(?,?,?,?,?,?,0)"
					execute(t, c, "BEGIN IMMEDIATE")
					operation()
					if factFirst {
						execute(t, c, fact, incarnation, boardID, origin, opID, strings.Repeat("a", 64))
						constraint(t, c, 1811, "operation byte fact mismatch", pin, incarnation, boardID, origin, opID, id, hash)
						execute(t, c, "ROLLBACK")
						execute(t, c, "BEGIN IMMEDIATE")
						operation()
						execute(t, c, fact, incarnation, boardID, origin, opID, hash)
						execute(t, c, pin, incarnation, boardID, origin, opID, id, hash)
					} else {
						execute(t, c, pin, incarnation, boardID, origin, opID, id, hash)
						constraint(t, c, 787, "FOREIGN KEY constraint failed", "COMMIT")
						constraint(t, c, 1811, "operation byte fact mismatch", fact, incarnation, boardID, origin, opID, strings.Repeat("a", 64))
						execute(t, c, fact, incarnation, boardID, origin, opID, hash)
					}
					if err := schema.Validate(ctx(t), c, identity); err != nil {
						t.Fatal(err)
					}
					execute(t, c, "COMMIT")
					// Full COMMITTED evidence can be compacted without inventing a BLOB
					// obligation for its retained byte fact. Release only that outcome pin.
					execute(t, c, "BEGIN IMMEDIATE")
					execute(t, c, "DELETE FROM operation_blob_pin")
					execute(t, c, "DELETE FROM operation_full")
					execute(t, c, "UPDATE operation SET removed_at='2026-10-25T01:00:00Z',removal_reason='cleared',removal_revision=2")
					execute(t, c, "DELETE FROM blob")
					execute(t, c, "COMMIT")
					if err := schema.Validate(ctx(t), c, identity); err != nil {
						t.Fatal(err)
					}
				})
			})
		}
	}
}
