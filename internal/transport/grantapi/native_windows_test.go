//go:build windows && amd64 && h01_native

package grantapi

import (
	"bytes"
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

type nativeFixture struct {
	store    *S.Store
	native   *credentials.Service
	sessions *session.Service
	handler  *Handler
	owner    T.SecretToken
	next     int
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func fresh(t *testing.T) *nativeFixture {
	t.Helper()
	if os.Getenv("H01_NATIVE_OVERLAY") != "1" {
		t.Fatal("explicit private overlay required")
	}
	base := os.Getenv("S01_TEST_ROOT")
	if !filepath.IsAbs(base) {
		t.Fatal("private fixture root required")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(base)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("fixture outside public repository required")
	}
	root, err := os.MkdirTemp(base, "h01-")
	if err != nil {
		t.Fatal("fixture root unavailable")
	}
	t.Setenv("H01_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	check, err := setup.H01QualificationRoot()
	if err != nil || check != filepath.Join(root, "installation") {
		t.Fatal("overlay sentinel required")
	}
	m, err := setup.New(nil)
	if err != nil {
		t.Fatal("native provider unavailable")
	}
	if m.Init(nil).Exit != 0 {
		t.Fatal("synthetic initialization")
	}
	owner, err := m.LoadNativeOwner()
	if err != nil {
		t.Fatal("owner unavailable")
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal("account unavailable")
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal("private ACL unavailable")
	}
	dir := filepath.Join(root, "board")
	if windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}) != nil {
		t.Fatal("private board directory")
	}
	store, err := S.Create(testContext(t), dir, schema.Identity{BoardID: testBoard, Incarnation: testInc})
	if err != nil {
		t.Fatal("synthetic store unavailable")
	}
	native, err := credentials.SourceRoot(store)
	if err != nil {
		t.Fatal("source provider unavailable")
	}
	sessions, err := session.New(store, native)
	if err != nil {
		t.Fatal("session service unavailable")
	}
	h, err := New(store, native, sessions)
	if err != nil {
		t.Fatal("adapter unavailable")
	}
	f := &nativeFixture{store: store, native: native, sessions: sessions, handler: h, owner: T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer "))}
	t.Cleanup(func() {
		sessions.Close()
		native.Close()
		if store.Close() != nil {
			t.Error("store cleanup")
		}
	})
	return f
}

func (f *nativeFixture) body(command, target, kind string, rights []string, revision uint64) []byte {
	f.next++
	b := map[string]any{"boardId": testBoard, "incarnation": testInc, "opId": fmt.Sprintf("50000000-0000-0000-0000-%012d", f.next), "target": nil, "expectedRevision": revision, "expectedCardRevision": nil, "input": map[string]any{"granteeActorId": testActor, "kind": kind, "rights": rights}}
	if command == "grant.revoke" {
		b["target"] = map[string]any{"grantId": target}
		b["input"] = map[string]any{}
	}
	raw, _ := json.Marshal(b)
	return raw
}
func (f *nativeFixture) call(method, path, origin string, token T.SecretToken, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://"+f.native.Context().Authority+path, bytes.NewReader(body))
	r.RequestURI = path
	r.URL.Scheme = ""
	r.URL.Host = ""
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+string(token))
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func terminal(t *testing.T, w *httptest.ResponseRecorder, status int) T.Terminal {
	t.Helper()
	if w.Code != status {
		t.Fatalf("unexpected status %d, want %d", w.Code, status)
	}
	out, err := T.Decode[T.Terminal](w.Body.Bytes())
	if err != nil {
		t.Fatal("terminal shape")
	}
	return out
}
func grantID(t *testing.T, o T.Terminal) string {
	t.Helper()
	if o.Result == nil {
		t.Fatal("full grant result required")
	}
	var g struct {
		GrantID string `json:"grantId"`
	}
	if json.Unmarshal(*o.Result, &g) != nil || !schema.ValidUUID(g.GrantID) {
		t.Fatal("grant identity")
	}
	return g.GrantID
}

type counters struct{ revision, grants, activity, operations, actors int }

func (f *nativeFixture) counts(t *testing.T) counters {
	t.Helper()
	ctx := testContext(t)
	l, err := f.store.Reader(ctx)
	if err != nil {
		t.Fatal("count reader")
	}
	var out counters
	err = l.Do(ctx, func(c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT revision,(SELECT count(*) FROM grant_record),(SELECT count(*) FROM activity),(SELECT count(*) FROM operation),(SELECT count(*) FROM actor) FROM board WHERE singleton=1").Scan(&out.revision, &out.grants, &out.activity, &out.operations, &out.actors)
	})
	closeErr := l.Close()
	if err != nil || closeErr != nil {
		t.Fatal("count snapshot")
	}
	return out
}

func TestMountedCommonBoundaryAndStrictJSON(t *testing.T) {
	f := fresh(t)
	before := f.counts(t)
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		body   []byte
		status int
	}{
		{"host", func(r *http.Request) { r.Host = "wrong.localhost" }, wire("grant.create"), 400},
		{"plain transport", func(r *http.Request) { r.TLS = nil }, wire("grant.create"), 400},
		{"cross site preflight", func(r *http.Request) { r.Method = "OPTIONS"; r.Header.Set("Access-Control-Request-Method", "POST") }, wire("grant.create"), 403},
		{"duplicate Origin", func(r *http.Request) {
			r.Header.Add("Origin", f.native.Context().Origin)
			r.Header.Add("Origin", f.native.Context().Origin)
		}, wire("grant.create"), 403},
		{"duplicate bearer", func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+string(f.owner)) }, wire("grant.create"), 401},
		{"wrong media", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, wire("grant.create"), 400},
		{"duplicate media", func(r *http.Request) { r.Header.Add("Content-Type", "application/json") }, wire("grant.create"), 400},
		{"duplicate field", func(*http.Request) {}, []byte(`{"boardId":"` + testBoard + `","boardId":"` + testBoard + `"}`), 400},
		{"invalid UTF8", func(*http.Request) {}, []byte{0xff}, 400},
		{"unknown input", func(*http.Request) {}, bytes.Replace(wire("grant.create"), []byte(`"input":{`), []byte(`"input":{"untrusted":"<script>secret</script>",`), 1), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://"+f.native.Context().Authority+"/api/v1/grants", bytes.NewReader(tc.body))
			r.URL.Scheme = ""
			r.URL.Host = ""
			r.Header.Set("Authorization", "Bearer "+string(f.owner))
			r.Header.Set("Content-Type", "application/json")
			tc.mutate(r)
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("boundary status %d, want %d", w.Code, tc.status)
			}
			if _, err := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); err != nil {
				t.Fatal("boundary refusal shape")
			}
			if strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), string(f.owner)) {
				t.Fatal("unsafe response echo")
			}
			if f.counts(t) != before {
				t.Fatal("boundary validation effect")
			}
		})
	}
}

