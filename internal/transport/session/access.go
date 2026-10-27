package session

import (
	"context"
	"github.com/localswim/localswim/internal/board/grants"
	T "github.com/localswim/localswim/internal/contract/transport"
	"net/http"
)

// Access is the ordinary, server-resolved authority seam. Implementations must
// revalidate live credentials and durable grant rights in each supplied snapshot.
type Access interface {
	Current(context.Context, string, T.SecretToken) (T.SessionRead, error)
	Admission(string, T.SecretToken) (*grants.Service, error)
}

// Boundary enforces the transport's Host/Origin, channel and media checks and
// supplies a server-resolved credential before entering an ordinary adapter.
type Boundary interface {
	Wrap(http.Handler) http.Handler
}
