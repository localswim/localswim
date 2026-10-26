//go:build windows && amd64 && g02_native

package credentials

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

const board = "10000000-0000-0000-0000-000000000001"
const inc = "20000000-0000-0000-0000-000000000001"
const grantee = "30000000-0000-0000-0000-000000000003"
const op = "50000000-0000-0000-0000-000000000001"
const op2 = "50000000-0000-0000-0000-000000000002"

type fixture struct {
	s         *Service
	store     *S.Store
	manager   *setup.Manager
	owner     T.SecretToken
	root, dir string
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func fresh(t *testing.T) fixture {
	t.Helper()
	// The private qualification overlay replaces nativeRoot with ONLY this
	// test-root resolver. Without it, NEVER construct a production Manager.
	if os.Getenv("G02_NATIVE_OVERLAY") != "1" {
		t.Fatal("requires explicit private N01-root qualification overlay")
	}
	base := os.Getenv("S01_TEST_ROOT")
	if !filepath.IsAbs(base) {
		t.Fatal("absolute private S01_TEST_ROOT required")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(filepath.Clean(base))+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("synthetic roots outside public repository required")
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal("synthetic fixture container unavailable")
	}
	root, err := os.MkdirTemp(base, "g02-")
	if err != nil {
		t.Fatal("synthetic fixture unavailable")
	}
	t.Setenv("G02_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	qualifiedRoot, qualifiedErr := setup.G02QualificationRoot()
	if qualifiedErr != nil || qualifiedRoot != filepath.Join(root, "installation") {
		t.Fatal("private native-root overlay required")
	}
	m, err := setup.New(nil)
	if err != nil {
		t.Fatal("synthetic provider unavailable")
	}
	if r := m.Init(nil); r.Exit != 0 {
		t.Fatal("synthetic initialization refused", r.Exit)
	}
	n, err := m.LoadNativeOwner()
	if err != nil {
		t.Fatal("synthetic owner unavailable")
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal("account unavailable")
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		t.Fatal("account unavailable")
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal("ACL unavailable")
	}
	dir := filepath.Join(root, "board")
	if err = windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); err != nil {
		t.Fatal("private directory unavailable")
	}
	store, err := S.Create(ctx(t), dir, schema.Identity{BoardID: board, Incarnation: inc})
	if err != nil {
		t.Fatal("synthetic store unavailable")
	}
	s, err := SourceRoot(store)
	if err != nil {
		t.Fatal("real source-root composition unavailable")
	}
	t.Cleanup(func() {
		s.Close()
		if err := store.Close(); err != nil {
			t.Error("store cleanup")
		}
	})
	return fixture{s, store, m, T.SecretToken(strings.TrimPrefix(n.Authorization(), "Bearer ")), root, dir}
}

func raw(t *testing.T, s *S.Store, f func(*sql.Conn) error) {
	t.Helper()
	l, e := s.Writer(ctx(t))
	if e != nil {
		t.Fatal("writer unavailable")
	}
	e = l.Do(ctx(t), f)
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		t.Fatal("fixture query failed")
	}
}
func number(t *testing.T, s *S.Store, q string) int64 {
	t.Helper()
	l, e := s.Reader(ctx(t))
	if e != nil {
		t.Fatal("reader unavailable")
	}
	var n int64
	e = l.Do(ctx(t), func(c *sql.Conn) error { return c.QueryRowContext(ctx(t), q).Scan(&n) })
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		t.Fatal("fixture read failed")
	}
	return n
}
func makeIntent(t *testing.T, actor, id, command, grant string, rev uint64) contract.Intent {
	t.Helper()
	var target any
	input := any(map[string]any{})
	if command == "grant.create" {
		input = map[string]any{"granteeActorId": grantee, "kind": "automation", "rights": []string{"read", "read-own-outcome"}}
	}
	if command == "grant.revoke" {
		target = map[string]any{"grantId": grant}
	}
	wire, _ := json.Marshal(map[string]any{"boardId": board, "incarnation": inc, "opId": id, "target": target, "expectedRevision": rev, "expectedCardRevision": nil, "input": input})
	i, e := contract.Freeze(wire, contract.ResolvedRoute{Command: contract.Command(command), BoardID: board, TargetID: grant}, contract.AuthenticatedActor{ActorID: actor})
	if e != nil {
		t.Fatal("freeze failed")
	}
	return i
}
func firstGrant(t *testing.T, f fixture) string {
	t.Helper()
	result := f.s.SubmitNative(ctx(t), f.owner, makeIntent(t, f.s.context.OwnerActorID, op, "grant.create", "", 0), grants.Route{BoardID: board, Incarnation: inc})
	if result.Preparation != Confirmed || result.Result == nil || result.Result.Terminal == nil || result.Result.Terminal.Outcome != "committed" {
		t.Fatal("first grant did not commit")
	}
	var body struct {
		GrantID string `json:"grantId"`
	}
	if json.Unmarshal(*result.Result.Terminal.Result, &body) != nil || body.GrantID == "" {
		t.Fatal("grant identity unavailable")
	}
	return body.GrantID
}
func issue(t *testing.T, f fixture, g string) IssueResult {
	t.Helper()
	r := f.s.Issue(ctx(t), f.owner, T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(board), Incarnation: A.UUID(inc), GrantID: A.UUID(g)})
	if r.Status != Confirmed {
		t.Fatal("issue unconfirmed", r.Status)
	}
	return r
}
func tokenOf(t *testing.T, r IssueResult) T.SecretToken {
	t.Helper()
	body, e := r.MarshalEphemeral()
	if e != nil {
		t.Fatal("ephemeral serialization")
	}
	var dto struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(body, &dto) != nil || dto.Token == "" {
		t.Fatal("secret delivery")
	}
	return T.SecretToken(dto.Token)
}
func allowed(t *testing.T, s *Service, token T.SecretToken, actor string, right string) bool {
	t.Helper()
	a, e := s.Admission("cli", token)
	if e != nil {
		return false
	}
	l, e := s.store.Reader(ctx(t))
	if e != nil {
		return false
	}
	defer l.Close()
	return l.Do(ctx(t), func(c *sql.Conn) error { return a.Require(ctx(t), access{ctx(t), c}, board, inc, actor, right) }) == nil
}