func TestCompactNativeReceiptRemainsExact(t *testing.T) {
	f := fresh(t)
	body := f.body("grant.create", "", "automation", []string{"read"}, 0)
	first := terminal(t, f.call("POST", "/api/v1/grants", "", f.owner, body), 201)
	ctx := testContext(t)
	lease, err := f.store.Writer(ctx)
	if err != nil {
		t.Fatal("synthetic retention writer")
	}
	err = lease.Do(ctx, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, "DELETE FROM operation_full"); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, "UPDATE operation SET removed_at=terminal_at,removal_reason='expired',removal_revision=1"); err != nil {
			return err
		}
		if err := schema.Validate(ctx, c, schema.Identity{BoardID: testBoard, Incarnation: testInc}); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, "COMMIT")
		return err
	})
	closeErr := lease.Close()
	if err != nil || closeErr != nil {
		t.Fatal("synthetic retention snapshot")
	}
	before := f.counts(t)
	retained := terminal(t, f.call("POST", "/api/v1/grants", "", f.owner, body), 200)
	if retained.Retention != "compact" || retained.Result != nil || retained.Removal == nil || retained.Fingerprint != first.Fingerprint || retained.OpID != first.OpID || f.counts(t) != before {
		t.Fatal("compact terminal identity or replay")
	}
}

