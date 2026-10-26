//go:build windows && amd64 && t01_native

package credentials

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

const sessionBoard = "10000000-0000-0000-0000-000000000001"
const sessionInc = "20000000-0000-0000-0000-000000000001"
const sessionActor = "30000000-0000-0000-0000-000000000003"
const sessionOp = "50000000-0000-0000-0000-000000000001"
const sessionOp2 = "50000000-0000-0000-0000-000000000002"

type sessionFixture struct {
	s         *Service
	store     *S.Store
	manager   *setup.Manager
	owner     T.SecretToken
	root, dir string
}

func sessionContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func sessionFresh(t *testing.T) sessionFixture {
	t.Helper()
	// The private qualification overlay replaces nativeRoot with ONLY this
	// test-root resolver. Without it, NEVER construct a production Manager.
	if os.Getenv("T01_NATIVE_OVERLAY") != "1" {
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
	root, err := os.MkdirTemp(base, "t01-access-")
	if err != nil {
		t.Fatal("synthetic fixture unavailable")
	}
	t.Setenv("T01_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	qualifiedRoot, qualifiedErr := setup.T01QualificationRoot()
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
	store, err := S.Create(sessionContext(t), dir, schema.Identity{BoardID: sessionBoard, Incarnation: sessionInc})
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
	return sessionFixture{s, store, m, T.SecretToken(strings.TrimPrefix(n.Authorization(), "Bearer ")), root, dir}
}

func sessionRaw(t *testing.T, s *S.Store, f func(*sql.Conn) error) {
	t.Helper()
	l, e := s.Writer(sessionContext(t))
	if e != nil {
		t.Fatal("writer unavailable")
	}
	e = l.Do(sessionContext(t), f)
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		t.Fatal("sessionFixture query failed")
	}
}
func sessionNumber(t *testing.T, s *S.Store, q string) int64 {
	t.Helper()
	l, e := s.Reader(sessionContext(t))
	if e != nil {
		t.Fatal("reader unavailable")
	}
	var n int64
	e = l.Do(sessionContext(t), func(c *sql.Conn) error { return c.QueryRowContext(sessionContext(t), q).Scan(&n) })
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		t.Fatal("sessionFixture read failed")
	}
	return n
}
func sessionIntent(t *testing.T, actor, id, command, grant string, rev uint64) contract.Intent {
	t.Helper()
	var target any
	input := any(map[string]any{})
	if command == "grant.create" {
		input = map[string]any{"granteeActorId": sessionActor, "kind": "automation", "rights": []string{"read", "read-own-outcome"}}
	}
	if command == "grant.revoke" {
		target = map[string]any{"grantId": grant}
	}
	wire, _ := json.Marshal(map[string]any{"boardId": sessionBoard, "incarnation": sessionInc, "opId": id, "target": target, "expectedRevision": rev, "expectedCardRevision": nil, "input": input})
	i, e := contract.Freeze(wire, contract.ResolvedRoute{Command: contract.Command(command), BoardID: sessionBoard, TargetID: grant}, contract.AuthenticatedActor{ActorID: actor})
	if e != nil {
		t.Fatal("freeze failed")
	}
	return i
}
func sessionGrant(t *testing.T, f sessionFixture) string {
	t.Helper()
	result := f.s.SubmitNative(sessionContext(t), f.owner, sessionIntent(t, f.s.context.OwnerActorID, sessionOp, "grant.create", "", 0), grants.Route{BoardID: sessionBoard, Incarnation: sessionInc})
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
func sessionIssue(t *testing.T, f sessionFixture, g string) IssueResult {
	t.Helper()
	r := f.s.Issue(sessionContext(t), f.owner, T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(sessionBoard), Incarnation: A.UUID(sessionInc), GrantID: A.UUID(g)})
	if r.Status != Confirmed {
		t.Fatal("issue unconfirmed", r.Status)
	}
	return r
}
func sessionToken(t *testing.T, r IssueResult) T.SecretToken {
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

func sessionRequest(f sessionFixture, grant string) T.CodeRequest {
	return T.CodeRequest{InstallationID: T.UUIDv4(f.s.context.InstallationID), BoardID: A.UUID(sessionBoard), Incarnation: A.UUID(sessionInc), GrantID: A.UUID(grant)}
}

func sessionState(t *testing.T, f sessionFixture) string {
	t.Helper()
	var state string
	sessionRaw(t, f.store, func(c *sql.Conn) error {
		return c.QueryRowContext(sessionContext(t), `SELECT revision||':'||next_ticket||':'||
 (SELECT count(*) FROM actor)||':'||(SELECT count(*) FROM grant_record)||':'||
 (SELECT count(*) FROM grant_right)||':'||(SELECT count(*) FROM activity)||':'||
 (SELECT count(*) FROM operation)||':'||coalesce((SELECT group_concat(
 credential_hash||credential_id||installation_id||origin||board_id||incarnation||grant_id||actor_id||issued_at||expires_at)
 FROM cli_credential),'') FROM board`).Scan(&state)
	})
	return state
}

func TestSessionAccessNativeScopeNoDurableChangesAndCLI(t *testing.T) {
	f := sessionFresh(t)
	g := sessionGrant(t, f)
	token := sessionToken(t, sessionIssue(t, f, g))
	before := sessionState(t, f)
	now := time.Now().UTC().Truncate(time.Second)
	f.s.now = func() time.Time { return now }
	called := 0
	err := f.s.WithBrowserGrant(sessionContext(t), f.owner, sessionRequest(f, g), func(scope BrowserGrantScope) error {
		called++
		if scope != (BrowserGrantScope{InstallationID: f.s.context.InstallationID, Origin: f.s.context.Origin, BoardID: sessionBoard, Incarnation: sessionInc, GrantID: g, ActorID: sessionActor, IssuedAt: now}) {
			t.Error("scope differs from authenticated current selection")
		}
		return nil
	})
	if err != nil || called != 1 || !f.s.Active() || sessionState(t, f) != before {
		t.Fatal("publication changed durable state or failed")
	}
	sessionRaw(t, f.store, func(c *sql.Conn) error {
		credential, err := f.s.VerifyCLI(sessionContext(t), access{sessionContext(t), c}, token, now)
		if err != nil || credential.InstallationID != f.s.context.InstallationID || credential.Origin != f.s.context.Origin || credential.BoardID != sessionBoard || credential.Incarnation != sessionInc || credential.GrantID != g || credential.ActorID != sessionActor || credential.Mode != "cli" || !credential.IdleExpiresAt.IsZero() {
			t.Error("exact durable CLI identity not resolved")
		}
		return nil
	})
	old := token
	token = sessionToken(t, sessionIssue(t, f, g))
	sessionRaw(t, f.store, func(c *sql.Conn) error {
		if _, err := f.s.VerifyCLI(sessionContext(t), access{sessionContext(t), c}, old, now); err == nil {
			t.Error("replaced CLI hash accepted")
		}
		if _, err := f.s.VerifyCLI(sessionContext(t), access{sessionContext(t), c}, token, now.Add(30*24*time.Hour)); err == nil {
			t.Error("CLI accepted at expiration")
		}
		return nil
	})
	f.s.Close()
	called = 0
	if f.s.Active() || f.s.WithBrowserGrant(sessionContext(t), f.owner, sessionRequest(f, g), func(BrowserGrantScope) error { called++; return nil }) == nil || called != 0 {
		t.Fatal("closed provider published a scope")
	}
	sessionRaw(t, f.store, func(c *sql.Conn) error {
		if _, err := f.s.VerifyCLI(sessionContext(t), access{sessionContext(t), c}, token, now); err == nil {
			t.Error("closed provider verified CLI")
		}
		return nil
	})
}

func TestSessionAccessNativeRefusalsNeverPublish(t *testing.T) {
	f := sessionFresh(t)
	g := sessionGrant(t, f)
	valid := sessionRequest(f, g)
	for _, which := range []string{"owner", "installation", "board", "incarnation", "grant", "malformed", "cancelled", "disabled"} {
		t.Run(which, func(t *testing.T) {
			req, owner, ctx := valid, f.owner, sessionContext(t)
			switch which {
			case "owner":
				owner = T.SecretToken(strings.Repeat("A", 43))
			case "installation":
				req.InstallationID = T.UUIDv4("10000000-0000-4000-8000-000000000099")
			case "board":
				req.BoardID = A.UUID(sessionActor)
			case "incarnation":
				req.Incarnation = A.UUID(sessionActor)
			case "grant":
				req.GrantID = A.UUID(sessionActor)
			case "malformed":
				req.GrantID = "bad"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "disabled":
				sessionRaw(t, f.store, func(c *sql.Conn) error { _, err := c.ExecContext(ctx, "UPDATE grant_record SET enabled=0"); return err })
			}
			before := sessionState(t, f)
			called := false
			err := f.s.WithBrowserGrant(ctx, owner, req, func(BrowserGrantScope) error { called = true; return nil })
			if err == nil || called || sessionState(t, f) != before {
				t.Fatal("denial published or altered state")
			}
		})
	}
}

func TestSessionAccessPublicationFailureAndUncertainty(t *testing.T) {
	for _, stage := range []string{"callback", "panic", "begin", "before-commit", "commit", "after-commit", "readback", "close", "rollback"} {
		t.Run(stage, func(t *testing.T) {
			f := sessionFresh(t)
			g := sessionGrant(t, f)
			f.s.fault = func(at string) error {
				if at == stage || stage == "rollback" && at == "before-commit" {
					return errors.New("synthetic failure")
				}
				return nil
			}
			called := 0
			err := f.s.WithBrowserGrant(sessionContext(t), f.owner, sessionRequest(f, g), func(BrowserGrantScope) error {
				called++
				if stage == "panic" {
					panic("private callback detail")
				}
				if stage == "callback" {
					return errors.New("private callback detail")
				}
				return nil
			})
			if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") || (stage == "begin" && called != 0) || (stage != "begin" && called != 1) {
				t.Fatal("publication failure was confirmed or leaked")
			}
		})
	}
}

