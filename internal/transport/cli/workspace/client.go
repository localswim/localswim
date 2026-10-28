// Package workspace is the explicit local CLI workspace connection boundary.
// Host configuration capability and selected board authority remain separate.
package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	L "github.com/localswim/localswim/internal/transport/local"
	W "github.com/localswim/localswim/internal/workspace"
)

type closer interface{ Close() error }
type Client struct {
	mu               sync.Mutex
	root             string
	observedRevision A.Positive
	lastRevision     A.Positive
	info             W.ClientInfo
	do               func(*http.Request) (*http.Response, error)
	stop             func()
	readClient       func(string) (W.ClientInfo, error)
	readSelection    func(string, T.WorkspaceKey, T.WorkspaceKey) (*W.SelectionRoot, error)
	readBoard        func(string) (L.ClientInfo, error)
	owned            []closer
	closed, closing  bool
	done             chan struct{}
}

func (*Client) String() string               { return "workspace CLI client [redacted]" }
func (c *Client) GoString() string           { return c.String() }
func (*Client) MarshalJSON() ([]byte, error) { return nil, errors.New("client is not serializable") }
func (c *Client) Done() <-chan struct{}      { return c.done }
func fail(code string, exit int) error       { return &core.Failure{Code: code, Exit: exit} }

// Open returns cleanup custody on failure if native resources remain unresolved.
// Its authenticated Projects read verifies the actual workspace channel before
// any configuration control can be issued. Runtime-mode alone is not authority.
func Open(ctx context.Context, root string) (*Client, error) {
	c := &Client{root: root, readClient: W.ReadClient, readSelection: W.ReadSelection, readBoard: L.ReadClient, done: make(chan struct{})}
	info, e := c.readClient(root)
	if e != nil {
		c.retain(e)
		return c.failedOpen(fail("CREDENTIAL_UNAVAILABLE", 3))
	}
	c.info = info
	tr, e := transport(info)
	if e != nil {
		return c.failedOpen(e)
	}
	c.stop = tr.CloseIdleConnections
	h := &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c.do = h.Do
	observed, e := c.Projects(ctx)
	if e != nil {
		return c.failedOpen(e)
	}
	c.observedRevision = observed.ConfigRevision
	return c, nil
}
func (c *Client) failedOpen(e error) (*Client, error) {
	if c.Close() != nil {
		return c, e
	}
	return nil, e
}
func transport(info W.ClientInfo) (*http.Transport, error) {
	u, e := url.Parse(info.Origin)
	port := 0
	if e == nil {
		port, _ = strconv.Atoi(u.Port())
	}
	secret, e2 := base64.RawURLEncoding.Strict().DecodeString(string(info.Token))
	defer clear(secret)
	if e != nil || port < 1024 || port > 65535 || info.Origin != "http://127.0.0.1:"+strconv.Itoa(port) || e2 != nil || len(secret) != 32 || T.Validate(T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: T.UUIDv4(info.WorkspaceID)}) != nil || T.Validate(T.WorkspaceBootstrap{APIVersion: 1, WorkspaceID: T.UUIDv4(info.WorkspaceID), Epoch: T.UUIDv4(info.Epoch), Mode: "human"}) != nil {
		return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	tr := &http.Transport{DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false, ResponseHeaderTimeout: 20 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != u.Host {
			return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, "tcp4", u.Host)
	}
	return tr, nil
}
func cleanupFrom(e error) (closer, bool) {
	var x L.WorkspaceCleanup
	if errors.As(e, &x) {
		return x, true
	}
	return nil, false
}
func (c *Client) retain(e error) {
	if x, ok := cleanupFrom(e); ok {
		c.owned = append(c.owned, x)
	}
}