func TestFirstOwnerSuccessAndStaleRefusal(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			f := fresh(t)
			expected := uint64(0)
			if stale {
				expected = 5
			}
			i := makeIntent(t, f.s.context.OwnerActorID, op, "grant.create", "", expected)
			r := f.s.SubmitNative(ctx(t), f.owner, i, grants.Route{BoardID: board, Incarnation: inc})
			if r.Preparation != Confirmed || r.Result == nil || r.Result.Terminal == nil {
				t.Fatal("fresh command unresolved")
			}
			want := T.TerminalOutcome("committed")
			rev := int64(1)
			if stale {
				want = "not_committed"
				rev = 0
			}
			if r.Result.Terminal.Outcome != want || number(t, f.store, "SELECT revision FROM board") != rev || number(t, f.store, "SELECT count(*) FROM operation") != 1 {
				t.Fatal("first command invariants")
			}
			if number(t, f.store, "SELECT count(*) FROM actor WHERE label='native owner' AND kind='human'") != 1 {
				t.Fatal("actual owner attribution absent")
			}
			if stale && (number(t, f.store, "SELECT count(*) FROM grant_record") != 0 || number(t, f.store, "SELECT count(*) FROM activity") != 0) {
				t.Fatal("refusal changed domain")
			}
		})
	}
}

