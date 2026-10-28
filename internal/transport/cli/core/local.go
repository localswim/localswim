package core

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

// NewLocalBoardConnection is the explicit loopback HTTP composition. It neither
// accepts a native owner nor falls back from the separately pinned TLS path.
// Open still derives the current actor/grant from the real service before use.
func NewLocalBoardConnection(c evidence.Context, m CredentialMetadata, token T.SecretToken, onClose ...func()) (*Connection, error) {
	return newLocalBoardConnection(c, m, token, "", onClose...)
}

// NewLocalWorkspaceBoardConnection uses only a validated workspace identity as a
// mode selector. The session route is derived from the selected BoardID, never
// accepted as a metadata URL. Open still verifies the live actor and full scope.
func NewLocalWorkspaceBoardConnection(c evidence.Context, m CredentialMetadata, token T.SecretToken, workspaceID string, onClose ...func()) (*Connection, error) {
	if !v4(workspaceID) {
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	return newLocalBoardConnection(c, m, token, workspaceID, onClose...)
}

func newLocalBoardConnection(c evidence.Context, m CredentialMetadata, token T.SecretToken, workspaceID string, onClose ...func()) (*Connection, error) {
	if !evidence.ValidLocalContext(c) || m.Selected.InstallationID != c.InstallationID ||
		!uuid(m.Selected.BoardID) || !uuid(m.Selected.Incarnation) || !uuid(m.Selected.GrantID) ||
		!uuid(m.ActorID) || !time.Now().Before(m.ExpiresAt) {
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(string(token))
	if err != nil || len(secret) != 32 {
		clear(secret)
		return nil, &Failure{"CREDENTIAL_UNAVAILABLE", 1}
	}
	clear(secret)
	u, _ := url.Parse(c.Origin)
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true,
		ForceAttemptHTTP2: false, ResponseHeaderTimeout: 20 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != u.Host {
			return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", u.Port()))
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var once sync.Once
	close := func() {
		once.Do(func() {
			transport.CloseIdleConnections()
			for _, done := range onClose {
				if done != nil {
					done()
				}
			}
		})
	}
	sessionPath := "/api/v1/session"
	if workspaceID != "" {
		sessionPath = "/api/v1/boards/" + m.Selected.BoardID + "/session"
	}
	return &Connection{context: c, metadata: m, local: true, sessionPath: sessionPath, workspaceID: workspaceID, do: client.Do, close: close,
		authorization: "Bearer " + string(token)}, nil
}
