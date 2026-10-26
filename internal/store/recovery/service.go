package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

type Route struct{ BoardID, Incarnation string }

// Service requires G01 current authentication/rights and the actual S01 Store.
// There is no callback that can substitute cached rights or native-owner power.
type Service struct {
	store    *S.Store
	grants   *grants.Service
	registry *Registry
	engine   *writer.Engine
	now      func() time.Time
	cacheMu  sync.Mutex
	previews map[A.Digest][]byte
	fault    func(string) error
}

func New(store *S.Store, g *grants.Service) *Service {
	if store == nil || g == nil {
		return nil
	}
	return &Service{store: store, grants: g, registry: RegistryFor(store), engine: writer.New(store), now: time.Now, previews: map[A.Digest][]byte{}}
}
func (s *Service) Pins() *Registry {
	if s == nil {
		return nil
	}
	return s.registry
}
func deny(code string) *T.RefusalResult {
	return &T.RefusalResult{Error: T.Error{Code: code, Message: "Recovery request refused"}}
}

type PreviewError struct{ Code string }

func (e *PreviewError) Error() string { return e.Code }
func fail(code string) error          { return &PreviewError{Code: code} }
func sampled(now func() time.Time) (A.Timestamp, error) {
	t := now().UTC().Truncate(time.Second)
	if t.Year() < 1 || t.Year() > 9999 {
		return "", errors.New("timestamp overflow")
	}
	return A.Timestamp(t.Format(stamp)), nil
}
func previewTime(now func() time.Time) (A.Timestamp, error) {
	at, e := sampled(now)
	if e != nil {
		return "", e
	}
	t, _ := time.Parse(stamp, string(at))
	if t.Add(300*time.Second).Year() > 9999 {
		return "", errors.New("preview expiry overflow")
	}
	return at, nil
}
func previewHash(p A.Preview) (A.Digest, error) {
	raw, e := json.Marshal(p)
	if e != nil {
		return "", e
	}
	v, e := contract.DecodeJSON(raw)
	if e != nil {
		return "", e
	}
	delete(v.(map[string]any), "previewHash")
	b, e := contract.Canonical(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return A.Digest(hex.EncodeToString(h[:])), nil
}
func makePreview(b boardState, actor string, q A.PreviewRequest, at A.Timestamp, inv A.PreviewInventory) (A.Preview, error) {
	t, e := time.Parse(stamp, string(at))
	if e != nil {
		return A.Preview{}, e
	}
	input, e := contract.DecodeJSON(q.Input)
	if e != nil {
		return A.Preview{}, e
	}
	raw, e := contract.Canonical(input)
	if e != nil {
		return A.Preview{}, e
	}
	p := A.Preview{APIVersion: 1, BoardID: b.identity.BoardID, Incarnation: b.identity.Incarnation, ActorID: A.UUID(actor), ExpectedRevision: q.ExpectedRevision, RetentionVersion: b.version, Command: q.Command, Input: raw, PreviewedAt: at, ExpiresAt: A.Timestamp(t.Add(300 * time.Second).Format(stamp)), Inventory: inv}
	p.PreviewHash, e = previewHash(p)
	if e != nil {
		return p, e
	}
	return p, A.Validate(p)
}

// Preview performs no persistent writes, including no recovery_preview row. Its
// immutable server cache is private, not a receipt; restart makes it unavailable.
// An ordinary authenticated actor ID is independently supplied by the adapter;
// G01 re-verifies the actual credential in the same coherent reader snapshot.
func (s *Service) Preview(ctx context.Context, route Route, actor string, q A.PreviewRequest) (A.Preview, error) {
	var p A.Preview
	if s == nil || ctx == nil || !schema.ValidUUID(route.BoardID) || !schema.ValidUUID(route.Incarnation) || !schema.ValidUUID(actor) || A.Validate(q) != nil {
		return p, fail("INVALID_INPUT")
	}
	ctx, cancel := context.WithTimeout(ctx, transactionLimit)
	defer cancel()
	g, e := s.registry.Guard(ctx)
	if e != nil {
		return p, fail("UNAVAILABLE")
	}
	defer g.Close()
	l, e := s.store.Reader(ctx)
	if e != nil {
		return p, fail("UNAVAILABLE")
	}
	e = l.Do(ctx, func(c *sql.Conn) (err error) {
		defer func() {
			if recover() != nil {
				err = errors.New("preview callback failed")
			}
		}()
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		r := readAccess{ctx, c}
		if e := s.grants.Require(ctx, r, route.BoardID, route.Incarnation, actor, "read", "history-admin"); e != nil {
			return fail("FORBIDDEN")
		}
		b, e := board(r)
		if e != nil {
			return e
		}
		if b.identity.BoardID != A.UUID(route.BoardID) || b.identity.Incarnation != A.UUID(route.Incarnation) || q.Incarnation != b.identity.Incarnation {
			return fail("INCARNATION_CONFLICT")
		}
		if q.ExpectedRevision != b.revision {
			return fail("REVISION_CONFLICT")
		}
		at, e := previewTime(s.now)
		if e != nil {
			return e
		}
		pins, e := g.Facts()
		if e != nil {
			return e
		}
		sel, e := selectRecords(r, b, q.Command, q.Input, at, pins, true)
		if e != nil {
			return e
		}
		p, e = makePreview(b, actor, q, at, sel.inventory)
		return e
	})
	closeErr := l.Close()
	if e != nil {
		var safe *PreviewError
		if errors.As(e, &safe) {
			return A.Preview{}, safe
		}
		return A.Preview{}, fail("UNAVAILABLE")
	}
	if closeErr != nil {
		return A.Preview{}, fail("UNAVAILABLE")
	}
	raw, e := A.Canonical(p)
	if e != nil {
		return A.Preview{}, e
	}
	s.cacheMu.Lock()
	// Expired entries are no longer promised; retain every unexpired immutable
	// response, and discard old cache material when a new preview is published.
	for hash, entry := range s.previews {
		var times struct{ ExpiresAt A.Timestamp }
		if json.Unmarshal(entry, &times) != nil || times.ExpiresAt <= p.PreviewedAt {
			delete(s.previews, hash)
		}
	}
	s.previews[p.PreviewHash] = bytes.Clone(raw)
	s.cacheMu.Unlock()
	return p, nil
}
func (s *Service) cached(h A.Digest) (A.Preview, error) {
	s.cacheMu.Lock()
	raw := bytes.Clone(s.previews[h])
	s.cacheMu.Unlock()
	if len(raw) == 0 {
		return A.Preview{}, fail("PREVIEW_UNAVAILABLE")
	}
	return A.Decode[A.Preview](raw)
}

// Confirm delegates identity-first lookup, exact retained own-outcome authority,
// refusal recording, revision admission, COMMIT reconciliation and fencing to
// S02. Only NEW identities enter the preview checks. No automatic retry occurs.
func (s *Service) Confirm(ctx context.Context, route Route, i contract.Intent) writer.Result {
	if s == nil {
		return writer.New(nil).Submit(ctx, i, writer.Callbacks{})
	}
	id, _, _, target, pre, contextErr := i.Context()
	if contextErr != nil || !schema.ValidUUID(route.BoardID) || !schema.ValidUUID(route.Incarnation) || id.BoardID != route.BoardID || id.Incarnation != route.Incarnation || target != nil || pre.ExpectedCardRevision != nil || (i.Command() != "history.clear" && i.Command() != "retention.set") {
		return writer.Result{Refused: &T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "INVALID_INPUT", Message: "Recovery route mismatch"}}}
	}
	var guard *PinGuard
	defer func() {
		if guard != nil {
			guard.Close()
		}
	}()
	var selected *selection
	admit := func(ctx context.Context, r writer.Read, i contract.Intent) (*T.RefusalResult, error) {
		id, _, actor, target, pre, e := i.Context()
		if e != nil || id.BoardID != route.BoardID || id.Incarnation != route.Incarnation || target != nil || pre.ExpectedCardRevision != nil || (i.Command() != "history.clear" && i.Command() != "retention.set") {
			return deny("INVALID_INPUT"), nil
		}
		if d, e := s.grants.Admit(ctx, r, i); d != nil || e != nil {
			return d, e
		}
		if e = s.grants.Require(ctx, r, id.BoardID, id.Incarnation, actor, "read", "history-admin"); e != nil {
			return deny("FORBIDDEN"), nil
		}
		guard, e = s.registry.Guard(ctx)
		if e != nil {
			return nil, e
		}
		var env struct {
			Payload struct {
				Input       json.RawMessage
				PreviewHash A.Digest
			}
		}
		if e = json.Unmarshal(i.CanonicalBytes(), &env); e != nil {
			return nil, e
		}
		p, e := s.cached(env.Payload.PreviewHash)
		if e != nil {
			return deny("PREVIEW_UNAVAILABLE"), nil
		}
		now, e := sampled(s.now)
		if e != nil {
			return nil, e
		}
		if now >= p.ExpiresAt {
			return deny("PREVIEW_EXPIRED"), nil
		}
		b, e := board(r)
		if e != nil {
			return nil, e
		}
		input, e := contract.DecodeJSON(env.Payload.Input)
		if e != nil {
			return nil, e
		}
		canonical, e := contract.Canonical(input)
		if e != nil {
			return nil, e
		}
		if p.BoardID != A.UUID(id.BoardID) || p.Incarnation != A.UUID(id.Incarnation) || p.ActorID != A.UUID(actor) || p.Command != A.RecoveryCommand(i.Command()) || p.ExpectedRevision != A.Number(pre.ExpectedRevision) || p.RetentionVersion != b.version || !bytes.Equal(p.Input, canonical) {
			return deny("PREVIEW_MISMATCH"), nil
		}
		pins, e := guard.Facts()
		if e != nil {
			return nil, e
		}
		sel, e := selectRecords(r, b, p.Command, p.Input, p.PreviewedAt, pins, true)
		if e != nil {
			return nil, e
		}
		fresh, e := makePreview(b, actor, A.PreviewRequest{Incarnation: p.Incarnation, ExpectedRevision: p.ExpectedRevision, Command: p.Command, Input: p.Input}, p.PreviewedAt, sel.inventory)
		if e != nil {
			return nil, e
		}
		if fresh.PreviewHash != p.PreviewHash || fresh.ExpiresAt != p.ExpiresAt {
			return deny("PREVIEW_MISMATCH"), nil
		}
		selected = &sel
		return nil, nil
	}
	apply := func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		if selected == nil || guard == nil {
			return writer.Effect{}, errors.New("recovery admission missing")
		}
		id, op, actor, _, _, e := i.Context()
		if e != nil {
			return writer.Effect{}, e
		}
		pins, e := guard.Facts()
		if e != nil {
			return writer.Effect{}, e
		}
		if e = prune(w, *selected, at, A.Reason(reasonFor(i.Command())), pins); e != nil {
			return writer.Effect{}, e
		}
		if e = s.inject("after-prune"); e != nil {
			return writer.Effect{}, e
		}
		b, e := board(w)
		if e != nil {
			return writer.Effect{}, e
		}
		rev := A.Number(b.revision)
		ref := A.FactRef{Kind: "board", ID: A.UUID(id.BoardID), Revision: &rev}
		if e = activity(w, b, actor, &op, string(i.Command()), at, []A.FactRef{ref}); e != nil {
			return writer.Effect{}, e
		}
		raw, e := A.Canonical(struct {
			RetentionVersion A.Positive `json:"retentionVersion"`
			CoverageRevision A.Number   `json:"coverageRevision"`
		}{b.version, rev})
		return writer.Effect{Result: raw, References: []A.FactRef{ref}}, e
	}
	return s.engine.Submit(ctx, i, writer.Callbacks{Authority: s.grants.Authority, Admit: admit, Apply: apply})
}
func reasonFor(c contract.Command) string {
	if c == "history.clear" {
		return "cleared"
	}
	return "expired"
}
