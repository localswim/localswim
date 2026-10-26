//go:build !windows || !amd64

package setup

func (m *Manager) ProtectedRoot() (string, error) {
	return "", fail("PLATFORM_UNQUALIFIED", 3)
}
