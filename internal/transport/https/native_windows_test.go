//go:build windows && amd64 && t01_native

package https

import (
	"bytes"
	"context"
	"crypto/tls"
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
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/session"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

const board = "10000000-0000-0000-0000-000000000001"
const incarnation = "20000000-0000-0000-0000-000000000001"
const actor = "30000000-0000-0000-0000-000000000003"

type fixture struct {
	store    *S.Store
	native   *credentials.Service
	sessions *session.Service
	owner    T.SecretToken
	request  T.CodeRequest
	client   *http.Client
	server   *Server
	handler  *Handler
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
	sessions, e := session.New(store, native)
	if e != nil {
		t.Fatal("sessions unavailable")
	}
	handler, e := New(native, sessions)
	if e != nil {
		t.Fatal("handler unavailable")
	}
	f := fixture{store: store, native: native, sessions: sessions, owner: T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer ")), handler: handler}
	t.Cleanup(func() {
		if f.server != nil {
			f.server.Close()
		}
		f.clientClose()
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
	cfg, e := native.Context().TLSConfig()
	if e != nil {
		t.Fatal("pin unavailable")
	}
	transport := &http.Transport{TLSClientConfig: cfg, Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strings.Split(native.Context().Authority, ":")[1]))
	}}
	f.client = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	return f
}
func (f fixture) clientClose() {
	if f.client != nil {
		f.client.CloseIdleConnections()
	}
}
func (f *fixture) start(t *testing.T) {
	t.Helper()
	s, e := Listen(testContext(t), f.native, f.handler)
	if e != nil {
		t.Fatal("TLS listener unavailable")
	}
	f.server = s
	t.Cleanup(s.Close)
}
func (f fixture) call(t *testing.T, method, path, origin string, token T.SecretToken, body []byte, extra map[string]string) (int, []byte, http.Header) {
	t.Helper()
	r, e := http.NewRequest(method, f.native.Context().Origin+path, bytes.NewReader(body))
	if e != nil {
		t.Fatal("request construction")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+string(token))
	}
	if body != nil || method == http.MethodDelete {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		if k == "Host" {
			r.Host = v
		} else {
			r.Header.Set(k, v)
		}
	}
	response, e := f.client.Do(r)
	if e != nil {
		t.Fatal("TLS request unavailable", errorCategory(e))
	}
	defer response.Body.Close()
	b, e := io.ReadAll(response.Body)
	if e != nil {
		t.Fatal("response unavailable")
	}
	return response.StatusCode, b, response.Header
}

func errorCategory(e error) string {
	var invalid x509.CertificateInvalidError
	if errors.As(e, &invalid) {
		return fmt.Sprintf("certificate-invalid-reason-%d", invalid.Reason)
	}
	var errno syscall.Errno
	if errors.As(e, &errno) {
		return fmt.Sprintf("system-error-%d", uint32(errno))
	}
	result := ""
	for e != nil {
		result += fmt.Sprintf("%T ", e)
		e = errors.Unwrap(e)
	}
	return result
}
func (f fixture) issueCode(t *testing.T) T.CodeSecret {
	t.Helper()
	out, e := f.sessions.IssueCode(testContext(t), f.owner, f.request)
	if e != nil {
		t.Fatal("code issuance refused")
	}
	return out
}
func (f fixture) exchange(t *testing.T) T.ExchangeSecret {
	t.Helper()
	out, e := f.sessions.Exchange(testContext(t), f.issueCode(t).Code)
	if e != nil {
		t.Fatal("exchange refused")
	}
	return out
}
func raw(t *testing.T, f fixture, fn func(*sql.Conn) error) {
	t.Helper()
	ctx := testContext(t)
	l, e := f.store.Writer(ctx)
	if e != nil {
		t.Fatal("writer fixture")
	}
	e = l.Do(ctx, fn)
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		t.Fatal("fixture write")
	}
}

