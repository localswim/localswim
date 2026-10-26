//go:build windows && amd64

package setup

// ProtectedRoot returns the canonical root selected by manager construction for
// native protected-tree inventory. It proves neither existence nor authority.
// The path must never enter public output, logs, assets or wire responses.
func (m *Manager) ProtectedRoot() (string, error) {
	if m == nil || m.fs == nil || m.fs.root == "" {
		return "", fail("PLATFORM_UNQUALIFIED", 3)
	}
	return m.fs.root, nil
}
