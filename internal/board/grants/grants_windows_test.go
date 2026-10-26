//go:build windows && amd64

package grants

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
)

const board = "10000000-0000-0000-0000-000000000001"
const inc = "20000000-0000-0000-0000-000000000001"
const actor = "30000000-0000-0000-0000-000000000001"
const grant = "30000000-0000-0000-0000-000000000002"
const grantee = "30000000-0000-0000-0000-000000000003"
const op = "50000000-0000-0000-0000-000000000001"
const op2 = "50000000-0000-0000-0000-000000000002"
const installation = "60000000-0000-0000-0000-000000000001"
const origin = "https://synthetic.localhost:47443"

var sid = schema.Identity{BoardID: board, Incarnation: inc}
var now = time.Date(2026, 10, 26, 0, 0, 1, 0, time.UTC)

func testContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}
func fixture(t *testing.T) (*writer.Engine, *S.Store) {
	t.Helper()
	root := os.Getenv("S01_TEST_ROOT")
	if root == "" {
		t.Skip("native synthetic fixtures require explicit private S01_TEST_ROOT")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("absolute private root required")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(filepath.Clean(root))+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("fixtures must be outside public repository")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
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
	s, err := S.Create(testContext(t), p, sid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	e := writer.New(s)

	raw(t, s, func(c *sql.Conn) error {
		for _, q := range []string{
			`INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES('` + inc + `','` + actor + `','human','Synthetic','` + board + `','` + inc + `')`,
			`INSERT INTO grant_fact VALUES('` + inc + `','` + grant + `')`,
			`INSERT INTO grant_record VALUES('` + inc + `','` + grant + `','` + actor + `',1)`,
			`INSERT INTO grant_right VALUES('` + inc + `','` + grant + `','read-own-outcome')`,
		} {
			if _, err := c.ExecContext(testContext(t), q); err != nil {
				return err
			}
		}
		return nil
	})
	return e, s
}
func raw(t *testing.T, s *S.Store, fn func(*sql.Conn) error) {
	t.Helper()
	l, err := s.Writer(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err = l.Do(testContext(t), fn); err != nil {
		t.Fatal(err)
	}
}
func value(t *testing.T, s *S.Store, q string) int64 {
	t.Helper()
	l, err := s.Reader(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var n int64
	if err = l.Do(testContext(t), func(c *sql.Conn) error { return c.QueryRowContext(testContext(t), q).Scan(&n) }); err != nil {
		t.Fatal(err)
	}
	return n
}

type read struct{ c *sql.Conn }

func (r read) Query(q string, args ...any) (*sql.Rows, error) {
	return r.c.QueryContext(context.Background(), q, args...)
}
func (r read) Scan(q string, args []any, dest ...any) error {
	return r.c.QueryRowContext(context.Background(), q, args...).Scan(dest...)
}

func cred() Credential {
	return Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: actor, GrantID: grant, Mode: "cli", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
}
func service(t *testing.T, c Credential) *Service {
	t.Helper()
	s, err := New(installation, origin, func(context.Context, writer.Read, time.Time) (Credential, error) { return c, nil })
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	return s
}
func rights(t *testing.T, s *S.Store, kind string, rs ...string) {
	t.Helper()
	raw(t, s, func(c *sql.Conn) error {
		if _, err := c.ExecContext(testContext(t), "DELETE FROM grant_right"); err != nil {
			return err
		}
		// This synthetic fixture intentionally bypasses immutable actor update by
		// selecting kind at initial insertion in a separate fixture when necessary.
		var actual string
		if err := c.QueryRowContext(testContext(t), "SELECT kind FROM actor WHERE actor_id=?", actor).Scan(&actual); err != nil {
			return err
		}
		if actual != kind {
			return errors.New("fixture kind mismatch")
		}
		for _, r := range rs {
			if _, err := c.ExecContext(testContext(t), "INSERT INTO grant_right VALUES(?,?,?)", inc, grant, r); err != nil {
				return err
			}
		}
		return nil
	})
}
func intent(t *testing.T, id, cmd, target string, revision uint64, actorID, kind string, rs []string) contract.Intent {
	t.Helper()
	input := any(map[string]any{})
	var tgt any
	if cmd == "grant.create" {
		input = map[string]any{"granteeActorId": grantee, "kind": kind, "rights": rs}
	}
	if cmd == "grant.revoke" {
		tgt = map[string]any{"grantId": target}
	}
	if cmd == "card.create" {
		input = map[string]any{"title": "Synthetic", "description": "Synthetic"}
	}
	wire, _ := json.Marshal(map[string]any{"boardId": board, "opId": id, "incarnation": inc, "target": tgt, "expectedRevision": revision, "expectedCardRevision": nil, "input": input})
	i, err := T.FreezeMutation(wire, contract.ResolvedRoute{Command: contract.Command(cmd), BoardID: board, TargetID: target}, contract.AuthenticatedActor{ActorID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func key(t *testing.T, i contract.Intent) writer.LookupIdentity {
	t.Helper()
	o, err := outcome.Base(i)
	if err != nil {
		t.Fatal(err)
	}
	return outcome.IdentityOf(o)
}
func terminal(t *testing.T, r writer.Result, want string) {
	t.Helper()
	if r.Terminal == nil || string(r.Terminal.Outcome) != want {
		t.Fatalf("want %s: terminal=%v refused=%v unknown=%v", want, r.Terminal != nil, r.Refused != nil, r.Unknown != nil)
	}
	if err := T.Validate(*r.Terminal); err != nil {
		t.Fatal(err)
	}
}
func denied(t *testing.T, r writer.Result) {
	t.Helper()
	if r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("expected current authority refusal")
	}
}

func TestRightsMatrix(t *testing.T) {
	all := []string{"approve", "attach", "comment", "create", "edit", "export", "grant-admin", "history-admin", "read", "read-own-outcome", "restore", "review-handoff"}
	for _, kind := range []string{"human", "automation"} {
		for _, right := range all {
			t.Run(kind+"-"+right, func(t *testing.T) {
				want := kind == "human" || (right != "approve" && right != "history-admin" && right != "grant-admin")
				if (ValidateRights(kind, []string{right}) == nil) != want {
					t.Fatal("matrix")
				}
			})
		}
	}
	for n, rs := range [][]string{{"read", "approve"}, {"read", "read"}, {"unknown"}} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			if ValidateRights("human", rs) == nil {
				t.Fatal("invalid rights admitted")
			}
		})
	}
	if ValidateRights("human", nil) != nil || ValidateRights("unknown", nil) == nil {
		t.Fatal("empty/kind")
	}
}

func TestCurrentCredentialBindingsAndNoImpliedRights(t *testing.T) {
	_, db := fixture(t)
	rights(t, db, "human", "grant-admin")
	c := cred()
	cases := map[string]func(*Credential){
		"wrong-board": func(c *Credential) { c.BoardID = grantee }, "wrong-incarnation": func(c *Credential) { c.Incarnation = grantee }, "wrong-actor": func(c *Credential) { c.ActorID = grantee }, "wrong-installation": func(c *Credential) { c.InstallationID = grantee }, "wrong-origin": func(c *Credential) { c.Origin = "https://other.localhost:47443" }, "wrong-grant": func(c *Credential) { c.GrantID = grantee }, "expired-cli": func(c *Credential) { c.ExpiresAt = now }, "future-cli": func(c *Credential) { c.IssuedAt = now.Add(time.Hour) }, "overlong-cli": func(c *Credential) { c.ExpiresAt = now.Add(31 * 24 * time.Hour) }, "expired-session": func(c *Credential) { c.Mode = "browser"; c.IdleExpiresAt = now }, "overlong-session": func(c *Credential) { c.Mode = "browser"; c.IdleExpiresAt = now.Add(31 * time.Minute) }, "unknown-mode": func(c *Credential) { c.Mode = "claimed" },
	}
	raw(t, db, func(conn *sql.Conn) error {
		if service(t, c).Require(testContext(t), read{conn}, board, inc, actor, "grant-admin") != nil {
			t.Fatal("explicit right denied")
		}
		for _, right := range []string{"read", "read-own-outcome", "approve", "history-admin"} {
			if service(t, c).Require(testContext(t), read{conn}, board, inc, actor, right) == nil {
				t.Fatal("implied right", right)
			}
		}
		for name, change := range cases {
			t.Run(name, func(t *testing.T) {
				bad := c
				change(&bad)
				if service(t, bad).Require(testContext(t), read{conn}, board, inc, actor, "grant-admin") == nil {
					t.Fatal("bad binding admitted")
				}
			})
		}
		return nil
	})
	raw(t, db, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "UPDATE grant_record SET enabled=0")
		return err
	})
	raw(t, db, func(conn *sql.Conn) error {
		if service(t, c).Require(testContext(t), read{conn}, board, inc, actor, "grant-admin") == nil {
			t.Fatal("disabled grant admitted")
		}
		return nil
	})
}

func TestLedgeredGrantCreateRevokeAndOwnOutcome(t *testing.T) {
	e, db := fixture(t)
	rights(t, db, "human", "grant-admin", "read-own-outcome")
	s := service(t, cred())
	i := intent(t, op, "grant.create", "", 0, actor, "automation", []string{"comment", "read-own-outcome"})
	cb := s.GrantCallbacks(Route{BoardID: board, Incarnation: inc})
	r := e.Submit(testContext(t), i, cb)
	terminal(t, r, "committed")
	// Exact prior identities resolve without a new command admission or Apply.
	terminal(t, e.Submit(testContext(t), i, writer.Callbacks{Authority: s.Authority}), "committed")
	var id string
	raw(t, db, func(c *sql.Conn) error {
		return c.QueryRowContext(testContext(t), "SELECT grant_id FROM grant_record WHERE actor_id=?", grantee).Scan(&id)
	})
	if value(t, db, "SELECT revision FROM board") != 1 || value(t, db, "SELECT count(*) FROM activity") != 1 || value(t, db, "SELECT count(*) FROM actor") != 2 {
		t.Fatal("create atomic effects")
	}
	j := intent(t, op2, "grant.revoke", id, 1, actor, "", nil)
	terminal(t, e.Submit(testContext(t), j, s.GrantCallbacks(Route{board, inc, id})), "committed")
	if value(t, db, "SELECT revision FROM board") != 2 || value(t, db, "SELECT count(*) FROM activity") != 2 || value(t, db, "SELECT count(*) FROM grant_record WHERE enabled=0") != 1 {
		t.Fatal("revoke atomic effects")
	}
	terminal(t, e.Lookup(testContext(t), key(t, j), s.Authority), "committed")
	rights(t, db, "human", "grant-admin")
	denied(t, e.Lookup(testContext(t), key(t, j), s.Authority))
	if value(t, db, "SELECT count(*) FROM operation") != 2 {
		t.Fatal("lookup changed ledger")
	}
}

func TestGrantRefusalsLeaveDomainUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		rs         []string
		route      Route
		rev        uint64
	}{
		{"forbidden-automation", "automation", []string{"approve"}, Route{board, inc, ""}, 0},
		{"wrong-scope", "human", []string{"read"}, Route{grantee, inc, ""}, 0},
		{"wrong-incarnation", "human", []string{"read"}, Route{board, grantee, ""}, 0},
		{"unexpected-create-target", "human", []string{"read"}, Route{board, inc, grant}, 0},
		{"stale-revision", "human", []string{"read"}, Route{board, inc, ""}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, db := fixture(t)
			rights(t, db, "human", "grant-admin", "read-own-outcome")
			s := service(t, cred())
			i := intent(t, op, "grant.create", "", tc.rev, actor, tc.kind, tc.rs)
			terminal(t, e.Submit(testContext(t), i, s.GrantCallbacks(tc.route)), "not_committed")
			if value(t, db, "SELECT revision FROM board") != 0 || value(t, db, "SELECT count(*) FROM activity") != 0 || value(t, db, "SELECT count(*) FROM grant_record") != 1 || value(t, db, "SELECT count(*) FROM actor") != 1 {
				t.Fatal("refusal domain effect")
			}
		})
	}
	for _, route := range []Route{{board, inc, grantee}, {grantee, inc, grant}} {
		t.Run("revoke-target-"+route.GrantID, func(t *testing.T) {
			e, db := fixture(t)
			rights(t, db, "human", "grant-admin", "read-own-outcome")
			s := service(t, cred())
			i := intent(t, op, "grant.revoke", grant, 0, actor, "", nil)
			terminal(t, e.Submit(testContext(t), i, s.GrantCallbacks(route)), "not_committed")
			if value(t, db, "SELECT enabled FROM grant_record") != 1 || value(t, db, "SELECT revision FROM board") != 0 {
				t.Fatal("wrong revoke effect")
			}
		})
	}
}

