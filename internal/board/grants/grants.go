// Package grants provides current grant admission inside the shared writer.
// Credential verification is a trusted integration boundary, never client input.
package grants

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/localswim/localswim/internal/board/policy"
	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

var ErrDenied = errors.New("current authority unavailable")
var ErrUnauthenticated = errors.New("current credential unavailable")

// Credential is the result of independently verifying a live credential. It is
// NOT itself authority and contains no rights. Only Verifier may supply it.
// Verifier must check the active hash, replacement/revocation and restart epoch
// in the supplied snapshot; a cached principal or bearer string is insufficient.
type Credential struct {
	InstallationID, Origin, BoardID, Incarnation, ActorID, GrantID string
	Mode                                                           string // cli, browser, native-owner
	IssuedAt, ExpiresAt, IdleExpiresAt                             time.Time
}

// Verifier executes on EACH Authority/Admit/Require invocation. It must use Read
// for durable authentication state, avoid external IO, and honor cancellation.
// Production CLI persistence is deliberately unavailable until its schema
// provider exists; no default verifier or allow-all fallback is supplied here.
type Verifier func(context.Context, writer.Read, time.Time) (Credential, error)

type Service struct {
	installation, origin string
	verify               Verifier
	now                  func() time.Time
}

func New(installationID, origin string, verify Verifier) (*Service, error) {
	if !schema.ValidUUID(installationID) || origin == "" || verify == nil {
		return nil, ErrDenied
	}
	return &Service{installationID, origin, verify, time.Now}, nil
}

var known = map[string]bool{
	"read": true, "read-own-outcome": true, "create": true, "edit": true,
	"comment": true, "attach": true, "export": true, "restore": true,
	"review-handoff": true, "approve": true, "grant-admin": true, "history-admin": true,
}

// ValidateRights refuses rather than normalizing unknown, duplicate or unsorted
// rights. Empty rights are valid and imply no authority.
func ValidateRights(kind string, rights []string) error {
	if kind != "human" && kind != "automation" {
		return ErrDenied
	}
	for n, r := range rights {
		if !known[r] || (n > 0 && rights[n-1] >= r) || (kind == "automation" && (r == "approve" || r == "grant-admin" || r == "history-admin")) {
			return ErrDenied
		}
	}
	return nil
}

type principal struct {
	credential Credential
	kind       string
	rights     []string
}

func (s *Service) current(ctx context.Context, r writer.Read, board, inc, actor string) (principal, error) {
	var p principal
	if s == nil || s.verify == nil || s.now == nil || ctx == nil || r == nil || ctx.Err() != nil {
		return p, ErrDenied
	}
	c, err := s.verify(ctx, r, s.now().UTC())
	// The verifier may wait while another request refreshes the credential.
	// Validate its returned copy against a fresh clock, including expiries that
	// elapsed during verification and the maximum remaining browser idle time.
	now := s.now().UTC()
	if err != nil || c.InstallationID != s.installation || c.Origin != s.origin || c.IssuedAt.IsZero() || now.Before(c.IssuedAt) || !now.Before(c.ExpiresAt) {
		return p, ErrUnauthenticated
	}
	if !schema.ValidUUID(c.ActorID) || c.ActorID != actor || !schema.ValidUUID(board) || !schema.ValidUUID(inc) {
		return p, ErrDenied
	}
	var actualBoard, actualInc string
	if err := r.Scan("SELECT board_id,incarnation FROM board WHERE singleton=1", nil, &actualBoard, &actualInc); err != nil {
		return p, err
	}
	if board != actualBoard || inc != actualInc {
		return p, ErrDenied
	}
	// Historical attribution must exist independently of a supplied actor claim.
	if err := r.Scan("SELECT kind FROM actor WHERE incarnation=? AND actor_id=?", []any{inc, actor}, &p.kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return principal{}, ErrDenied
		}
		return principal{}, err
	}
	p.credential = c
	if c.Mode == "native-owner" {
		if p.kind != "human" || c.BoardID != "" || c.Incarnation != "" || c.GrantID != "" {
			return principal{}, ErrDenied
		}
		return p, nil
	}
	if c.Mode != "cli" && c.Mode != "browser" || c.BoardID != board || c.Incarnation != inc || !schema.ValidUUID(c.GrantID) {
		return principal{}, ErrDenied
	}
	if c.Mode == "cli" && (c.ExpiresAt.After(c.IssuedAt.Add(30*24*time.Hour)) || !c.IdleExpiresAt.IsZero()) {
		return principal{}, ErrUnauthenticated
	}
	if c.Mode == "browser" && (c.ExpiresAt.After(c.IssuedAt.Add(8*time.Hour)) || c.IdleExpiresAt.IsZero() || !now.Before(c.IdleExpiresAt) || c.IdleExpiresAt.After(now.Add(30*time.Minute))) {
		return principal{}, ErrUnauthenticated
	}
	var enabled int
	var grantedActor string
	if err := r.Scan("SELECT actor_id,enabled FROM grant_record WHERE incarnation=? AND grant_id=?", []any{inc, c.GrantID}, &grantedActor, &enabled); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return principal{}, err
	} else if err != nil || enabled != 1 || grantedActor != actor {
		return principal{}, ErrDenied
	}
	rows, err := r.Query("SELECT right_name FROM grant_right WHERE incarnation=? AND grant_id=? ORDER BY right_name", inc, c.GrantID)
	if err != nil {
		return principal{}, err
	}
	for rows.Next() {
		var right string
		if err = rows.Scan(&right); err != nil {
			break
		}
		p.rights = append(p.rights, right)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return principal{}, err
	}
	if ValidateRights(p.kind, p.rights) != nil {
		return principal{}, ErrDenied
	}
	return p, nil
}

