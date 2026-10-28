package local

import (
	"context"
	"database/sql"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/store/writer"
)

// ConfiguredReadAllowed projects no data and mints no credential. The already
// authenticated workspace channel selects only this engine's immutable local
// human or automation grant; current enabled state and read rights are resolved
// by the same Go grant admission used for board reads, in one current snapshot.
func (r *Runtime) ConfiguredReadAllowed(ctx context.Context, channel string) bool {
	if ctx == nil || ctx.Err() != nil || (channel != "browser" && channel != "cli") {
		return false
	}
	r.reqMu.Lock()
	if r.closing {
		r.reqMu.Unlock()
		return false
	}
	r.requests.Add(1)
	r.reqMu.Unlock()
	defer r.requests.Done()
	if !r.validateIdentity() {
		return false
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
		return false
	}
	lease, e := r.store.Reader(ctx)
	if e != nil {
		return false
	}
	e = lease.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		if e := admission.Require(ctx, snapshot{ctx, c}, r.id.BoardID, r.id.Incarnation, actor, "read"); e != nil {
			return e
		}
		_, e := c.ExecContext(ctx, "COMMIT")
		return e
	})
	ce := lease.Close()
	return e == nil && ce == nil
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