func TestOwnRevocationAndNarrowNativeEntitlement(t *testing.T) {
	e, db := fixture(t)
	rights(t, db, "human", "grant-admin", "read-own-outcome")
	s := service(t, cred())
	i := intent(t, op, "grant.revoke", grant, 0, actor, "", nil)
	denied(t, e.Submit(testContext(t), i, s.GrantCallbacks(Route{board, inc, grant})))
	if value(t, db, "SELECT revision FROM board") != 1 || value(t, db, "SELECT enabled FROM grant_record") != 0 || value(t, db, "SELECT count(*) FROM operation") != 1 {
		t.Fatal("own revoke did not commit")
	}
	denied(t, e.Lookup(testContext(t), key(t, i), s.Authority))
	c := cred()
	c.Mode = "native-owner"
	c.BoardID = ""
	c.Incarnation = ""
	c.GrantID = ""
	native := service(t, c)
	terminal(t, e.Lookup(testContext(t), key(t, i), native.Authority), "committed")
	terminal(t, e.Submit(testContext(t), i, writer.Callbacks{Authority: native.Authority}), "committed")
	raw(t, db, func(conn *sql.Conn) error {
		for _, right := range []string{"read", "read-own-outcome", "export", "approve", "history-admin", "grant-admin"} {
			if native.Require(testContext(t), read{conn}, board, inc, actor, right) == nil {
				t.Fatal("native ordinary bypass")
			}
		}
		k := key(t, i)
		if native.Authority(testContext(t), read{conn}, writer.AuthorityRequest{Identity: k}, writer.OwnOutcome) == nil {
			t.Fatal("native missing stored metadata")
		}
		k.ActorID = grantee
		if native.Authority(testContext(t), read{conn}, writer.AuthorityRequest{Identity: k, Operation: &writer.OperationIdentity{Command: "grant.revoke", Target: &contract.Target{GrantID: grant}}}, writer.OwnOutcome) == nil {
			t.Fatal("native arbitrary actor")
		}
		return nil
	})
	c.ExpiresAt = now
	denied(t, e.Lookup(testContext(t), key(t, i), service(t, c).Authority))
}