func TestNativeTLSRoutesAndBoundaries(t *testing.T) {
	f := fresh(t)
	f.start(t)
	origin := f.native.Context().Origin
	body, _ := json.Marshal(f.request)
	status, b, h := f.call(t, "POST", "/api/v1/session/codes", "", f.owner, body, nil)
	if status != 201 {
		t.Fatal("code route")
	}
	code, e := T.Decode[T.CodeSecret](b)
	if e != nil {
		t.Fatal("code response DTO")
	}
	if h.Get("Cache-Control") != "no-store" || h.Get("Set-Cookie") != "" || h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("secret delivery headers")
	}
	exchangeBody, e := T.MarshalEphemeral(T.ExchangeRequest{Code: code.Code})
	if e != nil {
		t.Fatal("exchange request DTO")
	}
	status, b, _ = f.call(t, "POST", "/api/v1/session/exchange", origin, "", exchangeBody, nil)
	if status != 200 {
		t.Fatal("exchange route")
	}
	session, e := T.Decode[T.ExchangeSecret](b)
	if e != nil || session.Session.ActorID != actor || session.Session.Grant.GrantID != f.request.GrantID {
		t.Fatal("exchange response DTO")
	}
	for _, tc := range []struct {
		method, path, origin, host, fetch string
		body                              []byte
		want                              int
	}{
		{"GET", "/api/v1/session", origin, "", "", nil, 200},
		{"GET", "/api/v1/session", "", "", "same-origin", nil, 200},
		{"GET", "/api/v1/session", "", "", "", nil, 403},
		{"GET", "/api/v1/session", "null", "", "", nil, 403},
		{"GET", "/api/v1/session", "https://other.localhost:47443", "", "", nil, 403},
		{"GET", "/api/v1/session", origin, "localhost:47443", "", nil, 400},
		{"GET", "/api/v1/session?code=redacted", origin, "", "", nil, 400},
		{"GET", "/api/v1/%73ession", origin, "", "", nil, 400},
		{"OPTIONS", "/api/v1/session", origin, "", "", nil, 403},
		{"POST", "/api/v1/session/exchange", "", "", "", exchangeBody, 403},
		{"DELETE", "/api/v1/session", "", "", "", nil, 403},
	} {
		got, out, _ := f.call(t, tc.method, tc.path, tc.origin, session.Token, tc.body, map[string]string{"Host": tc.host, "Sec-Fetch-Site": tc.fetch})
		if got != tc.want {
			t.Fatalf("boundary status %s %s got %d want %d", tc.method, tc.path, got, tc.want)
		}
		if got != 200 {
			if _, e := T.Decode[T.NonterminalRefusal](out); e != nil {
				t.Fatal("refusal DTO")
			}
			if bytes.Contains(out, []byte(string(session.Token))) || bytes.Contains(out, []byte("redacted")) {
				t.Fatal("secret URL echo")
			}
		}
	}
	status, _, _ = f.call(t, "POST", "/api/v1/session/exchange", origin, "", exchangeBody, nil)
	if status != 401 {
		t.Fatal("code reused")
	}
	status, _, _ = f.call(t, "POST", "/api/v1/session/codes", origin, session.Token, body, nil)
	if status != 401 {
		t.Fatal("browser bearer became native bootstrap")
	}
	status, _, _ = f.call(t, "OPTIONS", "/api/v1/session", origin, "", nil, map[string]string{"Access-Control-Request-Method": "POST"})
	if status != 403 {
		t.Fatal("unauthenticated preflight admitted")
	}
	for _, bad := range [][]byte{[]byte(`{}`), []byte(`{"installationId":null}`), append(append([]byte{}, body[:len(body)-1]...), []byte(`,"extra":true}`)...), []byte(strings.Repeat("x", 4097))} {
		status, out, _ := f.call(t, "POST", "/api/v1/session/codes", "", f.owner, bad, nil)
		if status != 400 {
			t.Fatal("nonexact session DTO accepted")
		}
		if _, e := T.Decode[T.NonterminalRefusal](out); e != nil {
			t.Fatal("invalid DTO refusal")
		}
	}
	status, _, _ = f.call(t, "POST", "/api/v1/session/codes", "", f.owner, body, map[string]string{"Content-Type": "text/plain"})
	if status != 400 {
		t.Fatal("non-JSON code write accepted")
	}
	status, _, _ = f.call(t, "DELETE", "/api/v1/session", origin, session.Token, nil, nil)
	if status != 200 {
		t.Fatal("browser revocation")
	}
	status, _, _ = f.call(t, "GET", "/api/v1/session", origin, session.Token, nil, nil)
	if status != 401 {
		t.Fatal("revoked browser authenticated")
	}
}

func TestNativeCLIDeliveryAndRestart(t *testing.T) {
	f := fresh(t)
	f.start(t)
	origin := f.native.Context().Origin
	body, _ := json.Marshal(f.request)
	status, b, _ := f.call(t, "POST", "/api/v1/session/cli-credentials", "", f.owner, body, nil)
	if status != 201 {
		t.Fatal("CLI issue route")
	}
	cli, e := T.Decode[T.CredentialSecret](b)
	if e != nil {
		t.Fatal("CLI secret DTO")
	}
	browser := f.exchange(t)
	code := f.issueCode(t)
	// Issuing browser codes never replaces the independently persisted CLI token.
	status, _, _ = f.call(t, "GET", "/api/v1/session", "", cli.Token, nil, nil)
	if status != 200 {
		t.Fatal("CLI replaced by browser code")
	}
	status, b, _ = f.call(t, "POST", "/api/v1/session/cli-credentials", "", f.owner, body, nil)
	if status != 201 {
		t.Fatal("explicit CLI replacement route")
	}
	replacementCLI, e := T.Decode[T.CredentialSecret](b)
	if e != nil {
		t.Fatal("replacement CLI DTO")
	}
	status, _, _ = f.call(t, "DELETE", "/api/v1/session", origin, cli.Token, nil, nil)
	if status != 401 {
		t.Fatal("old CLI replacement token authenticated")
	}
	status, _, _ = f.call(t, "GET", "/api/v1/session", "", replacementCLI.Token, nil, nil)
	if status != 200 {
		t.Fatal("stale token revoked replacement")
	}
	cli = replacementCLI
	f.sessions.Close()
	replacement, e := session.New(f.store, f.native)
	if e != nil {
		t.Fatal("restart session")
	}
	defer replacement.Close()
	f.handler.sessions = replacement
	if _, e = replacement.Exchange(testContext(t), code.Code); e == nil {
		t.Fatal("code survived restart")
	}
	if _, e = replacement.Current(testContext(t), "browser", browser.Token); e == nil {
		t.Fatal("browser survived restart")
	}
	if _, e = replacement.Current(testContext(t), "cli", cli.Token); e != nil {
		t.Fatal("persisted CLI lost on restart")
	}
	// Native owner has no ordinary board SessionRead fiction.
	if _, e = replacement.Current(testContext(t), "cli", f.owner); e == nil {
		t.Fatal("native owner became board credential")
	}
	status, _, _ = f.call(t, "DELETE", "/api/v1/session", origin, cli.Token, nil, nil)
	if status != 200 {
		t.Fatal("exact CLI self revoke")
	}
	if _, e = replacement.Current(testContext(t), "cli", cli.Token); e == nil {
		t.Fatal("revoked CLI authenticated")
	}
}

