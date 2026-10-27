// Package cards composes card domain admission with current grant authority.
package cards

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

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
	Payload                       struct {
		Title, Description, Priority, Lane, Text *string
		OwnerID                                  contract.OwnerInput `json:"ownerId"`
	}
}

func decode(i contract.Intent) (envelope, error) {
	var e envelope
	err := json.Unmarshal(i.CanonicalBytes(), &e)
	return e, err
}
func sameOwner(a *string, b *A.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == string(*b)
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

// Callbacks captures a server-resolved route. G01 verifies live credentials and
// current rights on every invocation in the same S02 transaction snapshot.
func (s *Service) Callbacks(route Route) writer.Callbacks {
	if s == nil || s.grants == nil {
		return writer.Callbacks{}
	}
	admit := func(ctx context.Context, r writer.Read, i contract.Intent) (*T.RefusalResult, error) {
		e, err := decode(i)
		if err != nil || !schema.ValidUUID(route.BoardID) || !schema.ValidUUID(route.Incarnation) || e.BoardID != route.BoardID || e.Incarnation != route.Incarnation {
			return deny("INVALID_INPUT"), nil
		}
		if e.Command != "card.create" && e.Command != "card.edit" && e.Command != "comment.add" {
			return deny("INVALID_INPUT"), nil
		}
		if e.Command == "card.create" {
			if route.CardID != "" || e.Target != nil {
				return deny("INVALID_INPUT"), nil
			}
		} else if !schema.ValidUUID(route.CardID) || e.Target == nil || e.Target.CardID != route.CardID || e.Target.GrantID != "" {
			return deny("INVALID_INPUT"), nil
		}
		if d, err := s.grants.Admit(ctx, r, i); d != nil || err != nil {
			return d, err
		}
		if e.Payload.OwnerID.Present && e.Payload.OwnerID.Value != nil {
			var exists int
			if err := r.Scan("SELECT count(*) FROM actor WHERE incarnation=? AND actor_id=?", []any{e.Incarnation, *e.Payload.OwnerID.Value}, &exists); err != nil {
				return nil, err
			}
			if exists != 1 {
				return deny("INVALID_INPUT"), nil
			}
		}
		var event, ticket uint64
		if err := r.Scan("SELECT next_event,next_ticket FROM board WHERE singleton=1", nil, &event, &ticket); err != nil {
			return nil, err
		}
		if e.Command == "card.create" {
			if event >= contract.MaxInteger || ticket >= contract.MaxInteger {
				return deny("COUNTER_OVERFLOW"), nil
			}
			return nil, nil
		}
		var lane string
		var archived int
		if err := r.Scan("SELECT lane,archived FROM card WHERE incarnation=? AND card_id=?", []any{e.Incarnation, route.CardID}, &lane, &archived); err != nil {
			return nil, err
		}
		if lane == "Completed" {
			return deny("COMPLETED"), nil
		}
		if e.Command == "card.edit" {
			current, err := loadSnapshot(r, e, route.CardID)
			if err != nil {
				return nil, err
			}
			changed := e.Payload.OwnerID.Present && !sameOwner(e.Payload.OwnerID.Value, current.OwnerID)
			for _, f := range []struct {
				supplied *string
				current  string
			}{
				{e.Payload.Title, current.Title}, {e.Payload.Description, current.Description},
				{e.Payload.Priority, string(current.Priority)}, {e.Payload.Lane, string(current.Lane)},
			} {
				changed = changed || f.supplied != nil && *f.supplied != f.current
			}
			if !changed {
				return deny("NO_CHANGE"), nil
			}
		}
		if event >= contract.MaxInteger {
			return deny("COUNTER_OVERFLOW"), nil
		}
		if e.Command == "card.edit" && e.Payload.Lane != nil && *e.Payload.Lane != lane {
			if lane != "Ready for Work" || archived != 0 {
				return deny("INVALID_TRANSITION"), nil
			}
			var invalid int
			if err := r.Scan(`SELECT count(*) FROM link l LEFT JOIN card c ON c.incarnation=l.incarnation AND c.card_id=l.internal_target_id LEFT JOIN approval a ON a.incarnation=c.incarnation AND a.approval_id=c.current_approval_id AND a.card_id=c.card_id LEFT JOIN actor h ON h.incarnation=a.incarnation AND h.actor_id=a.actor_id JOIN policy p ON p.incarnation=l.incarnation WHERE l.incarnation=? AND l.card_id=? AND l.active=1 AND l.kind='blocking' AND (c.card_id IS NULL OR c.lane<>'Completed' OR c.archived<>0 OR a.approval_id IS NULL OR h.kind<>'human' OR a.policy_version<>p.policy_version OR a.reviewed_card_revision<>c.revision-1)`, []any{e.Incarnation, route.CardID}, &invalid); err != nil {
				return nil, err
			}
			if invalid != 0 {
				return deny("PREREQUISITE_NOT_APPROVED"), nil
			}
		}
		return nil, nil
	}
	return writer.Callbacks{Authority: s.grants.Authority, Admit: admit, Apply: func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		if d, err := admit(ctx, w, i); err != nil || d != nil {
			return writer.Effect{}, errors.New("card admission changed")
		}
		e, err := decode(i)
		if err != nil {
			return writer.Effect{}, err
		}
		o, err := outcome.Base(i)
		if err != nil {
			return writer.Effect{}, err
		}
		id := route.CardID
		revision := A.Positive(1)
		exec := func(q string, args ...any) error { _, err := w.Exec(q, args...); return err }
		entity := func(kind, id, card string) error {
			return exec("INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at) VALUES(?,?,?,?,?,?,?,?)", e.Incarnation, kind, id, card, e.BoardID, e.Incarnation, e.ActorID, string(at))
		}
		if e.Command == "card.create" {
			id, err = uuid()
			if err != nil {
				return writer.Effect{}, err
			}
			var ticket uint64
			if err = w.Scan("SELECT next_ticket FROM board WHERE singleton=1", nil, &ticket); err != nil {
				return writer.Effect{}, err
			}
			priority := "P3"
			if e.Payload.Priority != nil {
				priority = *e.Payload.Priority
			}
			if err = entity("card", id, id); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("UPDATE board SET next_ticket=next_ticket+1 WHERE singleton=1"); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,?,?)", e.Incarnation, ticket, id); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("INSERT INTO card(incarnation,card_id,revision,title,description,priority,owner_id) VALUES(?,?,1,?,?,?,?)", e.Incarnation, id, *e.Payload.Title, *e.Payload.Description, priority, e.Payload.OwnerID.Value); err != nil {
				return writer.Effect{}, err
			}
		} else {
			if err = w.Scan("SELECT revision FROM card WHERE incarnation=? AND card_id=?", []any{e.Incarnation, id}, &revision); err != nil {
				return writer.Effect{}, err
			}
			prior, loadErr := loadSnapshot(w, e, id)
			if loadErr != nil {
				return writer.Effect{}, loadErr
			}
			revision++
			if err = exec("UPDATE card SET revision=revision+1,current_approval_id=NULL WHERE incarnation=? AND card_id=?", e.Incarnation, id); err != nil {
				return writer.Effect{}, err
			}
			// The snapshot trigger requires that the current revision advances first.
			if err = writeSnapshot(w, e, id, revision-1, prior); err != nil {
				return writer.Effect{}, err
			}
			if e.Command == "card.edit" {
				if e.Payload.OwnerID.Present {
					if err = exec("UPDATE card SET owner_id=? WHERE incarnation=? AND card_id=?", e.Payload.OwnerID.Value, e.Incarnation, id); err != nil {
						return writer.Effect{}, err
					}
				}
				for _, f := range []struct {
					column string
					value  *string
				}{{"title", e.Payload.Title}, {"description", e.Payload.Description}, {"priority", e.Payload.Priority}, {"lane", e.Payload.Lane}} {
					if f.value != nil {
						if err = exec("UPDATE card SET "+f.column+"=? WHERE incarnation=? AND card_id=?", *f.value, e.Incarnation, id); err != nil {
							return writer.Effect{}, err
						}
					}
				}
			}
		}
		cardRevision := A.Number(revision)
		refs := []A.FactRef{{Kind: "card", ID: A.UUID(id), Revision: &cardRevision}}
		result := map[string]string{"cardId": id}
		if e.Command == "comment.add" {
			comment, err := uuid()
			if err != nil {
				return writer.Effect{}, err
			}
			if err = entity("comment", comment, id); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("INSERT INTO comment(incarnation,comment_id,card_id,actor_id,text,created_at) VALUES(?,?,?,?,?,?)", e.Incarnation, comment, id, e.ActorID, *e.Payload.Text, string(at)); err != nil {
				return writer.Effect{}, err
			}
			if err = exec("INSERT INTO current_reference VALUES(?,?,'comment',?)", e.Incarnation, id, comment); err != nil {
				return writer.Effect{}, err
			}
			result["commentId"] = comment
			refs = append(refs, A.FactRef{Kind: "comment", ID: A.UUID(comment)})
		}
		var next, rev uint64
		if err = w.Scan("SELECT next_event,revision FROM board WHERE singleton=1", nil, &next, &rev); err != nil {
			return writer.Effect{}, err
		}
		r := A.Number(revision)
		affected, _ := json.Marshal([]A.FactRef{{Kind: "card", ID: A.UUID(id), Revision: &r}})
		if err = exec("UPDATE board SET next_event=next_event+1 WHERE singleton=1"); err != nil {
			return writer.Effect{}, err
		}
		if err = exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,?,?,?,?,?,?,?)", e.Incarnation, e.BoardID, e.Incarnation, next, e.ActorID, string(o.OpID), string(e.Command), rev, string(at), string(affected)); err != nil {
			return writer.Effect{}, err
		}
		raw, err := json.Marshal(result)
		return writer.Effect{Result: raw, References: refs, CardRevision: &revision}, err
	}}
}
