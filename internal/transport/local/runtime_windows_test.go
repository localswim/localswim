//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
	H "github.com/localswim/localswim/internal/transport/https"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}
func testRoot(t *testing.T) string {
	t.Helper()
	if base := os.Getenv("L02_TEST_ROOT"); base != "" {
		p, e := os.MkdirTemp(base, "case-")
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	return t.TempDir()
}
func startTest(t *testing.T) (*Runtime, string) {
	t.Helper()
	root := filepath.Join(testRoot(t), "local")
	r, e := Start(context.Background(), Options{DataDir: root, Port: freePort(t)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := r.Close(); e != nil {
			t.Error(e)
		}
	})
	return r, root
}

type response struct {
	status  int
	b       []byte
	cookies []*http.Cookie
}

func request(t *testing.T, r *Runtime, method, path string, b []byte, bot T.SecretToken, cookie *http.Cookie, change func(*http.Request)) response {
	t.Helper()
	q, e := http.NewRequest(method, r.Origin()+path, bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	if b != nil {
		q.Header.Set("Content-Type", "application/json")
	}
	if bot != "" {
		q.Header.Set("Authorization", "Bearer "+string(bot))
	}
	if cookie != nil {
		q.AddCookie(cookie)
		q.Header.Set("X-Localswim-Client", "browser")
		q.Header.Set("Origin", r.Origin())
	}
	if change != nil {
		change(q)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	res, e := client.Do(q)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	body, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return response{res.StatusCode, body, res.Cookies()}
}
func bootstrapTest(t *testing.T, r *Runtime) *http.Cookie {
	t.Helper()
	out := request(t, r, "POST", "/api/v1/local/bootstrap", []byte("{}"), "", nil, func(q *http.Request) {
		q.Header.Set("X-Localswim-Client", "browser")
		q.Header.Set("Origin", r.Origin())
	})
	if out.status != 200 || len(out.cookies) != 1 {
		t.Fatalf("bootstrap status %d", out.status)
	}
	c := out.cookies[0]
	if !c.HttpOnly || c.Secure || c.Domain != "" || c.Path != "/" || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie attributes")
	}
	if bytes.Contains(out.b, []byte(c.Value)) {
		t.Fatal("bootstrap body leaked bearer")
	}
	return c
}
func TestLocalLifecycleDurabilityAndRotation(t *testing.T) {
	r, root := startTest(t)
	info, e := ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	if fmt.Sprintf("%+v %#v", info, info) == "" || strings.Contains(fmt.Sprintf("%+v %#v", info, info), string(info.Token)) {
		t.Fatal("secret formatting")
	}
	if _, e = json.Marshal(info); e == nil {
		t.Fatal("client metadata must not marshal")
	}
	out := request(t, r, "GET", "/api/v1/session", nil, info.Token, nil, nil)
	session, e := T.Decode[T.SessionRead](out.b)
	if e != nil || out.status != 200 || session.Kind != "automation" || string(session.ActorID) != info.ActorID {
		t.Fatal("bot session")
	}
	for _, right := range session.Grant.Rights {
		if right == "approve" || right == "grant-admin" || right == "history-admin" {
			t.Fatal("bot gained human rights")
		}
	}
	cookie := bootstrapTest(t, r)
	for _, human := range []bool{false, true} {
		var selectedCookie *http.Cookie
		selectedToken := info.Token
		if human {
			selectedCookie = cookie
			selectedToken = ""
		}
		boardResponse := request(t, r, "GET", "/api/v1/boards/"+info.BoardID, nil, selectedToken, selectedCookie, nil)
		board, decodeErr := T.Decode[T.BoardRead](boardResponse.b)
		if boardResponse.status != 200 || decodeErr != nil || string(board.Result.Identity.BoardID) != info.BoardID || string(board.Result.Identity.Incarnation) != info.Incarnation {
			t.Fatalf("base board read human=%v status=%d decode=%v", human, boardResponse.status, decodeErr)
		}
	}
	out = request(t, r, "GET", "/api/v1/session", nil, "", cookie, nil)
	session, e = T.Decode[T.SessionRead](out.b)
	if e != nil || session.Kind != "human" || out.status != 200 {
		t.Fatal("human session")
	}
	if other, e := Start(context.Background(), Options{DataDir: root, Port: info.Port}); e == nil {
		other.Close()
		t.Fatal("duplicate writer started")
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadClient(root); e == nil {
		t.Fatal("inactive metadata survived")
	}
	r2, e := Start(context.Background(), Options{DataDir: root})
	if e != nil {
		t.Fatal(e)
	}
	defer r2.Close()
	newer, e := ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	if newer.InstallationID != info.InstallationID || newer.BoardID != info.BoardID || newer.Incarnation != info.Incarnation || newer.ActorID != info.ActorID || newer.GrantID != info.GrantID || newer.Epoch == info.Epoch || newer.Token == info.Token {
		t.Fatal("restart identity/epoch policy")
	}
	if out = request(t, r2, "GET", "/api/v1/session", nil, info.Token, nil, nil); out.status != 401 {
		t.Fatal("old bot credential survived")
	}
	if out = request(t, r2, "GET", "/api/v1/session", nil, "", cookie, nil); out.status != 401 {
		t.Fatal("old browser credential survived")
	}
	_ = bootstrapTest(t, r2)
}

func TestLocalBoundariesAndNoAuthorityBypass(t *testing.T) {
	r, root := startTest(t)
	info, _ := ReadClient(root)
	cookie := bootstrapTest(t, r)
	for _, test := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"host", func(q *http.Request) { q.Host = "localhost:" + fmt.Sprint(info.Port) }},
		{"origin", func(q *http.Request) { q.Header.Set("Origin", "http://evil.example") }},
		{"mixed", func(q *http.Request) { q.AddCookie(cookie) }},
		{"browser-bearer", func(q *http.Request) { q.Header.Set("X-Localswim-Client", "browser") }},
		{"duplicate", func(q *http.Request) { q.Header.Add("Authorization", "Bearer "+string(info.Token)) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := request(t, r, "GET", "/api/v1/session", nil, info.Token, nil, test.change)
			if out.status < 400 {
				t.Fatal("unsafe boundary accepted")
			}
		})
	}
	for _, path := range []string{"/api/v1/grants", "/api/v1/session/codes", "/api/v1/session/exchange", "/api/v1/session/cli-credentials"} {
		if out := request(t, r, "POST", path, []byte("{}"), info.Token, nil, nil); out.status != 404 {
			t.Fatalf("legacy route %s %d", path, out.status)
		}
	}
	out := request(t, r, "OPTIONS", "/api/v1/local/bootstrap", nil, "", nil, nil)
	if out.status != 403 {
		t.Fatal("preflight accepted")
	}
	out = request(t, r, "GET", "/api/v1/session", nil, "", cookie, func(q *http.Request) { q.Header.Del("Origin"); q.Header.Set("Sec-Fetch-Site", "same-origin") })
	if out.status != 200 {
		t.Fatal("same-origin read refused")
	}
	out = request(t, r, "GET", "/api/v1/session", nil, "", cookie, func(q *http.Request) { q.Header.Del("Origin") })
	if out.status != 403 {
		t.Fatal("unproven absent-origin browser read")
	}
	// Calling each adapter directly without the boundary-issued context refuses.
	for _, handler := range []http.Handler{r.board, r.attachment, r.export} {
		q := httptest.NewRequest("GET", r.Origin()+"/api/v1/boards/"+info.BoardID, nil)
		q.URL.Scheme = ""
		q.URL.Host = ""
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, q)
		if w.Code != 401 {
			t.Fatal("adapter accepted missing authority")
		}
	}
	// Even trusted context injection conveys no rights without the live registry.
	_, e := r.auth.Current(context.Background(), "cli", T.SecretToken(strings.Repeat("a", 43)))
	if e == nil {
		t.Fatal("forged token accepted")
	}
	_ = H.WithCredential(context.Background(), "cli", "")
}

