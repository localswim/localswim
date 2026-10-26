//go:build windows && amd64 && p01_native

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

const testInc = "20000000-0000-0000-0000-000000000001"
const testActor = "30000000-0000-0000-0000-000000000001"

type nativeFixture struct {
	ctx          context.Context
	selected     sourceSelection
	manager      *setup.Manager
	store        *S.Store
	native       *credentials.Service
	sessions     *session.Service
	owner, token T.SecretToken
	grant        string
	secret       T.CredentialSecret
	handler      http.Handler
}

func privateFixture(t *testing.T, port int) *nativeFixture {
	t.Helper()
	base := os.Getenv("P01_TEST_ROOT")
	if !filepath.IsAbs(base) {
		t.Fatal("private fixture root required")
	}
	root, e := os.MkdirTemp(base, "composition-")
	if e != nil {
		t.Fatal("fixture unavailable")
	}
	installation := filepath.Join(root, "installation")
	t.Setenv("P01_NATIVE_TEST_ROOT", installation)
	// This symbol exists only in the separately compiled private-root overlay.
	if checked, e := setup.P01QualificationRoot(); e != nil || checked != installation {
		t.Fatal("private root sentinel required")
	}
	m, e := setup.New(nil)
	if e != nil || m.Init(&port).Exit != 0 {
		t.Fatal("synthetic setup unavailable")
	}
	owner, e := m.LoadNativeOwner()
	if e != nil {
		t.Fatal("synthetic owner unavailable")
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal("ACL account unavailable")
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal("ACL unavailable")
	}
	dir := filepath.Join(root, "board")
	if windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}) != nil {
		t.Fatal("private board unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	f := &nativeFixture{ctx: ctx, manager: m, selected: sourceSelection{Directory: dir, Identity: schema.Identity{BoardID: testBoard, Incarnation: testInc}, InstallationID: owner.Context.InstallationID}, owner: T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer "))}
	f.store, e = S.Create(ctx, dir, f.selected.Identity)
	if e != nil {
		t.Fatal("synthetic storage unavailable")
	}
	f.native, e = credentials.SourceRoot(f.store)
	if e != nil {
		t.Fatal("native lifetime unavailable")
	}
	f.sessions, e = session.New(f.store, f.native)
	if e != nil {
		t.Fatal("session lifetime unavailable")
	}
	f.handler, e = routes(f.store, f.native, f.sessions, http.NotFoundHandler())
	if e != nil {
		t.Fatal("adapter registration unavailable")
	}
	t.Cleanup(func() { f.close(t) })
	return f
}

func (f *nativeFixture) close(t *testing.T) {
	if f.sessions != nil {
		f.sessions.Close()
		f.sessions = nil
	}
	if f.native != nil {
		f.native.Close()
		f.native = nil
	}
	if f.store != nil {
		if f.store.Close() != nil {
			t.Error("source cleanup failed")
		}
		f.store = nil
	}
}

func (f *nativeFixture) credential(t *testing.T) {
	t.Helper()
	wire, e := json.Marshal(map[string]any{"boardId": testBoard, "incarnation": testInc, "opId": "50000000-0000-0000-0000-000000000001", "target": nil, "expectedRevision": 0, "expectedCardRevision": nil, "input": map[string]any{"granteeActorId": testActor, "kind": "automation", "rights": []string{"export", "read", "read-own-outcome"}}})
	if e != nil {
		t.Fatal("fixture shape")
	}
	i, e := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "grant.create", BoardID: testBoard}, contract.AuthenticatedActor{ActorID: f.native.Context().OwnerActorID})
	if e != nil {
		t.Fatal("fixture intent")
	}
	out := f.native.SubmitNative(f.ctx, f.owner, i, grants.Route{BoardID: testBoard, Incarnation: testInc})
	if out.Result == nil || out.Result.Terminal == nil || out.Result.Terminal.Outcome != "committed" {
		t.Fatal("synthetic grant unavailable")
	}
	var grant string
	for _, r := range out.Result.Terminal.ResultRefs {
		if r.Kind == "grant" {
			grant = string(r.ID)
		}
	}
	f.grant = grant
	issued := f.native.Issue(f.ctx, f.owner, T.CodeRequest{InstallationID: T.UUIDv4(f.native.Context().InstallationID), BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), GrantID: A.UUID(grant)})
	raw, e := issued.MarshalEphemeral()
	if e != nil {
		t.Fatal("synthetic credential unavailable")
	}
	secret, e := T.Decode[T.CredentialSecret](raw)
	if e != nil {
		t.Fatal("synthetic credential invalid")
	}
	f.token = secret.Token
	f.secret = secret
}

