//go:build !windows || !amd64

package setup

import "crypto/tls"

// Manager refuses unqualified platforms; it never selects a different root or
// relaxes native security requirements to make setup succeed.
type Manager struct{}

func New(TrustStore) (*Manager, error) { return nil, fail("PLATFORM_UNQUALIFIED", 3) }
func (m *Manager) Status() Response {
	return response("status", emptyResult(), fail("PLATFORM_UNQUALIFIED", 3))
}
func (m *Manager) Init(*int) Response {
	return response("init", emptyResult(), fail("PLATFORM_UNQUALIFIED", 3))
}
func (m *Manager) RenewCertificate(string, string) Response {
	return response("certificate.renew", emptyResult(), fail("PLATFORM_UNQUALIFIED", 3))
}
func (m *Manager) RenewOwner(string, string, Confirmation) Response {
	return response("owner.renew", emptyResult(), fail("PLATFORM_UNQUALIFIED", 3))
}
func (m *Manager) ReadContext() (ConnectionContext, error) {
	return ConnectionContext{}, fail("PLATFORM_UNQUALIFIED", 3)
}
func (m *Manager) LoadNativeOwner() (NativeOwner, error) {
	return NativeOwner{}, fail("PLATFORM_UNQUALIFIED", 3)
}
func (m *Manager) PlanTrust(string, string, string) (TrustPlan, error) {
	return TrustPlan{}, fail("PLATFORM_UNQUALIFIED", 3)
}
func (m *Manager) ApplyTrust(TrustPlan, Confirmation) Response {
	return response("", emptyResult(), fail("PLATFORM_UNQUALIFIED", 3))
}

type ServerLease struct{ Context ConnectionContext }

func (s *ServerLease) Close()                           {}
func (s *ServerLease) TLSConfig() *tls.Config           { return nil }
func (m *Manager) AcquireServer() (*ServerLease, error) { return nil, fail("PLATFORM_UNQUALIFIED", 3) }
