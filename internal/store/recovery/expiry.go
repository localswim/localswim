package recovery

import (
	"context"
	"database/sql"
	"errors"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

// MaintenancePrincipal is supplied only by trusted internal server lifecycle
// wiring. It identifies a preexisting automation actor AND its exact legitimate
// system label. It is not a credential, grant, user claim or general permission.
type MaintenancePrincipal struct{ ActorID, Label string }
type MaintenanceVerifier func(context.Context, writer.Read) (MaintenancePrincipal, error)
type MaintenanceResult struct {
	Revision *A.Number
	Unknown  bool
}

// Expire applies only the currently persisted retention policy. Production P01
// must explicitly supply a trusted verifier and legitimate system attribution;
// there is no default scheduler, actor bootstrap or human/native-owner bypass.
// A COMMIT/cleanup uncertainty fences this Store, without automatic retry.
func (s *Service) Expire(ctx context.Context, route Route, verify MaintenanceVerifier) (MaintenanceResult, error) {
	unknown := MaintenanceResult{Unknown: true}
	if s == nil || ctx == nil || verify == nil || !schema.ValidUUID(route.BoardID) || !schema.ValidUUID(route.Incarnation) {
		return MaintenanceResult{}, errors.New("trusted maintenance context required")
	}
	ctx, cancel := context.WithTimeout(ctx, transactionLimit)
	defer cancel()
	l, e := s.store.Writer(ctx)
	if e != nil {
		return unknown, e
	}
	g, e := s.registry.Guard(ctx)
	if e != nil {
		l.Close()
		return unknown, e
	}
	defer g.Close()
	var committed A.Number
	attempted := false
	e = l.Do(ctx, func(c *sql.Conn) (err error) {
		defer func() {
			if recover() != nil {
				err = errors.New("maintenance callback failed")
			}
		}()
		if _, e := c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
			return e
		}
		w := writeAccess{readAccess{ctx, c}}
		b, e := board(w)
		if e != nil {
			return e
		}
		if b.identity.BoardID != A.UUID(route.BoardID) || b.identity.Incarnation != A.UUID(route.Incarnation) {
			return errors.New("maintenance identity mismatch")
		}
		p, e := verify(ctx, w.readAccess)
		if e != nil || !schema.ValidUUID(p.ActorID) || p.Label == "" {
			return errors.New("maintenance authority unavailable")
		}
		var kind, label string
		if e = w.Scan("SELECT kind,label FROM actor WHERE incarnation=? AND actor_id=?", []any{route.Incarnation, p.ActorID}, &kind, &label); e != nil || kind != "automation" || label != p.Label {
			return errors.New("maintenance attribution unavailable")
		}
		at, e := sampled(s.now)
		if e != nil {
			return e
		}
		input, e := A.Canonical(b.retention)
		if e != nil {
			return e
		}
		pins, e := g.Facts()
		if e != nil {
			return e
		}
		// Selection reuses the exact policy/expiry predicate, but maintenance
		// neither changes the policy nor increments its retention version.
		// Max retentionVersion must not disable ordinary expiry.
		sel, e := selectRecords(w, b, "retention.set", input, at, pins, false)
		if e != nil {
			return e
		}
		if e = s.inject("maintenance-before-prune"); e != nil {
			return e
		}
		if e = exec(w, "UPDATE board SET revision=revision+1 WHERE singleton=1"); e != nil {
			return e
		}
		if e = prune(w, sel, at, "expired", pins); e != nil {
			return e
		}
		b.revision++
		refRev := b.revision
		ref := A.FactRef{Kind: "board", ID: b.identity.BoardID, Revision: &refRev}
		if e = activity(w, b, p.ActorID, nil, "recovery.expire", at, []A.FactRef{ref}); e != nil {
			return e
		}
		if e = schema.Validate(ctx, c, schema.Identity{BoardID: route.BoardID, Incarnation: route.Incarnation}); e != nil {
			return e
		}
		if e = s.inject("maintenance-before-commit"); e != nil {
			return e
		}
		attempted = true
		if _, e = c.ExecContext(ctx, "COMMIT"); e != nil {
			return e
		}
		committed = b.revision
		return s.inject("maintenance-after-commit")
	})
	// Lease.Close performs an independent bounded rollback. A failed cleanup
	// fences internally. No failed transaction is returned to the pool.
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		if attempted || closeErr != nil {
			s.store.Fence()
		}
		return unknown, errors.New("maintenance outcome unavailable")
	}
	return MaintenanceResult{Revision: &committed}, nil
}
func (s *Service) inject(stage string) error {
	if s.fault != nil {
		return s.fault(stage)
	}
	return nil
}
