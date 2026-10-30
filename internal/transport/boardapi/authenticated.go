package boardapi

import (
	"crypto/rand"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/transport/session"
	"net/http"
)

// NewAuthenticated requires a transport boundary and current authority provider.
// Unlike New it never installs native-owner lookup or administration routes.
func NewAuthenticated(store *S.Store, access session.Access, boundary session.Boundary) (*Handler, error) {
	if store == nil || access == nil || boundary == nil {
		return nil, ErrUnavailable
	}
	h := &Handler{store: store, sessions: access, recovery: map[[32]byte]recoveryEntry{}}
	if _, e := rand.Read(h.cursorKey[:]); e != nil {
		return nil, ErrUnavailable
	}
	if _, e := rand.Read(h.cursorEpoch[:]); e != nil {
		return nil, ErrUnavailable
	}
	h.guarded = boundary.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, status := selectRoute(r)
		if status != 0 {
			refuse(w, status, "INVALID_INPUT")
			return
		}
		if q.kind == "preview" || q.kind == "confirm" {
			refuse(w, 404, "NOT_FOUND")
			return
		}
		if len(r.Header.Values("Content-Encoding")) > 0 || len(r.Header.Values("Content-Transfer-Encoding")) > 0 {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		h.ordinary(w, r, q)
	}))
	return h, nil
}
