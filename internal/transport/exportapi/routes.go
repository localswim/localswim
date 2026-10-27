// Package exportapi adapts the accepted snapshot export route to A02.
package exportapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/localswim/localswim/internal/board/credentials"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
)

var ErrUnavailable = errors.New("export transport unavailable")

type Handler struct {
	store    *S.Store
	sessions session.Access
	guarded  http.Handler
}

// New borrows existing source and authentication lifetimes. P01 owns mounting.
func New(store *S.Store, native *credentials.Service, sessions *session.Service) (*Handler, error) {
	if store == nil || native == nil || sessions == nil {
		return nil, ErrUnavailable
	}
	b, e := H.New(native, sessions)
	if e != nil {
		return nil, ErrUnavailable
	}
	h := &Handler{store: store, sessions: sessions}
	h.guarded = b.Wrap(http.HandlerFunc(h.dispatch))
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.guarded == nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	h.guarded.ServeHTTP(w, r)
}

// No cleaning, redirects, query semantics or encoded path aliases are accepted.
func selectRoute(r *http.Request) (string, int) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		return "", 400
	}
	p := strings.Split(r.URL.Path, "/")
	if len(p) != 6 || p[0] != "" || p[1] != "api" || p[2] != "v1" || p[3] != "boards" || !schema.ValidUUID(p[4]) || p[5] != "exports" {
		return "", 404
	}
	if r.Method != http.MethodPost {
		return "", 405
	}
	return p[4], 0
}