func TestLocalMalformedCookiesRefuseBeforeChannelSelection(t *testing.T) {
	r, root := startTest(t)
	info, e := ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	cookie := bootstrapTest(t, r)
	for _, bad := range []struct{ name, value string }{
		{"unterminated", cookie.Name + `="unterminated`},
		{"backslash", cookie.Name + `=bad\value`},
	} {
		t.Run(bad.name, func(t *testing.T) {
			duplicate := cookie.Name + "=" + cookie.Value + "; " + bad.value
			for _, test := range []struct {
				name, method, path, raw string
				bot                     T.SecretToken
				browser                 bool
			}{
				{"browser-duplicate", "GET", "/api/v1/session", duplicate, "", true},
				{"bootstrap-duplicate", "POST", "/api/v1/local/bootstrap", duplicate, "", true},
				{"bot-malformed-mix", "GET", "/api/v1/session", bad.value, info.Token, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					var b []byte
					if test.method == "POST" {
						b = []byte("{}")
					}
					out := request(t, r, test.method, test.path, b, test.bot, nil, func(q *http.Request) {
						q.Header.Set("Cookie", test.raw)
						if test.browser {
							q.Header.Set("X-Localswim-Client", "browser")
							q.Header.Set("Origin", r.Origin())
						}
					})
					if out.status != 401 || len(out.cookies) != 0 {
						t.Fatalf("malformed auth status=%d replacedCookie=%v", out.status, len(out.cookies) != 0)
					}
				})
			}
		})
	}
	// A syntactically valid stale cookie may be replaced only at explicit bootstrap.
	stale := *cookie
	stale.Value = strings.Repeat("a", 43)
	out := request(t, r, "GET", "/api/v1/session", nil, "", &stale, nil)
	if out.status != 401 {
		t.Fatal("normal request replaced stale cookie")
	}
	out = request(t, r, "POST", "/api/v1/local/bootstrap", []byte("{}"), "", &stale, nil)
	if out.status != 200 || len(out.cookies) != 1 || out.cookies[0].Value == stale.Value {
		t.Fatal("well-formed stale bootstrap replacement")
	}
}

