package attachments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/policy"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

// Detach removes only current membership. Snapshot and operation protection of
// the immutable attachment remain inside the existing writer/registry boundary.
func (s *Service) Detach(ctx context.Context, g *grants.Service, route Route, i contract.Intent) (writer.Result, bool) {
	if s == nil || s.store == nil || s.registry == nil || g == nil {
		return refused("UNAVAILABLE"), false
	}
	input, err := i.Input()
	p, ok := input.(*contract.AttachmentDetachInput)
	id, op, actor, target, _, contextErr := i.Context()
	if err != nil || !ok || contextErr != nil || i.Command() != "attachment.detach" || !schema.ValidUUID(route.BoardID) || !schema.ValidUUID(route.Incarnation) || !schema.ValidUUID(route.CardID) || id.BoardID != route.BoardID || id.Incarnation != route.Incarnation || target == nil || target.CardID != route.CardID || target.GrantID != "" || !schema.ValidUUID(p.AttachmentID) {
		return refused("INVALID_INPUT"), false
	}
	var guard *recovery.PinGuard
	defer func() {
		if guard != nil {
			guard.Close()
		}
	}()
	applied := false
	admit := func(ctx context.Context, r writer.Read, _ contract.Intent) (*T.RefusalResult, error) {
		if d, e := g.Admit(ctx, r, i); d != nil || e != nil {
			return d, e
		}
		var lane string
		if e := r.Scan("SELECT lane FROM card WHERE incarnation=? AND card_id=?", []any{route.Incarnation, route.CardID}, &lane); e != nil {
			return nil, e
		}
		wf, e := policy.Load(r, route.Incarnation)
		if e != nil {
			return nil, e
		}
		if A.WorkflowRole(wf, A.Lane(lane)) == "completed" {
			return deny("COMPLETED"), nil
		}
		var current int
		e = r.Scan("SELECT EXISTS(SELECT 1 FROM current_reference WHERE incarnation=a.incarnation AND card_id=a.card_id AND kind='attachment' AND entity_id=a.attachment_id) FROM attachment a WHERE a.incarnation=? AND a.card_id=? AND a.attachment_id=?", []any{route.Incarnation, route.CardID, p.AttachmentID}, &current)
		if errors.Is(e, sql.ErrNoRows) {
			return deny("NOT_FOUND"), nil
		}
		if e != nil {
			return nil, e
		}
		if current == 0 {
			return deny("NO_CHANGE"), nil
		}
		var next uint64
		if e = r.Scan("SELECT next_event FROM board WHERE singleton=1", nil, &next); e != nil {
			return nil, e
		}
		if next >= contract.MaxInteger {
			return deny("COUNTER_OVERFLOW"), nil
		}
		guard, e = s.registry.Guard(ctx)
		return nil, e
	}
	cb := writer.Callbacks{Authority: g.Authority, Admit: admit, Apply: func(ctx context.Context, w writer.Write, _ contract.Intent, at A.Timestamp) (writer.Effect, error) {
		prior, e := cards.LoadSnapshot(w, route.Incarnation, route.CardID)
		if e != nil {
			return writer.Effect{}, e
		}
		var rev A.Positive
		if e = w.Scan("SELECT revision FROM card WHERE incarnation=? AND card_id=?", []any{route.Incarnation, route.CardID}, &rev); e != nil {
			return writer.Effect{}, e
		}
		exec := func(q string, args ...any) error { _, e := w.Exec(q, args...); return e }
		if e = exec("UPDATE card SET revision=revision+1,current_approval_id=NULL WHERE incarnation=? AND card_id=?", route.Incarnation, route.CardID); e != nil {
			return writer.Effect{}, e
		}
		// The prior state was captured above. Advance the revision for the
		// snapshot_earlier invariant, then pin every prior reference before removal.
		if e = cards.WriteSnapshot(w, route.Incarnation, route.CardID, rev, prior); e != nil {
			return writer.Effect{}, e
		}
		if e = policy.PinCurrent(w, route.Incarnation, route.CardID); e != nil {
			return writer.Effect{}, e
		}
		if e = exec("DELETE FROM current_attachment_pin WHERE incarnation=? AND attachment_id=?", route.Incarnation, p.AttachmentID); e != nil {
			return writer.Effect{}, e
		}
		// The exact-reference FK clears this card's cover only if it names
		// this attachment. The prior choice and pins are already captured.
		if e = exec("DELETE FROM current_reference WHERE incarnation=? AND card_id=? AND kind='attachment' AND entity_id=?", route.Incarnation, route.CardID, p.AttachmentID); e != nil {
			return writer.Effect{}, e
		}
		rev++
		cardRev := A.Number(rev)
		refs := []A.FactRef{{Kind: "attachment", ID: A.UUID(p.AttachmentID)}, {Kind: "card", ID: A.UUID(route.CardID), Revision: &cardRev}}
		affected, e := json.Marshal(refs)
		if e != nil {
			return writer.Effect{}, e
		}
		var event, boardRev uint64
		if e = w.Scan("SELECT next_event,revision FROM board WHERE singleton=1", nil, &event, &boardRev); e != nil {
			return writer.Effect{}, e
		}
		if e = exec("UPDATE board SET next_event=next_event+1 WHERE singleton=1"); e != nil {
			return writer.Effect{}, e
		}
		if e = exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,?,?,?,'attachment.detach',?,?,?)", route.Incarnation, route.BoardID, route.Incarnation, event, actor, op, boardRev, string(at), string(affected)); e != nil {
			return writer.Effect{}, e
		}
		result, e := json.Marshal(map[string]string{"cardId": route.CardID, "attachmentId": p.AttachmentID})
		applied = e == nil
		return writer.Effect{Result: result, References: refs, CardRevision: &rev}, e
	}}
	result := writer.New(s.store).Submit(ctx, i, cb)
	return result, applied && result.Terminal != nil && result.Terminal.Outcome == "committed"
}
