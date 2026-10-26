// Package attachments adapts only attachment.add and authorized raw download.
package attachments

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	B "github.com/localswim/localswim/internal/board/attachments"
	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
)

var ErrUnavailable = errors.New("attachment transport unavailable")

type Handler struct {
	service  *B.Service
	sessions *session.Service
	guarded  http.Handler
}

// New borrows the existing authority lifetimes. Every mount independently uses
// T01 Wrap for TLS/Host/Origin/fetch/current-bearer boundary enforcement.
func New(store *S.Store, native *credentials.Service, sessions *session.Service) (*Handler, error) {
	if store == nil || native == nil || sessions == nil {
		return nil, ErrUnavailable
	}
	boundary, e := H.New(native, sessions)
	if e != nil {
		return nil, ErrUnavailable
	}
	h := &Handler{service: B.New(store), sessions: sessions}
	h.guarded = boundary.Wrap(http.HandlerFunc(h.dispatch))
	return h, nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.guarded == nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	h.guarded.ServeHTTP(w, r)
}

type route struct{ board, card, attachment string }

func selectRoute(r *http.Request) (route, int) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		return route{}, 400
	}
	p := strings.Split(r.URL.Path, "/")
	if len(p) < 6 || p[0] != "" || p[1] != "api" || p[2] != "v1" || p[3] != "boards" || !schema.ValidUUID(p[4]) {
		return route{}, 404
	}
	if len(p) == 8 && p[5] == "cards" && schema.ValidUUID(p[6]) && p[7] == "attachments" {
		if r.Method != http.MethodPost {
			return route{}, 405
		}
		return route{board: p[4], card: p[6]}, 0
	}
	if len(p) == 7 && p[5] == "attachments" && schema.ValidUUID(p[6]) {
		if r.Method != http.MethodGet {
			return route{}, 405
		}
		return route{board: p[4], attachment: p[6]}, 0
	}
	return route{}, 404
}
func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	route, status := selectRoute(r)
	if status != 0 {
		code := "INVALID_INPUT"
		if status == 404 {
			code = "NOT_FOUND"
		}
		refuse(w, status, code)
		return
	}
	if len(r.Header.Values("Content-Encoding")) > 0 || len(r.Header.Values("Content-Transfer-Encoding")) > 0 {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
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
	if string(current.Grant.BoardID) != route.board {
		refuse(w, 403, "FORBIDDEN")
		return
	}
	g, e := h.sessions.Admission(mode, token)
	if e != nil {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if route.attachment != "" {
		b, e := ioEmpty(r)
		if e != nil || !b {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		out, e := h.service.Read(r.Context(), g, route.board, string(current.Grant.Incarnation), string(current.ActorID), route.attachment)
		if e != nil {
			switch {
			case errors.Is(e, grants.ErrDenied):
				refuse(w, 403, "FORBIDDEN")
			case errors.Is(e, B.ErrNotFound):
				refuse(w, 404, "NOT_FOUND")
			default:
				refuse(w, 503, "UNAVAILABLE")
			}
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/octet-stream")
		// A generated inert filename preserves no untrusted paths, control bytes,
		// executable extension or inline media behavior from the display name.
		w.Header().Set("Content-Disposition", `attachment; filename="attachment-`+route.attachment+`.bin"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(out.Bytes)))
		w.WriteHeader(200)
		_, _ = w.Write(out.Bytes)
		return
	}
	body, status := multipartBody(w, r)
	if status != 0 {
		code := "INVALID_INPUT"
		if status == 413 {
			code = "INPUT_TOO_LARGE"
		}
		refuse(w, status, code)
		return
	}
	i, e := T.FreezeMutation(body.metadata, contract.ResolvedRoute{Command: "attachment.add", BoardID: route.board, TargetID: route.card}, contract.AuthenticatedActor{ActorID: string(current.ActorID)})
	if e != nil {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	id, _, _, _, _, e := i.Context()
	if e != nil {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	// Preserve a frozen old-incarnation identity for S02's source-authority
	// unknown response; never rewrite it to the current incarnation.
	result, fresh := h.service.Add(r.Context(), g, B.Route{BoardID: route.board, Incarnation: id.Incarnation, CardID: route.card}, i, body.file)
	respondResult(w, result, fresh)
}

func respond(w http.ResponseWriter, status int, v any) {
	b, e := T.Readable(v)
	if e != nil {
		status = 503
		b, _ = T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "UNAVAILABLE", Message: "Request refused"}})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func ioEmpty(r *http.Request) (bool, error) {
	b, e := io.ReadAll(io.LimitReader(r.Body, 1))
	return len(b) == 0, e
}
func refuse(w http.ResponseWriter, status int, code string) {
	respond(w, status, T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}})
}
func respondResult(w http.ResponseWriter, r writer.Result, fresh bool) {
	if r.Terminal != nil && r.Unknown == nil && r.Refused == nil {
		status := 200
		if fresh && r.Terminal.Outcome == "committed" {
			status = 201
		}
		respond(w, status, *r.Terminal)
		return
	}
	if r.Unknown != nil && r.Terminal == nil && r.Refused == nil {
		respond(w, 503, *r.Unknown)
		return
	}
	if r.Refused != nil && r.Terminal == nil && r.Unknown == nil {
		status := 403
		switch r.Refused.Error.Code {
		case "INVALID_INPUT":
			status = 400
		case "NOT_FOUND":
			status = 404
		case "OPERATION_MISMATCH", "REVISION_CONFLICT", "INCARNATION_CONFLICT":
			status = 409
		case "UNAVAILABLE":
			status = 503
		}
		respond(w, status, *r.Refused)
		return
	}
	refuse(w, 503, "UNAVAILABLE")
}
