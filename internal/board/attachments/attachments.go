// Package attachments commits immutable attachment associations and raw BLOBs.
package attachments

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

const MaxFileBytes = 10 * 1024 * 1024

var ErrIntegrity = errors.New("attachment integrity unavailable")
var ErrUnavailable = errors.New("attachment unavailable")
var ErrNotFound = errors.New("attachment not found")

// Route is resolved from the current authority and the exact HTTP route.
type Route struct{ BoardID, Incarnation, CardID string }

type Service struct {
	store    *S.Store
	registry *recovery.Registry
}

func New(s *S.Store) *Service { return &Service{store: s, registry: recovery.RegistryFor(s)} }

func refused(code string) writer.Result {
	return writer.Result{Refused: &T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}}}
}
func deny(code string) *T.RefusalResult {
	return &T.RefusalResult{Error: T.Error{Code: code, Message: "Mutation refused"}}
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func uuid() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Add verifies its private immutable upload buffer before writer checkout.
// The registry guard is acquired only by admission inside S02's writer slot and
// survives Submit's COMMIT cleanup and readback. Retained receipts skip admission,
// so replay needs neither new quota nor mutable card state checks.
func (s *Service) Add(ctx context.Context, g *grants.Service, route Route, i contract.Intent, upload *Upload) (writer.Result, bool) {
	if s == nil || s.store == nil || s.registry == nil || g == nil {
		return refused("UNAVAILABLE"), false
	}
	in, err := i.Input()
	p, ok := in.(*contract.AttachmentInput)
	id, op, actor, target, _, contextErr := i.Context()
	if upload == nil || upload.Len() > MaxFileBytes {
		return refused("INVALID_INPUT"), false
	}
	data := upload.data
	if err != nil || !ok || contextErr != nil || i.Command() != "attachment.add" || !schema.ValidUUID(route.BoardID) || !schema.ValidUUID(route.Incarnation) || !schema.ValidUUID(route.CardID) || id.BoardID != route.BoardID || id.Incarnation != route.Incarnation || target == nil || target.CardID != route.CardID || target.GrantID != "" || uint64(len(data)) != p.ByteLength || digest(data) != p.SHA256 {
		return refused("INVALID_INPUT"), false
	}
	var guard *recovery.PinGuard
	defer func() {
		if guard != nil {
			guard.Close()
		}
	}()
	created := ""
	admit := func(ctx context.Context, r writer.Read, _ contract.Intent) (*T.RefusalResult, error) {
		if d, e := g.Admit(ctx, r, i); d != nil || e != nil {
			return d, e
		}
		var lane string
		var next uint64
		if e := r.Scan("SELECT lane FROM card WHERE incarnation=? AND card_id=?", []any{route.Incarnation, route.CardID}, &lane); e != nil {
			return nil, e
		}
		if lane == "Completed" {
			return deny("COMPLETED"), nil
		}
		if e := r.Scan("SELECT next_event FROM board WHERE singleton=1", nil, &next); e != nil {
			return nil, e
		}
		if next >= contract.MaxInteger {
			return deny("COUNTER_OVERFLOW"), nil
		}
		if guard == nil {
			var e error
			guard, e = s.registry.Guard(ctx)
			if e != nil {
				return nil, e
			}
		}
		var length uint64
		var actual uint64
		var exact int
		// SQLite compares complete BLOB values; no second file-sized result
		// buffer is needed. The supplied value already passed its SHA-256 check.
		e := r.Scan("SELECT byte_length,length(bytes),bytes=? FROM blob WHERE incarnation=? AND sha256=?", []any{data, route.Incarnation, p.SHA256}, &length, &actual, &exact)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if e == nil && (length != p.ByteLength || actual != length || exact != 1) {
			s.store.Fence()
			return nil, ErrIntegrity
		}
		// Retained facts and immutable associations must agree even if their BLOB
		// has legitimately been reclaimed after the last protective pin.
		var conflicts uint64
		if e = r.Scan("SELECT (SELECT count(*) FROM attachment WHERE incarnation=? AND sha256=? AND byte_length<>?)+(SELECT count(*) FROM byte_fact WHERE incarnation=? AND sha256=? AND byte_length<>?)", []any{route.Incarnation, p.SHA256, p.ByteLength, route.Incarnation, p.SHA256, p.ByteLength}, &conflicts); e != nil {
			return nil, e
		}
		if conflicts != 0 {
			s.store.Fence()
			return nil, ErrIntegrity
		}
		if e = guard.CheckQuotaWith(r, A.ByteFact{SHA256: A.Digest(p.SHA256), ByteLength: A.Number(p.ByteLength)}); e != nil {
			if errors.Is(e, recovery.ErrByteQuota) {
				return deny("BYTE_QUOTA_EXCEEDED"), nil
			}
			s.store.Fence()
			return nil, ErrIntegrity
		}
		return nil, nil
	}
	cb := writer.Callbacks{Authority: g.Authority, Admit: admit, Apply: func(ctx context.Context, w writer.Write, _ contract.Intent, at A.Timestamp) (writer.Effect, error) {
		// S02 owns the writer transaction and all revisions/receipt persistence.
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
		if e = cards.WriteSnapshot(w, route.Incarnation, route.CardID, rev, prior); e != nil {
			return writer.Effect{}, e
		}
		rev++
		attachment, e := uuid()
		if e != nil {
			return writer.Effect{}, e
		}
		if e = exec("INSERT INTO blob(incarnation,sha256,byte_length,bytes) SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM blob WHERE incarnation=? AND sha256=?)", route.Incarnation, p.SHA256, p.ByteLength, data, route.Incarnation, p.SHA256); e != nil {
			return writer.Effect{}, e
		}
		if e = exec("INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at) VALUES(?,'attachment',?,?,?,?,?,?)", route.Incarnation, attachment, route.CardID, route.BoardID, route.Incarnation, actor, string(at)); e != nil {
			return writer.Effect{}, e
		}
		if e = exec("INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,?,?,?,?,?)", route.Incarnation, attachment, route.CardID, p.Name, p.MediaLabel, p.SHA256, p.ByteLength, string(at)); e != nil {
			return writer.Effect{}, e
		}
		if e = exec("INSERT INTO current_reference VALUES(?,?,'attachment',?)", route.Incarnation, route.CardID, attachment); e != nil {
			return writer.Effect{}, e
		}
		if e = exec("INSERT INTO current_attachment_pin VALUES(?,?,?,?)", route.Incarnation, attachment, p.SHA256, p.ByteLength); e != nil {
			return writer.Effect{}, e
		}
		var event, boardRev uint64
		if e = w.Scan("SELECT next_event,revision FROM board WHERE singleton=1", nil, &event, &boardRev); e != nil {
			return writer.Effect{}, e
		}
		cardRev := A.Number(rev)
		refs := []A.FactRef{{Kind: "attachment", ID: A.UUID(attachment)}, {Kind: "card", ID: A.UUID(route.CardID), Revision: &cardRev}}
		affected, e := json.Marshal(refs)
		if e != nil {
			return writer.Effect{}, e
		}
		if e = exec("UPDATE board SET next_event=next_event+1 WHERE singleton=1"); e != nil {
			return writer.Effect{}, e
		}
		if e = exec("INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,?,?,?,'attachment.add',?,?,?)", route.Incarnation, route.BoardID, route.Incarnation, event, actor, op, boardRev, string(at), string(affected)); e != nil {
			return writer.Effect{}, e
		}
		result, e := json.Marshal(map[string]string{"cardId": route.CardID, "attachmentId": attachment})
		if e == nil {
			created = attachment
		}
		return writer.Effect{Result: result, References: refs, CardRevision: &rev}, e
	}}
	result := writer.New(s.store).Submit(ctx, i, cb)
	fresh := false
	if created != "" && result.Terminal != nil && result.Terminal.Outcome == "committed" {
		for _, r := range result.Terminal.ResultRefs {
			fresh = fresh || r.Kind == "attachment" && string(r.ID) == created
		}
	}
	return result, fresh
}
