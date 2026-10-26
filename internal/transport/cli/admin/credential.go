package admin

import (
	"context"
	"errors"
	"io"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/setup"
)

type credentialMaterial interface {
	Context() setup.ConnectionContext
	Metadata() setup.CredentialMetadata
	WithToken(func(T.SecretToken) error) error
	Close()
}
type credentialLease interface {
	Context() setup.ConnectionContext
	LoadForRevocation() (credentialMaterial, error)
	Disable() error
	Publish(T.CredentialSecret) (setup.CredentialMetadata, error)
	Remove() error
	Close()
}
type nativeLease struct{ *setup.CredentialLease }

func (n nativeLease) LoadForRevocation() (credentialMaterial, error) {
	return n.CredentialLease.LoadForRevocation()
}
func (r Runner) lease(s setup.CredentialSelection) (credentialLease, error) {
	if r.prepare != nil {
		return r.prepare(s)
	}
	if r.Manager == nil {
		return nil, &core.Failure{Code: "CREDENTIAL_UNAVAILABLE", Exit: 3}
	}
	l, e := r.Manager.PrepareCredential(s)
	if e != nil {
		return nil, providerFailure(e)
	}
	return nativeLease{l}, nil
}
func credentialError(command, code string, exit int) *T.CredentialCLI {
	if exit < 1 || exit > 4 {
		exit = 3
	}
	if exit == 4 {
		code = "CREDENTIAL_OUTCOME_UNKNOWN"
	}
	return &T.CredentialCLI{APIVersion: 1, Scope: "credential", Command: T.CredentialCommand(command), Outcome: T.CredentialOutcome(map[int]string{1: "failed", 2: "refused", 3: "unavailable", 4: "unknown"}[exit]), Error: &T.CredentialError{Code: code, Message: code}}
}
func publicCredential(m setup.CredentialMetadata, available bool) *T.CredentialResult {
	id := T.UUIDv4(m.Selected.InstallationID)
	b := A.UUID(m.Selected.BoardID)
	i := A.UUID(m.Selected.Incarnation)
	g := A.UUID(m.Selected.GrantID)
	a := A.UUID(m.ActorID)
	credential := T.UUIDv4(m.CredentialID)
	issued := A.Timestamp(m.IssuedAt.UTC().Format("2006-01-02T15:04:05Z"))
	expires := A.Timestamp(m.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"))
	return &T.CredentialResult{InstallationID: &id, BoardID: &b, Incarnation: &i, GrantID: &g, ActorID: &a, CredentialID: &credential, IssuedAt: &issued, ExpiresAt: &expires, LocallyAvailable: available}
}
func (r Runner) credential(ctx context.Context, q invocation, stdin io.Reader, stderr io.Writer) (any, int) {
	command := "issue"
	if q.kind == "credential revoke" {
		command = "revoke"
	}
	fail := func(err error) (any, int) {
		code, exit := failureClass(err)
		return *credentialError(command, code, exit), exit
	}
	if ctx == nil || ctx.Err() != nil {
		return fail(&core.Failure{Code: "UNAVAILABLE", Exit: 3})
	}
	if command == "issue" {
		c, err := r.client(ctx, q.selected, true)
		if err != nil {
			return fail(err)
		}
		defer c.Close()
		id := c.Context().InstallationID
		expected := "issue-cli " + id + " " + q.selected.BoardID + " " + q.incarnation + " " + q.grant
		prompt := "Installation " + id + " origin " + c.Context().Origin + " board " + q.selected.BoardID + " incarnation " + q.incarnation + " grant " + q.grant + ". Replaces any previous CLI credential for this grant; expiry 30 days."
		_, code := r.confirmation(ctx, stdin, stderr, prompt, expected)
		if code != "" {
			return fail(&core.Failure{Code: code, Exit: 2})
		}
		s := setup.CredentialSelection{InstallationID: id, BoardID: q.selected.BoardID, Incarnation: q.incarnation, GrantID: q.grant}
		l, err := r.lease(s)
		if err != nil {
			return fail(err)
		}
		defer l.Close()
		if l.Context().InstallationID != id || l.Context().Origin != c.Context().Origin {
			return fail(&core.Failure{Code: "CREDENTIAL_CONTEXT_MISMATCH", Exit: 2})
		}
		if err = l.Disable(); err != nil {
			return fail(err)
		}
		var published setup.CredentialMetadata
		req := T.CredentialIssueRequest{InstallationID: T.UUIDv4(id), BoardID: A.UUID(s.BoardID), Incarnation: A.UUID(s.Incarnation), GrantID: A.UUID(s.GrantID)}
		err = c.IssueCredential(ctx, req, func(secret T.CredentialSecret) error {
			var e error
			published, e = l.Publish(secret)
			return e
		})
		if err != nil {
			return fail(err)
		}
		if published.Selected != s || published.ActorID == "" || published.CredentialID == "" {
			return fail(&core.Failure{Code: "CREDENTIAL_OUTCOME_UNKNOWN", Exit: 4})
		}
		return T.CredentialCLI{APIVersion: 1, Scope: "credential", Command: "issue", Outcome: "confirmed", Result: publicCredential(published, true)}, 0
	}
	// Self-revocation has no native-owner fallback. Resolve the current installation
	// only to select the exact protected file required by the special D01 grammar.
	var local setup.ConnectionContext
	var err error
	if r.Manager != nil {
		local, err = r.Manager.ReadContext()
	} else if r.prepare != nil {
		// The private test seam supplies only a synthetic lease, not a new CLI root.
		local.InstallationID = q.selected.InstallationID
	} else {
		err = &core.Failure{Code: "CREDENTIAL_UNAVAILABLE", Exit: 3}
	}
	if err != nil {
		return fail(err)
	}
	s := setup.CredentialSelection{InstallationID: local.InstallationID, BoardID: q.selected.BoardID, Incarnation: q.incarnation, GrantID: q.grant}
	l, err := r.lease(s)
	if err != nil {
		return fail(err)
	}
	defer l.Close()
	m, err := l.LoadForRevocation()
	if err != nil {
		if code, _ := failureClass(err); code == "CREDENTIAL_DISABLED" {
			return fail(&core.Failure{Code: "CREDENTIAL_OUTCOME_UNKNOWN", Exit: 4})
		}
		return fail(err)
	}
	defer m.Close()
	meta, connection := m.Metadata(), m.Context()
	if meta.Selected.BoardID != s.BoardID || meta.Selected.Incarnation != s.Incarnation || meta.Selected.GrantID != s.GrantID || meta.Selected.InstallationID != l.Context().InstallationID {
		return fail(&core.Failure{Code: "CREDENTIAL_CONTEXT_MISMATCH", Exit: 2})
	}
	var c adminClient
	if r.open != nil {
		c, err = r.open(ctx, selectors(meta.Selected), false)
	} else {
		var raw *core.Connection
		err = m.WithToken(func(token T.SecretToken) error {
			var e error
			raw, e = core.NewBoardConnection(connection, connectionMetadata(meta), token)
			return e
		})
		if err == nil {
			c, err = core.Open(ctx, &singleConnection{c: raw}, selectors(meta.Selected))
		}
	}
	if err != nil {
		return fail(err)
	}
	defer c.Close()
	if err = c.RevokeCredential(ctx, func() error {
		if e := l.Disable(); e != nil {
			return providerFailure(e)
		}
		return nil
	}); err != nil {
		return fail(err)
	}
	if err = l.Remove(); err != nil {
		return fail(&core.Failure{Code: "CREDENTIAL_OUTCOME_UNKNOWN", Exit: 4})
	}
	return T.CredentialCLI{APIVersion: 1, Scope: "credential", Command: "revoke", Outcome: "confirmed", Result: publicCredential(meta, false)}, 0
}

// singleConnection makes one exact loaded bearer available for its one session
// preflight and self-revocation. It cannot resolve another selection or owner.
type singleConnection struct{ c *core.Connection }

func (s *singleConnection) Open(_ context.Context, selected core.Selectors) (*core.Connection, error) {
	if s.c == nil {
		return nil, errors.New("unavailable")
	}
	c := s.c
	s.c = nil
	return c, nil
}
func (*singleConnection) NativeOwner(context.Context, evidence.Record) (*core.Connection, error) {
	return nil, &core.Failure{Code: "FORBIDDEN", Exit: 2}
}