func TestIssueReplaceRestartRightsExpiryAndSelfRevoke(t *testing.T) {
	f := fresh(t)
	g := firstGrant(t, f)
	r1 := issue(t, f, g)
	token1 := tokenOf(t, r1)
	if !allowed(t, f.s, token1, grantee, "read") || allowed(t, f.s, token1, grantee, "approve") {
		t.Fatal("current rights")
	}
	r2 := issue(t, f, g)
	token2 := tokenOf(t, r2)
	if token1 == token2 || r1.Public.CredentialID == r2.Public.CredentialID || allowed(t, f.s, token1, grantee, "read") || !allowed(t, f.s, token2, grantee, "read") {
		t.Fatal("replacement overlap")
	}
	if f.s.SelfRevoke(ctx(t), token1) != Refused {
		t.Fatal("old token revoked replacement")
	}
	if number(t, f.store, "SELECT count(*) FROM cli_credential") != 1 || number(t, f.store, "SELECT revision FROM board") != 1 || number(t, f.store, "SELECT count(*) FROM operation") != 1 || number(t, f.store, "SELECT count(*) FROM activity") != 1 || number(t, f.store, "SELECT count(*) FROM actor") != 2 {
		t.Fatal("issue altered domain")
	}
	f.s.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal("restart close")
	}
	store, err := S.Existing(ctx(t), f.dir, schema.Identity{BoardID: board, Incarnation: inc})
	if err != nil {
		t.Fatal("restart reopen")
	}
	defer store.Close()
	s, err := SourceRoot(store)
	if err != nil {
		t.Fatal("restart service")
	}
	defer s.Close()
	if !allowed(t, s, token2, grantee, "read") {
		t.Fatal("restart lost CLI")
	}
	raw(t, store, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx(t), "DELETE FROM grant_right WHERE right_name='read'")
		return e
	})
	if allowed(t, s, token2, grantee, "read") || !allowed(t, s, token2, grantee, "read-own-outcome") {
		t.Fatal("cached rights")
	}
	l, err := store.Reader(ctx(t))
	if err != nil {
		t.Fatal("reader")
	}
	err = l.Do(ctx(t), func(c *sql.Conn) error {
		_, e := s.cli(ctx(t), access{ctx(t), c}, token2, time.Now().Add(31*24*time.Hour))
		if e == nil {
			return errors.New("expired accepted")
		}
		return nil
	})
	l.Close()
	if err != nil {
		t.Fatal("expiry")
	}
	if s.SelfRevoke(ctx(t), token2) != Confirmed || allowed(t, s, token2, grantee, "read-own-outcome") {
		t.Fatal("self revoke")
	}
	if number(t, store, "SELECT enabled FROM grant_record") != 1 || number(t, store, "SELECT revision FROM board") != 1 {
		t.Fatal("self revoke changed grant/domain")
	}
}

func TestBindingsModesAndNoSecretOutput(t *testing.T) {
	f := fresh(t)
	g := firstGrant(t, f)
	r := issue(t, f, g)
	token := tokenOf(t, r)
	h, _ := tokenHash(token)
	for _, mode := range []string{"browser", "native-owner"} {
		a, e := f.s.Admission(mode, token)
		if e == nil {
			l, _ := f.store.Reader(ctx(t))
			e = l.Do(ctx(t), func(c *sql.Conn) error { return a.Require(ctx(t), access{ctx(t), c}, board, inc, grantee, "read") })
			l.Close()
		}
		if e == nil {
			t.Fatal("cross mode")
		}
	}
	if allowed(t, f.s, f.owner, grantee, "read") || allowed(t, f.s, token, f.s.context.OwnerActorID, "read") {
		t.Fatal("cross actor/token")
	}
	encoded, e := json.Marshal(r)
	if e != nil {
		t.Fatal("public serialization")
	}
	for _, output := range []string{string(encoded), fmt.Sprintf("%v %+v %#v", r, r, r)} {
		if strings.Contains(output, string(token)) || strings.Contains(output, h) {
			t.Fatal("secret leaked")
		}
	}
	if _, e := T.Readable(*r.secret); e == nil {
		t.Fatal("public secret rendering")
	}
	for _, bad := range []T.SecretToken{T.SecretToken(string(token) + "\n"), T.SecretToken("garbage"), T.SecretToken(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))} {
		if allowed(t, f.s, bad, grantee, "read") {
			t.Fatal("bad bearer")
		}
	}
	raw(t, f.store, func(c *sql.Conn) error { _, e := c.ExecContext(ctx(t), "DELETE FROM cli_credential"); return e })
	if allowed(t, f.s, token, grantee, "read") {
		t.Fatal("missing hash accepted")
	}
}

type brokenEntropy struct{ remaining int }

func (b *brokenEntropy) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n := min(len(p), b.remaining)
	for i := range p[:n] {
		p[i] = byte(i)
	}
	b.remaining -= n
	return n, nil
}

