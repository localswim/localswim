// Package session owns restart-local browser codes and bearers. Grant authority
// remains current G01 state in the caller's coherent board snapshot.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/setup"
)

var ErrDenied = errors.New("session authority unavailable")

type code struct {
	credential grants.Credential
	expires    time.Time
}
type Service struct {
	mu         sync.Mutex
	store      *S.Store
	native     *credentials.Service
	connection setup.ConnectionContext
	codes      map[[32]byte]code
	sessions   map[[32]byte]grants.Credential
	closed     bool
	now        func() time.Time
	random     io.Reader
}

func (*Service) String() string     { return "browser sessions [redacted]" }
func (s *Service) GoString() string { return s.String() }

func New(store *S.Store, native *credentials.Service) (*Service, error) {
	if store == nil || native == nil {
		return nil, ErrDenied
	}
	c := native.Context()
	if c.InstallationID == "" || c.Origin == "" {
		return nil, ErrDenied
	}
	return &Service{store: store, native: native, connection: c, codes: make(map[[32]byte]code), sessions: make(map[[32]byte]grants.Credential), now: time.Now, random: rand.Reader}, nil
}
func (s *Service) Close() {
	if s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closed = true
		clear(s.codes)
		clear(s.sessions)
	}
}
func digest(secret string, n int) ([32]byte, error) {
	b, e := base64.RawURLEncoding.Strict().DecodeString(secret)
	if e != nil || len(b) != n || base64.RawURLEncoding.EncodeToString(b) != secret {
		return [32]byte{}, ErrDenied
	}
	return sha256.Sum256(b), nil
}
func stamp(t time.Time) A.Timestamp {
	return A.Timestamp(t.UTC().Truncate(time.Second).Format(time.RFC3339))
}

// IssueCode serializes native authentication and enabled-grant selection with
// revocation through G02; it never issues/replaces a durable CLI credential.
func (s *Service) IssueCode(ctx context.Context, owner T.SecretToken, req T.CodeRequest) (T.CodeSecret, error) {
	var out T.CodeSecret
	if s == nil || T.Validate(req) != nil {
		return out, ErrDenied
	}
	err := s.native.WithBrowserGrant(ctx, owner, req, func(scope credentials.BrowserGrantScope) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return ErrDenied
		}

		if scope.InstallationID != s.connection.InstallationID || scope.Origin != s.connection.Origin || scope.BoardID != string(req.BoardID) || scope.Incarnation != string(req.Incarnation) || scope.GrantID != string(req.GrantID) || scope.ActorID == "" || scope.IssuedAt.IsZero() {
			return ErrDenied
		}
		now := scope.IssuedAt.UTC().Truncate(time.Second)
		c := grants.Credential{InstallationID: scope.InstallationID, Origin: scope.Origin, BoardID: scope.BoardID, Incarnation: scope.Incarnation, GrantID: scope.GrantID, ActorID: scope.ActorID}
		b := make([]byte, 16)
		if _, e := io.ReadFull(s.random, b); e != nil {
			return ErrDenied
		}
		secret := base64.RawURLEncoding.EncodeToString(b)
		h, _ := digest(secret, 16)
		if _, exists := s.codes[h]; exists {
			return ErrDenied
		}
		c.Mode = "browser"
		c.IssuedAt = now
		c.ExpiresAt = now.Add(8 * time.Hour)
		c.IdleExpiresAt = now.Add(30 * time.Minute)
		out = T.CodeSecret{APIVersion: 1, InstallationID: req.InstallationID, BoardID: req.BoardID, Incarnation: req.Incarnation, GrantID: req.GrantID, ActorID: A.UUID(c.ActorID), Code: T.SecretCode(secret), IssuedAt: stamp(now), ExpiresAt: stamp(now.Add(time.Minute))}
		if T.Validate(out) != nil {
			return ErrDenied
		}
		s.prune(now)
		s.codes[h] = code{c, now.Add(time.Minute)}
		return nil
	})
	if err != nil {
		if key, e := digest(string(out.Code), 16); e == nil {
			s.mu.Lock()
			delete(s.codes, key)
			s.mu.Unlock()
		}
		return T.CodeSecret{}, ErrDenied
	}
	return out, nil
}
func (s *Service) prune(now time.Time) {
	for h, c := range s.codes {
		if !now.Before(c.expires) {
			delete(s.codes, h)
		}
	}
	for h, c := range s.sessions {
		if !now.Before(c.ExpiresAt) || !now.Before(c.IdleExpiresAt) {
			delete(s.sessions, h)
		}
	}
}

