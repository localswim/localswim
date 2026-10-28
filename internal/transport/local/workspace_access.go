package local

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/store/writer"
)

// WorkspaceIdentityCurrent checks an already owned member without interpreting
// its configured grants or minting any capability. Admission keeps native pins
// owned until this check returns. The caller must also verify registry roots.
func (r *Runtime) WorkspaceIdentityCurrent(expected BoardIdentity) bool {
	r.reqMu.Lock()
	if r.closing {
		r.reqMu.Unlock()
		return false
	}
	r.requests.Add(1)
	r.reqMu.Unlock()
	defer r.requests.Done()
	return r.Identity() == expected && r.validateIdentity()
}

// ConfiguredReadAllowed projects no data and mints no credential. The already
// authenticated workspace channel selects only this engine's immutable local
// human or automation grant; current enabled state and read rights are resolved
// by the same Go grant admission used for board reads, in one current snapshot.
func (r *Runtime) ConfiguredReadAllowed(ctx context.Context, channel string) bool {
	return r.withConfiguredRead(ctx, channel, nil) == nil
}

// withConfiguredRead keeps current grant admission and any projection in one
// physical read transaction. It admits an engine request before native identity
// IO or reader acquisition, so Close cannot release an admitted read. The budget
// bounds context-aware SQL/waits; it does not claim to interrupt native IO.
func (r *Runtime) withConfiguredRead(ctx context.Context, channel string, project func(writer.Read) error) error {
	if ctx == nil || ctx.Err() != nil || (channel != "browser" && channel != "cli") {
		return grants.ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	r.reqMu.Lock()
	if r.closing {
		r.reqMu.Unlock()
		return grants.ErrDenied
	}
	r.requests.Add(1)
	r.reqMu.Unlock()
	defer r.requests.Done()
	if !r.validateIdentity() {
		return grants.ErrDenied
	}
	actor, grant := r.id.BotID, r.id.BotGrant
	if channel == "browser" {
		actor, grant = r.id.HumanID, r.id.HumanGrant
	}
	admission, e := grants.New(r.id.InstallationID, r.Origin(), func(ctx context.Context, rd writer.Read, now time.Time) (grants.Credential, error) {
		idle := time.Time{}
		if channel == "browser" {
			idle = now.Add(time.Minute)
		}
		return grants.Credential{InstallationID: r.id.InstallationID, Origin: r.Origin(), BoardID: r.id.BoardID, Incarnation: r.id.Incarnation, ActorID: actor, GrantID: grant, Mode: channel, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), IdleExpiresAt: idle}, nil
	})
	if e != nil {
		return e
	}
	lease, e := r.store.Reader(ctx)
	if e != nil {
		return e
	}
	e = lease.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		rd := snapshot{ctx, c}
		if e := admission.Require(ctx, rd, r.id.BoardID, r.id.Incarnation, actor, "read"); e != nil {
			return e
		}
		if project != nil {
			if e := project(rd); e != nil {
				return e
			}
		}
		if !r.validateIdentity() {
			return grants.ErrDenied
		}
		_, e := c.ExecContext(ctx, "COMMIT")
		return e
	})
	return errors.Join(e, lease.Close())
}

// OpenWorkspaceEngine keeps the board's owned lifetime independent of the
// bounded activation attempt. Canceling a completed attempt never closes an
// attached engine or releases an admitted writer.
func OpenWorkspaceEngine(lifetime, acquisition context.Context, o EngineOptions) (*Runtime, error) {
	if lifetime == nil || lifetime.Err() != nil || acquisition == nil || acquisition.Err() != nil || !uuidV4(o.WorkspaceID) || !canonicalOrigin(o.Origin) {
		return nil, errLocal
	}
	r, e := openEngine(acquisition, o, nil)
	if e == nil {
		r.watch(lifetime)
	}
	return r, e
}
