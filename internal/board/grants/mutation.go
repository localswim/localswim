package grants

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

// Route is selected independently by H01: create's validated board scope, or
// revoke's stored grant scope AND route target. It must never be copied from an
// unvalidated submitted semantic envelope. Each callback rechecks stored scope.
type Route struct {
	BoardID, Incarnation, GrantID string
}

type envelope struct {
	BoardID, Incarnation, ActorID string
	Command                       contract.Command
	Target                        *contract.Target
	ExpectedRevision              uint64
	ExpectedCardRevision          *uint64
	Payload                       struct {
		GranteeActorID string   `json:"granteeActorId"`
		Kind           string   `json:"kind"`
		Rights         []string `json:"rights"`
	}
}

func decode(i contract.Intent) (envelope, error) {
	var e envelope
	err := json.Unmarshal(i.CanonicalBytes(), &e)
	if err != nil || e.Command != i.Command() {
		return e, ErrDenied
	}
	return e, nil
}

// Admit checks current command rights only. Consumers must compose their own
// domain invariants. Grant mutations must use GrantCallbacks for exact scope and
// target checks plus effect generation.
func (s *Service) Admit(ctx context.Context, r writer.Read, i contract.Intent) (*T.RefusalResult, error) {
	e, err := decode(i)
	if err != nil {
		return deny("INVALID_INPUT"), nil
	}
	p, err := s.current(ctx, r, e.BoardID, e.Incarnation, e.ActorID)
	if err != nil {
		return deny("FORBIDDEN"), nil
	}
	rights := commandRights(e.Command)
	if rights == nil {
		return deny("FORBIDDEN"), nil
	}
	if p.credential.Mode == "native-owner" {
		m := &writer.OperationIdentity{Command: e.Command, Target: e.Target, Preconditions: contract.Preconditions{ExpectedRevision: e.ExpectedRevision, ExpectedCardRevision: e.ExpectedCardRevision}}
		if administrative(m) {
			return nil, nil
		}
		return deny("FORBIDDEN"), nil
	}
	if !has(p, rights...) {
		return deny("FORBIDDEN"), nil
	}
	return nil, nil
}

// GrantCallbacks binds the independently resolved route by value. S02 owns exact
// board CAS, ledger identity, atomic COMMIT, refusal persistence and reconciliation.
// These callbacks never issue a credential or infer a native bootstrap actor.
func (s *Service) GrantCallbacks(route Route) writer.Callbacks {
	admit := func(ctx context.Context, r writer.Read, i contract.Intent) (*T.RefusalResult, error) {
		e, err := decode(i)
		if err != nil || !schema.ValidUUID(route.BoardID) || !schema.ValidUUID(route.Incarnation) || e.BoardID != route.BoardID || e.Incarnation != route.Incarnation || e.ExpectedCardRevision != nil {
			return deny("INVALID_INPUT"), nil
		}
		if refusal, err := s.Admit(ctx, r, i); refusal != nil || err != nil {
			return refusal, err
		}
		switch e.Command {
		case "grant.create":
			if route.GrantID != "" || e.Target != nil || !schema.ValidUUID(e.Payload.GranteeActorID) || ValidateRights(e.Payload.Kind, e.Payload.Rights) != nil {
				return deny("INVALID_INPUT"), nil
			}
			var kind string
			err := r.Scan("SELECT kind FROM actor WHERE incarnation=? AND actor_id=?", []any{e.Incarnation, e.Payload.GranteeActorID}, &kind)
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			if err == nil && kind != e.Payload.Kind {
				return deny("INVALID_INPUT"), nil
			}
		case "grant.revoke":
			if !schema.ValidUUID(route.GrantID) || e.Target == nil || e.Target.CardID != "" || e.Target.GrantID != route.GrantID {
				return deny("INVALID_INPUT"), nil
			}
			var enabled int
			if err := r.Scan("SELECT enabled FROM grant_record WHERE incarnation=? AND grant_id=?", []any{e.Incarnation, route.GrantID}, &enabled); err != nil {
				if err == sql.ErrNoRows {
					return deny("NOT_FOUND"), nil
				}
				return nil, err
			}
			if enabled != 1 {
				return deny("FORBIDDEN"), nil
			}
		default:
			return deny("INVALID_INPUT"), nil
		}
		var next uint64
		if err := r.Scan("SELECT next_event FROM board WHERE singleton=1", nil, &next); err != nil {
			return nil, err
		}
		if next >= contract.MaxInteger {
			return deny("COUNTER_OVERFLOW"), nil
		}
		return nil, nil
	}
	return writer.Callbacks{Authority: s.Authority, Admit: admit, Apply: func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		if refused, err := admit(ctx, w, i); err != nil || refused != nil {
			return writer.Effect{}, ErrDenied
		}
		e, err := decode(i)
		if err != nil {
			return writer.Effect{}, err
		}
		o, err := outcome.Base(i)
		if err != nil {
			return writer.Effect{}, err
		}
		id := route.GrantID
		if e.Command == "grant.create" {
			var b [16]byte
			if _, err := rand.Read(b[:]); err != nil {
				return writer.Effect{}, err
			}
			b[6] = b[6]&15 | 64
			b[8] = b[8]&63 | 128
			id = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
			var count int
			if err := w.Scan("SELECT count(*) FROM actor WHERE incarnation=? AND actor_id=?", []any{e.Incarnation, e.Payload.GranteeActorID}, &count); err != nil {
				return writer.Effect{}, err
			}
			if count == 0 {
				if _, err := w.Exec("INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES(?,?,?,'',?,?)", e.Incarnation, e.Payload.GranteeActorID, e.Payload.Kind, e.BoardID, e.Incarnation); err != nil {
					return writer.Effect{}, err
				}
			}
			for _, q := range []struct {
				query string
				args  []any
			}{
				{"INSERT INTO grant_fact(incarnation,grant_id) VALUES(?,?)", []any{e.Incarnation, id}},
				{"INSERT INTO grant_record(incarnation,grant_id,actor_id,enabled) VALUES(?,?,?,1)", []any{e.Incarnation, id, e.Payload.GranteeActorID}},
			} {
				if _, err := w.Exec(q.query, q.args...); err != nil {
					return writer.Effect{}, err
				}
			}
			for _, right := range e.Payload.Rights {
				if _, err := w.Exec("INSERT INTO grant_right(incarnation,grant_id,right_name) VALUES(?,?,?)", e.Incarnation, id, right); err != nil {
					return writer.Effect{}, err
				}
			}
		} else {
			if _, err := w.Exec("UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=? AND enabled=1", e.Incarnation, id); err != nil {
				return writer.Effect{}, err
			}
		}
		var next, rev uint64
		if err := w.Scan("SELECT next_event,revision FROM board WHERE singleton=1", nil, &next, &rev); err != nil {
			return writer.Effect{}, err
		}
		if _, err := w.Exec("UPDATE board SET next_event=next_event+1 WHERE singleton=1"); err != nil {
			return writer.Effect{}, err
		}
		// affected lists card IDs; a grant mutation affects no cards.
		if _, err := w.Exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,?,?,?,?,?,?,'[]')", e.Incarnation, e.BoardID, e.Incarnation, next, e.ActorID, string(o.OpID), string(e.Command), rev, string(at)); err != nil {
			return writer.Effect{}, err
		}
		result, err := json.Marshal(struct {
			GrantID string `json:"grantId"`
		}{id})
		return writer.Effect{Result: result, References: []A.FactRef{{Kind: "grant", ID: A.UUID(id)}}}, err
	}}
}
