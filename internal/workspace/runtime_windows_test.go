//go:build windows && amd64

package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
	"golang.org/x/sys/windows"
)

func testBase(t *testing.T) string {
	t.Helper()
	if base := os.Getenv("L02_TEST_ROOT"); base != "" {
		p, e := os.MkdirTemp(base, "ws-")
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	return t.TempDir()
}
func testPort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}
func testStart(t *testing.T, root string, port int) *Runtime {
	t.Helper()
	r, e := Start(context.Background(), Options{DataDir: root, Port: port})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := r.Close(); e != nil {
			t.Error(e)
		}
	})
	return r
}
func testHTTP(t *testing.T, r *Runtime, method, path string, value any, browser *http.Client) (int, []byte) {
	t.Helper()
	var body []byte
	var e error
	if value != nil {
		body, e = json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
	}
	q, e := http.NewRequest(method, r.Origin()+path, bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	client := browser
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
		defer client.CloseIdleConnections()
		info, e := ReadClient(r.files.Root())
		if e != nil {
			t.Fatal(e)
		}
		q.Header.Set("Authorization", "Bearer "+string(info.Token))
	} else {
		q.Header.Set("X-Localswim-Client", "browser")
		q.Header.Set("Origin", r.Origin())
		q.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if value != nil {
		q.Header.Set("Content-Type", "application/json")
	}
	out, e := client.Do(q)
	if e != nil {
		t.Fatal(e)
	}
	defer out.Body.Close()
	b, e := io.ReadAll(out.Body)
	if e != nil {
		t.Fatal(e)
	}
	return out.StatusCode, b
}
func projectRequest(root string, revision uint64) T.WorkspaceProjectRequest {
	return T.WorkspaceProjectRequest{APIVersion: 1, ExpectedConfigRevision: A.Positive(revision), Key: "harbor", Label: "Harbor", Root: T.WorkspacePath(root)}
}
func createFixture(t *testing.T, r *Runtime, project string) T.WorkspaceBoardRequest {
	t.Helper()
	code, b := testHTTP(t, r, "POST", "/api/v1/workspace/projects", projectRequest(project, 1), nil)
	if code != 200 {
		t.Fatalf("project status %d: %s", code, b)
	}
	v := T.WorkspaceBoardRequest{APIVersion: 1, ExpectedConfigRevision: 2, ProjectKey: "harbor", BoardKey: "main", Label: "Main", DataDir: T.WorkspacePath(filepath.Join(project, "main")), Action: "create"}
	code, b = testHTTP(t, r, "POST", "/api/v1/workspace/boards", v, nil)
	out, e := T.Decode[T.WorkspaceControlResult](b)
	if code != 200 || e != nil || out.Status != "registered" || out.ConfigRevision != 3 {
		t.Fatalf("create status %d decode %v", code, e)
	}
	return v
}
func TestWorkspaceHTTPCreateResolveCASAndReopen(t *testing.T) {
	base := testBase(t)
	root := filepath.Join(base, "w")
	port := testPort(t)
	r := testStart(t, root, port)
	v := createFixture(t, r, filepath.Join(base, "p"))
	info, e := ReadClient(root)
	if e != nil || info.WorkspaceID != string(r.snapshot().WorkspaceID) {
		t.Fatal("workspace rendezvous")
	}
	code, b := testHTTP(t, r, "GET", "/api/v1/workspace/projects/harbor/boards/main", nil, nil)
	resolved, e := T.Decode[T.WorkspaceResolve](b)
	if code != 200 || e != nil || resolved.Member.Availability != "ready" || resolved.Member.Identity == nil {
		t.Fatalf("resolve %d %v", code, e)
	}
	if strings.Contains(string(b), base) || strings.Contains(string(b), "Token") {
		t.Fatal("private control data disclosed")
	}
	code, b = testHTTP(t, r, "POST", "/api/v1/workspace/boards", v, nil)
	conflict, e := T.Decode[T.WorkspaceControlError](b)
	if code != 412 || e != nil || conflict.Error.Code != "CONFIG_CHANGED" || conflict.ConfigRevision != 3 {
		t.Fatal("stale config not refused")
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "workspace-service.json")); !os.IsNotExist(e) {
		t.Fatal("owned host rendezvous retained")
	}
	reopened := testStart(t, root, 0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, b = testHTTP(t, reopened, "GET", "/api/v1/workspace/projects/harbor/boards/main", nil, nil)
		now, e := T.Decode[T.WorkspaceResolve](b)
		if code == 200 && e == nil && now.Member.Availability == "ready" {
			if *now.Member.Identity != *resolved.Member.Identity {
				t.Fatal("durable identity changed")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reopen never ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, e = Start(context.Background(), Options{DataDir: root, Port: testPort(t)}); e == nil {
		t.Fatal("second workspace owner accepted")
	}
}
func TestWorkspaceHTTPPublicationUncertaintyRetainsInitializedStore(t *testing.T) {
	for _, point := range []string{"before-config-publication", "after-config-publication"} {
		t.Run(point, func(t *testing.T) {
			base := testBase(t)
			r := testStart(t, filepath.Join(base, "w"), testPort(t))
			project := filepath.Join(base, "p")
			code, _ := testHTTP(t, r, "POST", "/api/v1/workspace/projects", projectRequest(project, 1), nil)
			if code != 200 {
				t.Fatal("project")
			}
			r.publishFault = func(label string) error {
				if label == point {
					return ErrUnavailable
				}
				return nil
			}
			request := T.WorkspaceBoardRequest{APIVersion: 1, ExpectedConfigRevision: 2, ProjectKey: "harbor", BoardKey: "main", Label: "Main", DataDir: T.WorkspacePath(filepath.Join(project, "main")), Action: "create"}
			code, b := testHTTP(t, r, "POST", "/api/v1/workspace/boards", request, nil)
			out, e := T.Decode[T.WorkspaceControlResult](b)
			if code != 200 || e != nil || out.Status != "UNKNOWN" || out.Identity == nil {
				t.Fatal("uncertain publication falsely confirmed")
			}
			id := *out.Identity
			r.publishFault = nil
			status := T.WorkspaceStatusRequest{APIVersion: 1, ProjectKey: "harbor", BoardKey: "main", DataDir: &request.DataDir, ExpectedIdentity: &id}
			code, b = testHTTP(t, r, "POST", "/api/v1/workspace/boards/status", status, nil)
			out, e = T.Decode[T.WorkspaceControlResult](b)
			want := T.WorkspaceRegistrationStatus("initialized-unregistered")
			if point == "after-config-publication" {
				want = "registered"
			}
			if code != 200 || e != nil || out.Status != want || out.Identity == nil || *out.Identity != id {
				t.Fatal("explicit status did not resolve registration")
			}
			// A deliberate register never repeats initialization or changes the tuple.
			if want == "initialized-unregistered" {
				request.Action = "register"
				request.ExpectedIdentity = &id
				code, b = testHTTP(t, r, "POST", "/api/v1/workspace/boards", request, nil)
				out, e = T.Decode[T.WorkspaceControlResult](b)
				if code != 200 || e != nil || out.Status != "registered" || *out.Identity != id {
					t.Fatal("explicit registration failed")
				}
			}
		})
	}
}
func TestWorkspaceHTTPReadGrantDisclosureAndBrowserControlRefusal(t *testing.T) {
	for _, kind := range []string{"automation", "human"} {
		t.Run(kind, func(t *testing.T) {
			base := testBase(t)
			root := filepath.Join(base, "w")
			r := testStart(t, root, testPort(t))
			v := createFixture(t, r, filepath.Join(base, "p"))
			if e := r.Close(); e != nil {
				t.Fatal(e)
			}
			// Deliberate technical fixture: revoke only the configured read right while
			// the store is offline. This is not a product seeding or user-journey path.
			db, e := sql.Open("sqlite", filepath.Join(string(v.DataDir), "board", "board.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			_, e = db.Exec("DELETE FROM grant_right WHERE right_name='read' AND grant_id IN (SELECT g.grant_id FROM grant_record g JOIN actor a ON a.actor_id=g.actor_id AND a.incarnation=g.incarnation WHERE a.kind=?)", kind)
			if e != nil {
				_ = db.Close()
				t.Fatal(e)
			}
			if e = db.Close(); e != nil {
				t.Fatal(e)
			}
			r = testStart(t, root, 0)
			jar, _ := cookiejar.New(nil)
			browser := &http.Client{Jar: jar, Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
			defer browser.CloseIdleConnections()
			code, _ := testHTTP(t, r, "POST", "/api/v1/workspace/bootstrap", map[string]any{}, browser)
			if code != 200 {
				t.Fatal("workspace browser bootstrap")
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				r.mu.Lock()
				s := r.states["harbor/main"]
				ready := s != nil && s.engine != nil
				r.mu.Unlock()
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("engine unavailable")
				}
				time.Sleep(20 * time.Millisecond)
			}
			for _, channel := range []string{"cli", "browser"} {
				var client *http.Client
				if channel == "browser" {
					client = browser
				}
				code, b := testHTTP(t, r, "GET", "/api/v1/workspace/projects/harbor/boards/main", nil, client)
				out, e := T.Decode[T.WorkspaceResolve](b)
				denied := (kind == "human" && channel == "browser") || (kind == "automation" && channel == "cli")
				if code != 200 || e != nil {
					t.Fatalf("resolve %d %v", code, e)
				}
				if denied {
					if out.Member.Availability != "unavailable" || out.Member.Identity != nil || out.Member.MemberEpoch != nil {
						t.Fatal("revoked grant disclosed identity")
					}
				} else if out.Member.Availability != "ready" {
					t.Fatal("other channel affected")
				}
			}
			code, _ = testHTTP(t, r, "POST", "/api/v1/workspace/projects", projectRequest(filepath.Join(base, "x"), 3), browser)
			if code != 403 {
				t.Fatal("browser control mutation accepted")
			}
		})
	}
}

func TestWorkspaceHTTPIndependentMissingBusyAndReplacedMembers(t *testing.T) {
	for _, want := range []T.WorkspaceAvailability{"missing", "busy", "identity_conflict"} {
		t.Run(string(want), func(t *testing.T) {
			base := testBase(t)
			root := filepath.Join(base, "w")
			r := testStart(t, root, testPort(t))
			v := createFixture(t, r, filepath.Join(base, "p"))
			second := v
			second.ExpectedConfigRevision = 3
			second.BoardKey = "second"
			second.DataDir = T.WorkspacePath(filepath.Join(base, "p", "second"))
			code, b := testHTTP(t, r, "POST", "/api/v1/workspace/boards", second, nil)
			if code != 200 {
				t.Fatalf("second member %d %s", code, b)
			}
			if e := r.Close(); e != nil {
				t.Fatal(e)
			}
			switch want {
			case "missing":
				if e := os.Rename(string(v.DataDir), string(v.DataDir)+"-held"); e != nil {
					t.Fatal(e)
				}
			case "busy":
				competing, e := L.Start(context.Background(), L.Options{DataDir: string(v.DataDir)})
				if e != nil {
					t.Fatal(e)
				}
				defer competing.Close()
			case "identity_conflict":
				if e := os.Rename(string(v.DataDir), string(v.DataDir)+"-held"); e != nil {
					t.Fatal(e)
				}
				replacement, e := L.Start(context.Background(), L.Options{DataDir: string(v.DataDir), Port: testPort(t)})
				if e != nil {
					t.Fatal(e)
				}
				if e = replacement.Close(); e != nil {
					t.Fatal(e)
				}
			}
			r = testStart(t, root, 0)
			deadline := time.Now().Add(10 * time.Second)
			for {
				code, b = testHTTP(t, r, "GET", "/api/v1/workspace/boards", nil, nil)
				out, e := T.Decode[T.WorkspaceBoards](b)
				if code != 200 || e != nil {
					t.Fatalf("list %d %v", code, e)
				}
				states := map[T.WorkspaceKey]T.WorkspaceAvailability{}
				for _, m := range out.Boards {
					states[m.BoardKey] = m.Availability
					if m.BoardKey == "main" && (m.Identity != nil || m.MemberEpoch != nil) {
						t.Fatal("failed member disclosed ready identity")
					}
				}
				if states["main"] == want && states["second"] == "ready" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("independent members wanted %s got %v", want, states)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

type diagnosticHost struct {
	runtimeHost
	gate    chan struct{}
	pending bool
}

func (h *diagnosticHost) Done() <-chan struct{} { return h.gate }
func (h *diagnosticHost) Close() error {
	if h.pending {
		return ErrUnavailable
	}
	e := h.runtimeHost.Close()
	select {
	case <-h.runtimeHost.Done():
		select {
		case <-h.gate:
		default:
			close(h.gate)
		}
		return ErrUnavailable
	default:
		return e
	}
}
func TestWorkspaceCloseDistinguishesDiagnosticFromOwnedCleanup(t *testing.T) {
	base := testBase(t)
	root := filepath.Join(base, "w")
	r, e := Start(context.Background(), Options{DataDir: root, Port: testPort(t)})
	if e != nil {
		t.Fatal(e)
	}
	h := &diagnosticHost{runtimeHost: r.host, gate: make(chan struct{}), pending: true}
	r.host = h
	if e = r.Close(); e == nil {
		t.Fatal("pending close falsely succeeded")
	}
	select {
	case <-r.Done():
		t.Fatal("pending cleanup completed")
	default:
	}
	if f, e := L.OpenWorkspaceFiles(root, false); e == nil {
		_ = f.Close()
		t.Fatal("ownership released before cleanup")
	}
	h.pending = false
	if e = r.Close(); e == nil {
		t.Fatal("completed shutdown diagnostic lost")
	}
	select {
	case <-r.Done():
	default:
		t.Fatal("completed host diagnostic prevented registry release")
	}
	if _, e = os.Stat(filepath.Join(root, "workspace-service.json")); !os.IsNotExist(e) {
		t.Fatal("own rendezvous retained after completed cleanup")
	}
	f, e := L.OpenWorkspaceFiles(root, false)
	if e != nil {
		t.Fatal("completed diagnostic retained ownership")
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestWorkspaceSlowControlDoesNotSerializeHealthyWork(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "w"), testPort(t))
	v := createFixture(t, r, filepath.Join(base, "p"))
	entered, release := make(chan struct{}), make(chan struct{})
	var claimed atomic.Bool
	r.publishFault = func(label string) error {
		if label == "before-config-publication" && claimed.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return nil
	}
	request := v
	request.ExpectedConfigRevision = 3
	request.BoardKey = "slow"
	request.DataDir = T.WorkspacePath(filepath.Join(base, "p", "slow"))
	delayed := make(chan T.WorkspaceControlResult, 1)
	go func() {
		code, b := testHTTP(t, r, "POST", "/api/v1/workspace/boards", request, nil)
		out, e := T.Decode[T.WorkspaceControlResult](b)
		if code != 200 || e != nil {
			t.Errorf("delayed control %d %v", code, e)
		}
		delayed <- out
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("fault seam not reached")
	}
	// The labeled delay stands in for slow native initialization/control work;
	// ordinary loopback HTTP requests and native CAS remain real.
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		code, _ := testHTTP(t, r, "GET", "/api/v1/workspace/projects", nil, nil)
		if code != 200 {
			t.Error("project read blocked")
		}
		status := T.WorkspaceStatusRequest{APIVersion: 1, ProjectKey: "harbor", BoardKey: "main"}
		code, _ = testHTTP(t, r, "POST", "/api/v1/workspace/boards/status", status, nil)
		if code != 200 {
			t.Error("status blocked")
		}
		code, b := testHTTP(t, r, "GET", "/api/v1/workspace/projects/harbor/boards/main", nil, nil)
		out, e := T.Decode[T.WorkspaceResolve](b)
		if code != 200 || e != nil || out.Member.Availability != "ready" {
			t.Error("selected member blocked")
		}
		p := projectRequest(filepath.Join(base, "other"), 3)
		p.Key = "orchard"
		code, _ = testHTTP(t, r, "POST", "/api/v1/workspace/projects", p, nil)
		if code != 200 {
			t.Error("independent project control blocked")
		}
	}()
	select {
	case <-completed:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("slow control serialized healthy work")
	}
	close(release)
	var out T.WorkspaceControlResult
	select {
	case out = <-delayed:
	case <-time.After(10 * time.Second):
		t.Fatal("delayed control did not end")
	}
	if out.Status != "UNKNOWN" || out.Identity == nil {
		t.Fatal("stale publication falsely confirmed")
	}
	r.publishFault = nil
	status := T.WorkspaceStatusRequest{APIVersion: 1, ProjectKey: "harbor", BoardKey: "slow", DataDir: &request.DataDir, ExpectedIdentity: out.Identity}
	code, b := testHTTP(t, r, "POST", "/api/v1/workspace/boards/status", status, nil)
	result, e := T.Decode[T.WorkspaceControlResult](b)
	if code != 200 || e != nil || result.Status != "initialized-unregistered" || result.ConfigRevision != 4 {
		t.Fatal("stale initialization not retained truthfully")
	}
}

type retainedFileCleanup struct {
	h        windows.Handle
	failOnce bool
}

func (c *retainedFileCleanup) Close() error {
	if c.failOnce {
		c.failOnce = false
		return ErrUnavailable
	}
	if c.h == 0 {
		return nil
	}
	if e := windows.CloseHandle(c.h); e != nil {
		return e
	}
	c.h = 0
	return nil
}
func TestWorkspaceCloseRetriesRetainedFileBeforeRendezvousRemoval(t *testing.T) {
	base := testBase(t)
	root := filepath.Join(base, "w")
	r, e := Start(context.Background(), Options{DataDir: root, Port: testPort(t)})
	if e != nil {
		t.Fatal(e)
	}
	// Labeled native cleanup fixture: an unresolved subordinate read owns the
	// service file without delete sharing. Cleanup must precede another removal.
	path, e := windows.UTF16PtrFromString(filepath.Join(root, "workspace-service.json"))
	if e != nil {
		t.Fatal(e)
	}
	handle, e := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	cleanup := &retainedFileCleanup{h: handle, failOnce: true}
	r.cleanups = append(r.cleanups, cleanup)
	if e = r.Close(); e == nil {
		t.Fatal("unresolved subordinate cleanup falsely succeeded")
	}
	select {
	case <-r.Done():
		t.Fatal("ownership released before subordinate cleanup")
	default:
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-r.Done():
	default:
		t.Fatal("retry did not complete")
	}
	if _, e = os.Stat(filepath.Join(root, "workspace-service.json")); !os.IsNotExist(e) {
		t.Fatal("retry did not remove own rendezvous")
	}
}