func (f *nativeFixture) request(method, path, body string, token T.SecretToken) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, f.native.Context().Origin+path, strings.NewReader(body))
	r.URL.Scheme = ""
	r.URL.Host = ""
	r.TLS = &tls.ConnectionState{}
	r.Host = f.native.Context().Authority
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+string(token))
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestNativeOwnedAdapterRegistrationAndDenial(t *testing.T) {
	f := privateFixture(t, 47443)
	f.credential(t)
	base := "/api/v1/boards/" + testBoard
	for _, q := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/v1/session", "", 200},
		{"GET", base, "", 200},
		{"GET", base + "/cards/" + testCard, "", 404},
		{"GET", base + "/cards/" + testCard + "/history", "", 404},
		{"PUT", base, "", 405},
		{"POST", base + "/cards", "{", 400},
		{"POST", base + "/cards/" + testCard + "/comments", "{", 400},
		{"POST", base + "/cards/" + testCard + "/link-add", "{", 400},
		{"POST", base + "/cards/" + testCard + "/link-remove", "{", 400},
		{"POST", base + "/cards/" + testCard + "/review-handoff", "{", 400},
		{"POST", base + "/cards/" + testCard + "/approve", "{", 400},
		{"PATCH", base + "/cards/" + testCard, "{", 400},
		{"POST", base + "/recovery/preview", "{", 400},
		{"POST", base + "/recovery/confirm", "{", 400},
		{"POST", base + "/operations/" + testCard, "", 405},
		{"GET", base + "/cards/" + testCard + "/attachments", "", 405},
		{"POST", base + "/attachments/" + testCard, "", 405},
		{"GET", base + "/exports", "", 405},
		{"POST", base + "/exports", "{", 400},
		{"GET", "/api/v1/grants", "", 405},
		{"POST", "/api/v1/grants", "{", 400},
		{"GET", "/api/v1/grants/" + testCard, "", 405},
		{"DELETE", "/api/v1/grants/" + testCard, "{", 400},
		{"GET", "/api/v1/session/codes", "", 405},
		{"POST", "/api/v1/session/codes", "{", 400},
		{"GET", "/api/v1/session/cli-credentials", "", 405},
		{"POST", "/api/v1/session/cli-credentials", "{", 400},
		{"POST", "/api/v1/session", "", 405},
		{"GET", "/api/v1/session?unexpected=1", "", 400},
		{"GET", base + "?unexpected=1", "", 400},
		{"GET", base + "/attachments/" + testCard + "?unexpected=1", "", 400},
		{"POST", base + "/exports?unexpected=1", "{}", 400},
		{"POST", "/api/v1/grants?unexpected=1", "{}", 400},
		{"GET", base + "/unknown", "", 404},
		{"GET", base + "/cards/../exports", "", 404},
		{"GET", base + "/exports/", "", 404},
	} {
		w := f.request(q.method, q.path, q.body, f.token)
		if w.Code != q.want {
			t.Errorf("%s %s: got %d want %d", q.method, q.path, w.Code, q.want)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("shared boundary missing for %s", q.path)
		}
		denied := f.request(q.method, q.path, q.body, "")
		denialStatus := 401
		if strings.HasPrefix(q.path, "/api/v1/session?") {
			denialStatus = 400
		}
		if denied.Code != denialStatus {
			t.Errorf("missing credential allowed %s: %d", q.path, denied.Code)
		}
	}
	// Exchange is the sole unauthenticated browser entry, still requiring Origin.
	if w := f.request("POST", "/api/v1/session/exchange", "{}", ""); w.Code != 403 {
		t.Fatal("exchange missing-Origin boundary")
	}
	for _, path := range []string{base, base + "/exports", base + "/attachments/" + testCard, "/api/v1/grants", "/api/v1/session"} {
		r := httptest.NewRequest("GET", path, nil)
		r.TLS = &tls.ConnectionState{}
		r.Host = f.native.Context().Authority
		r.Header.Set("Origin", "https://foreign.invalid")
		r.Header.Set("Authorization", "Bearer "+string(f.token))
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("foreign origin allowed %s", path)
		}
	}
	request := T.CodeRequest{InstallationID: T.UUIDv4(f.native.Context().InstallationID), BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), GrantID: A.UUID(f.grant)}
	wire, e := json.Marshal(request)
	if e != nil {
		t.Fatal("code fixture shape")
	}
	w := f.request("POST", "/api/v1/session/codes", string(wire), f.owner)
	if w.Code != 201 {
		t.Fatal("mounted code issue failed")
	}
	code, e := T.Decode[T.CodeSecret](w.Body.Bytes())
	if e != nil {
		t.Fatal("mounted code response invalid")
	}
	exchange, e := T.MarshalEphemeral(T.ExchangeRequest{Code: code.Code})
	if e != nil {
		t.Fatal("exchange fixture shape")
	}
	callBrowser := func(method, path string, body []byte, token T.SecretToken) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.TLS = &tls.ConnectionState{}
		r.Host = f.native.Context().Authority
		r.Header.Set("Origin", f.native.Context().Origin)
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+string(token))
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	w = callBrowser("POST", "/api/v1/session/exchange", exchange, "")
	if w.Code != 200 {
		t.Fatal("mounted code exchange failed")
	}
	browser, e := T.Decode[T.ExchangeSecret](w.Body.Bytes())
	if e != nil {
		t.Fatal("mounted exchange invalid")
	}
	if callBrowser("GET", "/api/v1/session", nil, browser.Token).Code != 200 {
		t.Fatal("mounted browser current failed")
	}
	if callBrowser("DELETE", "/api/v1/session", nil, browser.Token).Code != 200 {
		t.Fatal("mounted browser revoke failed")
	}
	if callBrowser("GET", "/api/v1/session", nil, browser.Token).Code != 401 {
		t.Fatal("revoked browser retained authority")
	}
	w = f.request("POST", "/api/v1/session/cli-credentials", string(wire), f.owner)
	if w.Code != 201 {
		t.Fatal("mounted credential issue failed")
	}
	credential, e := T.Decode[T.CredentialSecret](w.Body.Bytes())
	if e != nil {
		t.Fatal("mounted credential invalid")
	}
	if f.request("GET", "/api/v1/session", "", credential.Token).Code != 200 {
		t.Fatal("mounted credential current failed")
	}
	if f.request("DELETE", "/api/v1/session", "", credential.Token).Code != 200 {
		t.Fatal("mounted credential revoke failed")
	}
	if f.request("GET", "/api/v1/session", "", credential.Token).Code != 403 {
		t.Fatal("revoked CLI credential retained authority")
	}
	// Source fencing must preserve read-only observation but refuse new mutation
	// ownership; composition never resets or reopens a fenced source implicitly.
	f.store.Fence()
	if _, e := f.store.Writer(f.ctx); e == nil {
		t.Fatal("source fence bypass")
	}
}