func has(p principal, rights ...string) bool {
	for _, want := range rights {
		found := false
		for _, got := range p.rights {
			found = found || got == want
		}
		if !known[want] || !found {
			return false
		}
	}
	return true
}

// Require checks ordinary reads/export/etc in the caller's current snapshot.
// Native owner has no ordinary rights, including read or read-own-outcome.
func (s *Service) Require(ctx context.Context, r writer.Read, board, inc, actor string, rights ...string) error {
	if err := s.require(ctx, r, board, inc, actor, "", "", rights...); err != nil {
		return ErrDenied
	}
	return nil
}

// RequireCurrent also binds the snapshot credential to the mode and grant
// resolved by the ordinary request boundary.
func (s *Service) RequireCurrent(ctx context.Context, r writer.Read, board, inc, actor, mode, grant string, rights ...string) error {
	if (mode != "cli" && mode != "browser") || !schema.ValidUUID(grant) {
		return ErrDenied
	}
	return s.require(ctx, r, board, inc, actor, mode, grant, rights...)
}

func (s *Service) require(ctx context.Context, r writer.Read, board, inc, actor, mode, grant string, rights ...string) error {
	p, err := s.current(ctx, r, board, inc, actor)
	if err != nil {
		return err
	}
	if p.credential.Mode == "native-owner" || !has(p, rights...) {
		return ErrDenied
	}
	if mode != "" && (p.credential.Mode != mode || p.credential.GrantID != grant) {
		return ErrDenied
	}
	w, err := policy.Load(r, inc)
	if err != nil {
		return err
	}
	if !policy.AllowsRights(w, p.kind, rights...) {
		return ErrDenied
	}
	return nil
}

func administrative(m *writer.OperationIdentity) bool {
	if m == nil || m.Preconditions.ExpectedCardRevision != nil {
		return false
	}
	return m.Command == "grant.create" && m.Target == nil || m.Command == "grant.revoke" && m.Target != nil && m.Target.CardID == "" && schema.ValidUUID(m.Target.GrantID)
}

// Authority is suitable for S02 Submission and OwnOutcome. A retained receipt
// can resolve without Submission/Admit, but current credential and own-outcome
// rights are checked again. Native entitlement requires stored grant metadata.
func (s *Service) Authority(ctx context.Context, r writer.Read, req writer.AuthorityRequest, purpose writer.Purpose) error {
	if req.Identity.Validate() != nil {
		return ErrDenied
	}
	p, err := s.current(ctx, r, req.Identity.BoardID, req.Identity.Incarnation, req.Identity.ActorID)
	if err != nil {
		return ErrDenied
	}
	if purpose != writer.Submission && purpose != writer.OwnOutcome {
		return ErrDenied
	}
	if p.credential.Mode == "native-owner" {
		if !administrative(req.Operation) {
			return ErrDenied
		}
		return nil
	}
	if purpose == writer.OwnOutcome {
		w, err := policy.Load(r, req.Identity.Incarnation)
		if err != nil || !has(p, "read-own-outcome") || !policy.AllowsRights(w, p.kind, "read-own-outcome") {
			return ErrDenied
		}
	}
	return nil
}

func commandRights(c contract.Command) []string {
	switch c {
	case "card.create":
		return []string{"create"}
	case "card.edit", "link.add", "link.remove", "workflow.edit":
		return []string{"edit"}
	case "comment.add":
		return []string{"comment"}
	case "attachment.add", "attachment.detach":
		return []string{"attach"}
	case "review-handoff":
		return []string{"review-handoff"}
	case "approve":
		return []string{"approve"}
	case "grant.create", "grant.revoke":
		return []string{"grant-admin"}
	case "history.clear", "retention.set":
		return []string{"read", "history-admin"}
	}
	return nil
}

func deny(code string) *T.RefusalResult {
	return &T.RefusalResult{Error: T.Error{Code: code, Message: "Mutation refused"}}
}