func TestEntropyFailuresAndAuthOnlyRollback(t *testing.T) {
	f := fresh(t)
	g := firstGrant(t, f)
	r := issue(t, f, g)
	token := tokenOf(t, r)
	for _, n := range []int{0, 15, 16, 47} {
		f.s.random = &brokenEntropy{n}
		x := f.s.Issue(ctx(t), f.owner, T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(board), Incarnation: A.UUID(inc), GrantID: A.UUID(g)})
		if x.Status != Failed || x.secret != nil || x.Public != nil || !allowed(t, f.s, token, grantee, "read") {
			t.Fatal("entropy failure altered active credential")
		}
	}
	f.s.random = rand.Reader
	if number(t, f.store, "SELECT count(*) FROM cli_credential") != 1 || number(t, f.store, "SELECT revision FROM board") != 1 {
		t.Fatal("entropy state")
	}
}

func TestTransactionUncertaintyAndFences(t *testing.T) {
	for _, stage := range []string{"begin", "before-commit", "commit", "after-commit", "readback", "close", "rollback"} {
		t.Run(stage, func(t *testing.T) {
			f := fresh(t)
			g := firstGrant(t, f)
			prior := tokenOf(t, issue(t, f, g))
			f.s.fault = func(at string) error {
				if at == stage || stage == "rollback" && at == "before-commit" {
					return errors.New("synthetic")
				}
				return nil
			}
			r := f.s.Issue(ctx(t), f.owner, T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(board), Incarnation: A.UUID(inc), GrantID: A.UUID(g)})
			want := Unknown
			if stage == "begin" || stage == "before-commit" {
				want = Failed
			}
			if r.Status != want || r.Public != nil || r.secret != nil {
				t.Fatal("failure classification", r.Status)
			}
			if stage == "close" || stage == "rollback" {
				if l, e := f.store.Writer(ctx(t)); e == nil {
					l.Close()
					t.Fatal("cleanup uncertainty not fenced")
				}
			}
			if stage == "before-commit" || stage == "begin" {
				if !allowed(t, f.s, prior, grantee, "read") {
					t.Fatal("known rollback lost prior")
				}
			}
		})
	}
}

func TestAttributionRefusalsProvenanceAndPreparationFaults(t *testing.T) {
	for _, which := range []string{"wrong-actor", "wrong-board", "automation", "preserve-history", "readback", "rollback"} {
		t.Run(which, func(t *testing.T) {
			f := fresh(t)
			actor := f.s.context.OwnerActorID
			if which == "automation" || which == "preserve-history" {
				kind := "human"
				if which == "automation" {
					kind = "automation"
				}
				raw(t, f.store, func(c *sql.Conn) error {
					_, e := c.ExecContext(ctx(t), "INSERT INTO actor VALUES(?,?,?,?,?,?)", inc, actor, kind, "old label", grantee, board)
					return e
				})
			}
			if which == "wrong-actor" {
				actor = grantee
			}
			i := makeIntent(t, actor, op, "grant.create", "", 0)
			route := grants.Route{BoardID: board, Incarnation: inc}
			if which == "wrong-board" {
				route.BoardID = grantee
			}
			if which == "readback" || which == "rollback" {
				f.s.fault = func(at string) error {
					if at == which || which == "rollback" && at == "before-commit" {
						return errors.New("synthetic")
					}
					return nil
				}
			}
			r := f.s.SubmitNative(ctx(t), f.owner, i, route)
			if which == "preserve-history" {
				if r.Result == nil || r.Result.Terminal == nil || r.Result.Terminal.Outcome != "committed" {
					t.Fatal("historical human refused")
				}
				if number(t, f.store, "SELECT count(*) FROM actor WHERE label='old label' AND origin_board='"+grantee+"' AND origin_incarnation='"+board+"'") != 1 {
					t.Fatal("provenance rewritten")
				}
				return
			}
			if r.Preparation == Confirmed || r.Result != nil || number(t, f.store, "SELECT count(*) FROM operation") != 0 || number(t, f.store, "SELECT count(*) FROM grant_record") != 0 || number(t, f.store, "SELECT revision FROM board") != 0 {
				t.Fatal("preparation refusal dispatched")
			}
		})
	}
}

