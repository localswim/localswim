package admin

import (
	"context"
	"errors"
	"io"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
)

// codeConsole is a private channel, never an ordinary writer chosen by a caller.
// The only production constructor opens and validates the native CONOUT$ device.
type codeConsole interface {
	Present(T.SecretCode) error
	Close() error
}

func (r Runner) code(ctx context.Context, q invocation, stdin io.Reader, stderr io.Writer) (any, int) {
	fail := func(err error) (any, int) {
		code, exit := failureClass(err)
		if code == "OUTCOME_UNKNOWN" {
			code = "CODE_PRESENTATION_UNKNOWN"
		}
		return T.SessionCodeCLI{APIVersion: 1, Scope: "session-code", Command: "issue", Outcome: T.SessionCodeOutcome(map[int]string{1: "failed", 2: "refused", 3: "unavailable", 4: "unknown"}[exit]), Error: &T.CredentialError{Code: code, Message: code}}, exit
	}
	c, err := r.client(ctx, q.selected, true)
	if err != nil {
		return fail(err)
	}
	defer c.Close()
	open := r.console
	if open == nil {
		open = openCodeConsole
	}
	sink, err := open(stdin)
	if err != nil {
		return fail(err)
	}
	defer sink.Close()
	id := c.Context().InstallationID
	expected := "issue-code " + id + " " + q.selected.BoardID + " " + q.incarnation + " " + q.grant
	prompt := "Installation " + id + " origin " + c.Context().Origin + " board " + q.selected.BoardID + " incarnation " + q.incarnation + " grant " + q.grant + ". One-use browser login code expires in 60 seconds; presentation does not prove login."
	_, code := r.confirmation(ctx, stdin, stderr, prompt, expected)
	if code != "" {
		return fail(&core.Failure{Code: code, Exit: 2})
	}
	req := T.CodeRequest{InstallationID: T.UUIDv4(id), BoardID: A.UUID(q.selected.BoardID), Incarnation: A.UUID(q.incarnation), GrantID: A.UUID(q.grant)}
	var result *T.SessionCodeResult
	err = c.IssueCode(ctx, req, func(s T.CodeSecret) error {
		issued, e := time.Parse("2006-01-02T15:04:05Z", string(s.IssuedAt))
		expires, f := time.Parse("2006-01-02T15:04:05Z", string(s.ExpiresAt))
		if e != nil || f != nil || r.clock().Before(issued) || !r.clock().Before(expires) {
			return errors.New("private presentation unavailable")
		}
		if e := sink.Present(s.Code); e != nil {
			return e
		}
		result = &T.SessionCodeResult{InstallationID: &s.InstallationID, BoardID: &s.BoardID, Incarnation: &s.Incarnation, GrantID: &s.GrantID, ActorID: &s.ActorID, IssuedAt: &s.IssuedAt, ExpiresAt: &s.ExpiresAt, Presented: true}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	if result == nil {
		return fail(&core.Failure{Code: "OUTCOME_UNKNOWN", Exit: 4})
	}
	return T.SessionCodeCLI{APIVersion: 1, Scope: "session-code", Command: "issue", Outcome: "presented", Result: result}, 0
}
