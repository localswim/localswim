//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/assets"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func workspaceTest(t *testing.T, workspace http.Handler) (*WorkspaceHost, *Runtime, *Runtime, string, string) {
	t.Helper()
	a, e := assets.Handler()
	if e != nil {
		t.Fatal(e)
	}
	h, e := StartWorkspaceHost(context.Background(), WorkspaceHostOptions{WorkspaceID: uuid(), Port: freePort(t), Assets: a, Workspace: workspace})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := h.Close(); e != nil {
			t.Error(e)
		}
	})
	base := testRoot(t)
	rootA, rootB := filepath.Join(base, "a"), filepath.Join(base, "b")
	port := freePort(t)
	runtimes := make([]*Runtime, 0, 2)
	for _, root := range []string{rootA, rootB} {
		r, e := OpenEngine(context.Background(), EngineOptions{DataDir: root, Port: port, Origin: h.Origin(), WorkspaceID: h.id})
		if e != nil {
			t.Fatal(e)
		}
		if e := h.Attach(r); e != nil {
			_ = r.Close()
			t.Fatal(e)
		}
		runtimes = append(runtimes, r)
	}
	return h, runtimes[0], runtimes[1], rootA, rootB
}

func workspaceBootstrapTest(t *testing.T, r *Runtime) *http.Cookie {
	t.Helper()
	b, _ := json.Marshal(T.BoardBootstrapRequest{APIVersion: 1, InstallationID: T.UUIDv4(r.id.InstallationID), Incarnation: A.UUID(r.id.Incarnation)})
	out := request(t, r, "POST", r.boardPath()+"local/bootstrap", b, "", nil, func(q *http.Request) {
		q.Header.Set("Origin", r.Origin())
		q.Header.Set("X-Localswim-Client", "browser")
	})
	v, e := T.Decode[T.LocalBootstrap](out.b)
	if out.status != 200 || e != nil || string(v.BoardID) != r.id.BoardID || string(v.Epoch) != r.epoch || len(out.cookies) != 1 {
		t.Fatalf("selected bootstrap status=%d decode=%v", out.status, e)
	}
	c := out.cookies[0]
	if c.Path != strings.TrimSuffix(r.boardPath(), "/") || c.Domain != "" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("board cookie scope")
	}
	return c
}

