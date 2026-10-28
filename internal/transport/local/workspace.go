package local

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
)

// WorkspaceHostOptions configures the transport bridge, not persistent workspace
// membership. Its owner must acquire the workspace root before starting the host.
// Workspace receives authenticated workspace requests only; it cannot use the
// host capability as board authority. No mutable current-board selection exists.
type WorkspaceHostOptions struct {
	WorkspaceID string
	Port        int
	Assets      http.Handler
	Workspace   http.Handler
}

type workspaceCredential struct{ issued, expires, idle time.Time }
type workspaceChannelKey struct{}

// WorkspaceChannel is meaningful only inside the host's workspace handler.
// These channels convey no durable board actor, grant or mutation authority.
func WorkspaceChannel(ctx context.Context) string {
	mode, _ := ctx.Value(workspaceChannelKey{}).(string)
	return mode
}

type WorkspaceHost struct {
	id, origin, epoch string
	automation        T.SecretToken
	assets, workspace http.Handler
	server            *http.Server
	listener          net.Listener
	mu                sync.Mutex
	closing           bool
	requests          sync.WaitGroup
	members           map[string]*Runtime
	detaching         map[string]bool
	credentials       map[[32]byte]workspaceCredential
	closeMu           sync.Mutex
	closed            bool
	closeErr          error
	done              chan struct{}
}

func (h *WorkspaceHost) Origin() string                 { return h.origin }
func (h *WorkspaceHost) Epoch() string                  { return h.epoch }
func (h *WorkspaceHost) AutomationToken() T.SecretToken { return h.automation }
func (h *WorkspaceHost) Done() <-chan struct{}          { return h.done }

// StartWorkspaceHost binds exactly the configured numeric loopback port. Empty
// hosts already serve the shell; independent member acquisition does not block it.
func StartWorkspaceHost(ctx context.Context, o WorkspaceHostOptions) (*WorkspaceHost, error) {
	if ctx == nil || ctx.Err() != nil || !uuidV4(o.WorkspaceID) || o.Port < 1024 || o.Port > 65535 {
		return nil, errLocal
	}
	bot, e := token()
	if e != nil {
		return nil, e
	}
	l, e := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(o.Port))
	if e != nil {
		return nil, errLocal
	}
	h := &WorkspaceHost{id: o.WorkspaceID, origin: origin(o.Port), epoch: uuid(), automation: bot, assets: o.Assets, workspace: o.Workspace, listener: l, members: map[string]*Runtime{}, detaching: map[string]bool{}, credentials: map[[32]byte]workspaceCredential{}, done: make(chan struct{})}
	h.server = newServer(h)
	go func() { _ = h.server.Serve(l); _ = h.Close() }()
	go func() {
		select {
		case <-ctx.Done():
			_ = h.Close()
		case <-h.done:
		}
	}()
	return h, nil
}