func TestNativeCurrentLeaseExpiryAndClose(t *testing.T) {
	f := fresh(t)
	if _, e := f.manager.AcquireServer(); e == nil {
		t.Fatal("service did not hold native lifetime exclusion")
	}
	if r := f.manager.RenewOwner(f.s.context.InstallationID, f.s.context.IssuerSHA256, setup.Confirmation{}); r.Exit == 0 {
		t.Fatal("renewed running owner")
	}
	f.s.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	r := f.s.SubmitNative(ctx(t), f.owner, makeIntent(t, f.s.context.OwnerActorID, op, "grant.create", "", 0), grants.Route{BoardID: board, Incarnation: inc})
	if r.Preparation != Refused || r.Result != nil || number(t, f.store, "SELECT count(*) FROM actor") != 0 {
		t.Fatal("expired owner prepared")
	}
	f.s.now = time.Now
	f.s.Close()
	if r := f.s.SubmitNative(ctx(t), f.owner, makeIntent(t, f.s.context.OwnerActorID, op, "grant.create", "", 0), grants.Route{BoardID: board, Incarnation: inc}); r.Preparation != Refused {
		t.Fatal("closed owner accepted")
	}
	l, e := f.manager.AcquireServer()
	if e != nil {
		t.Fatal("native lease not released")
	}
	l.Close()
}

func TestNativeLookupCompactAndOriginalContext(t *testing.T) {
	f := fresh(t)
	i := makeIntent(t, f.s.context.OwnerActorID, op, "grant.create", "", 0)
	r := f.s.SubmitNative(ctx(t), f.owner, i, grants.Route{BoardID: board, Incarnation: inc})
	if r.Result == nil || r.Result.Terminal == nil {
		t.Fatal("grant")
	}
	o, e := outcome.Base(i)
	if e != nil {
		t.Fatal("identity")
	}
	key := outcome.IdentityOf(o)
	raw(t, f.store, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx(t), "DELETE FROM operation_full"); e != nil {
			return e
		}
		_, e := c.ExecContext(ctx(t), "UPDATE operation SET removed_at=terminal_at,removal_reason='expired',removal_revision=1")
		return e
	})
	r2 := f.s.NativeLookup(ctx(t), f.owner, f.s.context.InstallationID, f.s.context.Origin, key)
	if r2.Terminal == nil || r2.Terminal.Retention != "compact" || r2.Terminal.Outcome != "committed" {
		t.Fatal("compact native receipt unavailable")
	}
	for _, bad := range []string{"installation", "origin", "actor", "hash", "incarnation"} {
		k := key
		id, origin := f.s.context.InstallationID, f.s.context.Origin
		switch bad {
		case "installation":
			id = grantee
		case "origin":
			origin += "/"
		case "actor":
			k.ActorID = grantee
		case "hash":
			k.Fingerprint = strings.Repeat("0", 64)
		case "incarnation":
			k.Incarnation = board
		}
		x := f.s.NativeLookup(ctx(t), f.owner, id, origin, k)
		if x.Terminal != nil {
			t.Fatal("wrong original context resolved", bad)
		}
	}
	if number(t, f.store, "SELECT count(*) FROM actor") != 2 {
		t.Fatal("lookup registered actor")
	}
}

