package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/localswim/localswim/internal/transport/cli/core"
)

const maxMetadata = 16384
const maxFile = 10485760

func sha(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

type result struct {
	value         any
	exit          int
	localComplete bool
}

func refused(code string, exit int) result {
	out := core.Refused(code, exit)
	return result{value: out.Value(), exit: out.Exit()}
}
func classifyRead(status int, raw []byte) result {
	out := core.ReadFailure(status, raw)
	return result{value: out.Value(), exit: out.Exit()}
}
func readMetadata(ctx context.Context, path string, stdin io.Reader) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errLocal
	}
	if path != "-" {
		return nativeRead(ctx, path, maxMetadata)
	}
	if stdin == nil {
		return nil, errLocal
	}
	b, e := io.ReadAll(io.LimitReader(stdin, maxMetadata+1))
	if e != nil || len(b) > maxMetadata || ctx.Err() != nil {
		return nil, errLocal
	}
	return b, nil
}

// Interrupted input is never returned as a complete byte buffer.
func fixedFailure(err error) (string, int) {
	var f *core.Failure
	if errors.As(err, &f) && f.Exit >= 1 && f.Exit <= 3 {
		return f.Code, f.Exit
	}
	return "CREDENTIAL_UNAVAILABLE", 3
}
