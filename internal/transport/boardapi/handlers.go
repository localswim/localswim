package boardapi

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/workflow"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/writer"
	H "github.com/localswim/localswim/internal/transport/https"
)

// Two maximum prose fields can expand to six-byte JSON escapes per byte.
const maxBody = 800000

func bearer(r *http.Request) (T.SecretToken, bool) {
	v := r.Header.Values("Authorization")
	if len(v) != 1 || !strings.HasPrefix(v[0], "Bearer ") {
		return "", false
	}
	s := strings.TrimPrefix(v[0], "Bearer ")
	return T.SecretToken(s), len(s) == 43 && !strings.ContainsAny(s, " \t\r\n,")
}
func statusFor(code string) int {
	switch code {
	case "INVALID_INPUT":
		return 400
	case "UNAUTHENTICATED":
		return 401
	case "NOT_FOUND":
		return 404
	case "REVISION_CONFLICT", "INCARNATION_CONFLICT", "OPERATION_MISMATCH":
		return 409
	case "INPUT_TOO_LARGE":
		return 413
	case "UNAVAILABLE":
		return 503
	}
	return 403
}
func refuse(w http.ResponseWriter, status int, code string) {
	respond(w, status, T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}})
}
func respond(w http.ResponseWriter, status int, value any) {
	b, e := T.Readable(value)
	if e != nil {
		status = 503
		b, _ = T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "UNAVAILABLE", Message: "Request refused"}})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func result(w http.ResponseWriter, out writer.Result, fresh, lookup bool) {
	if out.Terminal != nil && out.Unknown == nil && out.Refused == nil {
		status := 200
		if fresh && !lookup && out.Terminal.Outcome == "committed" {
			switch out.Terminal.Command {
			case "card.create", "comment.add", "link.add":
				status = 201
			}
		}
		respond(w, status, *out.Terminal)
		return
	}
	if out.Unknown != nil && out.Terminal == nil && out.Refused == nil {
		status := 503
		if lookup && out.LookupCompleted {
			status = 200
		}
		respond(w, status, *out.Unknown)
		return
	}
	if out.Refused != nil && out.Terminal == nil && out.Unknown == nil {
		respond(w, statusFor(out.Refused.Error.Code), *out.Refused)
		return
	}
	refuse(w, 503, "UNAVAILABLE")
}
func body(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if e != nil {
		var too *http.MaxBytesError
		if errors.As(e, &too) {
			refuse(w, 413, "INPUT_TOO_LARGE")
		} else {
			refuse(w, 400, "INVALID_INPUT")
		}
		return nil, false
	}
	return b, true
}
func empty(w http.ResponseWriter, r *http.Request) bool {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if e != nil || len(b) != 0 {
		refuse(w, 400, "INVALID_INPUT")
		return false
	}
	return true
}