func TestNativePrivatePinnedLifecycle(t *testing.T) {
	for _, port := range []int{47443, 48443} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			f := privateFixture(t, port)
			f.close(t)
			ctx, cancel := context.WithCancel(f.ctx)
			s, e := startService(ctx, f.selected, http.NotFoundHandler())
			if e != nil {
				t.Fatal("composition startup")
			}
			t.Cleanup(func() { _ = s.close() })
			if _, e := f.manager.AcquireServer(); e == nil {
				t.Fatal("native lifetime exclusion bypass")
			}
			if _, e := S.Existing(f.ctx, f.selected.Directory, f.selected.Identity); e == nil {
				t.Fatal("source exclusion bypass")
			}
			cfg, e := s.native.Context().TLSConfig()
			if e != nil {
				t.Fatal("pin unavailable")
			}
			transport := &http.Transport{TLSClientConfig: cfg, Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", port))
			}}
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			r, e := client.Get(s.native.Context().Origin + "/api/v1/session")
			if e != nil {
				t.Fatal("pinned TLS unavailable")
			}
			r.Body.Close()
			if r.StatusCode != 401 {
				t.Fatal("native boundary absent")
			}
			// Native startup never prepares actor attribution, grants or credentials.
			lease, e := s.store.Reader(f.ctx)
			if e != nil {
				t.Fatal("reader unavailable")
			}
			if e = lease.Do(f.ctx, func(c *sql.Conn) error {
				for _, table := range []string{"actor", "grant_fact", "grant_record", "cli_credential"} {
					var count int
					if err := c.QueryRowContext(f.ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
						return err
					}
					if count != 0 {
						return errComposition
					}
				}
				return nil
			}); e != nil {
				t.Fatal("startup changed authority")
			}
			if lease.Close() != nil {
				t.Fatal("source reader cleanup")
			}
			cancel()
			if e = s.close(); e != nil {
				t.Fatal("composition shutdown")
			}
			if s.native.Active() {
				t.Fatal("native source remained active")
			}
			leaseNative, e := f.manager.AcquireServer()
			if e != nil {
				t.Fatal("native lifetime not released")
			}
			leaseNative.Close()
			restarted, e := startService(f.ctx, f.selected, http.NotFoundHandler())
			if e != nil {
				t.Fatal("composition restart")
			}
			// Actual transport failure must complete the composition and retain
			// an unavailable result; listener loss cannot leave authority armed.
			restarted.server.Close()
			select {
			case <-restarted.done:
			case <-time.After(15 * time.Second):
				t.Fatal("transport failure did not stop composition")
			}
			if restarted.close() == nil || restarted.native.Active() {
				t.Fatal("transport failure was reported healthy")
			}
		})
	}
}
