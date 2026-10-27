package https

import (
	"context"
	T "github.com/localswim/localswim/internal/contract/transport"
)

// WithCredential is a trusted server-composition seam, never a client claim.
// The receiving domain adapter still rechecks Access and G01 in its transaction.
func WithCredential(ctx context.Context, mode string, token T.SecretToken) context.Context {
	return context.WithValue(ctx, authKey{}, authentication{mode, token})
}
