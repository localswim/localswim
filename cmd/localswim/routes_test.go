package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/transport/cli/admin"
	"github.com/localswim/localswim/internal/transport/cli/archive"
	"github.com/localswim/localswim/internal/transport/cli/core"
)

const testBoard = "10000000-0000-0000-0000-000000000001"
const testCard = "20000000-0000-0000-0000-000000000001"

func TestRouteSelectionPreservesRequest(t *testing.T) {
	mark := func(id string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Adapter", id)
			w.Header().Set("Method", r.Method)
			w.Header().Set("Path", r.URL.EscapedPath())
			w.WriteHeader(418)
		})
	}
	h := dispatchRoutes(mark("session"), mark("board"), mark("attachment"), mark("grant"), mark("export"), mark("asset"))
	base := "/api/v1/boards/" + testBoard
	for _, q := range []struct{ path, adapter string }{
		{"/api/v1/session", "session"}, {"/api/v1/session/codes", "session"}, {"/api/v1/session/cli-credentials", "session"}, {"/api/v1/session/exchange", "session"},
		{"/api/v1/grants", "grant"}, {"/api/v1/grants/" + testCard, "grant"},
		{base, "board"}, {base + "/cards", "board"}, {base + "/cards/" + testCard, "board"}, {base + "/cards/" + testCard + "/history", "board"}, {base + "/operations/" + testCard, "board"}, {base + "/recovery/confirm", "board"}, {base + "/recovery/preview", "board"},
		{base + "/cards/" + testCard + "/attachments", "attachment"}, {base + "/attachments/" + testCard, "attachment"}, {base + "/exports", "export"},
		{base + "/cards/" + testCard + "/comments", "board"}, {base + "/cards/" + testCard + "/link-add", "board"}, {base + "/cards/" + testCard + "/link-remove", "board"}, {base + "/cards/" + testCard + "/review-handoff", "board"}, {base + "/cards/" + testCard + "/approve", "board"},
		{"/api/v1/unknown", "session"}, {"/", "asset"}, {"/api/v1/boards//cards/../exports", "board"}, {base + "/exports/", "export"},
	} {
		for _, method := range []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"} {
			r := httptest.NewRequest(method, q.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 418 || w.Header().Get("Adapter") != q.adapter || w.Header().Get("Method") != method || w.Header().Get("Path") != r.URL.EscapedPath() {
				t.Errorf("selection changed request: %s %s", method, q.path)
			}
		}
	}
}

type recordingRunner struct {
	name   string
	called *[]string
	args   *[]string
}

func (r recordingRunner) Run(_ context.Context, args []string, _ io.Reader, _, _ io.Writer) int {
	*r.called = append(*r.called, r.name)
	*r.args = args
	return 42
}

func TestCLIDispatchDelegatesWholeInvocation(t *testing.T) {
	var called, received []string
	c := commands{recordingRunner{"core", &called, &received}, recordingRunner{"admin", &called, &received}, recordingRunner{"archive", &called, &received}}
	for _, q := range []struct {
		args  []string
		owner string
	}{
		{[]string{"--json", "setup", "status"}, "admin"}, {[]string{"setup", "init", "--port", "48443"}, "admin"},
		{[]string{"grant", "create", "--input", "-"}, "admin"}, {[]string{"credential", "issue"}, "admin"}, {[]string{"session", "code"}, "admin"}, {[]string{"recovery", "confirm"}, "admin"},
		{[]string{"attachment", "add"}, "archive"}, {[]string{"export", "create"}, "archive"}, {[]string{"--json", "restore", "status"}, "archive"},
		{[]string{"board", "read"}, "core"}, {[]string{"operation", "read"}, "core"}, {[]string{"--unknown", "setup", "init"}, "core"},
	} {
		called = nil
		received = nil
		if c.run(context.Background(), q.args, nil, nil, nil) != 42 || !reflect.DeepEqual(called, []string{q.owner}) || !reflect.DeepEqual(received, q.args) {
			t.Errorf("dispatch changed invocation %v", q.args)
		}
	}
}

func TestCompositionRejectsAbsentLifetimes(t *testing.T) {
	if _, e := routes(nil, nil, nil, http.NotFoundHandler()); e == nil {
		t.Fatal("missing authority accepted")
	}
	if _, e := startService(nil, sourceSelection{}, http.NotFoundHandler()); e == nil {
		t.Fatal("missing context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := startService(ctx, sourceSelection{}, http.NotFoundHandler()); e == nil {
		t.Fatal("canceled startup accepted")
	}
}

func TestRequestLifetimeStopsAdmissionAndJoinsHandlers(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	l := &requestLifetime{next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) })}
	go func() { l.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)); close(finished) }()
	<-entered
	l.stop()
	w := httptest.NewRecorder()
	l.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal("closing lifetime accepted new request")
	}
	joined := make(chan struct{})
	go func() { l.active.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("active handler was not joined")
	default:
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("completed handler retained")
	}
	<-finished
}

func TestActualCLIRunnersRefuseWithoutAuthority(t *testing.T) {
	c := cliCommands(core.Runner{}, admin.Runner{}, archive.Runner{})
	for _, q := range []struct {
		args []string
		want int
	}{
		{[]string{"--json", "board", "read"}, 2},
		{[]string{"--json", "grant", "create"}, 2},
		{[]string{"--json", "attachment", "add"}, 1},
		{[]string{"--json", "operation", "read", testCard, "--evidence", "absent"}, 4},
	} {
		w := httptest.NewRecorder()
		if got := c.run(context.Background(), q.args, nil, w, io.Discard); got != q.want {
			t.Errorf("%v: exit %d want %d", q.args, got, q.want)
		}
		if w.Body.Len() == 0 || w.Body.Bytes()[w.Body.Len()-1] != '\n' {
			t.Error("runner output missing final LF")
		}
	}
}
