//go:build windows && amd64 && t01_native

package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

const board = "10000000-0000-0000-0000-000000000001"
const incarnation = "20000000-0000-0000-0000-000000000001"
const actor = "30000000-0000-0000-0000-000000000003"

type fixture struct {
	store    *S.Store
	native   *credentials.Service
	sessions *Service
	owner    T.SecretToken
	request  T.CodeRequest
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func fresh(t *testing.T) fixture {
	t.Helper()
	if os.Getenv("T01_NATIVE_OVERLAY") != "1" {
		t.Fatal("explicit private root overlay required")
	}
	base := os.Getenv("S01_TEST_ROOT")
	if !filepath.IsAbs(base) {
		t.Fatal("private fixture root required")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(base)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("fixtures outside public repository required")
	}
	root, e := os.MkdirTemp(base, "t01-")
	if e != nil {
		t.Fatal("fixture unavailable")
	}
	t.Setenv("T01_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	check, e := setup.T01QualificationRoot()
	if e != nil || check != filepath.Join(root, "installation") {
		t.Fatal("private root sentinel required")
	}
	m, e := setup.New(nil)
	if e != nil {
		t.Fatal("provider unavailable")
	}
	if m.Init(nil).Exit != 0 {
		t.Fatal("synthetic initialization")
	}
	owner, e := m.LoadNativeOwner()
	if e != nil {
		t.Fatal("owner unavailable")
	}
	account := windows.GetCurrentProcessToken()
	user, e := account.GetTokenUser()
	if e != nil {
		t.Fatal("account unavailable")
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal("private ACL unavailable")
	}
	dir := filepath.Join(root, "board")
	if windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}) != nil {
		t.Fatal("private directory unavailable")
	}
	store, e := S.Create(testContext(t), dir, schema.Identity{BoardID: board, Incarnation: incarnation})
	if e != nil {
		t.Fatal("store unavailable")
	}
	native, e := credentials.SourceRoot(store)
	if e != nil {
		t.Fatal("source root unavailable")
	}
	sessions, e := New(store, native)
	if e != nil {
		t.Fatal("sessions unavailable")
	}
	f := fixture{store: store, native: native, sessions: sessions, owner: T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer "))}
	t.Cleanup(func() {
		sessions.Close()
		native.Close()
		if store.Close() != nil {
			t.Error("store cleanup")
		}
	})
	wire, _ := json.Marshal(map[string]any{"boardId": board, "incarnation": incarnation, "opId": "50000000-0000-0000-0000-000000000001", "target": nil, "expectedRevision": 0, "expectedCardRevision": nil, "input": map[string]any{"granteeActorId": actor, "kind": "automation", "rights": []string{"read", "read-own-outcome"}}})
	intent, e := contract.Freeze(wire, contract.ResolvedRoute{Command: "grant.create", BoardID: board}, contract.AuthenticatedActor{ActorID: native.Context().OwnerActorID})
	if e != nil {
		t.Fatal("grant fixture freeze")
	}
	result := native.SubmitNative(testContext(t), f.owner, intent, grants.Route{BoardID: board, Incarnation: incarnation})
	if result.Preparation != credentials.Confirmed || result.Result == nil || result.Result.Terminal == nil || result.Result.Terminal.Outcome != "committed" {
		t.Fatal("grant fixture admission")
	}
	var grant struct {
		GrantID string `json:"grantId"`
	}
	if json.Unmarshal(*result.Result.Terminal.Result, &grant) != nil || grant.GrantID == "" {
		t.Fatal("grant fixture identity")
	}
	f.request = T.CodeRequest{InstallationID: T.UUIDv4(native.Context().InstallationID), BoardID: A.UUID(board), Incarnation: A.UUID(incarnation), GrantID: A.UUID(grant.GrantID)}
	return f
}

func issue(t *testing.T, f fixture) T.CodeSecret {
	t.Helper()
	out, e := f.sessions.IssueCode(testContext(t), f.owner, f.request)
	if e != nil {
		t.Fatal("code issue")
	}
	issued, e := time.Parse(time.RFC3339, string(out.IssuedAt))
	if e != nil {
		t.Fatal("code timestamp")
	}
	f.sessions.now = func() time.Time { return issued }
	return out
}
func exchange(t *testing.T, f fixture) T.ExchangeSecret {
	t.Helper()
	out, e := f.sessions.Exchange(testContext(t), issue(t, f).Code)
	if e != nil {
		t.Fatal("code exchange")
	}
	return out
}

type failingEntropy struct{}

func (failingEntropy) Read([]byte) (int, error) { return 0, errors.New("injected entropy failure") }
func TestNativeExpirySlidingEntropyAndScope(t *testing.T) {
	f := fresh(t)
	s := f.sessions
	start := time.Now().UTC().Truncate(time.Second)
	s.now = func() time.Time { return start }
	first := issue(t, f)
	start, _ = time.Parse(time.RFC3339, string(first.IssuedAt))
	s.now = func() time.Time { return start.Add(time.Minute) }
	if _, e := s.Exchange(testContext(t), first.Code); e == nil {
		t.Fatal("code valid at exact expiry")
	}
	s.now = func() time.Time { return start }
	s.random = failingEntropy{}
	if _, e := s.IssueCode(testContext(t), f.owner, f.request); e == nil || len(s.codes) != 0 {
		t.Fatal("entropy failure published code")
	}
	s.random = strings.NewReader(strings.Repeat("x", 16))
	issued := issue(t, f)
	s.random = failingEntropy{}
	if _, e := s.Exchange(testContext(t), issued.Code); e == nil || len(s.sessions) != 0 || len(s.codes) != 0 {
		t.Fatal("entropy failure minted/retained reusable code")
	}
	s.random = strings.NewReader(strings.Repeat("a", 16) + strings.Repeat("b", 32))
	bearer := exchange(t, f)
	h, _ := digest(string(bearer.Token), 32)
	original := s.sessions[h]
	start = original.IssuedAt
	// Invoke the real registry verifier in a real current board snapshot at chosen
	// admission instants; G01 separately rechecks grants in normal Current calls.
	check := func(at time.Time) (grants.Credential, error) {
		var got grants.Credential
		var verifyErr error
		e := s.snapshot(testContext(t), func(r writer.Read) error {
			got, verifyErr = s.browser(testContext(t), r, bearer.Token, at)
			return verifyErr
		})
		return got, e
	}
	c, e := check(start.Add(20 * time.Minute))
	if e != nil || !c.IdleExpiresAt.Equal(start.Add(50*time.Minute)) {
		t.Fatal("sliding idle expiry")
	}
	// Simulate continuous use up to the absolute limit.
	for n := 40; n < 480; n += 20 {
		if _, e = check(start.Add(time.Duration(n) * time.Minute)); e != nil {
			t.Fatal("active session prematurely expired")
		}
	}
	c, e = check(start.Add(479 * time.Minute))
	if e != nil || !c.IdleExpiresAt.Equal(original.ExpiresAt) {
		t.Fatal("idle widened absolute expiry")
	}
	c, e = check(original.ExpiresAt.Add(-time.Nanosecond))
	if e != nil || !c.IdleExpiresAt.Equal(original.ExpiresAt) {
		t.Fatal("session refused just before absolute expiry")
	}
	if _, e = check(original.ExpiresAt); e == nil {
		t.Fatal("absolute expiry boundary")
	}
	s.random = strings.NewReader(strings.Repeat("c", 16) + strings.Repeat("d", 32))
	bearer = exchange(t, f)
	h, _ = digest(string(bearer.Token), 32)
	second := s.sessions[h]
	reportedIssued, issuedErr := time.Parse(time.RFC3339, string(bearer.IssuedAt))
	reportedIdle, idleErr := time.Parse(time.RFC3339, string(bearer.IdleExpiresAt))
	reportedAbsolute, absoluteErr := time.Parse(time.RFC3339, string(bearer.ExpiresAt))
	if errors.Join(issuedErr, idleErr, absoluteErr) != nil || !second.IssuedAt.Equal(reportedIssued) || !second.IdleExpiresAt.Equal(reportedIdle) || !second.ExpiresAt.Equal(reportedAbsolute) {
		t.Fatal("second bearer timestamp bindings")
	}
	// Each edge belongs to this newly issued bearer, independent of whether its
	// real native issuance crossed a whole-second boundary since the first one.
	c, e = check(second.IdleExpiresAt.Add(-time.Nanosecond))
	if e != nil || !c.IdleExpiresAt.Equal(second.IdleExpiresAt.Add(30*time.Minute-time.Nanosecond)) {
		t.Fatal("session refused just before idle expiry")
	}
	if _, e = check(c.IdleExpiresAt); e == nil {
		t.Fatal("idle expiry boundary")
	}
	// A distinct fresh bearer exercises equality at the initial, unslid idle edge.
	s.random = strings.NewReader(strings.Repeat("e", 16) + strings.Repeat("f", 32))
	bearer = exchange(t, f)
	h, _ = digest(string(bearer.Token), 32)
	if _, e = check(s.sessions[h].IdleExpiresAt); e == nil {
		t.Fatal("initial idle expiry boundary")
	}
	bad := f.request
	bad.InstallationID = "11111111-1111-4111-8111-111111111111"
	if _, e = s.IssueCode(testContext(t), f.owner, bad); e == nil {
		t.Fatal("wrong installation")
	}
	bad = f.request
	bad.Incarnation = "22222222-2222-2222-2222-222222222222"
	if _, e = s.IssueCode(testContext(t), f.owner, bad); e == nil {
		t.Fatal("wrong incarnation")
	}
}
func TestNativeCodeRechecksGrantAndActorKind(t *testing.T) {
	f := fresh(t)
	pending := issue(t, f)
	l, e := f.store.Writer(testContext(t))
	if e != nil {
		t.Fatal("fixture writer")
	}
	e = l.Do(testContext(t), func(c *sql.Conn) error {
		_, e := c.ExecContext(testContext(t), "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", incarnation, string(f.request.GrantID))
		return e
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal("fixture revoke")
	}
	if _, e = f.sessions.Exchange(testContext(t), pending.Code); e == nil {
		t.Fatal("code bypassed revoked grant")
	}
}