func TestWorkspaceBrowserCookieJarScopesExactBoardAndDescendants(t *testing.T) {
	h, a, b, _, _ := workspaceTest(t, http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if WorkspaceChannel(q.Context()) != "browser" {
			t.Error("workspace browser channel missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	jar, e := cookiejar.New(nil)
	if e != nil {
		t.Fatal(e)
	}
	client := &http.Client{Jar: jar, Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	call := func(method, path string, body []byte) response {
		t.Helper()
		q, e := http.NewRequest(method, h.Origin()+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		q.Header.Set("X-Localswim-Client", "browser")
		if method == "GET" {
			q.Header.Set("Sec-Fetch-Site", "same-origin")
		} else {
			q.Header.Set("Origin", h.Origin())
			q.Header.Set("Content-Type", "application/json")
		}
		// All positive requests let the real jar select and attach its cookies.
		res, e := client.Do(q)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		out, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return response{res.StatusCode, out, res.Cookies()}
	}
	cookiesAt := func(path string) []*http.Cookie {
		t.Helper()
		u, e := url.Parse(h.Origin() + path)
		if e != nil {
			t.Fatal(e)
		}
		return jar.Cookies(u)
	}
	bootstrap := func(r *Runtime) {
		t.Helper()
		wire, _ := json.Marshal(T.BoardBootstrapRequest{APIVersion: 1, InstallationID: T.UUIDv4(r.id.InstallationID), Incarnation: A.UUID(r.id.Incarnation)})
		out := call("POST", r.boardPath()+"local/bootstrap", wire)
		view, e := T.Decode[T.LocalBootstrap](out.b)
		if out.status != 200 || e != nil || string(view.BoardID) != r.id.BoardID || len(out.cookies) != 1 || out.cookies[0].Path != strings.TrimSuffix(r.boardPath(), "/") {
			t.Fatalf("jar bootstrap status=%d decode=%v", out.status, e)
		}
	}
	bootstrap(a)
	if len(cookiesAt(strings.TrimSuffix(b.boardPath(), "/"))) != 0 || len(cookiesAt(b.boardPath()+"session")) != 0 {
		t.Fatal("A cookie selected for foreign B")
	}
	for _, p := range []string{strings.TrimSuffix(b.boardPath(), "/"), b.boardPath() + "session"} {
		if out := call("GET", p, nil); out.status != 401 {
			t.Fatal("A-only jar authenticated B")
		}
	}
	// The cookie must match a whole path segment: a textual BoardID prefix is
	// neither the same board nor one of its descendant resources.
	if len(cookiesAt(strings.TrimSuffix(a.boardPath(), "/")+"suffix")) != 0 {
		t.Fatal("board cookie matched a different path segment")
	}
	bootstrap(b)
	for _, r := range []*Runtime{a, b} {
		base := strings.TrimSuffix(r.boardPath(), "/")
		for _, p := range []string{base, r.boardPath() + "session"} {
			cookies := cookiesAt(p)
			if len(cookies) != 1 || cookies[0].Name != r.cookieName() {
				t.Fatal("jar selected the wrong board cookie")
			}
			if out := call("GET", p, nil); out.status != 200 {
				t.Fatalf("jar board/session read status=%d", out.status)
			}
		}
		created := term(t, call("POST", r.boardPath()+"cards", mutationWire(t, r, "", 0, 0, map[string]any{"title": "Browser jar synthetic", "description": "Scoped real-cookie selection"})), "committed")
		if created.Revision == nil || *created.Revision != 1 || string(created.BoardID) != r.id.BoardID {
			t.Fatal("jar mutation context")
		}
		out := call("GET", base, nil)
		view, e := T.Decode[T.BoardRead](out.b)
		if out.status != 200 || e != nil || len(view.Result.Cards) != 1 || view.Result.Revision != 1 {
			t.Fatal("jar exact BoardRead after mutation")
		}
	}
	if len(cookiesAt("/api/v1/workspace/catalog")) != 0 {
		t.Fatal("board cookies leaked to sibling workspace channel")
	}
	if out := call("GET", "/api/v1/workspace/catalog", nil); out.status != 401 {
		t.Fatal("board jar authenticated workspace sibling")
	}
	ws := call("POST", "/api/v1/workspace/bootstrap", []byte("{}"))
	if ws.status != 200 || len(ws.cookies) != 1 {
		t.Fatal("workspace jar bootstrap")
	}
	if out := call("GET", "/api/v1/workspace/catalog", nil); out.status != 204 {
		t.Fatal("workspace jar channel")
	}
	for _, r := range []*Runtime{a, b} {
		cookies := cookiesAt(strings.TrimSuffix(r.boardPath(), "/"))
		if len(cookies) != 1 || cookies[0].Name != r.cookieName() {
			t.Fatal("workspace cookie selected as board authority")
		}
		if out := call("GET", strings.TrimSuffix(r.boardPath(), "/"), nil); out.status != 200 {
			t.Fatal("workspace bootstrap overwrote board jar state")
		}
	}
}

func TestWorkspaceTwoIndependentBoardsOverActualHTTP(t *testing.T) {
	workspace := http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if mode := WorkspaceChannel(q.Context()); mode != "browser" && mode != "cli" {
			t.Error("missing workspace channel")
		}
		w.WriteHeader(204)
	})
	h, a, b, rootA, rootB := workspaceTest(t, workspace)
	infoA, e := ReadClient(rootA)
	if e != nil {
		t.Fatal(e)
	}
	infoB, e := ReadClient(rootB)
	if e != nil {
		t.Fatal(e)
	}
	if infoA.Origin != h.Origin() || infoA.WorkspaceID != h.id || infoA.Port != infoB.Port || infoA.SessionPath() != a.boardPath()+"session" || a.listener != nil || b.listener != nil {
		t.Fatal("listener-free member metadata")
	}
	// Equal retained defaults do not bind a second listener.
	l, e := net.Listen("tcp4", "127.0.0.1:"+strings.TrimPrefix(origin(infoA.Port), "http://127.0.0.1:"))
	if e != nil {
		t.Fatal("member unexpectedly bound its standalone port")
	}
	l.Close()
	mode := request(t, a, "GET", "/api/v1/runtime-mode", nil, "", nil, nil)
	v, e := T.Decode[T.WorkspaceRuntimeMode](mode.b)
	if mode.status != 200 || e != nil || string(v.WorkspaceID) != h.id {
		t.Fatal("runtime mode")
	}
	for _, p := range []string{"/api/v1/session", "/api/v1/local/bootstrap"} {
		method, body := "GET", []byte(nil)
		if strings.HasSuffix(p, "bootstrap") {
			method, body = "POST", []byte("{}")
		}
		if out := request(t, a, method, p, body, infoA.Token, nil, nil); out.status != 404 {
			t.Fatal("workspace default-board route exists")
		}
	}
	for _, p := range []string{"/", "/board/unknown/board", "/styles/app.css", "/js/app.js"} {
		if out := request(t, a, "GET", p, nil, "", nil, nil); out.status != 200 {
			t.Fatalf("shell/assets path %s status=%d", p, out.status)
		}
	}
	if out := request(t, a, "GET", "/board/Bad/board", nil, "", nil, nil); out.status != 404 {
		t.Fatal("malformed shell route")
	}
	cookieA, cookieB := workspaceBootstrapTest(t, a), workspaceBootstrapTest(t, b)
	if cookieA.Name == cookieB.Name {
		t.Fatal("board cookie collision")
	}
	for _, r := range []*Runtime{a, b} {
		if out := request(t, r, "POST", r.boardPath()+"local/bootstrap", []byte("{}"), "", nil, func(q *http.Request) {
			q.Header.Set("Origin", r.Origin())
			q.Header.Set("X-Localswim-Client", "browser")
		}); out.status != 400 {
			t.Fatal("missing expected tuple accepted")
		}
	}
	for _, test := range []struct {
		r      *Runtime
		token  T.SecretToken
		cookie *http.Cookie
	}{{b, infoA.Token, nil}, {a, infoB.Token, nil}, {b, "", cookieA}, {a, "", cookieB}, {a, h.AutomationToken(), nil}} {
		if out := request(t, test.r, "GET", test.r.boardPath()+"session", nil, test.token, test.cookie, nil); out.status != 401 {
			t.Fatal("cross-channel/board authority accepted")
		}
	}
	// Workspace capability is a separate host channel, not a board grant.
	out := request(t, a, "POST", "/api/v1/workspace/bootstrap", []byte("{}"), "", nil, func(q *http.Request) {
		q.Header.Set("Origin", h.Origin())
		q.Header.Set("X-Localswim-Client", "browser")
	})
	wv, e := T.Decode[T.WorkspaceBootstrap](out.b)
	if out.status != 200 || e != nil || string(wv.WorkspaceID) != h.id || len(out.cookies) != 1 || out.cookies[0].Path != "/api/v1/workspace/" {
		t.Fatal("workspace bootstrap")
	}
	wc := out.cookies[0]
	if out := request(t, a, "GET", a.boardPath()+"session", nil, "", wc, nil); out.status != 401 {
		t.Fatal("workspace cookie became board authority")
	}
	for _, test := range []struct {
		token  T.SecretToken
		cookie *http.Cookie
		want   int
	}{{h.AutomationToken(), nil, 204}, {"", wc, 204}, {infoA.Token, nil, 401}, {"", cookieA, 401}} {
		if out := request(t, a, "GET", "/api/v1/workspace/catalog", nil, test.token, test.cookie, nil); out.status != test.want {
			t.Fatalf("workspace channel status=%d want=%d", out.status, test.want)
		}
	}
	for j, r := range []*Runtime{a, b} {
		info := infoA
		if j == 1 {
			info = infoB
		}
		created := term(t, request(t, r, "POST", r.boardPath()+"cards", mutationWire(t, r, "", 0, 0, map[string]any{"title": "Same ticket", "description": "Independent synthetic board"}), info.Token, nil, nil), "committed")
		if created.Revision == nil || *created.Revision != 1 {
			t.Fatal("board revisions were shared")
		}
	}
	for _, test := range []struct {
		r      *Runtime
		info   ClientInfo
		cookie *http.Cookie
	}{{a, infoA, cookieA}, {b, infoB, cookieB}} {
		out := request(t, test.r, "GET", strings.TrimSuffix(test.r.boardPath(), "/"), nil, test.info.Token, nil, nil)
		board, e := T.Decode[T.BoardRead](out.b)
		if e != nil || len(board.Result.Cards) != 1 || board.Result.Cards[0].Ticket != 1 {
			t.Fatal("independent board contents/ticket sequence")
		}
		card := string(board.Result.Cards[0].CardID)
		base := test.r.boardPath() + "cards/" + card
		term(t, request(t, test.r, "PATCH", base, mutationWire(t, test.r, card, 1, 1, map[string]any{"lane": "In Progress"}), test.info.Token, nil, nil), "committed")
		term(t, request(t, test.r, "POST", base+"/review-handoff", mutationWire(t, test.r, card, 2, 2, map[string]any{}), test.info.Token, nil, nil), "committed")
		term(t, request(t, test.r, "POST", base+"/approve", mutationWire(t, test.r, card, 3, 3, map[string]any{"policyVersion": 1}), test.info.Token, nil, nil), "not_committed")
		term(t, request(t, test.r, "POST", base+"/approve", mutationWire(t, test.r, card, 3, 3, map[string]any{"policyVersion": 1}), "", test.cookie, nil), "committed")
	}
	identityA, identityB := a.Identity(), b.Identity()
	if e := h.Close(); e != nil {
		t.Fatal(e)
	}
	for _, root := range []string{rootA, rootB} {
		if _, e := ReadClient(root); e == nil {
			t.Fatal("closed member metadata survived")
		}
	}
	// Reopen with no port flag uses the unchanged standalone default and exact
	// actors/grants/tuple; no workspace origin is silently retained.
	r, e := Start(context.Background(), Options{DataDir: rootA})
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	current, e := ReadClient(rootA)
	if e != nil || r.Identity() != identityA || identityA == identityB || current.WorkspaceID != "" || current.Origin != origin(infoA.Port) || current.SessionPath() != "/api/v1/session" || current.Epoch == infoA.Epoch || current.Token == infoA.Token || current.ActorID != infoA.ActorID || current.GrantID != infoA.GrantID {
		t.Fatal("standalone identity/default/rotation compatibility")
	}
	if out := request(t, r, "GET", "/api/v1/session", nil, infoA.Token, nil, nil); out.status != 401 {
		t.Fatal("workspace bot capability survived standalone reopen")
	}
	mode = request(t, r, "GET", "/api/v1/runtime-mode", nil, "", nil, nil)
	if _, e := T.Decode[T.StandaloneRuntimeMode](mode.b); mode.status != 200 || e != nil {
		t.Fatal("standalone mode")
	}
}

func TestWorkspaceIdentityRevalidationAndStrictMetadata(t *testing.T) {
	h, a, _, rootA, _ := workspaceTest(t, nil)
	info, e := ReadClient(rootA)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(rootA, "service.json")
	original, e := readPrivateBounded(path, 64<<10)
	if e != nil {
		t.Fatal(e)
	}
	var valid map[string]any
	if e := json.Unmarshal(original, &valid); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["metadataVersion"] = 3 },
		func(m map[string]any) { delete(m, "metadataVersion") },
		func(m map[string]any) { m["origin"] = "http://localhost:8820" },
		func(m map[string]any) { m["origin"] = "http://127.0.0.1:08820" },
		func(m map[string]any) { m["sessionPath"] = "/api/v1/session" },
		func(m map[string]any) { m["host"] = map[string]any{"mode": "standalone", "workspaceId": h.id} },
		func(m map[string]any) { m["host"] = map[string]any{"mode": "workspace", "workspaceId": "invalid"} },
	} {
		var m map[string]any
		json.Unmarshal(original, &m)
		change(m)
		b, _ := json.Marshal(m)
		if e := publish(path, b, true); e != nil {
			t.Fatal(e)
		}
		if _, e := ReadClient(rootA); e == nil {
			t.Fatal("invalid tagged metadata accepted")
		}
	}
	if e := publish(path, original, true); e != nil {
		t.Fatal(e)
	}
	idPath := filepath.Join(rootA, "identity.json")
	old, e := readPrivate(idPath)
	if e != nil {
		t.Fatal(e)
	}
	replaced := a.id
	replaced.Incarnation = uuid()
	b, _ := json.Marshal(replaced)
	if e := publish(idPath, b, true); e != nil {
		t.Fatal(e)
	}
	if out := request(t, a, "GET", a.boardPath()+"session", nil, info.Token, nil, nil); out.status != 503 {
		t.Fatal("replaced durable identity was dispatched")
	}
	if e := publish(idPath, old, true); e != nil {
		t.Fatal(e)
	}
	if e := h.Detach(a.id.BoardID); e != nil {
		t.Fatal(e)
	}
	if r, e := OpenEngine(context.Background(), EngineOptions{DataDir: rootA, WorkspaceID: h.id, Origin: h.Origin()}); e == nil {
		r.Close()
		t.Fatal("existing member opened without expected tuple")
	}
	expected := a.Identity()
	missing := filepath.Join(filepath.Dir(rootA), "missing")
	if r, e := OpenEngine(context.Background(), EngineOptions{DataDir: missing, WorkspaceID: h.id, Origin: h.Origin(), ExpectedIdentity: &expected}); e == nil {
		r.Close()
		t.Fatal("missing expected member initialized")
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("expected missing root was created")
	}
	expected.Incarnation = uuid()
	if r, e := OpenEngine(context.Background(), EngineOptions{DataDir: rootA, WorkspaceID: h.id, Origin: h.Origin(), ExpectedIdentity: &expected}); e == nil {
		r.Close()
		t.Fatal("expected tuple mismatch reopened")
	}
	expected = a.Identity()
	r, e := OpenEngine(context.Background(), EngineOptions{DataDir: rootA, WorkspaceID: h.id, Origin: h.Origin(), ExpectedIdentity: &expected})
	if e != nil {
		t.Fatal(e)
	}
	if e := h.Attach(r); e != nil {
		r.Close()
		t.Fatal(e)
	}
}

func TestWorkspaceConcurrentCloseAndDetach(t *testing.T) {
	h, a, b, _, _ := workspaceTest(t, nil)
	// Pause both independent engine cleanups. Host close must snapshot and gate
	// all members before it blocks on either cleanup, without its global lock.
	a.closeMu.Lock()
	b.closeMu.Lock()
	var unlock sync.Once
	unblock := func() { unlock.Do(func() { a.closeMu.Unlock(); b.closeMu.Unlock() }) }
	defer unblock()
	results := make(chan error, 3)
	go func() { results <- h.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		gated := h.detaching[a.id.BoardID] && h.detaching[b.id.BoardID]
		h.mu.Unlock()
		if gated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host held bookkeeping over member cleanup")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { results <- h.Detach(a.id.BoardID) }()
	go func() { results <- h.Detach(b.id.BoardID) }()
	unblock()
	for range 3 {
		select {
		case e := <-results:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent cleanup did not finish")
		}
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("host lifetime not released")
	}
	for _, r := range []*Runtime{a, b} {
		select {
		case <-r.Done():
		default:
			t.Fatal("member lifetime not released")
		}
	}
}

func TestWorkspaceDrainRetainsIndependentEngineOwnership(t *testing.T) {
	h, a, b, rootA, _ := workspaceTest(t, nil)
	infoA, _ := ReadClient(rootA)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	original := a.board
	a.board = http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { close(entered); <-release; original.ServeHTTP(w, q) })
	returned := make(chan response, 1)
	go func() {
		returned <- request(t, a, "GET", strings.TrimSuffix(a.boardPath(), "/"), nil, infoA.Token, nil, nil)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("member request not admitted")
	}
	infoB, _ := ReadClient(b.root)
	if out := request(t, b, "GET", b.boardPath()+"session", nil, infoB.Token, nil, nil); out.status != 200 {
		t.Fatal("slow A blocked healthy B")
	}
	closed := make(chan error, 1)
	go func() { closed <- h.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		closing := h.closing
		h.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host admission did not close")
		}
		time.Sleep(time.Millisecond)
	}
	q := httptest.NewRequest("GET", h.Origin()+a.boardPath()+"session", nil)
	q.URL.Scheme, q.URL.Host = "", ""
	w := httptest.NewRecorder()
	h.ServeHTTP(w, q)
	if w.Code != 503 {
		t.Fatal("late host entry admitted")
	}
	if r, e := Start(context.Background(), Options{DataDir: rootA}); e == nil {
		r.Close()
		t.Fatal("draining member lost ownership")
	}
	select {
	case <-closed:
		t.Fatal("host closed before admitted request drained")
	default:
	}
	unblock()
	select {
	case out := <-returned:
		if out.status != 200 {
			t.Fatal("admitted operation lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not return")
	}
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close did not finish")
	}
}

