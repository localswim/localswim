package credentials

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

var ErrUnavailable = errors.New("credential service unavailable")

// Active is a nonblocking availability check, not authentication. Callers may
// already hold the board writer, so lifecycle contention must refuse promptly.
func (s *Service) Active() bool {
	if s == nil || !s.mu.TryRLock() {
		return false
	}
	defer s.mu.RUnlock()
	return !s.closed && s.lease != nil && s.provider != nil && s.store != nil
}

// VerifyCLI resolves only the exact current bearer hash in the supplied board
// snapshot. G01 must independently resolve the current grant and its rights.
// Nonblocking lifecycle admission preserves native-before-board lock ordering
// even when this callback already holds a board lease.
func (s *Service) VerifyCLI(ctx context.Context, r writer.Read, token T.SecretToken, now time.Time) (grants.Credential, error) {
	if s == nil || !s.mu.TryRLock() {
		return grants.Credential{}, ErrDenied
	}
	defer s.mu.RUnlock()
	if s.closed || s.lease == nil || s.provider == nil || s.store == nil {
		return grants.Credential{}, ErrDenied
	}
	return s.cli(ctx, r, token, now)
}

// BrowserGrantScope is current grant selection metadata, not an authenticated
// browser credential. It carries no rights or browser session lifetime.
type BrowserGrantScope struct {
	InstallationID string
	Origin         string
	BoardID        string
	Incarnation    string
	GrantID        string
	ActorID        string
	IssuedAt       time.Time
}

// WithBrowserGrant authenticates the native owner and selects an existing
// enabled grant while holding the owned native lifetime before the S01 writer.
// It neither mints a CLI credential nor changes any durable state. Publication
// must do bounded in-memory work only: no external IO, other leases, or nested
// lifecycle admission. The caller must discard its unpublished reservation on
// every error, including uncertain commit or cleanup. Callback errors are never
// exposed. Only a nil return confirms successful publication.
func (s *Service) WithBrowserGrant(ctx context.Context, owner T.SecretToken, request T.CodeRequest, publish func(BrowserGrantScope) error) error {
	if s == nil || ctx == nil || ctx.Err() != nil || publish == nil {
		return ErrUnavailable
	}
	if T.Validate(request) != nil {
		return ErrDenied
	}
	if !s.mu.TryRLock() {
		return ErrUnavailable
	}
	defer s.mu.RUnlock()
	if s.closed || s.lease == nil || s.provider == nil || s.store == nil || s.now == nil {
		return ErrUnavailable
	}
	var scope BrowserGrantScope
	status := s.transaction(ctx, func(ctx context.Context, c *sql.Conn) error {
		now := s.now().UTC().Truncate(time.Second)
		if _, err := s.native(owner, now); err != nil || string(request.InstallationID) != s.context.InstallationID {
			return ErrDenied
		}
		actor, _, _, err := currentScope(ctx, c, string(request.BoardID), string(request.Incarnation), string(request.GrantID))
		if err != nil {
			return err
		}
		scope = BrowserGrantScope{InstallationID: s.context.InstallationID, Origin: s.context.Origin, BoardID: string(request.BoardID), Incarnation: string(request.Incarnation), GrantID: string(request.GrantID), ActorID: actor, IssuedAt: now}
		if ctx.Err() != nil || publish(scope) != nil || ctx.Err() != nil {
			return errors.New("browser publication failed")
		}
		return nil
	}, func(ctx context.Context, c *sql.Conn) error {
		actor, _, _, err := currentScope(ctx, c, scope.BoardID, scope.Incarnation, scope.GrantID)
		if err != nil || actor != scope.ActorID {
			return ErrDenied
		}
		return nil
	})
	if status == Confirmed {
		return nil
	}
	if status == Refused {
		return ErrDenied
	}
	return ErrUnavailable
}