func TestVerifierAndCallbacksFailClosed(t *testing.T) {
	e, db := fixture(t)
	rights(t, db, "human", "grant-admin", "read-own-outcome")
	if _, err := New(installation, origin, nil); err == nil {
		t.Fatal("nil verifier")
	}
	s, _ := New(installation, origin, func(context.Context, writer.Read, time.Time) (Credential, error) { return Credential{}, ErrDenied })
	s.now = func() time.Time { return now }
	i := intent(t, op, "grant.create", "", 0, actor, "human", []string{"read"})
	denied(t, e.Submit(testContext(t), i, s.GrantCallbacks(Route{board, inc, ""})))
	if e.Submit(testContext(t), i, writer.Callbacks{}).Terminal != nil {
		t.Fatal("missing callbacks committed")
	}
	if value(t, db, "SELECT count(*) FROM operation") != 0 || value(t, db, "SELECT revision FROM board") != 0 {
		t.Fatal("unauthenticated effect")
	}
}

func TestConcurrentRevokeAndAdmissionSerialize(t *testing.T) {
	for _, revokeFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(revokeFirst), func(t *testing.T) {
			e, db := fixture(t)
			rights(t, db, "human", "grant-admin", "read-own-outcome")
			ordinary := service(t, cred())
			nc := cred()
			nc.Mode = "native-owner"
			nc.BoardID = ""
			nc.Incarnation = ""
			nc.GrantID = ""
			native := service(t, nc)
			reached := make(chan struct{})
			release := make(chan struct{})
			var once atomic.Bool
			selected := ordinary
			if revokeFirst {
				selected = native
			}
			original := selected.verify
			selected.verify = func(ctx context.Context, r writer.Read, at time.Time) (Credential, error) {
				if once.CompareAndSwap(false, true) {
					close(reached)
					select {
					case <-release:
					case <-ctx.Done():
						return Credential{}, ctx.Err()
					}
				}
				return original(ctx, r, at)
			}
			createRev, revokeRev := uint64(0), uint64(1)
			if revokeFirst {
				revokeRev = 0
				createRev = 1
			}
			create := intent(t, op, "grant.create", "", createRev, actor, "automation", []string{"read"})
			revoke := intent(t, op2, "grant.revoke", grant, revokeRev, actor, "", nil)
			first, second := make(chan writer.Result, 1), make(chan writer.Result, 1)
			if revokeFirst {
				go func() {
					first <- e.Submit(context.Background(), revoke, native.GrantCallbacks(Route{board, inc, grant}))
				}()
			} else {
				go func() {
					first <- e.Submit(context.Background(), create, ordinary.GrantCallbacks(Route{board, inc, ""}))
				}()
			}
			select {
			case <-reached:
			case <-time.After(5 * time.Second):
				t.Fatal("first not admitted")
			}
			if revokeFirst {
				go func() {
					second <- e.Submit(context.Background(), create, ordinary.GrantCallbacks(Route{board, inc, ""}))
				}()
			} else {
				go func() {
					second <- e.Submit(context.Background(), revoke, native.GrantCallbacks(Route{board, inc, grant}))
				}()
			}
			close(release)
			firstResult := <-first
			if !revokeFirst && firstResult.Refused != nil {
				// The later revoke may win before S02's fresh receipt readback.
				// That current-authority denial does not undo the first COMMIT.
				denied(t, firstResult)
			} else {
				terminal(t, firstResult, "committed")
			}
			r := <-second
			if revokeFirst {
				denied(t, r)
				if value(t, db, "SELECT revision FROM board") != 1 || value(t, db, "SELECT count(*) FROM grant_record") != 1 {
					t.Fatal("revoked admission effect")
				}
			} else {
				terminal(t, r, "committed")
				if value(t, db, "SELECT revision FROM board") != 2 || value(t, db, "SELECT count(*) FROM grant_record") != 2 || value(t, db, "SELECT count(*) FROM operation WHERE terminal_class='COMMITTED'") != 2 {
					t.Fatal("admitted-before-revoke effect")
				}
			}
		})
	}
}

