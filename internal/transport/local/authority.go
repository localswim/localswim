package local

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/writer"
)

type authority struct {
	mu          sync.Mutex
	store       *S.Store
	id          identity
	origin      string
	closed      bool
	credentials map[[32]byte]grants.Credential
}

func newAuthority(s *S.Store, id identity, origin string, bot T.SecretToken) *authority {
	a := &authority{store: s, id: id, origin: origin, credentials: map[[32]byte]grants.Credential{}}
	now := time.Now().UTC()
	a.credentials[sha256.Sum256([]byte(bot))] = grants.Credential{InstallationID: id.InstallationID, Origin: origin, BoardID: id.BoardID, Incarnation: id.Incarnation, ActorID: id.BotID, GrantID: id.BotGrant, Mode: "cli", IssuedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	return a
}
func (a *authority) close() { a.mu.Lock(); defer a.mu.Unlock(); a.closed = true; clear(a.credentials) }
func (a *authority) browser() (T.SecretToken, error) {
	t, e := token()
	if e != nil {
		return "", e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", errLocal
	}
	now := time.Now().UTC()
	for k, c := range a.credentials {
		if !now.Before(c.ExpiresAt) || (c.Mode == "browser" && !now.Before(c.IdleExpiresAt)) {
			delete(a.credentials, k)
		}
	}
	if len(a.credentials) >= 128 {
		return "", errLocal
	}
	a.credentials[sha256.Sum256([]byte(t))] = grants.Credential{InstallationID: a.id.InstallationID, Origin: a.origin, BoardID: a.id.BoardID, Incarnation: a.id.Incarnation, ActorID: a.id.HumanID, GrantID: a.id.HumanGrant, Mode: "browser", IssuedAt: now, ExpiresAt: now.Add(8 * time.Hour), IdleExpiresAt: now.Add(30 * time.Minute)}
	return t, nil
}
func (a *authority) verify(mode string, t T.SecretToken, now time.Time) (grants.Credential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := sha256.Sum256([]byte(t))
	c, ok := a.credentials[key]
	if a.closed || !ok || c.Mode != mode || now.Before(c.IssuedAt) || !now.Before(c.ExpiresAt) || (mode == "browser" && !now.Before(c.IdleExpiresAt)) {
		return grants.Credential{}, grants.ErrDenied
	}
	if mode == "browser" {
		c.IdleExpiresAt = now.Add(30 * time.Minute)
		if c.IdleExpiresAt.After(c.ExpiresAt) {
			c.IdleExpiresAt = c.ExpiresAt
		}
		a.credentials[key] = c
	}
	return c, nil
}
func (a *authority) Admission(mode string, t T.SecretToken) (*grants.Service, error) {
	if mode != "cli" && mode != "browser" {
		return nil, grants.ErrDenied
	}
	return grants.New(a.id.InstallationID, a.origin, func(ctx context.Context, r writer.Read, now time.Time) (grants.Credential, error) {
		if ctx == nil || ctx.Err() != nil || r == nil {
			return grants.Credential{}, grants.ErrDenied
		}
		return a.verify(mode, t, now)
	})
}

type snapshot struct {
	ctx context.Context
	c   *sql.Conn
}

func (s snapshot) Scan(q string, args []any, dest ...any) error {
	return s.c.QueryRowContext(s.ctx, q, args...).Scan(dest...)
}
func (s snapshot) Query(q string, args ...any) (*sql.Rows, error) {
	return s.c.QueryContext(s.ctx, q, args...)
}
func (a *authority) Current(ctx context.Context, mode string, t T.SecretToken) (T.SessionRead, error) {
	var out T.SessionRead
	if ctx == nil || ctx.Err() != nil {
		return out, grants.ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	admission, e := a.Admission(mode, t)
	if e != nil {
		return out, e
	}
	l, e := a.store.Reader(ctx)
	if e != nil {
		return out, e
	}
	e = l.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		credential, e := a.verify(mode, t, time.Now().UTC())
		if e != nil {
			return e
		}
		rd := snapshot{ctx, c}
		if e = admission.Require(ctx, rd, a.id.BoardID, a.id.Incarnation, credential.ActorID); e != nil {
			return e
		}
		var kind string
		if e = rd.Scan("SELECT kind FROM actor WHERE incarnation=? AND actor_id=?", []any{a.id.Incarnation, credential.ActorID}, &kind); e != nil {
			return e
		}
		rows, e := rd.Query("SELECT right_name FROM grant_right WHERE incarnation=? AND grant_id=? ORDER BY right_name", a.id.Incarnation, credential.GrantID)
		if e != nil {
			return e
		}
		rights := T.Rights{}
		for rows.Next() {
			var right string
			if e = rows.Scan(&right); e != nil {
				break
			}
			rights = append(rights, right)
		}
		if e = errors.Join(e, rows.Err(), rows.Close()); e != nil {
			return e
		}
		out = T.SessionRead{APIVersion: 1, ActorID: A.UUID(credential.ActorID), Kind: A.Kind(kind), Grant: T.SessionGrant{GrantID: A.UUID(credential.GrantID), BoardID: A.UUID(a.id.BoardID), Incarnation: A.UUID(a.id.Incarnation), Rights: rights}}
		if e = T.Validate(out); e != nil {
			return e
		}
		_, e = c.ExecContext(ctx, "COMMIT")
		return e
	})
	e = errors.Join(e, l.Close())
	if e != nil {
		return T.SessionRead{}, grants.ErrDenied
	}
	return out, nil
}