func TestBothSerializedGrantRevokeIssueOrders(t *testing.T) {
	for _, issueFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(issueFirst), func(t *testing.T) {
			f := fresh(t)
			g := firstGrant(t, f)
			request := T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(board), Incarnation: A.UUID(inc), GrantID: A.UUID(g)}
			var issued IssueResult
			var revoked NativeSubmission
			if issueFirst {
				entered, release := make(chan struct{}), make(chan struct{})
				var once atomic.Bool
				f.s.fault = func(at string) error {
					if at == "before-commit" && once.CompareAndSwap(false, true) {
						close(entered)
						<-release
					}
					return nil
				}
				var wg sync.WaitGroup
				wg.Add(1)
				go func() { defer wg.Done(); issued = f.s.Issue(ctx(t), f.owner, request) }()
				<-entered
				wg.Add(1)
				go func() {
					defer wg.Done()
					revoked = f.s.SubmitNative(ctx(t), f.owner, makeIntent(t, f.s.context.OwnerActorID, op2, "grant.revoke", g, 1), grants.Route{BoardID: board, Incarnation: inc, GrantID: g})
				}()
				close(release)
				wg.Wait()
				if issued.Status != Confirmed || allowed(t, f.s, tokenOf(t, issued), grantee, "read") {
					t.Fatal("issue-first revocation bypass")
				}
			} else {
				entered, release := make(chan struct{}), make(chan struct{})
				a, err := f.s.admission("native-owner", f.owner, false)
				if err != nil {
					t.Fatal("admission")
				}
				cb := a.GrantCallbacks(grants.Route{BoardID: board, Incarnation: inc, GrantID: g})
				apply := cb.Apply
				cb.Apply = func(c context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
					effect, err := apply(c, w, i, at)
					close(entered)
					<-release
					return effect, err
				}
				revokeIntent := makeIntent(t, f.s.context.OwnerActorID, op2, "grant.revoke", g, 1)
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					f.s.mu.RLock()
					defer f.s.mu.RUnlock()
					r := writer.New(f.store).Submit(ctx(t), revokeIntent, cb)
					revoked = NativeSubmission{Preparation: Confirmed, Result: &r}
				}()
				<-entered
				wg.Add(1)
				go func() { defer wg.Done(); issued = f.s.Issue(ctx(t), f.owner, request) }()
				close(release)
				wg.Wait()
				if issued.Status != Refused {
					t.Fatal("revoked grant issue")
				}
			}
			if revoked.Result == nil || revoked.Result.Terminal == nil || revoked.Result.Terminal.Outcome != "committed" || number(t, f.store, "SELECT enabled FROM grant_record") != 0 {
				t.Fatal("revoke unresolved")
			}
		})
	}
}

func TestFirstDeniedGrantDurableRefusal(t *testing.T) {
	f := fresh(t)
	wire, _ := json.Marshal(map[string]any{"boardId": board, "incarnation": inc, "opId": op, "target": nil, "expectedRevision": 0, "expectedCardRevision": nil, "input": map[string]any{"granteeActorId": grantee, "kind": "automation", "rights": []string{"approve"}}})
	i, err := contract.Freeze(wire, contract.ResolvedRoute{Command: "grant.create", BoardID: board}, contract.AuthenticatedActor{ActorID: f.s.context.OwnerActorID})
	if err != nil {
		t.Fatal("strict input freeze")
	}
	r := f.s.SubmitNative(ctx(t), f.owner, i, grants.Route{BoardID: board, Incarnation: inc})
	if r.Preparation != Confirmed || r.Result == nil || r.Result.Terminal == nil || r.Result.Terminal.Outcome != "not_committed" || number(t, f.store, "SELECT count(*) FROM operation") != 1 || number(t, f.store, "SELECT revision FROM board") != 0 || number(t, f.store, "SELECT count(*) FROM grant_record") != 0 || number(t, f.store, "SELECT count(*) FROM actor") != 1 {
		t.Fatal("first denied command not durably refused")
	}
}

type checkingProvider struct {
	nativeProvider
	loads atomic.Int64
	alter func(*setup.NativeOwner) error
}

func (p *checkingProvider) LoadNativeOwner() (setup.NativeOwner, error) {
	p.loads.Add(1)
	n, err := p.nativeProvider.LoadNativeOwner()
	if err == nil && p.alter != nil {
		err = p.alter(&n)
	}
	return n, err
}

func TestCurrentProviderReloadAndNoImplicitAttribution(t *testing.T) {
	f := fresh(t)
	request := T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(board), Incarnation: A.UUID(inc), GrantID: A.UUID(grantee)}
	if r := f.s.Issue(ctx(t), f.owner, request); r.Status != Refused || number(t, f.store, "SELECT count(*) FROM actor") != 0 {
		t.Fatal("issuance bootstrapped actor")
	}
	p := &checkingProvider{nativeProvider: f.manager}
	f.s.provider = p
	for _, which := range []string{"missing", "actor", "installation", "origin", "future", "expired", "token"} {
		p.alter = func(n *setup.NativeOwner) error {
			switch which {
			case "missing":
				return ErrDenied
			case "actor":
				n.Context.OwnerActorID = grantee
			case "installation":
				n.Context.InstallationID = grantee
			case "origin":
				n.Context.Origin += "/"
			case "future":
				n.IssuedAt = time.Now().Add(time.Hour)
			case "expired":
				n.ExpiresAt = time.Now().Add(-time.Hour)
			}
			return nil
		}
		token := f.owner
		if which == "token" {
			token = T.SecretToken(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
		}
		r := f.s.SubmitNative(ctx(t), token, makeIntent(t, f.s.context.OwnerActorID, op, "grant.create", "", 0), grants.Route{BoardID: board, Incarnation: inc})
		if r.Preparation != Refused || r.Result != nil || number(t, f.store, "SELECT count(*) FROM actor") != 0 {
			t.Fatal("noncurrent owner admitted", which)
		}
	}
	p.alter = nil
	firstGrant(t, f)
	if p.loads.Load() < 10 {
		t.Fatal("current native provider was cached")
	}
	if _, err := f.s.Admission("native-owner", f.owner); err == nil {
		t.Fatal("unguarded native callback exposed")
	}
}