func TestNativeRoutesExactScopeAndRetainedReceipts(t *testing.T) {
	f := fresh(t)
	body := f.body("grant.create", "", "automation", []string{"read", "read-own-outcome"}, 0)
	o := terminal(t, f.call("POST", "/api/v1/grants", "", f.owner, body), 201)
	if o.Outcome != "committed" || string(o.ActorID) != f.native.Context().OwnerActorID || o.Target != nil {
		t.Fatal("native actor/target binding")
	}
	id := grantID(t, o)
	before := f.counts(t)
	again := terminal(t, f.call("POST", "/api/v1/grants", "", f.owner, body), 200)
	if again.Fingerprint != o.Fingerprint || f.counts(t) != before {
		t.Fatal("retained matching receipt replayed")
	}
	changed := bytes.Replace(body, []byte(`"rights":["read","read-own-outcome"]`), []byte(`"rights":["read"]`), 1)
	w := f.call("POST", "/api/v1/grants", "", f.owner, changed)
	// A native mismatch has no authenticated retained command metadata; G01
	// deliberately refuses its narrow own-grant-outcome authority before S02
	// discloses a mismatch. It does not prove an original noncommit.
	if w.Code != 403 || f.counts(t) != before {
		t.Fatal("retained mismatch")
	}
	for _, tc := range []struct {
		method, path string
		body         []byte
		status       int
	}{
		{"DELETE", "/api/v1/grants/" + id, f.body("grant.revoke", testGrant, "", nil, 1), 400},
		{"DELETE", "/api/v1/grants/" + testGrant, f.body("grant.revoke", testGrant, "", nil, 1), 404},
		{"POST", "/api/v1/grants/", body, 404}, {"POST", "/api/v1/grants?x=1", body, 400}, {"GET", "/api/v1/grants", body, 405},
		{"POST", "/api/v1/grants", bytes.Replace(f.body("grant.create", "", "automation", []string{"read"}, 1), []byte(testBoard), []byte(testActor), 1), 403},
	} {
		w := f.call(tc.method, tc.path, "", f.owner, tc.body)
		if w.Code != tc.status {
			t.Fatalf("route refusal status %d, want %d", w.Code, tc.status)
		}
		if _, err := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); err != nil {
			t.Fatal("route refusal shape")
		}
		if f.counts(t) != before {
			t.Fatal("route validation side effects")
		}
	}
	rb := f.body("grant.revoke", id, "", nil, 1)
	ro := terminal(t, f.call("DELETE", "/api/v1/grants/"+id, "", f.owner, rb), 200)
	if ro.Outcome != "committed" || ro.Target == nil || string(*ro.Target.GrantID) != id {
		t.Fatal("revoke route identity")
	}
	after := f.counts(t)
	terminal(t, f.call("DELETE", "/api/v1/grants/"+id, "", f.owner, rb), 200)
	if f.counts(t) != after {
		t.Fatal("retained revoke replay")
	}
}

func TestNativeDeniedInputsStaleAndBoundaryHaveNoGrantEffects(t *testing.T) {
	f := fresh(t)
	for _, rights := range [][]string{{"approve"}, {"grant-admin"}, {"history-admin"}} {
		o := terminal(t, f.call("POST", "/api/v1/grants", "", f.owner, f.body("grant.create", "", "automation", rights, 0)), 200)
		if o.Outcome != "not_committed" || o.Code != "INVALID_INPUT" {
			t.Fatal("automation privilege admitted")
		}
	}
	state := f.counts(t)
	if state.revision != 0 || state.grants != 0 || state.activity != 0 || state.operations != 3 || state.actors != 1 {
		t.Fatal("denied grant effects")
	}
	stale := f.body("grant.create", "", "automation", []string{"read"}, 7)
	o := terminal(t, f.call("POST", "/api/v1/grants", "", f.owner, stale), 200)
	if o.Outcome != "not_committed" || o.Code != "REVISION_CONFLICT" {
		t.Fatal("exact board revision")
	}
	terminal(t, f.call("POST", "/api/v1/grants", "", f.owner, stale), 200)
	before := f.counts(t)
	for _, tc := range []struct {
		token  T.SecretToken
		origin string
		body   []byte
		status int
	}{{T.SecretToken(strings.Repeat("A", 43)), "", wire("grant.create"), 401}, {f.owner, "null", wire("grant.create"), 403}, {f.owner, "https://wrong.localhost", wire("grant.create"), 403}, {f.owner, "", []byte(strings.Repeat("x", 4097)), 413}, {f.owner, "", bytes.Replace(wire("grant.create"), []byte(`"input":{`), []byte(`"actorId":"`+testActor+`","input":{`), 1), 400}} {
		w := f.call("POST", "/api/v1/grants", tc.origin, tc.token, tc.body)
		if w.Code != tc.status {
			t.Fatalf("boundary status %d, want %d", w.Code, tc.status)
		}
		if _, err := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); err != nil {
			t.Fatal("prevalidation shape")
		}
		if f.counts(t) != before {
			t.Fatal("prevalidation side effects")
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("HTTP boundary")
		}
	}
}

func (f *nativeFixture) cli(t *testing.T, kind string, rights []string, revision uint64) (string, T.SecretToken) {
	t.Helper()
	o := terminal(t, f.call("POST", "/api/v1/grants", "", f.owner, f.body("grant.create", "", kind, rights, revision)), 201)
	id := grantID(t, o)
	issue := f.native.Issue(testContext(t), f.owner, T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.native.Context().InstallationID), BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), GrantID: A.UUID(id)})
	raw, err := issue.MarshalEphemeral()
	if err != nil {
		t.Fatal("synthetic CLI issue")
	}
	secret, err := T.Decode[T.CredentialSecret](raw)
	if err != nil {
		t.Fatal("synthetic CLI shape")
	}
	return id, secret.Token
}