// Close is retryable and Done closes only after all native custody is released.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closing = true
	if c.stop != nil {
		c.stop()
		c.stop = nil
	}
	for len(c.owned) > 0 {
		if c.owned[0].Close() != nil {
			return fail("UNAVAILABLE", 3)
		}
		c.owned = c.owned[1:]
	}
	c.closed = true
	c.info.Token = ""
	close(c.done)
	return nil
}
func (c *Client) current() error {
	if c.closed || c.closing {
		return fail("UNAVAILABLE", 3)
	}
	info, e := c.readClient(c.root)
	if e != nil {
		c.retain(e)
		return fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	// Epoch is not in the catalog schema. The rotating authenticated capability
	// plus exact reread of private epoch/origin/workspace binds the live channel.
	if info != c.info {
		return fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	return nil
}
func (c *Client) request(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if ctx == nil || ctx.Err() != nil || c.do == nil {
		return 0, nil, fail("UNAVAILABLE", 3)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	r, e := http.NewRequestWithContext(ctx, method, c.info.Origin+path, rd)
	if e != nil {
		return 0, nil, fail("UNAVAILABLE", 3)
	}
	r.GetBody = nil
	r.Header.Set("Authorization", "Bearer "+string(c.info.Token))
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.do(r)
	if e != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return 0, nil, fail("UNAVAILABLE", 3)
	}
	if resp == nil || resp.Body == nil {
		return 0, nil, fail("UNAVAILABLE", 3)
	}
	defer resp.Body.Close()
	media, _, e := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || resp.Header.Get("Cache-Control") != "no-store" || len(resp.Header.Values("Content-Encoding")) != 0 || resp.Uncompressed {
		return 0, nil, fail("UNAVAILABLE", 3)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, W.ConfigLimit+1))
	if e != nil || int64(len(b)) > W.ConfigLimit || ctx.Err() != nil || (resp.ContentLength >= 0 && int64(len(b)) != resp.ContentLength) {
		return 0, nil, fail("UNAVAILABLE", 3)
	}
	return resp.StatusCode, b, nil
}
func refusal(status int, b []byte) error {
	code := refusalCode(b)
	if code != "" {
		switch status {
		case 400:
			if code == "INVALID_INPUT" {
				return fail(code, 2)
			}
		case 401:
			if code == "UNAUTHENTICATED" {
				return fail(code, 2)
			}
		case 403:
			if code == "FORBIDDEN" {
				return fail(code, 2)
			}
		case 404:
			if code == "NOT_FOUND" {
				return fail(code, 2)
			}
		case 409:
			if code == "INVALID_INPUT" || code == "UNAVAILABLE" {
				return fail(code, 2)
			}
		case 412:
			if code == "CONFIG_CHANGED" {
				return fail(code, 2)
			}
		case 503:
			if code == "UNAVAILABLE" {
				return fail(code, 3)
			}
		}
	}
	return fail("UNAVAILABLE", 3)
}
func refusalCode(b []byte) string {
	if v, e := T.Decode[T.WorkspaceControlError](b); e == nil {
		return v.Error.Code
	}
	// Authentication/Host admission occurs outside the workspace registry and
	// uses the existing generated refusal envelope, before any control runs.
	if v, e := T.Decode[T.NonterminalRefusal](b); e == nil && v.OpID == nil {
		return v.Error.Code
	}
	return ""
}
func knownRefusal(status int, b []byte) bool {
	code := refusalCode(b)
	return status == 400 && code == "INVALID_INPUT" || status == 401 && code == "UNAUTHENTICATED" || status == 403 && code == "FORBIDDEN" || status == 404 && code == "NOT_FOUND" || status == 409 && (code == "INVALID_INPUT" || code == "UNAVAILABLE") || status == 412 && code == "CONFIG_CHANGED" || status == 503 && code == "UNAVAILABLE"
}
func read[T1 any](c *Client, ctx context.Context, path string) (T1, error) {
	var zero T1
	if e := c.current(); e != nil {
		return zero, e
	}
	s, b, e := c.request(ctx, "GET", path, nil)
	if e != nil {
		return zero, e
	}
	if s != 200 {
		return zero, refusal(s, b)
	}
	v, e := T.Decode[T1](b)
	if e != nil {
		return zero, fail("UNAVAILABLE", 3)
	}
	return v, nil
}
func (c *Client) projects(ctx context.Context) (T.WorkspaceProjects, error) {
	v, e := read[T.WorkspaceProjects](c, ctx, "/api/v1/workspace/projects")
	if e == nil && string(v.WorkspaceID) != c.info.WorkspaceID {
		e = fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	if e == nil {
		c.lastRevision = v.ConfigRevision
	}
	return v, e
}
func (c *Client) Projects(ctx context.Context) (T.WorkspaceProjects, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.projects(ctx)
}
func validKey(k T.WorkspaceKey) bool {
	return T.Validate(T.WorkspaceProject{Key: k, Label: "validation"}) == nil
}
func (c *Client) Boards(ctx context.Context, p T.WorkspaceKey) (T.WorkspaceBoards, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := "/api/v1/workspace/boards"
	if p != "" {
		if !validKey(p) {
			return T.WorkspaceBoards{}, fail("INVALID_INPUT", 2)
		}
		path += "?projectKey=" + url.QueryEscape(string(p))
	}
	v, e := read[T.WorkspaceBoards](c, ctx, path)
	if e == nil && string(v.WorkspaceID) != c.info.WorkspaceID {
		e = fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	if e == nil && p != "" {
		for _, m := range v.Boards {
			if m.ProjectKey != p {
				return T.WorkspaceBoards{}, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
			}
		}
	}
	return v, e
}
func (c *Client) resolve(ctx context.Context, p, b T.WorkspaceKey) (T.WorkspaceResolve, error) {
	if !validKey(p) || !validKey(b) {
		return T.WorkspaceResolve{}, fail("INVALID_INPUT", 2)
	}
	v, e := read[T.WorkspaceResolve](c, ctx, "/api/v1/workspace/projects/"+string(p)+"/boards/"+string(b))
	if e == nil && (string(v.WorkspaceID) != c.info.WorkspaceID || v.Member.ProjectKey != p || v.Member.BoardKey != b) {
		e = fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	return v, e
}
func (c *Client) Resolve(ctx context.Context, p, b T.WorkspaceKey) (T.WorkspaceResolve, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resolve(ctx, p, b)
}
func (c *Client) control(ctx context.Context, path string, req any, p T.WorkspaceKey, b *T.WorkspaceKey) (T.WorkspaceControlResult, error) {
	if ctx == nil || ctx.Err() != nil {
		return T.WorkspaceControlResult{}, fail("UNAVAILABLE", 3)
	}
	wire, e := T.Readable(req)
	if e != nil {
		return T.WorkspaceControlResult{}, fail("INVALID_INPUT", 2)
	}
	s, raw, e := c.request(ctx, "POST", path, wire)
	revision := c.lastRevision
	switch v := req.(type) {
	case T.WorkspaceProjectRequest:
		revision = v.ExpectedConfigRevision
	case T.WorkspaceBoardRequest:
		revision = v.ExpectedConfigRevision
	}
	unknown := T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: T.UUIDv4(c.info.WorkspaceID), ConfigRevision: revision, Status: "UNKNOWN", ProjectKey: p, BoardKey: b}
	if e != nil {
		return unknown, fail("OUTCOME_UNKNOWN", 4)
	}
	if s != 200 {
		if knownRefusal(s, raw) {
			return T.WorkspaceControlResult{}, refusal(s, raw)
		}
		return unknown, fail("OUTCOME_UNKNOWN", 4)
	}
	v, e := T.Decode[T.WorkspaceControlResult](raw)
	if e != nil || string(v.WorkspaceID) != c.info.WorkspaceID || v.ProjectKey != p || (b == nil) != (v.BoardKey == nil) || b != nil && *b != *v.BoardKey || v.ConfigRevision < c.lastRevision {
		return unknown, fail("OUTCOME_UNKNOWN", 4)
	}
	if v.Status == "UNKNOWN" {
		// Uncertain registration is not verified identity evidence. A deliberate
		// status inspection can independently establish the tuple later.
		return unknown, fail("OUTCOME_UNKNOWN", 4)
	}
	valid := true
	switch request := req.(type) {
	case T.WorkspaceProjectRequest:
		valid = v.Status == "registered" && v.Identity == nil && v.ConfigRevision > request.ExpectedConfigRevision
	case T.WorkspaceBoardRequest:
		valid = v.Status == "registered" && v.Identity != nil && v.ConfigRevision > request.ExpectedConfigRevision && (request.ExpectedIdentity == nil || *v.Identity == *request.ExpectedIdentity)
	case T.WorkspaceStatusRequest:
		valid = (v.Status == "registered" || v.Status == "initialized-unregistered") && v.Identity != nil || v.Status == "unavailable" && v.Identity == nil
		if v.Identity != nil && request.ExpectedIdentity != nil && *v.Identity != *request.ExpectedIdentity {
			valid = false
		}
	}
	if !valid {
		return unknown, fail("OUTCOME_UNKNOWN", 4)
	}
	c.lastRevision = v.ConfigRevision
	return v, nil
}
func (c *Client) revision(ctx context.Context) (A.Positive, error) {
	if e := c.current(); e != nil {
		return 0, e
	}
	if c.observedRevision != 0 {
		r := c.observedRevision
		c.observedRevision = 0
		return r, nil
	}
	v, e := c.projects(ctx)
	return v.ConfigRevision, e
}
func (c *Client) CreateProject(ctx context.Context, v T.WorkspaceProjectRequest) (T.WorkspaceControlResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	observed, e := c.revision(ctx)
	if e != nil {
		return T.WorkspaceControlResult{}, e
	}
	v.APIVersion = 1
	v.ExpectedConfigRevision = observed
	return c.control(ctx, "/api/v1/workspace/projects", v, v.Key, nil)
}
func (c *Client) CreateBoard(ctx context.Context, v T.WorkspaceBoardRequest) (T.WorkspaceControlResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	observed, e := c.revision(ctx)
	if e != nil {
		return T.WorkspaceControlResult{}, e
	}
	v.APIVersion = 1
	v.ExpectedConfigRevision = observed
	return c.control(ctx, "/api/v1/workspace/boards", v, v.ProjectKey, &v.BoardKey)
}
func (c *Client) Status(ctx context.Context, v T.WorkspaceStatusRequest) (T.WorkspaceControlResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.current(); e != nil {
		return T.WorkspaceControlResult{}, e
	}
	v.APIVersion = 1
	return c.control(ctx, "/api/v1/workspace/boards/status", v, v.ProjectKey, &v.BoardKey)
}

// Selection contains only exact private native mapping, never host capability.
type Selection struct {
	client         *Client
	project, board T.WorkspaceKey
	dataDir        string
	info           L.ClientInfo
}

func (*Selection) String() string     { return "workspace board selection [redacted]" }
func (s *Selection) GoString() string { return s.String() }
func (*Selection) MarshalJSON() ([]byte, error) {
	return nil, errors.New("selection is not serializable")
}
func (s *Selection) DataDir() string { return s.dataDir }
func (s *Selection) Selectors() core.Selectors {
	return core.Selectors{InstallationID: s.info.InstallationID, BoardID: s.info.BoardID, Incarnation: s.info.Incarnation, GrantID: s.info.GrantID}
}
func (s *Selection) Context() evidence.Context {
	return evidence.Context{InstallationID: s.info.InstallationID, Origin: s.info.Origin}
}
func (s *Selection) Provider() core.Provider { return s }
func (c *Client) Select(ctx context.Context, p, b T.WorkspaceKey) (*Selection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, e := c.resolve(ctx, p, b)
	if e != nil {
		return nil, e
	}
	if v.Member.Availability != "ready" || v.Member.Identity == nil || v.Member.MemberEpoch == nil {
		return nil, fail("UNAVAILABLE", 3)
	}
	r, e := c.readSelection(c.root, p, b)
	if r != nil {
		c.owned = append(c.owned, r)
	}
	if e != nil {
		c.retain(e)
		return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	if r == nil || string(r.WorkspaceID) != c.info.WorkspaceID || r.Identity != *v.Member.Identity {
		return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	info, e := c.readBoard(r.DataDir())
	if e != nil {
		c.retain(e)
		return nil, fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	id := T.WorkspaceIdentity{InstallationID: T.UUIDv4(info.InstallationID), BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation)}
	if id != r.Identity || info.WorkspaceID != c.info.WorkspaceID || info.Origin != c.info.Origin || info.Epoch != string(*v.Member.MemberEpoch) {
		return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	s := &Selection{client: c, project: p, board: b, dataDir: r.DataDir(), info: info}
	// core.Open independently verifies the live selected automation session.
	connection, e := s.connection()
	if e != nil {
		return nil, e
	}
	verified, e := core.Open(ctx, fixedProvider{connection}, s.Selectors())
	if e != nil {
		return nil, e
	}
	verified.Close()
	return s, nil
}

type fixedProvider struct{ c *core.Connection }

func (p fixedProvider) Open(context.Context, core.Selectors) (*core.Connection, error) {
	return p.c, nil
}
func (fixedProvider) NativeOwner(context.Context, evidence.Record) (*core.Connection, error) {
	return nil, fail("FORBIDDEN", 2)
}
func (s *Selection) connection() (*core.Connection, error) {
	return core.NewLocalWorkspaceBoardConnection(s.Context(), core.CredentialMetadata{Selected: s.Selectors(), ActorID: s.info.ActorID, ExpiresAt: time.Now().Add(time.Hour)}, s.info.Token, s.info.WorkspaceID)
}
func (s *Selection) Open(ctx context.Context, selected core.Selectors) (*core.Connection, error) {
	if selected != s.Selectors() {
		return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	current, e := s.client.Select(ctx, s.project, s.board)
	if e != nil {
		return nil, e
	}
	if current.dataDir != s.dataDir || current.info != s.info {
		return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	return current.connection()
}
func (*Selection) NativeOwner(context.Context, evidence.Record) (*core.Connection, error) {
	return nil, fail("FORBIDDEN", 2)
}