// Attach transfers engine lifetime ownership on success. On refusal the caller
// retains it and must close it. Native IO occurs outside host bookkeeping locks.
func (h *WorkspaceHost) Attach(r *Runtime) error {
	if r == nil || r.workspaceID != h.id || r.Origin() != h.origin || r.server != nil || r.listener != nil || !r.validateIdentity() {
		return errLocal
	}
	r.reqMu.Lock()
	defer r.reqMu.Unlock()
	if r.closing {
		return errLocal
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing || h.members[r.id.BoardID] != nil {
		return errLocal
	}
	h.members[r.id.BoardID] = r
	return nil
}

// Detach closes dispatch before draining the engine. Failed cleanup retains the
// owned member, marked unavailable, so a deliberate retry cannot create a writer.
func (h *WorkspaceHost) Detach(boardID string) error {
	h.mu.Lock()
	r := h.members[boardID]
	if r == nil {
		closing := h.closing
		h.mu.Unlock()
		if closing {
			return nil
		}
		return errLocal
	}
	h.detaching[boardID] = true
	h.mu.Unlock()
	if e := r.Close(); e != nil {
		return e
	}
	h.mu.Lock()
	if h.members[boardID] == r {
		delete(h.members, boardID)
		delete(h.detaching, boardID)
	}
	h.mu.Unlock()
	return nil
}

func (h *WorkspaceHost) Close() error {
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.closed {
		return h.closeErr
	}
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e := h.server.Shutdown(ctx)
	if e != nil {
		_ = h.server.Close()
	}
	h.requests.Wait()
	h.mu.Lock()
	clear(h.credentials)
	members := make(map[string]*Runtime, len(h.members))
	for id, r := range h.members {
		members[id] = r
		h.detaching[id] = true
	}
	h.mu.Unlock()
	for id, r := range members {
		if ce := r.Close(); ce != nil {
			e = errors.Join(e, ce)
			continue
		}
		h.mu.Lock()
		if h.members[id] == r {
			delete(h.members, id)
			delete(h.detaching, id)
		}
		h.mu.Unlock()
	}
	h.mu.Lock()
	remaining := len(h.members)
	h.mu.Unlock()
	if remaining != 0 {
		return errors.Join(e, errLocal)
	}
	h.closed = true
	h.closeErr = e
	close(h.done)
	return e
}

func (h *WorkspaceHost) cookieName() string {
	return "localswim_workspace_" + strings.ReplaceAll(h.id, "-", "") + "_session"
}

func (h *WorkspaceHost) bootstrap(w http.ResponseWriter, q *http.Request) {
	if q.Method != "POST" || q.URL.RawQuery != "" {
		refuse(w, 405, "INVALID_INPUT")
		return
	}
	cookie, present, valid := sessionCookie(q, h.cookieName())
	if !valid {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if !exact(q, "Origin", h.origin) || !exact(q, "X-Localswim-Client", "browser") || len(q.Header.Values("Authorization")) != 0 {
		refuse(w, 403, "FORBIDDEN")
		return
	}
	if !media(q) || !strings.HasPrefix(q.Header.Get("Content-Type"), "application/json") {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, q.Body, 128))
	var empty struct{}
	if e != nil || decode(b, &empty) != nil || strings.TrimSpace(string(b)) != "{}" {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	var t T.SecretToken
	if present && validToken(cookie) && h.verifyBrowser(cookie) {
		t = T.SecretToken(cookie)
	}
	if t == "" {
		t, e = token()
		if e != nil {
			refuse(w, 503, "UNAVAILABLE")
			return
		}
		h.mu.Lock()
		now := time.Now().UTC()
		for k, c := range h.credentials {
			if !now.Before(c.expires) || !now.Before(c.idle) {
				delete(h.credentials, k)
			}
		}
		if h.closing || len(h.credentials) >= 128 {
			h.mu.Unlock()
			refuse(w, 503, "UNAVAILABLE")
			return
		}
		h.credentials[sha256.Sum256([]byte(t))] = workspaceCredential{now, now.Add(8 * time.Hour), now.Add(30 * time.Minute)}
		h.mu.Unlock()
	}
	b, e = T.Readable(T.WorkspaceBootstrap{APIVersion: 1, WorkspaceID: T.UUIDv4(h.id), Epoch: T.UUIDv4(h.epoch), Mode: "human"})
	if e != nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: h.cookieName(), Value: string(t), Path: "/api/v1/workspace/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (h *WorkspaceHost) verifyBrowser(raw string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := sha256.Sum256([]byte(raw))
	c, ok := h.credentials[k]
	now := time.Now().UTC()
	if h.closing || !ok || now.Before(c.issued) || !now.Before(c.expires) || !now.Before(c.idle) {
		return false
	}
	c.idle = now.Add(30 * time.Minute)
	if c.idle.After(c.expires) {
		c.idle = c.expires
	}
	h.credentials[k] = c
	return true
}

func (h *WorkspaceHost) authenticate(w http.ResponseWriter, q *http.Request) (string, bool) {
	cookie, present, valid := sessionCookie(q, h.cookieName())
	if !valid {
		refuse(w, 401, "UNAUTHENTICATED")
		return "", false
	}
	auth := q.Header.Values("Authorization")
	mode := "browser"
	if len(auth) != 0 {
		if len(auth) != 1 || len(q.Header.Values("Cookie")) != 0 || len(q.Header.Values("X-Localswim-Client")) != 0 || len(q.Header.Values("Origin")) != 0 || !strings.HasPrefix(auth[0], "Bearer ") {
			refuse(w, 401, "UNAUTHENTICATED")
			return "", false
		}
		raw := strings.TrimPrefix(auth[0], "Bearer ")
		if !validToken(raw) || sha256.Sum256([]byte(raw)) != sha256.Sum256([]byte(h.automation)) {
			refuse(w, 401, "UNAUTHENTICATED")
			return "", false
		}
		mode = "cli"
	} else {
		if !present || !validToken(cookie) || !exact(q, "X-Localswim-Client", "browser") || !h.verifyBrowser(cookie) {
			refuse(w, 401, "UNAUTHENTICATED")
			return "", false
		}
		if q.Method != "GET" && !exact(q, "Origin", h.origin) || q.Method == "GET" && len(q.Header.Values("Origin")) == 0 && !exact(q, "Sec-Fetch-Site", "same-origin") {
			refuse(w, 403, "FORBIDDEN")
			return "", false
		}
	}
	if q.Method != "GET" && !media(q) {
		refuse(w, 400, "INVALID_INPUT")
		return "", false
	}
	return mode, true
}

func (h *WorkspaceHost) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		headers(w)
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	h.requests.Add(1)
	h.mu.Unlock()
	defer h.requests.Done()
	if !requestBoundary(w, q, h.origin) {
		return
	}
	if q.URL.Path == "/api/v1/runtime-mode" {
		runtimeMode(w, q, T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: T.UUIDv4(h.id)})
		return
	}
	if q.URL.Path == "/api/v1/workspace/bootstrap" {
		h.bootstrap(w, q)
		return
	}
	if strings.HasPrefix(q.URL.Path, "/api/v1/workspace/") {
		mode, ok := h.authenticate(w, q)
		if !ok {
			return
		}
		if h.workspace == nil {
			refuse(w, 404, "NOT_FOUND")
			return
		}
		h.workspace.ServeHTTP(w, q.WithContext(context.WithValue(q.Context(), workspaceChannelKey{}, mode)))
		return
	}
	if strings.HasPrefix(q.URL.Path, "/api/") {
		p := strings.Split(q.URL.Path, "/")
		if len(p) >= 5 && p[1] == "api" && p[2] == "v1" && p[3] == "boards" {
			h.mu.Lock()
			r := h.members[p[4]]
			unavailable := h.detaching[p[4]]
			h.mu.Unlock()
			if r != nil && !unavailable {
				r.ServeHTTP(w, q)
				return
			}
		}
		refuse(w, 404, "NOT_FOUND")
		return
	}
	if q.Method != "GET" && q.Method != "HEAD" {
		refuse(w, 405, "INVALID_INPUT")
		return
	}
	if h.assets == nil || strings.HasPrefix(q.URL.Path, "/board/") && !shellPath(q.URL.Path) {
		refuse(w, 404, "NOT_FOUND")
		return
	}
	if shellPath(q.URL.Path) {
		q = q.Clone(q.Context())
		q.URL.Path = "/"
	}
	h.assets.ServeHTTP(w, q)
}

// Selection routes contain keys, never native paths. Their membership is resolved
// later through the workspace channel; syntactic validity alone serves the shell.
func shellPath(path string) bool {
	p := strings.Split(path, "/")
	return len(p) == 4 && p[1] == "board" && validKey(p[2]) && validKey(p[3])
}
func validKey(s string) bool {
	if len(s) < 1 || len(s) > 64 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	priorDash := false
	for _, c := range s {
		if c == '-' {
			if priorDash {
				return false
			}
			priorDash = true
			continue
		}
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
		priorDash = false
	}
	return true
}
