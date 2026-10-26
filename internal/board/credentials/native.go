package credentials

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

// NativeSubmission distinguishes attribution preparation from the board result.
// A failed/uncertain preparation never dispatches Submit and proves no board
// outcome. Successful preparation does not imply a grant committed.
type NativeSubmission struct {
	Preparation Status
	Result      *writer.Result
}

type nativeIntent struct {
	BoardID, Incarnation, ActorID string
	Command                       contract.Command
	Target                        *contract.Target
	ExpectedCardRevision          *uint64
}

func nativeEnvelope(i contract.Intent, route grants.Route) (nativeIntent, error) {
	var e nativeIntent
	if json.Unmarshal(i.CanonicalBytes(), &e) != nil || e.Command != i.Command() || e.BoardID != route.BoardID || e.Incarnation != route.Incarnation || e.ExpectedCardRevision != nil {
		return e, ErrDenied
	}
	if e.Command == "grant.create" && e.Target == nil && route.GrantID == "" {
		return e, nil
	}
	if e.Command == "grant.revoke" && e.Target != nil && e.Target.CardID == "" && e.Target.GrantID == route.GrantID && route.GrantID != "" {
		return e, nil
	}
	return e, ErrDenied
}

// SubmitNative is the explicit first-owner attribution preparation and grant
// submission composition. Its input MUST be an already strictly frozen Intent
// for the independently resolved grant route. No raw actor claim is authority.
// The same real N01 lifetime lease is held through preparation AND S02 Submit;
// native is always acquired before the board writer. Authority reloads again in
// every S02 G01 callback; preparation never caches command rights or human proof.
func (s *Service) SubmitNative(ctx context.Context, token T.SecretToken, i contract.Intent, route grants.Route) NativeSubmission {
	if s == nil {
		return NativeSubmission{Preparation: Unavailable}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, err := nativeEnvelope(i, route)
	if err != nil {
		return NativeSubmission{Preparation: Refused}
	}
	status := s.prepare(ctx, token, e)
	if status != Confirmed {
		return NativeSubmission{Preparation: status}
	}
	admission, err := s.admission("native-owner", token, false)
	if err != nil {
		return NativeSubmission{Preparation: Confirmed}
	}
	result := writer.New(s.store).Submit(ctx, i, admission.GrantCallbacks(route))
	return NativeSubmission{Preparation: Confirmed, Result: &result}
}

func (s *Service) prepare(ctx context.Context, token T.SecretToken, e nativeIntent) Status {
	var kind, label, originBoard, originInc string
	return s.transaction(ctx, func(ctx context.Context, c *sql.Conn) error {
		n, err := s.native(token, s.now().UTC())
		if err != nil || e.ActorID != n.ActorID {
			return ErrDenied
		}
		var b, i string
		if err = c.QueryRowContext(ctx, "SELECT board_id,incarnation FROM board WHERE singleton=1").Scan(&b, &i); err != nil || b != e.BoardID || i != e.Incarnation {
			return ErrDenied
		}
		err = c.QueryRowContext(ctx, "SELECT kind,label,origin_board,origin_incarnation FROM actor WHERE incarnation=? AND actor_id=?", i, n.ActorID).Scan(&kind, &label, &originBoard, &originInc)
		if err == nil {
			// Preserve original historical provenance and label verbatim. An
			// existing automation cannot become the administrative human actor.
			if kind != "human" {
				return ErrDenied
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		kind, label, originBoard, originInc = "human", "native owner", b, i
		_, err = c.ExecContext(ctx, "INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES(?,?,?,?,?,?)", i, n.ActorID, kind, label, originBoard, originInc)
		return err
	}, func(ctx context.Context, c *sql.Conn) error {
		var k, l, b, i string
		err := c.QueryRowContext(ctx, "SELECT kind,label,origin_board,origin_incarnation FROM actor WHERE incarnation=? AND actor_id=?", e.Incarnation, e.ActorID).Scan(&k, &l, &b, &i)
		if err != nil || k != kind || l != label || b != originBoard || i != originInc {
			return ErrDenied
		}
		return nil
	})
}

// NativeLookup never prepares attribution or creates a board fact. The immutable
// evidence's exact original installation/origin is checked before G01/S02 can
// inspect a retained full/compact receipt. G01 permits only this current owner's
// own grant.create/revoke outcome in the current exact board/incarnation.
func (s *Service) NativeLookup(ctx context.Context, token T.SecretToken, installation, origin string, k writer.LookupIdentity) writer.Result {
	if s == nil {
		return writer.New(nil).Lookup(ctx, k, nil)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if installation != s.context.InstallationID || origin != s.context.Origin {
		return writer.New(nil).Lookup(ctx, k, nil)
	}
	a, err := s.admission("native-owner", token, false)
	if err != nil {
		return writer.New(nil).Lookup(ctx, k, nil)
	}
	return writer.New(s.store).Lookup(ctx, k, a.Authority)
}
