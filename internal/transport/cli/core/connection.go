package core

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
func NewBoardConnection(c setup.ConnectionContext, m CredentialMetadata, token T.SecretToken, onClose ...func()) (*Connection, error) {
	if !v4(m.Selected.InstallationID) || m.Selected.InstallationID != c.InstallationID || !uuid(m.Selected.BoardID) || !uuid(m.Selected.Incarnation) || !uuid(m.Selected.GrantID) || !uuid(m.ActorID) {
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	if !time.Now().Before(m.ExpiresAt) {
		return nil, &Failure{"CREDENTIAL_EXPIRED", 2}
	}
	x, err := newPinned(c, m, token, false)
	if err == nil {
		prior := x.close
		var once sync.Once
		x.close = func() {
			once.Do(func() {
				prior()
				for _, done := range onClose {
					if done != nil {
						done()
					}
				}
			})
		}
	}
	return x, err
}
func NewNativeOwnerConnection(n setup.NativeOwner) (*Connection, error) {
	if !time.Now().Before(n.ExpiresAt) {
		return nil, &Failure{"CREDENTIAL_EXPIRED", 2}
	}
	return newPinned(n.Context, CredentialMetadata{ActorID: n.Context.OwnerActorID, ExpiresAt: n.ExpiresAt}, T.SecretToken(strings.TrimPrefix(n.Authorization(), "Bearer ")), true)
}
func newPinned(c setup.ConnectionContext, m CredentialMetadata, token T.SecretToken, native bool) (*Connection, error) {
	port := strconv.Itoa(c.Port)
	if !uuid(m.ActorID) || !v4(c.InstallationID) || c.Port < 1024 || c.Port > 65535 || c.Authority != c.Hostname+":"+port || c.Origin != "https://"+c.Authority || !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.localhost$`).MatchString(c.Hostname) {
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
	tr := &http.Transport{TLSClientConfig: tls, DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != c.Authority {
			return nil, errors.New("authority refused")
		}
		var d net.Dialer
		d.Timeout = 5 * time.Second
		conn, e := d.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", port))
		if e == nil {
			return conn, nil
		}
		return d.DialContext(ctx, "tcp6", net.JoinHostPort("::1", port))
	}
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Connection{context: evidence.Context{InstallationID: c.InstallationID, Origin: c.Origin}, metadata: m, native: native, do: client.Do, close: tr.CloseIdleConnections, authorization: "Bearer " + string(token)}, nil
}

func (c *Connection) request(ctx context.Context, method, path string, body io.Reader) (int, []byte, error) {
	status, h, b, err := c.requestRaw(ctx, method, path, body, "application/json", "application/json", 32*1024*1024)
	if err != nil {
		return 0, nil, err
	}
	media, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return 0, nil, errors.New("response unavailable")
	}
	return status, b, nil
}

func (c *Connection) requestRaw(ctx context.Context, method, path string, body io.Reader, contentType, accept string, maxBytes int64) (int, http.Header, []byte, error) {
	if c == nil || c.do == nil || ctx == nil || ctx.Err() != nil {
		return 0, nil, nil, errors.New("connection unavailable")
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || !strings.HasPrefix(path, "/api/v1/") || u.IsAbs() || u.Host != "" || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(path, "\\\r\n") || maxBytes < 1 || maxBytes > 150994944 {
		return 0, nil, nil, errors.New("request unavailable")
	}
	r, e := http.NewRequestWithContext(ctx, method, c.context.Origin+path, body)
	if e != nil {
		return 0, nil, nil, errors.New("request unavailable")
	}
	// No GetBody: transport redirects/retries must not replay submitted intent.
	r.GetBody = nil
	r.Header.Set("Authorization", c.authorization)
	r.Header.Set("Accept", accept)
	if body != nil {
		r.Header.Set("Content-Type", contentType)
	}
	resp, e := c.do(r)
	if e != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return 0, nil, nil, errors.New("response unavailable")
	}
	if resp == nil || resp.Body == nil {
		return 0, nil, nil, errors.New("response unavailable")
	}
	defer resp.Body.Close()
	if len(resp.Header.Values("Content-Encoding")) != 0 || resp.Uncompressed {
		return 0, nil, nil, errors.New("response unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if e != nil || int64(len(b)) > maxBytes || ctx.Err() != nil || ((resp.ContentLength > 0 || len(resp.Header.Values("Content-Length")) != 0) && int64(len(b)) != resp.ContentLength) {
		return 0, nil, nil, errors.New("response unavailable")
	}
	return resp.StatusCode, resp.Header.Clone(), b, nil
}
