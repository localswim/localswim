package admin

import (
	"context"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/setup"
)

// Provider resolves only the exact protected N01 file in the current native
// installation. NativeCurrent and NativeOwner are separate explicit capabilities.
// Constructing a provider never initializes setup or searches other grants.
type Provider struct{ Manager *setup.Manager }

func selection(s core.Selectors) setup.CredentialSelection {
	return setup.CredentialSelection{InstallationID: s.InstallationID, BoardID: s.BoardID, Incarnation: s.Incarnation, GrantID: s.GrantID}
}
func selectors(s setup.CredentialSelection) core.Selectors {
	return core.Selectors{InstallationID: s.InstallationID, BoardID: s.BoardID, Incarnation: s.Incarnation, GrantID: s.GrantID}
}
func connectionMetadata(m setup.CredentialMetadata) core.CredentialMetadata {
	return core.CredentialMetadata{Selected: selectors(m.Selected), ActorID: m.ActorID, ExpiresAt: m.ExpiresAt}
}
func providerFailure(err error) error {
	code, exit := failureClass(err)
	return &core.Failure{Code: code, Exit: exit}
}
func (p Provider) Open(ctx context.Context, s core.Selectors) (*core.Connection, error) {
	if err := core.ValidateSelectors(s, false); err != nil {
		return nil, err
	}
	if ctx == nil || ctx.Err() != nil || p.Manager == nil {
		return nil, &core.Failure{Code: "CREDENTIAL_UNAVAILABLE", Exit: 3}
	}
	m, err := p.Manager.OpenCredential(selection(s))
	if err != nil {
		return nil, providerFailure(err)
	}
	metadata, local := m.Metadata(), m.Context()
	var c *core.Connection
	err = m.WithToken(func(token T.SecretToken) error {
		var e error
		c, e = core.NewBoardConnection(local, connectionMetadata(metadata), token, m.Close)
		return e
	})
	if err != nil {
		m.Close()
		return nil, providerFailure(err)
	}
	return c, nil
}
func (p Provider) NativeCurrent(ctx context.Context) (*core.Connection, error) {
	if ctx == nil || ctx.Err() != nil || p.Manager == nil {
		return nil, &core.Failure{Code: "CREDENTIAL_UNAVAILABLE", Exit: 3}
	}
	n, err := p.Manager.LoadNativeOwner()
	if err != nil {
		return nil, providerFailure(err)
	}
	return core.NewNativeOwnerConnection(n)
}
func (p Provider) NativeOwner(ctx context.Context, original evidence.Record) (*core.Connection, error) {
	if !original.Valid() || (original.Identity().Command != "grant.create" && original.Identity().Command != "grant.revoke") {
		return nil, &core.Failure{Code: "OPERATION_MISMATCH", Exit: 4}
	}
	if ctx == nil || ctx.Err() != nil || p.Manager == nil {
		return nil, &core.Failure{Code: "CREDENTIAL_UNAVAILABLE", Exit: 3}
	}
	n, err := p.Manager.LoadNativeOwner()
	if err != nil {
		return nil, providerFailure(err)
	}
	if original.Context() != (evidence.Context{InstallationID: n.Context.InstallationID, Origin: n.Context.Origin}) || original.Identity().ActorID != n.Context.OwnerActorID {
		return nil, &core.Failure{Code: "CREDENTIAL_CONTEXT_MISMATCH", Exit: 4}
	}
	return core.NewNativeOwnerConnection(n)
}
