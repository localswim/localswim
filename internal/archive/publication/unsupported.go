//go:build !windows || !amd64

package publication

import (
	"context"
	A "github.com/localswim/localswim/internal/contract/archive"
	"time"
)

func nativeRestore(context.Context, Request, func() time.Time) Response {
	return conclusion("unknown", nil)
}
func nativeStatus(context.Context, string, A.UUID) Response { return conclusion("unknown", nil) }