func TestWorkspaceCleanupRetainsLockAndForeignEpoch(t *testing.T) {
	h, a, _, rootA, _ := workspaceTest(t, nil)
	p := filepath.Join(rootA, "service.json")
	original, e := readPrivateBounded(p, 64<<10)
	if e != nil {
		t.Fatal(e)
	}
	if e := publish(p, []byte("{}"), true); e != nil {
		t.Fatal(e)
	}
	if e := h.Detach(a.id.BoardID); e == nil {
		t.Fatal("malformed cleanup reported release")
	}
	if r, e := Start(context.Background(), Options{DataDir: rootA}); e == nil {
		r.Close()
		t.Fatal("failed cleanup released native root")
	}
	if e := publish(p, original, true); e != nil {
		t.Fatal(e)
	}
	if e := h.Detach(a.id.BoardID); e != nil {
		t.Fatal(e)
	}
	expected := a.Identity()
	r, e := OpenEngine(context.Background(), EngineOptions{DataDir: rootA, WorkspaceID: h.id, Origin: h.Origin(), ExpectedIdentity: &expected})
	if e != nil {
		t.Fatal(e)
	}
	m, _, e := decodeMetadata(original)
	if e != nil {
		t.Fatal(e)
	}
	foreign, _ := json.Marshal(workspaceMetadata{2, m.Identity, uuid(), m.Origin, m.BotToken, workspaceMetadataHost{"workspace", h.id}})
	if e := publish(p, foreign, true); e != nil {
		t.Fatal(e)
	}
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	got, e := readPrivateBounded(p, 64<<10)
	if e != nil || !bytes.Equal(got, foreign) {
		t.Fatal("foreign epoch metadata was removed")
	}
	// This test's deliberately foreign private fixture is retained, not replayed.
	if _, e := os.Stat(p); e != nil {
		t.Fatal(e)
	}
}

