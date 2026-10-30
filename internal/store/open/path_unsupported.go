//go:build !windows || !amd64

package open

import "errors"

type pathGuard struct{ path string }

func (*pathGuard) close() error { return nil }
func acquirePath(string, bool) (*pathGuard, error) {
	return nil, errors.New("storage platform unqualified")
}
func acquireInspectionPath(string) (*pathGuard, error) {
	return nil, errors.New("storage platform unqualified")
}

func acquireUpgradePath(string) (*pathGuard, error) {
	return nil, errors.New("storage platform unqualified")
}