func mutationWire(t *testing.T, r *Runtime, card string, rev, cr int, input any) []byte {
	t.Helper()
	var target, cardRev any
	if card != "" {
		target = map[string]string{"cardId": card}
		cardRev = cr
	}
	b, e := json.Marshal(map[string]any{"incarnation": r.id.Incarnation, "opId": uuid(), "target": target, "expectedRevision": rev, "expectedCardRevision": cardRev, "input": input})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func term(t *testing.T, out response, want string) T.Terminal {
	t.Helper()
	v, e := T.Decode[T.Terminal](out.b)
	if e != nil || v.Outcome != T.TerminalOutcome(want) {
		t.Fatalf("terminal status %d outcome %s decode %v", out.status, v.Outcome, e)
	}
	return v
}
func TestLocalRealDomainBotRefusalHumanApprovalAndPersistedBoard(t *testing.T) {
	r, root := startTest(t)
	info, _ := ReadClient(root)
	cookie := bootstrapTest(t, r)
	base := "/api/v1/boards/" + info.BoardID
	created := term(t, request(t, r, "POST", base+"/cards", mutationWire(t, r, "", 0, 0, map[string]any{"title": "Local synthetic", "description": "Durable text"}), info.Token, nil, nil), "committed")
	card := ""
	for _, ref := range created.ResultRefs {
		if ref.Kind == "card" {
			card = string(ref.ID)
		}
	}
	if card == "" {
		t.Fatal("missing card")
	}
	path := base + "/cards/" + card
	// Every bot attempt is checked on the shared domain path, with current CAS.
	completed := request(t, r, "PATCH", path, mutationWire(t, r, card, 1, 1, map[string]any{"lane": "Completed"}), info.Token, nil, nil)
	if completed.status != 400 {
		t.Fatal("direct Completed edit must fail strict command validation")
	}
	term(t, request(t, r, "POST", path+"/approve", mutationWire(t, r, card, 1, 1, map[string]any{"policyVersion": 1}), info.Token, nil, nil), "not_committed")
	term(t, request(t, r, "PATCH", path, mutationWire(t, r, card, 1, 1, map[string]any{"lane": "In Progress"}), info.Token, nil, nil), "committed")
	term(t, request(t, r, "POST", path+"/review-handoff", mutationWire(t, r, card, 2, 2, map[string]any{}), info.Token, nil, nil), "committed")
	term(t, request(t, r, "POST", path+"/approve", mutationWire(t, r, card, 3, 3, map[string]any{"policyVersion": 1}), info.Token, nil, nil), "not_committed")
	term(t, request(t, r, "POST", path+"/approve", mutationWire(t, r, card, 3, 3, map[string]any{"policyVersion": 1}), "", cookie, nil), "committed")
	out := request(t, r, "GET", path, nil, info.Token, nil, nil)
	read, e := T.Decode[T.CardRead](out.b)
	if e != nil || read.Result.Card.Lane != "Completed" || read.Result.Card.Priority != "P3" {
		t.Fatal("human approval/P3")
	}
	// Durable grant disablement is rechecked; token presence is never authority.
	l, e := r.store.Writer(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(context.Background(), func(c *sql.Conn) error {
		_, e := c.ExecContext(context.Background(), "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.BotGrant)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	l.Close()
	out = request(t, r, "GET", path, nil, info.Token, nil, nil)
	if out.status != 401 {
		t.Fatal("disabled durable grant accepted")
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	r2, e := Start(context.Background(), Options{DataDir: root, Port: info.Port})
	if e != nil {
		t.Fatal(e)
	}
	defer r2.Close()
	cookie2 := bootstrapTest(t, r2)
	out = request(t, r2, "GET", path, nil, "", cookie2, nil)
	read, e = T.Decode[T.CardRead](out.b)
	if e != nil || read.Result.Card.Lane != "Completed" {
		t.Fatal("board lost on restart")
	}
	newer, _ := ReadClient(root)
	out = request(t, r2, "GET", path, nil, newer.Token, nil, nil)
	if out.status != 401 {
		t.Fatal("restart silently reenables disabled grant")
	}
}

func TestLocalPartialInitializationAndMetadataSafety(t *testing.T) {
	root := filepath.Join(testRoot(t), "partial")
	release, e := pinRoot(root, true)
	if e != nil {
		t.Fatal(e)
	}
	release()
	if e = os.WriteFile(filepath.Join(root, "unexpected"), []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	if r, e := Start(context.Background(), Options{DataDir: root, Port: freePort(t)}); e == nil {
		r.Close()
		t.Fatal("unknown root adopted")
	}
	if _, e = os.Stat(filepath.Join(root, "unexpected")); e != nil {
		t.Fatal("partial evidence removed")
	}
	if _, e = ReadClient(root); e == nil {
		t.Fatal("partial metadata accepted")
	}
	for _, port := range []int{-1, 1, 65536} {
		if r, e := Start(context.Background(), Options{DataDir: filepath.Join(t.TempDir(), "bad"), Port: port}); e == nil {
			r.Close()
			t.Fatal("unsafe port accepted")
		}
	}
}

func TestLocalCloseGatesLateEntryAndDrainsAdmittedRequest(t *testing.T) {
	root := filepath.Join(testRoot(t), "closing")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	r, e := Start(context.Background(), Options{DataDir: root, Port: freePort(t), Assets: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { close(entered); <-release; w.WriteHeader(200) })})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		unblock()
		if e := r.Close(); e != nil {
			t.Error(e)
		}
	})
	info, e := ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	served := make(chan response, 1)
	go func() { served <- request(t, r, "GET", "/", nil, "", nil, nil) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("admitted handler did not enter")
	}
	closed := make(chan error, 1)
	go func() { closed <- r.Close() }()
	// Wait for the exact admission gate, rather than assuming a scheduling delay.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
waitGate:
	for {
		r.reqMu.Lock()
		closing := r.closing
		r.reqMu.Unlock()
		if closing {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("Close did not close admission")
			break waitGate
		}
	}
	late := func() {
		q := httptest.NewRequest("GET", r.Origin()+"/api/v1/boards/"+info.BoardID, nil)
		q.URL.Scheme = ""
		q.URL.Host = ""
		q.Header.Set("Authorization", "Bearer "+string(info.Token))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Code != 503 {
			t.Fatalf("late handler status %d", w.Code)
		}
	}
	// This is the scheduling seam of a net/http worker paused before ServeHTTP.
	late()
	select {
	case <-closed:
		t.Fatal("Close released lifetime before admitted handler drained")
	default:
	}
	if other, e := Start(context.Background(), Options{DataDir: root}); e == nil {
		other.Close()
		t.Fatal("writer exclusion released during drain")
	}
	unblock()
	select {
	case out := <-served:
		if out.status != 200 {
			t.Fatal("admitted request was not drained")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admitted request did not finish")
	}
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not complete")
	}
	late()
	select {
	case <-r.Done():
	default:
		t.Fatal("Done did not reflect completed lifetime")
	}
}