type access struct {
	ctx context.Context
	c   *sql.Conn
}

func (a access) Scan(q string, args []any, dest ...any) error {
	return a.c.QueryRowContext(a.ctx, q, args...).Scan(dest...)
}
func (a access) Query(q string, args ...any) (*sql.Rows, error) {
	return a.c.QueryContext(a.ctx, q, args...)
}

func (s *Service) snapshot(ctx context.Context, fn func(writer.Read) error) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l, e := s.store.Reader(ctx)
	if e != nil {
		return ErrDenied
	}
	e = l.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return ErrDenied
		}
		if e := fn(access{ctx, c}); e != nil {
			return e
		}
		_, e := c.ExecContext(ctx, "COMMIT")
		return e
	})
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		return ErrDenied
	}
	return nil
}

func projection(ctx context.Context, r writer.Read, c grants.Credential, admission *grants.Service) (T.SessionRead, error) {
	var out T.SessionRead
	if admission == nil || admission.Require(ctx, r, c.BoardID, c.Incarnation, c.ActorID) != nil {
		return out, ErrDenied
	}
	var kind string
	if r.Scan("SELECT kind FROM actor WHERE incarnation=? AND actor_id=?", []any{c.Incarnation, c.ActorID}, &kind) != nil {
		return out, ErrDenied
	}
	rights := T.Rights{}
	rows, e := r.Query("SELECT right_name FROM grant_right WHERE incarnation=? AND grant_id=? ORDER BY right_name", c.Incarnation, c.GrantID)
	if e != nil {
		return out, ErrDenied
	}
	for rows.Next() {
		var right string
		if e = rows.Scan(&right); e != nil {
			break
		}
		rights = append(rights, right)
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return out, ErrDenied
	}
	out = T.SessionRead{APIVersion: 1, ActorID: A.UUID(c.ActorID), Kind: A.Kind(kind), Grant: T.SessionGrant{GrantID: A.UUID(c.GrantID), BoardID: A.UUID(c.BoardID), Incarnation: A.UUID(c.Incarnation), Rights: rights}}
	if T.Validate(out) != nil {
		return T.SessionRead{}, ErrDenied
	}
	return out, nil
}