func TestWorkspaceHostSecurityBoundaries(t *testing.T) {
	h, a, _, rootA, _ := workspaceTest(t, http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(204) }))
	info, _ := ReadClient(rootA)
	for _, p := range []string{"/api/v1/runtime-mode", "/api/v1/workspace/bootstrap", a.boardPath() + "local/bootstrap"} {
		for _, change := range []func(*http.Request){
			func(q *http.Request) { q.Host = "localhost:" + strings.TrimPrefix(h.origin, "http://127.0.0.1:") },
			func(q *http.Request) { q.Header.Set("Origin", "http://evil.example") },
			func(q *http.Request) { q.Method = "OPTIONS" },
		} {
			if out := request(t, a, "GET", p, nil, "", nil, change); out.status < 400 {
				t.Fatal("unsafe host boundary accepted")
			}
		}
	}
	c := workspaceBootstrapTest(t, a)
	for _, p := range []string{a.boardPath() + "session", "/api/v1/workspace/catalog"} {
		if out := request(t, a, "GET", p, nil, info.Token, nil, func(q *http.Request) { q.Header.Set("Cookie", c.Name+"="+c.Value+"; broken") }); out.status != 401 {
			t.Fatal("malformed cookie not refused before channel")
		}
	}
	// Runtime mode grants no authority and is explicitly noncacheable.
	res, e := http.Get(h.origin + "/api/v1/runtime-mode")
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.Header.Get("Cache-Control") != "no-store" || len(res.Cookies()) != 0 {
		t.Fatal("runtime discriminator changed authority/cache policy")
	}
}