func TestOrdinaryCLIAndBrowserCurrentGrantChecks(t *testing.T) {
	f := fresh(t)
	admin, token := f.cli(t, "human", []string{"grant-admin", "read-own-outcome"}, 0)
	body := f.body("grant.create", "", "human", []string{"read"}, 1)
	o := terminal(t, f.call("POST", "/api/v1/grants", "", token, body), 201)
	if string(o.ActorID) != testActor {
		t.Fatal("CLI actor binding")
	}
	terminal(t, f.call("POST", "/api/v1/grants", "", token, body), 200)
	req := T.CodeRequest{InstallationID: T.UUIDv4(f.native.Context().InstallationID), BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), GrantID: A.UUID(admin)}
	code, err := f.sessions.IssueCode(testContext(t), f.owner, req)
	if err != nil {
		t.Fatal("synthetic browser code")
	}
	browser, err := f.sessions.Exchange(testContext(t), code.Code)
	if err != nil {
		t.Fatal("synthetic browser exchange")
	}
	w := f.call("POST", "/api/v1/grants", "", browser.Token, f.body("grant.create", "", "human", []string{"read"}, 2))
	if w.Code != 401 && w.Code != 403 {
		t.Fatal("browser missing Origin admitted")
	}
	terminal(t, f.call("POST", "/api/v1/grants", f.native.Context().Origin, browser.Token, f.body("grant.create", "", "human", []string{"read"}, 2)), 201)
	terminal(t, f.call("DELETE", "/api/v1/grants/"+admin, "", f.owner, f.body("grant.revoke", admin, "", nil, 3)), 200)
	before := f.counts(t)
	for _, tc := range []struct {
		token  T.SecretToken
		origin string
	}{{token, ""}, {browser.Token, f.native.Context().Origin}} {
		w := f.call("POST", "/api/v1/grants", tc.origin, tc.token, body)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("revoked actor admitted retained receipt")
		}
		if f.counts(t) != before {
			t.Fatal("revoked actor effects")
		}
	}
}

func TestLostResponseOriginalLookupAndSourceIncarnation(t *testing.T) {
	f := fresh(t)
	body := f.body("grant.create", "", "automation", []string{"read"}, 0)
	// Discard the entire response after dispatch; only exact stored evidence may
	// resolve it. This test adds no lookup route or replay behavior.
	_ = f.call("POST", "/api/v1/grants", "", f.owner, body)
	i, err := T.FreezeMutation(body, contract.ResolvedRoute{Command: "grant.create", BoardID: testBoard}, contract.AuthenticatedActor{ActorID: f.native.Context().OwnerActorID})
	if err != nil {
		t.Fatal("original freeze")
	}
	base, err := outcome.Base(i)
	if err != nil {
		t.Fatal("original identity")
	}
	k := outcome.IdentityOf(base)
	before := f.counts(t)
	look := f.native.NativeLookup(testContext(t), f.owner, f.native.Context().InstallationID, f.native.Context().Origin, k)
	if look.Terminal == nil || look.Terminal.Outcome != "committed" || f.counts(t) != before {
		t.Fatal("lost response exact native lookup")
	}
	k.Fingerprint = strings.Repeat("a", 64)
	mismatch := f.native.NativeLookup(testContext(t), f.owner, f.native.Context().InstallationID, f.native.Context().Origin, k)
	if mismatch.Refused == nil || mismatch.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("lookup mismatch")
	}
	old := bytes.Replace(f.body("grant.create", "", "automation", []string{"read"}, 1), []byte(testInc), []byte(testActor), 1)
	w := f.call("POST", "/api/v1/grants", "", f.owner, old)
	if w.Code != 503 {
		t.Fatal("source incarnation outcome must stay unknown")
	}
	if _, err := T.Decode[T.KnownIdentityUnknown](w.Body.Bytes()); err != nil {
		t.Fatal("source unknown shape")
	}
	if f.counts(t) != before {
		t.Fatal("old incarnation effects")
	}
}