// Exchange irreversibly consumes a matching code before current-grant checking.
// Concurrent attempts cannot both mint a bearer, including after delivery loss.
func (s *Service) Exchange(ctx context.Context, secret T.SecretCode) (T.ExchangeSecret, error) {
	var out T.ExchangeSecret
	if s == nil {
		return out, ErrDenied
	}
	h, e := digest(string(secret), 16)
	if e != nil {
		return out, ErrDenied
	}
	s.mu.Lock()
	c, ok := s.codes[h]
	delete(s.codes, h)
	now := s.now().UTC().Truncate(time.Second)
	closed := s.closed
	s.mu.Unlock()
	if closed || !ok || !now.Before(c.expires) || now.Before(c.credential.IssuedAt) || !s.native.Active() {
		return out, ErrDenied
	}
	credential := c.credential
	credential.IssuedAt = now
	credential.ExpiresAt = now.Add(8 * time.Hour)
	credential.IdleExpiresAt = now.Add(30 * time.Minute)
	admission, e := grants.New(s.connection.InstallationID, s.connection.Origin, func(context.Context, writer.Read, time.Time) (grants.Credential, error) {
		if !s.native.Active() {
			return grants.Credential{}, ErrDenied
		}
		return credential, nil
	})
	if e != nil {
		return out, ErrDenied
	}
	var view T.SessionRead
	if s.snapshot(ctx, func(r writer.Read) error { var e error; view, e = projection(ctx, r, credential, admission); return e }) != nil {
		return out, ErrDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.native.Active() {
		return out, ErrDenied
	}
	b := make([]byte, 32)
	if _, e = io.ReadFull(s.random, b); e != nil {
		return out, ErrDenied
	}
	bearer := base64.RawURLEncoding.EncodeToString(b)
	key, _ := digest(bearer, 32)
	if _, exists := s.sessions[key]; exists {
		return out, ErrDenied
	}
	out = T.ExchangeSecret{APIVersion: 1, Token: T.SecretToken(bearer), Session: view, IssuedAt: stamp(now), IdleExpiresAt: stamp(credential.IdleExpiresAt), ExpiresAt: stamp(credential.ExpiresAt)}
	if T.Validate(out) != nil {
		return T.ExchangeSecret{}, ErrDenied
	}
	s.prune(now)
	s.sessions[key] = credential
	return out, nil
}

func (s *Service) browser(ctx context.Context, r writer.Read, token T.SecretToken, now time.Time) (grants.Credential, error) {
	if ctx == nil || ctx.Err() != nil || r == nil || !s.native.Active() {
		return grants.Credential{}, ErrDenied
	}
	h, e := digest(string(token), 32)
	if e != nil {
		return grants.Credential{}, ErrDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.sessions[h]
	if s.closed || !ok || now.Before(c.IssuedAt) || !now.Before(c.ExpiresAt) || !now.Before(c.IdleExpiresAt) {
		delete(s.sessions, h)
		return grants.Credential{}, ErrDenied
	}
	c.IdleExpiresAt = now.Add(30 * time.Minute)
	if c.IdleExpiresAt.After(c.ExpiresAt) {
		c.IdleExpiresAt = c.ExpiresAt
	}
	s.sessions[h] = c
	return c, nil
}

// Admission rechecks the hash registry and current G01 rights for each callback
// in S02's supplied snapshot; no bearer stores rights or a client actor claim.
func (s *Service) Admission(mode string, token T.SecretToken) (*grants.Service, error) {
	if s == nil {
		return nil, ErrDenied
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrDenied
	}
	if mode == "cli" {
		return s.native.Admission(mode, token)
	}
	if mode != "browser" {
		return nil, ErrDenied
	}
	return grants.New(s.connection.InstallationID, s.connection.Origin, func(ctx context.Context, r writer.Read, now time.Time) (grants.Credential, error) {
		return s.browser(ctx, r, token, now)
	})
}
func (s *Service) Current(ctx context.Context, mode string, token T.SecretToken) (T.SessionRead, error) {
	var out T.SessionRead
	if s == nil {
		return out, ErrDenied
	}
	a, e := s.Admission(mode, token)
	if e != nil {
		return out, ErrDenied
	}
	e = s.snapshot(ctx, func(r writer.Read) error {
		var c grants.Credential
		var e error
		if mode == "cli" {
			c, e = s.native.VerifyCLI(ctx, r, token, s.now().UTC())
		} else {
			c, e = s.browser(ctx, r, token, s.now().UTC())
		}
		if e != nil {
			return ErrDenied
		}
		out, e = projection(ctx, r, c, a)
		return e
	})
	if e != nil {
		return T.SessionRead{}, ErrDenied
	}
	return out, nil
}
func (s *Service) Revoke(ctx context.Context, mode string, token T.SecretToken) error {
	if s == nil {
		return ErrDenied
	}
	if mode == "cli" {
		if s.native.SelfRevoke(ctx, token) == credentials.Confirmed {
			return nil
		}
		return ErrDenied
	}
	if mode != "browser" {
		return ErrDenied
	}
	if _, e := s.Current(ctx, mode, token); e != nil {
		return e
	}
	h, e := digest(string(token), 32)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[h]; !ok || s.closed {
		return ErrDenied
	}
	delete(s.sessions, h)
	return nil
}