func TestCloseWaitsThroughPreparationQueuedWriterAndSubmit(t *testing.T) {
	f := fresh(t)
	// Hold the board writer first. SubmitNative owns its lifecycle read lock
	// while queued for this writer; Close must retain N01 exclusion until the
	// complete preparation+S02 journey releases that lock.
	w, err := f.store.Writer(ctx(t))
	if err != nil {
		t.Fatal("writer")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	f.s.fault = func(at string) error {
		if at == "before-commit" && once.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return nil
	}
	i := makeIntent(t, f.s.context.OwnerActorID, op, "grant.create", "", 0)
	done := make(chan NativeSubmission, 1)
	go func() { done <- f.s.SubmitNative(ctx(t), f.owner, i, grants.Route{BoardID: board, Incarnation: inc}) }()
	// Detect the queued request's acquired lifecycle read lock without a second
	// RLock (which can deadlock once a lifecycle writer is waiting).
	deadline := time.Now().Add(2 * time.Second)
	for f.s.mu.TryLock() {
		f.s.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("request not queued")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { f.s.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("closed while native request queued")
	case <-time.After(20 * time.Millisecond):
	}
	if err = w.Close(); err != nil {
		t.Fatal("release writer")
	}
	<-entered
	if l, err := f.manager.AcquireServer(); err == nil {
		l.Close()
		t.Fatal("native lease released during preparation")
	}
	close(release)
	r := <-done
	if r.Result == nil || r.Result.Terminal == nil || r.Result.Terminal.Outcome != "committed" {
		t.Fatal("close interrupted submit or nested lifecycle lock deadlock")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not finish")
	}
	if l, err := f.manager.AcquireServer(); err != nil {
		t.Fatal("native lease still held after request")
	} else {
		l.Close()
	}
}

func TestIssueExactRequestAndStoredBindings(t *testing.T) {
	f := fresh(t)
	g := firstGrant(t, f)
	base := T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(board), Incarnation: A.UUID(inc), GrantID: A.UUID(g)}
	for _, which := range []string{"installation", "board", "incarnation", "grant"} {
		bad := base
		switch which {
		case "installation":
			bad.InstallationID = T.UUIDv4(grantee)
		case "board":
			bad.BoardID = A.UUID(grantee)
		case "incarnation":
			bad.Incarnation = A.UUID(grantee)
		case "grant":
			bad.GrantID = A.UUID(grantee)
		}
		if r := f.s.Issue(ctx(t), f.owner, bad); r.Status != Refused || r.Public != nil {
			t.Fatal("mismatching issue request", which)
		}
	}
	r := issue(t, f, g)
	token := tokenOf(t, r)
	// A synthetically foreign installation row remains exact foreign auth state;
	// neither the active token nor current rights can adopt it on this service.
	raw(t, f.store, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx(t), "CREATE TEMP TABLE saved AS SELECT * FROM cli_credential"); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx(t), "DELETE FROM cli_credential"); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx(t), "INSERT INTO cli_credential SELECT incarnation,board_id,grant_id,actor_id,?,origin,credential_id,credential_hash,issued_at,expires_at FROM saved", "70000000-0000-4000-8000-000000000001")
		return err
	})
	if allowed(t, f.s, token, grantee, "read") || f.s.SelfRevoke(ctx(t), token) != Refused {
		t.Fatal("foreign binding adopted")
	}
}

