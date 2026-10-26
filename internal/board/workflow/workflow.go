// Package workflow admits local links, review handoff and human approval.
package workflow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

type Route struct{ BoardID, Incarnation, CardID string }
type Service struct{ grants *grants.Service }

func New(g *grants.Service) *Service { return &Service{grants: g} }

type envelope struct {
	BoardID, Incarnation, ActorID string
	Command                       contract.Command
	Target                        *contract.Target
	ExpectedRevision              uint64
	ExpectedCardRevision          *uint64
	Payload                       struct {
		Kind, TargetCardID, LinkID string
		PolicyVersion              uint64
	}
}

func decode(i contract.Intent) (envelope, error) {
	var e envelope
	err := json.Unmarshal(i.CanonicalBytes(), &e)
	return e, err
}
func deny(code string) *T.RefusalResult {
	return &T.RefusalResult{Error: T.Error{Code: code, Message: "Mutation refused"}}
}
func uuid() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Callbacks binds an independently resolved owner/dependent card. Peer identity
// stays in the typed payload. G01 rechecks live credentials and current rights in
// the same serialized snapshot; no assignment or earlier check grants authority.
func (s *Service) Callbacks(route Route) writer.Callbacks {
	if s == nil || s.grants == nil {
		return writer.Callbacks{}
	}
	admit := func(ctx context.Context, r writer.Read, i contract.Intent) (*T.RefusalResult, error) {
		e, err := decode(i)
		if err != nil || !schema.ValidUUID(route.BoardID) || !schema.ValidUUID(route.Incarnation) || !schema.ValidUUID(route.CardID) || e.BoardID != route.BoardID || e.Incarnation != route.Incarnation || e.Target == nil || e.Target.CardID != route.CardID || e.Target.GrantID != "" || e.ExpectedCardRevision == nil {
			return deny("INVALID_INPUT"), nil
		}
		switch e.Command {
		case "link.add", "link.remove", "review-handoff", "approve":
		default:
			return deny("INVALID_INPUT"), nil
		}
		if d, err := s.grants.Admit(ctx, r, i); d != nil || err != nil {
			return d, err
		}
		var lane string
		if err := r.Scan("SELECT lane FROM card WHERE incarnation=? AND card_id=?", []any{e.Incarnation, route.CardID}, &lane); err != nil {
			return nil, err
		}
		if lane == "Completed" {
			return deny("COMPLETED"), nil
		}
		switch e.Command {
		case "review-handoff":
			if lane != "In Progress" {
				return deny("INVALID_TRANSITION"), nil
			}
		case "approve":
			if lane != "Ready for Review" {
				return deny("INVALID_TRANSITION"), nil
			}
			var kind string
			var policy uint64
			if err := r.Scan("SELECT a.kind,p.policy_version FROM actor a JOIN policy p ON p.incarnation=a.incarnation WHERE a.incarnation=? AND a.actor_id=?", []any{e.Incarnation, e.ActorID}, &kind, &policy); err != nil {
				return nil, err
			}
			if kind != "human" {
				return deny("FORBIDDEN"), nil
			}
			if policy != e.Payload.PolicyVersion {
				return deny("POLICY_CONFLICT"), nil
			}
		case "link.add":
			if !schema.ValidUUID(e.Payload.TargetCardID) || (e.Payload.Kind != "blocking" && e.Payload.Kind != "informational") {
				return deny("INVALID_INPUT"), nil
			}
			if e.Payload.TargetCardID == route.CardID {
				return deny("SELF_LINK"), nil
			}
			var n int
			if err := r.Scan("SELECT count(*) FROM card WHERE incarnation=? AND card_id=?", []any{e.Incarnation, e.Payload.TargetCardID}, &n); err != nil {
				return nil, err
			}
			if n != 1 {
				return deny("NOT_FOUND"), nil
			}
			if err := r.Scan("SELECT count(*) FROM link WHERE incarnation=? AND card_id=? AND kind=? AND target_board=? AND target_incarnation=? AND target_card_id=? AND active=1", []any{e.Incarnation, route.CardID, e.Payload.Kind, e.BoardID, e.Incarnation, e.Payload.TargetCardID}, &n); err != nil {
				return nil, err
			}
			if n != 0 {
				return deny("LINK_ALREADY_EXISTS"), nil
			}
			if e.Payload.Kind == "blocking" {
				// UNION deduplicates visited vertices, including legacy cycles. The
				// outer SELECT preserves the writer's read-only statement boundary.
				if err := r.Scan(`SELECT count(*) FROM (WITH RECURSIVE reachable(id) AS (SELECT ? UNION SELECT l.internal_target_id FROM link l JOIN reachable r ON l.card_id=r.id WHERE l.incarnation=? AND l.active=1 AND l.kind='blocking' AND l.internal_target_id IS NOT NULL) SELECT id FROM reachable WHERE id=?)`, []any{e.Payload.TargetCardID, e.Incarnation, route.CardID}, &n); err != nil {
					return nil, err
				}
				if n != 0 {
					return deny("BLOCKING_CYCLE"), nil
				}
			}
		case "link.remove":
			var owner string
			var active int
			if err := r.Scan("SELECT card_id,active FROM link WHERE incarnation=? AND link_id=?", []any{e.Incarnation, e.Payload.LinkID}, &owner, &active); err != nil {
				if err == sql.ErrNoRows {
					return deny("NOT_FOUND"), nil
				}
				return nil, err
			}
			if owner != route.CardID {
				return deny("INVALID_INPUT"), nil
			}
			if active == 0 {
				return deny("NOT_FOUND"), nil
			}
			var current int
			if err := r.Scan("SELECT count(*) FROM current_reference WHERE incarnation=? AND card_id=? AND kind='link' AND entity_id=?", []any{e.Incarnation, route.CardID, e.Payload.LinkID}, &current); err != nil {
				return nil, err
			}
			if current != 1 {
				return deny("NOT_FOUND"), nil
			}
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
	return writer.Callbacks{Authority: s.grants.Authority, Admit: admit, Apply: func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		if d, err := admit(ctx, w, i); err != nil || d != nil {
			return writer.Effect{}, errors.New("workflow admission changed")
		}
		e, err := decode(i)
		if err != nil {
			return writer.Effect{}, err
		}
		o, err := outcome.Base(i)
		if err != nil {
			return writer.Effect{}, err
		}
		exec := func(q string, args ...any) error { _, err := w.Exec(q, args...); return err }
		entity := func(kind, id string) error {
			return exec("INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at) VALUES(?,?,?,?,?,?,?,?)", e.Incarnation, kind, id, route.CardID, e.BoardID, e.Incarnation, e.ActorID, string(at))
		}
		prior, err := cards.LoadSnapshot(w, e.Incarnation, route.CardID)
		if err != nil {
			return writer.Effect{}, err
		}
		revision := A.Positive(*e.ExpectedCardRevision + 1)
		if err = exec("UPDATE card SET revision=revision+1,current_approval_id=NULL WHERE incarnation=? AND card_id=?", e.Incarnation, route.CardID); err != nil {
			return writer.Effect{}, err
		}
		if err = cards.WriteSnapshot(w, e.Incarnation, route.CardID, revision-1, prior); err != nil {
			return writer.Effect{}, err
		}
		r := A.Number(revision)
		refs := []A.FactRef{{Kind: "card", ID: A.UUID(route.CardID), Revision: &r}}
		result := map[string]string{"cardId": route.CardID}
		switch e.Command {
		case "link.add":
			id, err := uuid()
			if err != nil {
				return writer.Effect{}, err
			}
			if err = entity("link", id); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,?,?,?,?,?,1,?)", e.Incarnation, id, route.CardID, e.Payload.Kind, e.BoardID, e.Incarnation, e.Payload.TargetCardID, e.Payload.TargetCardID, string(at)); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("INSERT INTO current_reference VALUES(?,?,'link',?)", e.Incarnation, route.CardID, id); err != nil {
				return writer.Effect{}, err
			}
			result["linkId"] = id
			refs = append(refs, A.FactRef{Kind: "link", ID: A.UUID(id)})
		case "link.remove":
			if err = exec("UPDATE link SET active=0 WHERE incarnation=? AND link_id=?", e.Incarnation, e.Payload.LinkID); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("DELETE FROM current_reference WHERE incarnation=? AND card_id=? AND kind='link' AND entity_id=?", e.Incarnation, route.CardID, e.Payload.LinkID); err != nil {
				return writer.Effect{}, err
			}
			result["linkId"] = e.Payload.LinkID
			refs = append(refs, A.FactRef{Kind: "link", ID: A.UUID(e.Payload.LinkID)})
		case "review-handoff":
			if err = exec("UPDATE card SET lane='Ready for Review' WHERE incarnation=? AND card_id=?", e.Incarnation, route.CardID); err != nil {
				return writer.Effect{}, err
			}
		case "approve":
			id, err := uuid()
			if err != nil {
				return writer.Effect{}, err
			}
			if err = entity("approval", id); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,?,?,?,?)", e.Incarnation, id, route.CardID, e.ActorID, *e.ExpectedCardRevision, e.ExpectedRevision, e.Payload.PolicyVersion, string(at)); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("UPDATE card SET lane='Completed',current_approval_id=? WHERE incarnation=? AND card_id=?", id, e.Incarnation, route.CardID); err != nil {
				return writer.Effect{}, err
			}
			result["approvalId"] = id
			refs = append([]A.FactRef{{Kind: "approval", ID: A.UUID(id)}}, refs...)
		}
		var next, boardRevision uint64
		if err = w.Scan("SELECT next_event,revision FROM board WHERE singleton=1", nil, &next, &boardRevision); err != nil {
			return writer.Effect{}, err
		}
		affected, err := json.Marshal([]A.FactRef{{Kind: "card", ID: A.UUID(route.CardID), Revision: &r}})
		if err != nil {
			return writer.Effect{}, err
		}
		if err = exec("UPDATE board SET next_event=next_event+1 WHERE singleton=1"); err != nil {
			return writer.Effect{}, err
		}
		if err = exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,?,?,?,?,?,?,?)", e.Incarnation, e.BoardID, e.Incarnation, next, e.ActorID, string(o.OpID), string(e.Command), boardRevision, string(at), string(affected)); err != nil {
			return writer.Effect{}, err
		}
		raw, err := json.Marshal(result)
		return writer.Effect{Result: raw, References: refs, CardRevision: &revision}, err
	}}
}
