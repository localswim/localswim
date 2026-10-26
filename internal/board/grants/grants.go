// Package grants provides current grant admission inside the shared writer.
// Credential verification is a trusted integration boundary, never client input.
package grants

import (
	"context"
	"errors"
	"time"

	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

var ErrDenied = errors.New("current authority unavailable")

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
	now := s.now().UTC()
	c, err := s.verify(ctx, r, now)
	if err != nil || c.InstallationID != s.installation || c.Origin != s.origin || !schema.ValidUUID(c.ActorID) || c.ActorID != actor || !schema.ValidUUID(board) || !schema.ValidUUID(inc) || c.IssuedAt.IsZero() || now.Before(c.IssuedAt) || !now.Before(c.ExpiresAt) {
		return p, ErrDenied
	}
	var actualBoard, actualInc string
	if err := r.Scan("SELECT board_id,incarnation FROM board WHERE singleton=1", nil, &actualBoard, &actualInc); err != nil || board != actualBoard || inc != actualInc {
		return p, ErrDenied
	}
	// Historical attribution must exist independently of a supplied actor claim.
	if err := r.Scan("SELECT kind FROM actor WHERE incarnation=? AND actor_id=?", []any{inc, actor}, &p.kind); err != nil {
		return principal{}, ErrDenied
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
		return principal{}, ErrDenied
	}
	if c.Mode == "browser" && (c.ExpiresAt.After(c.IssuedAt.Add(8*time.Hour)) || c.IdleExpiresAt.IsZero() || !now.Before(c.IdleExpiresAt) || c.IdleExpiresAt.After(now.Add(30*time.Minute))) {
		return principal{}, ErrDenied
	}
	var enabled int
	var grantedActor string
	if err := r.Scan("SELECT actor_id,enabled FROM grant_record WHERE incarnation=? AND grant_id=?", []any{inc, c.GrantID}, &grantedActor, &enabled); err != nil || enabled != 1 || grantedActor != actor {
		return principal{}, ErrDenied
	}
	rows, err := r.Query("SELECT right_name FROM grant_right WHERE incarnation=? AND grant_id=? ORDER BY right_name", inc, c.GrantID)
	if err != nil {
		return principal{}, ErrDenied
	}
	defer rows.Close()
	for rows.Next() {
		var right string
		if err := rows.Scan(&right); err != nil {
			return principal{}, ErrDenied
		}
		p.rights = append(p.rights, right)
	}
	if rows.Err() != nil || ValidateRights(p.kind, p.rights) != nil {
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
	p, err := s.current(ctx, r, board, inc, actor)
	if err != nil || p.credential.Mode == "native-owner" || !has(p, rights...) {
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
		return err
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
	if purpose == writer.OwnOutcome && !has(p, "read-own-outcome") {
		return ErrDenied
	}
	return nil
}

func commandRights(c contract.Command) []string {
	switch c {
	case "card.create":
		return []string{"create"}
	case "card.edit", "link.add", "link.remove":
		return []string{"edit"}
	case "comment.add":
		return []string{"comment"}
	case "attachment.add":
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
