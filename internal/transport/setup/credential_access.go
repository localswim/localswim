package setup

import (
	"errors"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
)

// CredentialSelection names exactly one installation/board/incarnation/grant.
// It is never a filesystem path, discovery query or default profile.
type CredentialSelection struct {
	InstallationID, BoardID, Incarnation, GrantID string
}

// CredentialMetadata is public context validated against the exact private file.
// Rights are informational: the server independently admits every request.
type CredentialMetadata struct {
	Selected                    CredentialSelection
	ActorID, Kind, CredentialID string
	Rights                      []string
	IssuedAt, ExpiresAt         time.Time
}

type credentialLeaseState interface {
	context() ConnectionContext
	load(bool) (*CredentialMaterial, error)
	disable() error
	publish(T.CredentialSecret) (CredentialMetadata, error)
	remove() error
	close()
}

// CredentialLease holds native per-credential exclusion and directory pins.
// Prepare it before issuance; Disable must succeed before the HTTP request.
// Failed/unknown issuance or revocation leaves the durable disabled marker.
type CredentialLease struct{ state credentialLeaseState }

func (l *CredentialLease) String() string   { return "credential lease [redacted]" }
func (l *CredentialLease) GoString() string { return l.String() }
func (l *CredentialLease) MarshalJSON() ([]byte, error) {
	return nil, errors.New("credential lease is not serializable")
}
func (l *CredentialLease) Context() ConnectionContext {
	if l == nil || l.state == nil {
		return ConnectionContext{}
	}
	return l.state.context()
}
func (l *CredentialLease) LoadForRevocation() (*CredentialMaterial, error) {
	if l == nil || l.state == nil {
		return nil, fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	return l.state.load(true)
}
func (l *CredentialLease) Disable() error {
	if l == nil || l.state == nil {
		return fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	return l.state.disable()
}
func (l *CredentialLease) Publish(s T.CredentialSecret) (CredentialMetadata, error) {
	if l == nil || l.state == nil {
		return CredentialMetadata{}, fail("CREDENTIAL_OUTCOME_UNKNOWN", 4)
	}
	return l.state.publish(s)
}
func (l *CredentialLease) Remove() error {
	if l == nil || l.state == nil {
		return fail("CREDENTIAL_OUTCOME_UNKNOWN", 4)
	}
	return l.state.remove()
}
func (l *CredentialLease) Close() {
	if l != nil && l.state != nil {
		l.state.close()
	}
}

// CredentialMaterial deliberately exposes its bearer only within a callback.
// Close releases ordinary-read exclusion; revocation material borrows its lease.
// Neither formatting nor accidental JSON serialization discloses its contents.
type CredentialMaterial struct {
	context  ConnectionContext
	metadata CredentialMetadata
	use      func(func(T.SecretToken) error) error
	close    func()
}

func (m *CredentialMaterial) String() string   { return "credential material [redacted]" }
func (m *CredentialMaterial) GoString() string { return m.String() }
func (m *CredentialMaterial) MarshalJSON() ([]byte, error) {
	return nil, errors.New("credential material is not serializable")
}
func (m *CredentialMaterial) Context() ConnectionContext {
	if m == nil {
		return ConnectionContext{}
	}
	return m.context
}
func (m *CredentialMaterial) Metadata() CredentialMetadata {
	if m == nil {
		return CredentialMetadata{}
	}
	out := m.metadata
	out.Rights = append([]string(nil), out.Rights...)
	return out
}
func (m *CredentialMaterial) WithToken(f func(T.SecretToken) error) error {
	if m == nil || m.use == nil || f == nil {
		return fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	return m.use(f)
}
func (m *CredentialMaterial) Close() {
	if m != nil && m.close != nil {
		m.close()
	}
}