func TestAutomationCurrentRightsNeverElevate(t *testing.T) {
	_, db := fixture(t)
	raw(t, db, func(c *sql.Conn) error {
		for _, q := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO actor VALUES(?,?,'automation','Synthetic',?,?)", []any{inc, grantee, board, inc}},
			{"INSERT INTO grant_fact VALUES(?,?)", []any{inc, grantee}},
			{"INSERT INTO grant_record VALUES(?,?,?,1)", []any{inc, grantee, grantee}},
			{"INSERT INTO grant_right VALUES(?,?,'comment')", []any{inc, grantee}},
		} {
			if _, err := c.ExecContext(testContext(t), q.query, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	c := cred()
	c.ActorID = grantee
	c.GrantID = grantee
	s := service(t, c)
	raw(t, db, func(conn *sql.Conn) error {
		if s.Require(testContext(t), read{conn}, board, inc, grantee, "comment") != nil {
			t.Fatal("automation comment denied")
		}
		for _, r := range []string{"approve", "history-admin", "grant-admin", "read"} {
			if s.Require(testContext(t), read{conn}, board, inc, grantee, r) == nil {
				t.Fatal("automation elevation", r)
			}
		}
		return nil
	})
	// Defense in depth: even forbidden rights placed in storage cannot authorize
	// an automation actor. This is synthetic corruption, not grant creation.
	raw(t, db, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(testContext(t), "INSERT INTO grant_right VALUES(?,?,'approve')", inc, grantee)
		return err
	})
	raw(t, db, func(conn *sql.Conn) error {
		if s.Require(testContext(t), read{conn}, board, inc, grantee, "approve") == nil {
			t.Fatal("stored forbidden right admitted")
		}
		return nil
	})
}

func TestNativeGrantCreateAndCompactReceiptRestrictions(t *testing.T) {
	e, db := fixture(t)
	c := cred()
	c.Mode = "native-owner"
	c.BoardID = ""
	c.Incarnation = ""
	c.GrantID = ""
	s := service(t, c)
	i := intent(t, op, "grant.create", "", 0, actor, "human", []string{"read"})
	terminal(t, e.Submit(testContext(t), i, s.GrantCallbacks(Route{board, inc, ""})), "committed")
	terminal(t, e.Lookup(testContext(t), key(t, i), s.Authority), "committed")
	raw(t, db, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(testContext(t), "DELETE FROM operation_full WHERE op_id=?", op); err != nil {
			return err
		}
		_, err := conn.ExecContext(testContext(t), "UPDATE operation SET removed_at='2026-10-26T00:00:02Z',removal_reason='expired',removal_revision=1 WHERE op_id=?", op)
		return err
	})
	terminal(t, e.Lookup(testContext(t), key(t, i), s.Authority), "committed")
	k := key(t, i)
	k.Incarnation = grantee
	if e.Lookup(testContext(t), k, s.Authority).Terminal != nil {
		t.Fatal("source incarnation resolved")
	}
	k = key(t, i)
	k.ActorID = grantee
	denied(t, e.Lookup(testContext(t), k, s.Authority))
	k = key(t, i)
	k.Fingerprint = strings.Repeat("a", 64)
	denied(t, e.Lookup(testContext(t), k, s.Authority))
	raw(t, db, func(conn *sql.Conn) error {
		k := key(t, i)
		for _, m := range []*writer.OperationIdentity{
			{Command: "card.create"},
			{Command: "grant.revoke"},
			{Command: "grant.create", Target: &contract.Target{GrantID: grant}},
			{Command: "grant.revoke", Target: &contract.Target{GrantID: grant, CardID: grantee}},
		} {
			if s.Authority(testContext(t), read{conn}, writer.AuthorityRequest{Identity: k, Operation: m}, writer.OwnOutcome) == nil {
				t.Fatal("native malformed/unrelated metadata")
			}
		}
		return nil
	})
}

func TestCurrentVerifierRecheckedAtEveryAdmission(t *testing.T) {
	e, db := fixture(t)
	rights(t, db, "human", "grant-admin", "read-own-outcome")
	var calls atomic.Int32
	s, _ := New(installation, origin, func(context.Context, writer.Read, time.Time) (Credential, error) {
		if calls.Add(1) > 1 {
			return Credential{}, ErrDenied
		}
		return cred(), nil
	})
	s.now = func() time.Time { return now }
	i := intent(t, op, "grant.create", "", 0, actor, "human", []string{"read"})
	denied(t, e.Submit(testContext(t), i, s.GrantCallbacks(Route{board, inc, ""})))
	if calls.Load() < 3 || value(t, db, "SELECT revision FROM board") != 0 || value(t, db, "SELECT count(*) FROM activity") != 0 || value(t, db, "SELECT count(*) FROM operation WHERE terminal_class='NOT_COMMITTED'") != 1 {
		t.Fatal("stale verifier result reused")
	}
}