func TestConcurrentSameIntent(t *testing.T) {
	f := fresh(t)
	body := f.body("grant.create", "", "human", []string{"grant-admin", "read-own-outcome"}, 0)
	var group sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		group.Add(1)
		go func() { defer group.Done(); responses <- f.call("POST", "/api/v1/grants", "", f.owner, body) }()
	}
	group.Wait()
	close(responses)
	created := 0
	for w := range responses {
		o := terminal(t, w, w.Code)
		if o.Outcome != "committed" {
			t.Fatal("concurrent receipt")
		}
		if w.Code == 201 {
			created++
		} else if w.Code != 200 {
			t.Fatal("concurrent status")
		}
	}
	if created != 1 {
		t.Fatal("exactly one created response")
	}
	counts := f.counts(t)
	if counts.revision != 1 || counts.grants != 1 || counts.activity != 1 || counts.operations != 1 {
		t.Fatal("concurrent duplicate effects")
	}
}

func TestConcurrentRevokeAndOrdinaryAdmission(t *testing.T) {
	f := fresh(t)
	admin, token := f.cli(t, "human", []string{"grant-admin", "read-own-outcome"}, 0)
	createBody := f.body("grant.create", "", "human", []string{"read"}, 1)
	revokeBody := f.body("grant.revoke", admin, "", nil, 1)
	var group sync.WaitGroup
	var created, revoked *httptest.ResponseRecorder
	group.Add(2)
	go func() { defer group.Done(); created = f.call("POST", "/api/v1/grants", "", token, createBody) }()
	go func() {
		defer group.Done()
		revoked = f.call("DELETE", "/api/v1/grants/"+admin, "", f.owner, revokeBody)
	}()
	group.Wait()
	r := terminal(t, revoked, 200)
	c := f.counts(t)
	if c.revision != 2 || c.activity != 2 {
		t.Fatal("race did not serialize exactly one grant effect")
	}
	if r.Outcome == "committed" {
		if (created.Code != 401 && created.Code != 403) || c.grants != 1 || c.operations != 2 {
			t.Fatal("revocation-first admission")
		}
	} else {
		o := terminal(t, created, 201)
		if r.Outcome != "not_committed" || r.Code != "REVISION_CONFLICT" || o.Outcome != "committed" || c.grants != 2 || c.operations != 3 {
			t.Fatal("submission-first admission")
		}
	}
}

func TestUnavailablePreparationKeepsKnownIdentityUnknown(t *testing.T) {
	f := fresh(t)
	body := f.body("grant.create", "", "automation", []string{"read"}, 0)
	f.store.Fence()
	w := f.call("POST", "/api/v1/grants", "", f.owner, body)
	if w.Code != 503 {
		t.Fatal("fenced preparation status")
	}
	u, err := T.Decode[T.KnownIdentityUnknown](w.Body.Bytes())
	if err != nil || u.Outcome != "unknown" || u.ActorID != A.UUID(f.native.Context().OwnerActorID) {
		t.Fatal("known frozen uncertainty")
	}
	c := f.counts(t)
	if c.revision != 0 || c.grants != 0 || c.activity != 0 || c.operations != 0 || c.actors != 0 {
		t.Fatal("fenced preparation effects")
	}
}

func TestPinnedHTTPSGrantRoundTrip(t *testing.T) {
	f := fresh(t)
	server, err := H.Listen(testContext(t), f.native, f.handler)
	if err != nil {
		t.Fatal("synthetic HTTPS listener")
	}
	t.Cleanup(server.Close)
	configuration, err := f.native.Context().TLSConfig()
	if err != nil {
		t.Fatal("synthetic issuer pin")
	}
	transport := &http.Transport{TLSClientConfig: configuration, Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprint(f.native.Context().Port)))
	}}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	body := f.body("grant.create", "", "automation", []string{"read"}, 0)
	r, err := http.NewRequest("POST", f.native.Context().Origin+"/api/v1/grants", bytes.NewReader(body))
	if err != nil {
		t.Fatal("request construction")
	}
	r.Header.Set("Authorization", "Bearer "+string(f.owner))
	r.Header.Set("Content-Type", "application/json")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("pinned HTTPS request", errorCategory(err))
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("HTTPS response")
	}
	out, err := T.Decode[T.Terminal](raw)
	if err != nil || response.StatusCode != 201 || out.Outcome != "committed" || out.ActorID != A.UUID(f.native.Context().OwnerActorID) || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("HTTPS grant boundary")
	}
}

func errorCategory(err error) string {
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) {
		return fmt.Sprintf("certificate-invalid-reason-%d", invalid.Reason)
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("system-error-%d", uint32(errno))
	}
	category := ""
	for err != nil {
		category += fmt.Sprintf("%T ", err)
		err = errors.Unwrap(err)
	}
	return category
}

var _ http.Handler = (*Handler)(nil)
