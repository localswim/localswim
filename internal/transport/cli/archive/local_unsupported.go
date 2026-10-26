//go:build !windows || !amd64

package archive

import "context"

func nativeRead(context.Context, string, int64) ([]byte, error) { return nil, errLocal }
func nativeSave(context.Context, string, []byte, []string, func(string) error) saveState {
	return saveFailed
}
