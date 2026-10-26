//go:build !windows || !amd64

package setup

func (m *Manager) PrepareCredential(CredentialSelection) (*CredentialLease, error) {
	return nil, fail("PLATFORM_UNQUALIFIED", 3)
}
func (m *Manager) OpenCredential(CredentialSelection) (*CredentialMaterial, error) {
	return nil, fail("PLATFORM_UNQUALIFIED", 3)
}
