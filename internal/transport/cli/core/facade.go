package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

// NativeCurrentProvider is an explicitly injected current owner source. It is
// separate from ordinary selection and original-evidence NativeOwner lookup.
type NativeCurrentProvider interface {
	NativeCurrent(context.Context) (*Connection, error)
}

// Client exposes no bearer and owns its connection lifetime. Context and Metadata
// are value copies. Every ordinary client has passed the current session binding.
type Client struct {
	mu     sync.Mutex
	c      *Connection
	closed bool
	spent  bool
}

func (c *Client) String() string               { return "Authenticated CLI client [redacted]" }
func (c *Client) GoString() string             { return c.String() }
func (c *Client) MarshalJSON() ([]byte, error) { return nil, errors.New("client is not serializable") }
func (c *Client) Context() evidence.Context {
	if c == nil || c.c == nil {
		return evidence.Context{}
	}
	return c.c.context
}
func (c *Client) Metadata() CredentialMetadata {
	if c == nil || c.c == nil {
		return CredentialMetadata{}
	}
	return c.c.metadata
}
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		if c.c != nil && c.c.close != nil {
			c.c.close()
		}
	}
}

// Open performs the exact selected-file/current-session equality check before
// returning authority. There is no implicit source or native fallback.
func Open(ctx context.Context, p Provider, selected Selectors) (*Client, error) {
	if err := ValidateSelectors(selected, false); err != nil {
		return nil, err
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, &Failure{"UNAVAILABLE", 3}
	}
	if p == nil {
		return nil, &Failure{"CREDENTIAL_UNAVAILABLE", 3}
	}
	c, err := p.Open(ctx, selected)
	if err != nil {
		if c != nil && c.close != nil {
			c.close()
		}
		code, exit := failure(err)
		return nil, &Failure{code, exit}
	}
	cl := &Client{c: c}
	if c == nil || c.do == nil || c.native || c.metadata.Selected != selected || !uuid(c.metadata.ActorID) || !time.Now().Before(c.metadata.ExpiresAt) {
		cl.Close()
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	if c.workspaceID != "" {
		status, body, err := c.request(ctx, http.MethodGet, "/api/v1/runtime-mode", nil)
		if err != nil {
			cl.Close()
			return nil, &Failure{"UNAVAILABLE", 3}
		}
		mode, err := T.Decode[T.WorkspaceRuntimeMode](body)
		if status != http.StatusOK || err != nil || string(mode.WorkspaceID) != c.workspaceID {
			cl.Close()
			return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
		}
	}
	sessionPath := "/api/v1/session"
	if c.sessionPath != "" {
		sessionPath = c.sessionPath
	}
	status, b, err := c.request(ctx, http.MethodGet, sessionPath, nil)
	if err != nil {
		cl.Close()
		return nil, &Failure{"UNAVAILABLE", 3}
	}
	s, err := T.Decode[T.SessionRead](b)
	if status != 200 || err != nil {
		cl.Close()
		r := classifyReadFailure(status, b, nil)
		return nil, &Failure{diagnostic(r.value), r.exit}
	}
	if string(s.ActorID) != c.metadata.ActorID || string(s.Grant.BoardID) != selected.BoardID || string(s.Grant.Incarnation) != selected.Incarnation || string(s.Grant.GrantID) != selected.GrantID || c.local && s.Kind != "automation" {
		cl.Close()
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	return cl, nil
}

// OpenCurrent never tries ordinary credentials. The returned native client can
// submit grant commands and use typed ephemeral bootstrap methods only.
func OpenCurrent(ctx context.Context, p NativeCurrentProvider) (*Client, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, &Failure{"UNAVAILABLE", 3}
	}
	if p == nil {
		return nil, &Failure{"CREDENTIAL_UNAVAILABLE", 3}
	}
	c, err := p.NativeCurrent(ctx)
	if err != nil {
		if c != nil && c.close != nil {
			c.close()
		}
		code, exit := failure(err)
		return nil, &Failure{code, exit}
	}
	cl := &Client{c: c}
	if c == nil || c.do == nil || !c.native || !uuid(c.metadata.ActorID) || !time.Now().Before(c.metadata.ExpiresAt) {
		cl.Close()
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	return cl, nil
}

// Request is bounded read/export/preview transport. Domain mutations and native
// secrets have dedicated evidence/ephemeral entry points and cannot bypass them.
func (c *Client) Request(ctx context.Context, method, path string, body io.Reader, contentType, accept string, maxBytes int64) (int, http.Header, []byte, error) {
	if c == nil {
		return 0, nil, nil, &Failure{"UNAVAILABLE", 3}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.spent || c.c == nil || c.c.native || !ordinaryReadPath(c.c.metadata.Selected.BoardID, method, path) {
		return 0, nil, nil, &Failure{"FORBIDDEN", 2}
	}
	return c.c.requestRaw(ctx, method, path, body, contentType, accept, maxBytes)
}
func ordinaryReadPath(board, method, path string) bool {
	u, e := url.ParseRequestURI(path)
	if e != nil || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" {
		return false
	}
	p := strings.Split(u.Path, "/")
	if len(p) < 5 || strings.Join(p[:4], "/") != "/api/v1/boards" || p[4] != board {
		return false
	}
	if method == "GET" {
		return len(p) == 5 || len(p) == 7 && (p[5] == "cards" || p[5] == "attachments") && uuid(p[6]) || len(p) == 8 && p[5] == "cards" && uuid(p[6]) && p[7] == "history"
	}
	return method == "POST" && (len(p) == 6 && p[5] == "exports" || len(p) == 7 && p[5] == "recovery" && p[6] == "preview")
}

// Outcome contains only a validated public envelope and exit class. Value makes
// a fresh decoded copy; it cannot expose ephemeral transport or private errors.
type Outcome struct{ r result }

func (o Outcome) Exit() int {
	if o.r.value == nil {
		return 3
	}
	return o.r.exit
}
func (o Outcome) MarshalJSON() ([]byte, error) { return T.Readable(o.Value()) }
func (o Outcome) Value() any {
	b, e := T.Readable(o.r.value)
	if e != nil {
		return refused("UNAVAILABLE", 3, nil).value
	}
	switch o.r.value.(type) {
	case T.Terminal:
		v, _ := T.Decode[T.Terminal](b)
		return v
	case T.KnownIdentityUnknown:
		v, _ := T.Decode[T.KnownIdentityUnknown](b)
		return v
	default:
		v, _ := T.Decode[T.NonterminalRefusal](b)
		return v
	}
}
func Refused(code string, exit int) Outcome {
	if exit < 1 || exit > 4 {
		exit = 3
	}
	return Outcome{refused(code, exit, nil)}
}
func ReadFailure(status int, b []byte) Outcome { return Outcome{classifyReadFailure(status, b, nil)} }

// Dispatch freezes the independently resolved route/actor and persists exact
// immutable evidence before its sole HTTP attempt. It never rekeys or retries.
func (c *Client) Dispatch(ctx context.Context, store evidence.Store, route contract.ResolvedRoute, wire []byte, stderr io.Writer) Outcome {
	return c.dispatch(ctx, store, route, wire, nil, false, stderr)
}
func (c *Client) DispatchAttachment(ctx context.Context, store evidence.Store, route contract.ResolvedRoute, wire, raw []byte, stderr io.Writer) Outcome {
	return c.dispatch(ctx, store, route, wire, raw, true, stderr)
}
func (c *Client) dispatch(ctx context.Context, store evidence.Store, route contract.ResolvedRoute, wire, raw []byte, attachment bool, stderr io.Writer) Outcome {
	if c == nil || c.c == nil || ctx == nil || ctx.Err() != nil {
		return Refused("UNAVAILABLE", 3)
	}
	c.mu.Lock()
	unavailable := c.closed || c.spent
	c.mu.Unlock()
	if unavailable {
		return Refused("UNAVAILABLE", 3)
	}
	if c.c.native && route.Command != "grant.create" && route.Command != "grant.revoke" || !c.c.native && route.BoardID != c.c.metadata.Selected.BoardID {
		return Refused("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	if attachment != (route.Command == "attachment.add") {
		return Refused("INVALID_INPUT", 2)
	}
	x, err := evidence.Freeze(c.c.context, route, contract.AuthenticatedActor{ActorID: c.c.metadata.ActorID}, bytes.Clone(wire))
	if err != nil {
		return Refused("INVALID_INPUT", 2)
	}
	if !c.c.native && x.Identity().Incarnation != c.c.metadata.Selected.Incarnation {
		return Refused("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	method, path, ok := mutationRoute(route)
	if !ok {
		return Refused("INVALID_INPUT", 2)
	}
	var payload []byte
	contentType := "application/json"
	if attachment {
		if len(wire) > 16384 || len(x.Wire()) > 16384 || len(raw) > 10485760 {
			return Refused("INPUT_TOO_LARGE", 2)
		}
		payload = bytes.Clone(raw)
		v, e := contract.DecodeJSON(x.Wire())
		if e != nil {
			return Refused("INVALID_INPUT", 2)
		}
		input := v.(map[string]any)["input"].(map[string]any)
		h := sha256.Sum256(payload)
		size, _ := input["byteLength"].(json.Number).Int64()
		if size != int64(len(payload)) || input["sha256"] != hex.EncodeToString(h[:]) {
			return Refused("INVALID_INPUT", 2)
		}
	}
	if store == nil {
		return Refused("UNSAFE_LOCAL_MATERIAL", 1)
	}
	saved, err := store.Save(x)
	if err != nil {
		return Refused("INVOCATION_OR_IO", 1)
	}
	verified, err := store.Load(saved)
	if err != nil || !verified.Valid() || !bytes.Equal(verified.Bytes(), x.Bytes()) {
		return Refused("INVOCATION_OR_IO", 1)
	}
	if attachment {
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		for _, part := range []struct {
			name, media string
			data        []byte
		}{{"metadata", "application/json", x.Wire()}, {"file", "application/octet-stream", payload}} {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", `form-data; name="`+part.name+`"`)
			h.Set("Content-Type", part.media)
			w, e := m.CreatePart(h)
			if e != nil {
				return Refused("INVOCATION_OR_IO", 1)
			}
			if _, e = w.Write(part.data); e != nil {
				return Refused("INVOCATION_OR_IO", 1)
			}
		}
		if m.Close() != nil {
			return Refused("INVOCATION_OR_IO", 1)
		}
		payload = b.Bytes()
		contentType = m.FormDataContentType()
	} else {
		payload = x.Wire()
	}
	if ctx.Err() != nil {
		return Refused("UNAVAILABLE", 3)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.spent {
		return Refused("UNAVAILABLE", 3)
	}
	status, h, b, err := c.c.requestRaw(ctx, method, path, bytes.NewReader(payload), contentType, "application/json", 32*1024*1024)
	if err != nil || !jsonMedia(h) {
		return Outcome{refused("OUTCOME_UNKNOWN", 4, &x)}
	}
	return Outcome{(Runner{Evidence: store}).mark(classifyMutation(status, b, x, false), x, stderr)}
}
func mutationRoute(r contract.ResolvedRoute) (string, string, bool) {
	if !uuid(r.BoardID) {
		return "", "", false
	}
	for _, d := range contract.Commands() {
		if d.Command == r.Command {
			if d.Target != "none" && !uuid(r.TargetID) || d.Target == "none" && r.TargetID != "" {
				return "", "", false
			}
			p := strings.ReplaceAll(d.Route, "{boardId}", r.BoardID)
			p = strings.ReplaceAll(p, "{cardId}", r.TargetID)
			p = strings.ReplaceAll(p, "{grantId}", r.TargetID)
			return d.Method, p, true
		}
	}
	return "", "", false
}
func jsonMedia(h http.Header) bool {
	media, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && media == "application/json"
}

// Lookup reads original evidence only; it can never dispatch a mutation. Current
// native authority is limited to its own original grant.create/grant.revoke.
func (c *Client) Lookup(ctx context.Context, store evidence.Store, path string, stderr io.Writer) Outcome {
	if store == nil {
		return Refused("UNSAFE_LOCAL_MATERIAL", 4)
	}
	x, err := store.Load(path)
	if err != nil || !x.Valid() {
		return Refused("UNSAFE_LOCAL_MATERIAL", 4)
	}
	return c.lookupRecord(ctx, store, x, stderr)
}
func (c *Client) lookupRecord(ctx context.Context, store evidence.Store, x evidence.Record, stderr io.Writer) Outcome {
	i := x.Identity()
	if c == nil || c.c == nil || ctx == nil || ctx.Err() != nil {
		return Outcome{refused("UNAVAILABLE", 4, &x)}
	}
	if x.Context() != c.c.context || i.ActorID != c.c.metadata.ActorID || c.c.native && i.Command != "grant.create" && i.Command != "grant.revoke" || !c.c.native && (i.BoardID != c.c.metadata.Selected.BoardID || i.Incarnation != c.c.metadata.Selected.Incarnation) {
		return Outcome{refused("CREDENTIAL_CONTEXT_MISMATCH", 4, &x)}
	}
	query := url.Values{"incarnation": {i.Incarnation}, "fingerprintVersion": {"1"}, "fingerprint": {x.Fingerprint()}}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.spent {
		return Outcome{refused("UNAVAILABLE", 4, &x)}
	}
	status, b, err := c.c.request(ctx, "GET", "/api/v1/boards/"+i.BoardID+"/operations/"+i.OpID+"?"+query.Encode(), nil)
	if err != nil {
		return Outcome{refused("UNAVAILABLE", 4, &x)}
	}
	return Outcome{(Runner{Evidence: store}).mark(classifyMutation(status, b, x, true), x, stderr)}
}

// IssueCredential and IssueCode expose validated secret success only to the
// explicit in-memory callback. They never return a secret/public Outcome.
// Callback failures after possible issuance remain unknown; no retry follows.
func (c *Client) IssueCredential(ctx context.Context, req T.CredentialIssueRequest, consume func(T.CredentialSecret) error) error {
	return c.ephemeral(ctx, req, "/api/v1/session/cli-credentials", func(b []byte) error {
		x, e := T.Decode[T.CredentialSecret](b)
		if e != nil || x.InstallationID != req.InstallationID || x.BoardID != req.BoardID || x.Incarnation != req.Incarnation || x.GrantID != req.GrantID {
			return errors.New("secret response unavailable")
		}
		return consume(x)
	}, consume != nil)
}
func (c *Client) IssueCode(ctx context.Context, req T.CodeRequest, consume func(T.CodeSecret) error) error {
	return c.ephemeral(ctx, req, "/api/v1/session/codes", func(b []byte) error {
		x, e := T.Decode[T.CodeSecret](b)
		if e != nil || x.InstallationID != req.InstallationID || x.BoardID != req.BoardID || x.Incarnation != req.Incarnation || x.GrantID != req.GrantID {
			return errors.New("secret response unavailable")
		}
		return consume(x)
	}, consume != nil)
}
func (c *Client) ephemeral(ctx context.Context, req T.CodeRequest, path string, consume func([]byte) error, hasConsumer bool) error {
	if c == nil || c.c == nil || !c.c.native || !hasConsumer {
		return &Failure{"FORBIDDEN", 2}
	}
	if ctx == nil || ctx.Err() != nil {
		return &Failure{"UNAVAILABLE", 3}
	}
	if string(req.InstallationID) != c.c.context.InstallationID {
		return &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	wire, err := T.Readable(req)
	if err != nil {
		return &Failure{"INVALID_INPUT", 2}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.spent {
		return &Failure{"UNAVAILABLE", 3}
	}
	status, h, b, err := c.c.requestRaw(ctx, "POST", path, bytes.NewReader(wire), "application/json", "application/json", 32768)
	defer clear(b)
	if err != nil || !jsonMedia(h) {
		return &Failure{"OUTCOME_UNKNOWN", 4}
	}
	if status != 201 {
		x, e := T.Decode[T.NonterminalRefusal](b)
		if e == nil && x.OpID == nil && ephemeralRefusal(status, x.Error.Code) {
			return &Failure{x.Error.Code, 2}
		}
		return &Failure{"OUTCOME_UNKNOWN", 4}
	}
	if h.Get("Cache-Control") != "no-store" {
		return &Failure{"OUTCOME_UNKNOWN", 4}
	}
	if consume(b) != nil {
		return &Failure{"OUTCOME_UNKNOWN", 4}
	}
	return nil
}

func ephemeralRefusal(status int, code string) bool {
	switch status {
	case 400:
		return code == "INVALID_INPUT"
	case 401:
		return code == "UNAUTHENTICATED"
	case 403:
		return code == "FORBIDDEN"
	case 404:
		return code == "NOT_FOUND"
	case 409:
		return code == "REVISION_CONFLICT" || code == "INCARNATION_CONFLICT" || code == "OPERATION_MISMATCH"
	case 413:
		return code == "INPUT_TOO_LARGE"
	}
	return false
}

// RevokeCredential consumes this already session-bound ordinary client. The
// callback must durably disable/read back the exact selected local credential;
// only then is its captured bearer sent once to DELETE session. No further
// session GET, ordinary use, retry or native fallback is permitted. A callback
// may return a fixed Failure class; an unclassified publication error is unknown.
// Callbacks must not reenter this Client. Public metadata remains available for
// the caller's secret-free D01 CredentialCLI result; this is no domain receipt.
func (c *Client) RevokeCredential(ctx context.Context, beforeDispatch func() error) error {
	if c == nil || c.c == nil || c.c.native {
		return &Failure{"FORBIDDEN", 2}
	}
	if ctx == nil || ctx.Err() != nil {
		return &Failure{"UNAVAILABLE", 3}
	}
	if beforeDispatch == nil {
		return &Failure{"INVOCATION_OR_IO", 1}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.spent {
		return &Failure{"UNAVAILABLE", 3}
	}
	c.spent = true
	if err := beforeDispatch(); err != nil {
		var f *Failure
		if errors.As(err, &f) && f.Exit >= 1 && f.Exit <= 4 {
			return &Failure{safeCode(f.Code), f.Exit}
		}
		return &Failure{"OUTCOME_UNKNOWN", 4}
	}
	if ctx.Err() != nil {
		return &Failure{"UNAVAILABLE", 3}
	}
	status, h, b, err := c.c.requestRaw(ctx, "DELETE", "/api/v1/session", bytes.NewReader(nil), "application/json", "application/json", 32768)
	if err != nil || !jsonMedia(h) {
		return &Failure{"OUTCOME_UNKNOWN", 4}
	}
	if status != 200 {
		x, e := T.Decode[T.NonterminalRefusal](b)
		if e == nil && x.OpID == nil && ephemeralRefusal(status, x.Error.Code) {
			return &Failure{x.Error.Code, 2}
		}
		return &Failure{"OUTCOME_UNKNOWN", 4}
	}
	if _, err := T.Decode[T.SessionRevoked](b); err != nil {
		return &Failure{"OUTCOME_UNKNOWN", 4}
	}
	return nil
}
