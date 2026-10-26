package core

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/setup"
)

// Failure is a fixed category, never an underlying private OS/HTTP error.
type Failure struct {
	Code string
	Exit int
}

func (f *Failure) Error() string { return f.Code }

// Provider must load only the exact current native-validated credential file and
// disabled marker under the installation lifetime. No default provider exists.
// NativeOwner is exclusively the narrow original own-grant outcome entitlement.
// It must never create/reissue credentials, bootstrap actors or search grants.
type Provider interface {
	Open(context.Context, Selectors) (*Connection, error)
	NativeOwner(context.Context, evidence.Record) (*Connection, error)
}

// CredentialMetadata comes from a validated current private file, not CLI input.
type CredentialMetadata struct {
	Selected  Selectors
	ActorID   string
	ExpiresAt time.Time
}
type Connection struct {
	context       evidence.Context
	metadata      CredentialMetadata
	native        bool
	do            func(*http.Request) (*http.Response, error)
	close         func()
	authorization string
}

func (c *Connection) String() string   { return "CLI connection [redacted]" }
func (c *Connection) GoString() string { return c.String() }
func (c *Connection) MarshalJSON() ([]byte, error) {
	return nil, errors.New("connection is not serializable")
}

// NewBoardConnection requires N01's validated fixed issuer/name context and an
// independently validated selected credential file. GET session still resolves
// server actor/scope before every invocation's board dispatch.
func NewBoardConnection(c setup.ConnectionContext, m CredentialMetadata, token T.SecretToken) (*Connection, error) {
	if !v4(m.Selected.InstallationID) || m.Selected.InstallationID != c.InstallationID || !uuid(m.Selected.BoardID) || !uuid(m.Selected.Incarnation) || !uuid(m.Selected.GrantID) || !uuid(m.ActorID) {
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	if !time.Now().Before(m.ExpiresAt) {
		return nil, &Failure{"CREDENTIAL_EXPIRED", 2}
	}
	return newPinned(c, m, token, false)
}
func NewNativeOwnerConnection(n setup.NativeOwner) (*Connection, error) {
	if !time.Now().Before(n.ExpiresAt) {
		return nil, &Failure{"CREDENTIAL_EXPIRED", 2}
	}
	return newPinned(n.Context, CredentialMetadata{ActorID: n.Context.OwnerActorID, ExpiresAt: n.ExpiresAt}, T.SecretToken(strings.TrimPrefix(n.Authorization(), "Bearer ")), true)
}
func newPinned(c setup.ConnectionContext, m CredentialMetadata, token T.SecretToken, native bool) (*Connection, error) {
	if !uuid(m.ActorID) || c.Port != setup.DefaultPort || c.Authority != c.Hostname+":47443" || c.Origin != "https://"+c.Authority || strings.Count(c.Hostname, ".") != 1 || !strings.HasSuffix(c.Hostname, ".localhost") {
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	secret, e := base64.RawURLEncoding.Strict().DecodeString(string(token))
	if e != nil || len(secret) != 32 {
		return nil, &Failure{"CREDENTIAL_UNAVAILABLE", 1}
	}
	tls, e := c.TLSConfig()
	if e != nil {
		return nil, &Failure{"UNSAFE_LOCAL_MATERIAL", 1}
	}
	tr := &http.Transport{TLSClientConfig: tls, DisableKeepAlives: true, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != c.Authority {
			return nil, errors.New("authority refused")
		}
		var d net.Dialer
		d.Timeout = 5 * time.Second
		conn, e := d.DialContext(ctx, "tcp4", "127.0.0.1:47443")
		if e == nil {
			return conn, nil
		}
		return d.DialContext(ctx, "tcp6", "[::1]:47443")
	}
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Connection{context: evidence.Context{InstallationID: c.InstallationID, Origin: c.Origin}, metadata: m, native: native, do: client.Do, close: tr.CloseIdleConnections, authorization: "Bearer " + string(token)}, nil
}

func (c *Connection) request(ctx context.Context, method, path string, body io.Reader) (int, []byte, error) {
	if c == nil || c.do == nil || ctx == nil || ctx.Err() != nil {
		return 0, nil, errors.New("connection unavailable")
	}
	r, e := http.NewRequestWithContext(ctx, method, c.context.Origin+path, body)
	if e != nil {
		return 0, nil, errors.New("request unavailable")
	}
	// No GetBody: transport redirects/retries must not replay submitted intent.
	r.GetBody = nil
	r.Header.Set("Authorization", c.authorization)
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.do(r)
	if e != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return 0, nil, errors.New("response unavailable")
	}
	if resp == nil || resp.Body == nil {
		return 0, nil, errors.New("response unavailable")
	}
	defer resp.Body.Close()
	media, _, e := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(resp.Header.Values("Content-Encoding")) != 0 {
		return 0, nil, errors.New("response unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024+1))
	if e != nil || len(b) > 32*1024*1024 || ctx.Err() != nil {
		return 0, nil, errors.New("response unavailable")
	}
	return resp.StatusCode, b, nil
}
