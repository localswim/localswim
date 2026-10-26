// Package archive composes the shared attachment, export and offline restore
// providers. It has no credential defaults or executable argument wiring.
package archive

import (
	"context"
	"errors"
)

var errLocal = errors.New("local material unavailable")

type saveState uint8

const (
	saveFailed saveState = iota
	saveUnknown
	saveConfirmed
)

// nativeFiles is deliberately private. Product callers cannot replace native
// publication with a writer that claims a browser offer is a verified save.
type nativeFiles struct {
	fault func(string) error
}

func (n nativeFiles) inject(stage string) error {
	if n.fault != nil {
		return n.fault(stage)
	}
	return nil
}

func (n nativeFiles) read(ctx context.Context, path string, max int64) ([]byte, error) {
	return nativeRead(ctx, path, max)
}

func (n nativeFiles) save(ctx context.Context, path string, raw []byte, roots []string) saveState {
	return nativeSave(ctx, path, raw, roots, n.inject)
}