func lookupIdentity(r *http.Request, q route, actor string) (writer.LookupIdentity, error) {
	v, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || len(v) != 3 {
		return writer.LookupIdentity{}, ErrUnavailable
	}
	for _, key := range []string{"incarnation", "fingerprintVersion", "fingerprint"} {
		if len(v[key]) != 1 {
			return writer.LookupIdentity{}, ErrUnavailable
		}
	}
	k := writer.LookupIdentity{BoardID: q.board, Incarnation: v.Get("incarnation"), OpID: q.operation, ActorID: actor, FingerprintVersion: 1, Fingerprint: v.Get("fingerprint")}
	if v.Get("fingerprintVersion") != "1" || k.Validate() != nil {
		return writer.LookupIdentity{}, ErrUnavailable
	}
	return k, nil
}
func (h *Handler) nativeLookup(w http.ResponseWriter, r *http.Request, q route, token T.SecretToken) {
	if !empty(w, r) {
		return
	}
	actor := ""
	e := h.native.WithNativeSnapshot(r.Context(), token, func(id string, _ writer.Read) error { actor = id; return nil })
	if e != nil {
		if errors.Is(e, credentials.ErrDenied) {
			refuse(w, 401, "UNAUTHENTICATED")
		} else {
			refuse(w, 503, "UNAVAILABLE")
		}
		return
	}
	k, e := lookupIdentity(r, q, actor)
	if e != nil {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	c := h.native.Context()
	result(w, h.native.NativeLookup(r.Context(), token, c.InstallationID, c.Origin, k), false, true)
}

func (h *Handler) recoveryFor(mode string, token T.SecretToken, a *grants.Service, preview bool) *recovery.Service {
	key := recoveryKey(mode, string(token))
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for k, v := range h.recovery {
		if v.pending == 0 && !now.Before(v.until) {
			delete(h.recovery, k)
		}
	}
	entry, ok := h.recovery[key]
	if !ok {
		entry.service = recovery.New(h.store, a)
		entry.until = now.Add(5 * time.Minute)
	}
	if preview {
		// Keep an in-flight preview's instance while S03 samples its exact expiry.
		// Confirm-only traffic never retains a new service or extends its lifetime.
		entry.pending++
		h.recovery[key] = entry
	}
	return entry.service
}

func (h *Handler) finishPreview(mode string, token T.SecretToken, p *T.Preview) {
	key := recoveryKey(mode, string(token))
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.recovery[key]
	if !ok {
		return
	}
	if entry.pending > 0 {
		entry.pending--
	}
	if p != nil {
		until, e := time.Parse(time.RFC3339, string(p.ExpiresAt))
		if e == nil && until.After(entry.until) {
			entry.until = until
		}
	}
	h.recovery[key] = entry
}

func (h *Handler) ordinary(w http.ResponseWriter, r *http.Request, q route) {
	mode, token, ok := H.Credential(r.Context())
	if !ok {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	current, e := h.sessions.Current(r.Context(), mode, token)
	if e != nil {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if q.board != string(current.Grant.BoardID) {
		refuse(w, 403, "FORBIDDEN")
		return
	}
	a, e := h.sessions.Admission(mode, token)
	if e != nil {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if q.kind == "lookup" {
		if !empty(w, r) {
			return
		}
		k, e := lookupIdentity(r, q, string(current.ActorID))
		if e != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		result(w, writer.New(h.store).Lookup(r.Context(), k, a.Authority), false, true)
		return
	}
	if r.Method == "GET" {
		if !empty(w, r) {
			return
		}
		h.read(w, r, q, current, a)
		return
	}
	b, ok := body(w, r)
	if !ok {
		return
	}
	recRoute := recovery.Route{BoardID: q.board, Incarnation: string(current.Grant.Incarnation)}
	if q.kind == "preview" {
		req, e := T.Decode[T.PreviewRequest](b)
		if e != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		p, e := h.recoveryFor(mode, token, a, true).Preview(r.Context(), recRoute, string(current.ActorID), req)
		if e != nil {
			h.finishPreview(mode, token, nil)
			code := "UNAVAILABLE"
			var pe *recovery.PreviewError
			if errors.As(e, &pe) {
				code = pe.Code
			}
			refuse(w, statusFor(code), code)
			return
		}
		h.finishPreview(mode, token, &p)
		respond(w, 200, p)
		return
	}
	if q.kind == "confirm" {
		req, e := T.Decode[T.Confirmation](b)
		if e != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		q.command = contract.Command(req.Command)
	}
	i, e := T.FreezeMutation(b, contract.ResolvedRoute{Command: q.command, BoardID: q.board, TargetID: q.card}, contract.AuthenticatedActor{ActorID: string(current.ActorID)})
	if e != nil {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	if q.kind == "confirm" {
		result(w, h.recoveryFor(mode, token, a, false).Confirm(r.Context(), recRoute, i), false, false)
		return
	}
	var cb writer.Callbacks
	switch q.command {
	case "card.create", "card.edit", "comment.add":
		cb = cards.New(a).Callbacks(cards.Route{BoardID: q.board, Incarnation: string(current.Grant.Incarnation), CardID: q.card})
	default:
		cb = workflow.New(a).Callbacks(workflow.Route{BoardID: q.board, Incarnation: string(current.Grant.Incarnation), CardID: q.card})
	}
	// Capture the generated immutable entity ID to distinguish this invocation's
	// fresh commit from a competing identical submission's retained receipt.
	generated := ""
	apply := cb.Apply
	cb.Apply = func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		effect, e := apply(ctx, w, i, at)
		if e == nil {
			kind := ""
			switch i.Command() {
			case "card.create":
				kind = "card"
			case "comment.add":
				kind = "comment"
			case "link.add":
				kind = "link"
			}
			for _, ref := range effect.References {
				if string(ref.Kind) == kind {
					generated = string(ref.ID)
				}
			}
		}
		return effect, e
	}
	out := writer.New(h.store).Submit(r.Context(), i, cb)
	fresh := false
	if out.Terminal != nil && generated != "" {
		for _, ref := range out.Terminal.ResultRefs {
			if string(ref.ID) == generated {
				fresh = true
			}
		}
	}
	result(w, out, fresh, false)
}

type readAccess struct {
	ctx context.Context
	c   *sql.Conn
}

func (a readAccess) Scan(q string, args []any, dest ...any) error {
	return a.c.QueryRowContext(a.ctx, q, args...).Scan(dest...)
}
func (a readAccess) Query(q string, args ...any) (*sql.Rows, error) {
	return a.c.QueryContext(a.ctx, q, args...)
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request, q route, current T.SessionRead, a *grants.Service) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	l, e := h.store.Reader(ctx)
	if e != nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	var value any
	e = l.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		rd := readAccess{ctx, c}
		if e := a.Require(ctx, rd, q.board, string(current.Grant.Incarnation), string(current.ActorID), "read"); e != nil {
			return grants.ErrDenied
		}
		var e error
		value, e = project(rd, q)
		return e
	})
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		code := "UNAVAILABLE"
		if errors.Is(e, grants.ErrDenied) {
			code = "FORBIDDEN"
		} else if errors.Is(e, sql.ErrNoRows) {
			code = "NOT_FOUND"
		}
		refuse(w, statusFor(code), code)
		return
	}
	respond(w, 200, value)
}