func TestPreparationDoesNotCacheNativeAuthority(t *testing.T) {
	f := fresh(t)
	p := &checkingProvider{nativeProvider: f.manager}
	f.s.provider = p
	var once atomic.Bool
	f.s.fault = func(at string) error {
		if at == "before-commit" && once.CompareAndSwap(false, true) {
			p.alter = func(*setup.NativeOwner) error { return ErrDenied }
		}
		return nil
	}
	r := f.s.SubmitNative(ctx(t), f.owner, makeIntent(t, f.s.context.OwnerActorID, op, "grant.create", "", 0), grants.Route{BoardID: board, Incarnation: inc})
	if r.Preparation != Confirmed || r.Result == nil || r.Result.Terminal != nil || r.Result.Refused == nil || number(t, f.store, "SELECT count(*) FROM actor") != 1 || number(t, f.store, "SELECT count(*) FROM operation") != 0 || number(t, f.store, "SELECT count(*) FROM grant_record") != 0 {
		t.Fatal("preparation cached native authentication")
	}
}

func TestCLIAdmissionQueuedIssueAndCloseHasNoLockCycle(t *testing.T) {
	f := fresh(t)
	g := firstGrant(t, f)
	token := tokenOf(t, issue(t, f, g))
	a, err := f.s.Admission("cli", token)
	if err != nil || !allowed(t, f.s, token, grantee, "read") {
		t.Fatal("active CLI admission unavailable")
	}
	w, err := f.store.Writer(ctx(t))
	if err != nil {
		t.Fatal("writer unavailable")
	}
	queuedCtx, cancel := context.WithCancel(context.Background())
	issued := make(chan IssueResult, 1)
	go func() {
		issued <- f.s.Issue(queuedCtx, f.owner, T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(board), Incarnation: A.UUID(inc), GrantID: A.UUID(g)})
	}()
	// Ensure safe unwind even if a blocking verifier regresses: cancel the
	// queued Issue before releasing the board lease or waiting for Close.
	closed := make(chan struct{})
	issueDrained := false
	defer func() {
		cancel()
		if !issueDrained {
			<-issued
		}
		_ = w.Close()
		<-closed
	}()
	deadline := time.Now().Add(2 * time.Second)
	for f.s.mu.TryLock() {
		f.s.mu.Unlock()
		if time.Now().After(deadline) {
			// Start Close so the unconditional cleanup remains bounded.
			go func() { f.s.Close(); close(closed) }()
			t.Fatal("Issue did not acquire lifecycle admission")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { f.s.Close(); close(closed) }()
	// Detect queued Close without blocking. Issue owns one reader, so a
	// failed TryRLock proves the lifecycle writer has entered the wait queue.
	deadline = time.Now().Add(2 * time.Second)
	for f.s.mu.TryRLock() {
		f.s.mu.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("Close did not queue lifecycle exclusion")
		}
		time.Sleep(time.Millisecond)
	}
	authDone := make(chan error, 1)
	go func() {
		authDone <- w.Do(context.Background(), func(c *sql.Conn) error {
			return a.Require(context.Background(), access{context.Background(), c}, board, inc, grantee, "read")
		})
	}()
	select {
	case err := <-authDone:
		if err == nil {
			t.Fatal("shutdown contention admitted CLI authority")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("CLI callback blocked behind Close while holding board writer")
	}
	// Also check the already-created callback after actual Close. Unwind the
	// cycle candidates explicitly, then replace the deferred channel receive.
	cancel()
	<-issued
	issueDrained = true
	if err = w.Close(); err != nil {
		t.Fatal("writer cleanup")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not finish after queued Issue cancellation")
	}
	if allowed(t, f.s, token, grantee, "read") {
		t.Fatal("closed CLI admission accepted")
	}
	l, err := f.store.Reader(ctx(t))
	if err != nil {
		t.Fatal("closed callback reader")
	}
	defer l.Close()
	if err = l.Do(ctx(t), func(c *sql.Conn) error { return a.Require(ctx(t), access{ctx(t), c}, board, inc, grantee, "read") }); err == nil {
		t.Fatal("retained callback accepted after Close")
	}
	if r := f.s.SubmitNative(ctx(t), f.owner, makeIntent(t, f.s.context.OwnerActorID, op2, "grant.create", "", 1), grants.Route{BoardID: board, Incarnation: inc}); r.Preparation != Refused || r.Result != nil {
		t.Fatal("closed native wrapper admitted")
	}
}
