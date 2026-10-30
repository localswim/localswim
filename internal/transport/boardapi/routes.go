// Package boardapi adapts board/card reads, original outcome lookup, card
// commands and recovery administration. Admission remains in the shared services.
package boardapi

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/contract"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/schema"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
)

var ErrUnavailable = errors.New("board transport unavailable")

type Handler struct {
	store       *S.Store
	native      *credentials.Service
	sessions    session.Access
	boundary    *H.Handler
	guarded     http.Handler
	mu          sync.Mutex
	recovery    map[[32]byte]recoveryEntry
	cursorKey   [32]byte
	cursorEpoch [16]byte
}

type recoveryEntry struct {
	service *recovery.Service
	until   time.Time
	pending int
}

// New borrows existing source and authentication lifetimes. The composition
// owner mounts this beside T01, A01, H01 and H02 and closes their shared lifetime.
func New(store *S.Store, native *credentials.Service, sessions *session.Service) (*Handler, error) {
	if store == nil || native == nil || sessions == nil {
		return nil, ErrUnavailable
	}
	boundary, e := H.New(native, sessions)
	if e != nil {
		return nil, ErrUnavailable
	}
	h := &Handler{store: store, native: native, sessions: sessions, boundary: boundary, recovery: map[[32]byte]recoveryEntry{}}
	if _, e := rand.Read(h.cursorKey[:]); e != nil {
		return nil, ErrUnavailable
	}
	if _, e := rand.Read(h.cursorEpoch[:]); e != nil {
		return nil, ErrUnavailable
	}
	h.guarded = boundary.Native(http.HandlerFunc(h.dispatch))
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.guarded == nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	h.guarded.ServeHTTP(w, r)
}

type route struct {
	board, card, operation, kind string
	command                      contract.Command
}

func selectRoute(r *http.Request) (route, int) {
	if r.URL.RawPath != "" || r.URL.ForceQuery {
		return route{}, 400
	}
	p := strings.Split(r.URL.Path, "/")
	if len(p) < 5 || p[0] != "" || p[1] != "api" || p[2] != "v1" || p[3] != "boards" || !schema.ValidUUID(p[4]) {
		return route{}, 404
	}
	q := route{board: p[4]}
	method := ""
	switch {
	case len(p) == 5:
		q.kind = "board"
		method = "GET"
	case len(p) == 6 && p[5] == "cards":
		q.kind = "mutation"
		q.command = "card.create"
		method = "POST"
		if r.Method == "GET" {
			q.kind = "search"
			q.command = ""
			method = "GET"
		}
	case len(p) == 7 && p[5] == "operations" && schema.ValidUUID(p[6]):
		q.kind = "lookup"
		q.operation = p[6]
		method = "GET"
	case len(p) == 7 && p[5] == "recovery" && (p[6] == "preview" || p[6] == "confirm"):
		q.kind = p[6]
		method = "POST"
	case len(p) == 7 && p[5] == "cards" && schema.ValidUUID(p[6]):
		q.card = p[6]
		if r.Method == "PATCH" {
			q.kind = "mutation"
			q.command = "card.edit"
			method = "PATCH"
		} else {
			q.kind = "card"
			method = "GET"
		}
	case len(p) == 8 && p[5] == "cards" && schema.ValidUUID(p[6]):
		q.card = p[6]
		method = "POST"
		q.kind = "mutation"
		switch p[7] {
		case "history":
			q.kind = "history"
			method = "GET"
		case "comments":
			q.command = "comment.add"
		case "link-add":
			q.command = "link.add"
		case "link-remove":
			q.command = "link.remove"
		case "review-handoff":
			q.command = "review-handoff"
		case "approve":
			q.command = "approve"
		default:
			return route{}, 404
		}
	default:
		return route{}, 404
	}
	if q.kind != "lookup" && q.kind != "search" && r.URL.RawQuery != "" {
		return route{}, 400
	}
	if r.Method != method {
		return route{}, 405
	}
	return q, 0
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	q, status := selectRoute(r)
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
	// Only lookup has the narrow native-owner branch. Every ordinary request
	// enters T01 Wrap, including its browser Origin/fetch and current bearer checks.
	token, ok := bearer(r)
	if !ok {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if q.kind == "lookup" {
		mode := "cli"
		if len(r.Header.Values("Origin")) > 0 {
			mode = "browser"
		}
		_, e := h.sessions.Current(r.Context(), mode, token)
		if e != nil && mode == "browser" {
			_, e = h.sessions.Current(r.Context(), "cli", token)
		}
		if e != nil {
			h.nativeLookup(w, r, q, token)
			return
		}
	}
	h.boundary.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ordinary(w, r, q) })).ServeHTTP(w, r)
}

func recoveryKey(mode, token string) [32]byte { return sha256.Sum256([]byte(mode + "\x00" + token)) }