func TestNativeSingleUseRacesAndCurrentRights(t *testing.T) {
	f := fresh(t)
	code := f.issueCode(t)
	var successes atomic.Int32
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, e := f.sessions.Exchange(testContext(t), code.Code); e == nil {
				successes.Add(1)
			}
		}()
	}
	workers.Wait()
	if successes.Load() != 1 {
		t.Fatal("single-use race")
	}
	browser := f.exchange(t)
	raw(t, f, func(c *sql.Conn) error {
		_, e := c.ExecContext(testContext(t), "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", incarnation, string(f.request.GrantID))
		return e
	})
	view, e := f.sessions.Current(testContext(t), "browser", browser.Token)
	if e != nil || len(view.Grant.Rights) != 1 || view.Grant.Rights[0] != "read-own-outcome" {
		t.Fatal("rights cached/widened")
	}
	raw(t, f, func(c *sql.Conn) error {
		_, e := c.ExecContext(testContext(t), "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", incarnation, string(f.request.GrantID))
		return e
	})
	if _, e = f.sessions.Current(testContext(t), "browser", browser.Token); e == nil {
		t.Fatal("revoked grant authenticated")
	}
	if _, e = f.sessions.IssueCode(testContext(t), f.owner, f.request); e == nil {
		t.Fatal("code for disabled grant")
	}
	f.native.Close()
	if _, e = f.sessions.Current(testContext(t), "browser", browser.Token); e == nil {
		t.Fatal("closed native lifetime authenticated")
	}
}

func TestNativeExclusiveFamiliesAndCertificate(t *testing.T) {
	f := fresh(t)
	f.start(t)
	c := f.native.Context()
	port := strings.Split(c.Authority, ":")[1]
	for _, family := range []struct{ network, address string }{{"tcp4", "127.0.0.1"}, {"tcp6", "::1"}} {
		if l, e := net.Listen(family.network, net.JoinHostPort(family.address, port)); e == nil {
			l.Close()
			t.Fatal("listener not exclusive")
		}
		cfg, e := c.TLSConfig()
		if e != nil {
			t.Fatal("pin config")
		}
		conn, e := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, family.network, net.JoinHostPort(family.address, port), cfg)
		if e != nil {
			t.Fatal("TLS loopback family", errorCategory(e))
		}
		if conn.ConnectionState().Version < tls.VersionTLS12 {
			t.Fatal("TLS downgrade")
		}
		conn.Close()
	}
	cfg, e := c.TLSConfig()
	if e != nil {
		t.Fatal("pin config")
	}
	cfg.ServerName = "other.localhost"
	conn, e := tls.Dial("tcp4", net.JoinHostPort("127.0.0.1", port), cfg)
	if e == nil {
		conn.Close()
		t.Fatal("wrong certificate hostname accepted")
	}
	if second, e := Listen(testContext(t), f.native, f.handler); e == nil {
		second.Close()
		t.Fatal("second listener admitted")
	}
}

func TestNativeDedicatedAdapterHasNoHeaderPrincipal(t *testing.T) {
	f := fresh(t)
	called := false
	next := f.handler.Native(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, _, ok := Credential(r.Context()); ok {
			t.Fatal("native wrapper manufactured authenticated principal")
		}
		if result := f.native.Issue(r.Context(), T.SecretToken(strings.Repeat("A", 43)), f.request); result.Status == credentials.Confirmed {
			t.Fatal("header shape bypassed native provider")
		}
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("GET", f.native.Context().Origin+"/api/v1/boards/"+board+"/operations/50000000-0000-0000-0000-000000000001?incarnation="+incarnation+"&fingerprintVersion=1&fingerprint="+strings.Repeat("0", 64), nil)
	r.URL.Scheme = ""
	r.URL.Host = ""
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43))
	w := httptest.NewRecorder()
	next.ServeHTTP(w, r)
	if !called || w.Code != 204 {
		t.Fatal("native adapter boundary swallowed exact lookup query")
	}
}