func TestSessionAccessSerializesRevocation(t *testing.T) {
	f := sessionFresh(t)
	g := sessionGrant(t, f)
	ctx := sessionContext(t)
	entered, release := make(chan struct{}), make(chan struct{})
	published := make(chan error, 1)
	go func() {
		published <- f.s.WithBrowserGrant(ctx, f.owner, sessionRequest(f, g), func(BrowserGrantScope) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("publication not admitted")
	}
	intent := sessionIntent(t, f.s.context.OwnerActorID, sessionOp2, "grant.revoke", g, 1)
	revoked := make(chan NativeSubmission, 1)
	go func() {
		revoked <- f.s.SubmitNative(ctx, f.owner, intent, grants.Route{BoardID: sessionBoard, Incarnation: sessionInc, GrantID: g})
	}()
	select {
	case <-revoked:
		close(release)
		t.Fatal("revocation crossed publication writer")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-published; err != nil {
		t.Fatal("publication failed before revocation")
	}
	select {
	case r := <-revoked:
		if r.Preparation != Confirmed || r.Result == nil || r.Result.Terminal == nil || r.Result.Terminal.Outcome != "committed" {
			t.Fatal("revocation did not commit")
		}
	case <-ctx.Done():
		t.Fatal("revocation blocked after publication")
	}
	called := false
	if err := f.s.WithBrowserGrant(ctx, f.owner, sessionRequest(f, g), func(BrowserGrantScope) error { called = true; return nil }); !errors.Is(err, ErrDenied) || called {
		t.Fatal("revoked grant published")
	}
}
