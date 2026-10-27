package exportapi

import (
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/transport/session"
	"net/http"
)

// NewAuthenticated requires boundary authentication before dispatch and retains
// current-grant admission in the export snapshot.
func NewAuthenticated(store *S.Store, access session.Access, boundary session.Boundary) (*Handler, error) {
	if store == nil || access == nil || boundary == nil {
		return nil, ErrUnavailable
	}
	h := &Handler{store: store, sessions: access}
	h.guarded = boundary.Wrap(http.HandlerFunc(h.dispatch))
	return h, nil
}
