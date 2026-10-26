// Package credentials owns durable board CLI authentication state and current
// native-owner composition. It does not implement transport or terminal consent.
package credentials

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/setup"
)

var ErrDenied = errors.New("credential authority unavailable")

// The private interface is a trusted lifecycle seam, not plugin isolation.
// Production always uses the real N01 Manager and its real ServerLease.
type nativeProvider interface {
	AcquireServer() (*setup.ServerLease, error)
	LoadNativeOwner() (setup.NativeOwner, error)
}

// Service owns the N01 lifetime lease. Close waits for requests; no handler
// acquires another installation lock. The lock order is native then S01 writer.
type Service struct {
	mu       sync.RWMutex
	store    *S.Store
	provider nativeProvider
	lease    *setup.ServerLease
	context  setup.ConnectionContext
	closed   bool
	now      func() time.Time
	random   io.Reader
	fault    func(string) error
}

// SourceRoot loads the current native source root through N01 and acquires its
// service lifetime exclusion. It never accepts a caller-supplied principal,
// owner snapshot, boolean proof or alternate root. It performs no enrollment.
func SourceRoot(store *S.Store) (*Service, error) {
	m, err := setup.New(nil)
	if err != nil {
		return nil, ErrDenied
	}
	return start(store, m)
}

func start(store *S.Store, p nativeProvider) (*Service, error) {
	if store == nil || p == nil {
		return nil, ErrDenied
	}
	l, err := p.AcquireServer()
	if err != nil || l == nil {
		return nil, ErrDenied
	}
	s := &Service{store: store, provider: p, lease: l, context: l.Context, now: time.Now, random: rand.Reader}
	n, err := p.LoadNativeOwner()
	if err != nil || !sameContext(n.Context, s.context) || !schema.ValidUUID(n.Context.OwnerActorID) {
		l.Close()
		return nil, ErrDenied
	}
	return s, nil
}

func sameContext(a, b setup.ConnectionContext) bool {
	return a.InstallationID == b.InstallationID && a.Origin == b.Origin && a.OwnerActorID == b.OwnerActorID && a.IssuerSHA256 == b.IssuerSHA256 && a.Hostname == b.Hostname && a.Authority == b.Authority && a.Port == b.Port
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.lease.Close()
	}
}

// Context is public connection metadata, never a credential or cached authority.
func (s *Service) Context() setup.ConnectionContext {
	if s == nil {
		return setup.ConnectionContext{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return setup.ConnectionContext{}
	}
	return s.context
}

// TLSConfig shares the owned N01 service lifetime with the T01 listener. A
// closed service refuses further handshakes; the provider enforces leaf time.
func (s *Service) TLSConfig() *tls.Config {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.lease == nil {
		return nil
	}
	cfg := s.lease.TLSConfig()
	if cfg == nil || cfg.GetCertificate == nil {
		return nil
	}
	get := cfg.GetCertificate
	cfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.closed {
			return nil, ErrDenied
		}
		return get(hello)
	}
	return cfg
}

func (s *Service) native(token T.SecretToken, now time.Time) (grants.Credential, error) {
	if s.closed || s.lease == nil {
		return grants.Credential{}, ErrDenied
	}
	if _, err := tokenHash(token); err != nil {
		return grants.Credential{}, ErrDenied
	}
	// Reload validated active material EACH time, while the owned lease prevents
	// stopped renewal/generation replacement. A snapshot alone is insufficient.
	n, err := s.provider.LoadNativeOwner()
	if err != nil || !sameContext(n.Context, s.context) || !n.ActiveTokenMatches(string(token), s.context.InstallationID, now) {
		return grants.Credential{}, ErrDenied
	}
	return grants.Credential{InstallationID: s.context.InstallationID, Origin: s.context.Origin, ActorID: n.Context.OwnerActorID, Mode: "native-owner", IssuedAt: n.IssuedAt, ExpiresAt: n.ExpiresAt}, nil
}

func tokenHash(token T.SecretToken) (string, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(string(token))
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != string(token) {
		return "", ErrDenied
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (s *Service) cli(ctx context.Context, r writer.Read, token T.SecretToken, now time.Time) (grants.Credential, error) {
	var c grants.Credential
	if s.closed || ctx == nil || ctx.Err() != nil || r == nil {
		return c, ErrDenied
	}
	h, err := tokenHash(token)
	if err != nil {
		return c, ErrDenied
	}
	var issued, expires string
	err = r.Scan("SELECT installation_id,origin,board_id,incarnation,grant_id,actor_id,issued_at,expires_at FROM cli_credential WHERE credential_hash=?", []any{h}, &c.InstallationID, &c.Origin, &c.BoardID, &c.Incarnation, &c.GrantID, &c.ActorID, &issued, &expires)
	if err != nil || c.InstallationID != s.context.InstallationID || c.Origin != s.context.Origin {
		return grants.Credential{}, ErrDenied
	}
	c.IssuedAt, err = time.Parse(time.RFC3339, issued)
	if err != nil {
		return grants.Credential{}, ErrDenied
	}
	c.ExpiresAt, err = time.Parse(time.RFC3339, expires)
	if err != nil || c.ExpiresAt.Sub(c.IssuedAt) != 30*24*time.Hour || now.Before(c.IssuedAt) || !now.Before(c.ExpiresAt) {
		return grants.Credential{}, ErrDenied
	}
	c.Mode = "cli"
	return c, nil
}

// Admission composes the durable CLI verifier with G01 in the caller's exact DB
// snapshot. Only mode cli is accepted. Native requests MUST use SubmitNative or
// NativeLookup so the lifetime exclusion spans their entire S02 transaction,
// including COMMIT/readback. Browser registry/exchange is T01. Every callback
// rechecks state; current rights are resolved by G01, not a DTO. A CLI callback
// may already hold an S01 lease: lifecycle admission must never wait here.
func (s *Service) Admission(mode string, token T.SecretToken) (*grants.Service, error) {
	if s == nil || mode != "cli" {
		return nil, ErrDenied
	}
	return s.admission(mode, token, true)
}
func (s *Service) admission(mode string, token T.SecretToken, lock bool) (*grants.Service, error) {
	if mode != "cli" && mode != "native-owner" {
		return nil, ErrDenied
	}
	return grants.New(s.context.InstallationID, s.context.Origin, func(ctx context.Context, r writer.Read, now time.Time) (grants.Credential, error) {
		if lock {
			// A queued Issue can hold a lifecycle reader while waiting for the
			// board writer; Close then queues the lifecycle writer. Blocking a
			// board-held callback behind Close would complete a lock cycle.
			// Shutdown contention refuses promptly, without cached authority.
			if !s.mu.TryRLock() {
				return grants.Credential{}, ErrDenied
			}
			defer s.mu.RUnlock()
		}
		if ctx == nil || ctx.Err() != nil {
			return grants.Credential{}, ErrDenied
		}
		if mode == "native-owner" {
			return s.native(token, now)
		}
		return s.cli(ctx, r, token, now)
	})
}

type Status string

const (
	Confirmed   Status = "confirmed"
	Refused     Status = "refused"
	Failed      Status = "failed"
	Unavailable Status = "unavailable"
	Unknown     Status = "unknown"
)

// IssueResult never serializes its ephemeral secret implicitly. Public metadata
// is present only after exact persistence readback and successful cleanup.
type IssueResult struct {
	Status Status              `json:"status"`
	Public *T.CredentialResult `json:"public"`
	secret *T.CredentialSecret
}

func (r IssueResult) String() string   { return fmt.Sprintf("credential issue {%s}", r.Status) }
func (r IssueResult) GoString() string { return r.String() }
func (r IssueResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Status Status              `json:"status"`
		Public *T.CredentialResult `json:"public"`
	}{r.Status, r.Public})
}

// MarshalEphemeral is the only secret delivery path. The transport must use
// no-store, deliver once, and discard the bytes. A lost delivery stays unknown;
// this service supplies neither retrieval nor automatic replacement/retry.
func (r IssueResult) MarshalEphemeral() ([]byte, error) {
	if r.Status != Confirmed || r.secret == nil {
		return nil, ErrDenied
	}
	return T.MarshalEphemeral(*r.secret)
}

type access struct {
	ctx context.Context
	c   *sql.Conn
}

func (a access) Scan(q string, args []any, dest ...any) error {
	return a.c.QueryRowContext(a.ctx, q, args...).Scan(dest...)
}
func (a access) Query(q string, args ...any) (*sql.Rows, error) {
	return a.c.QueryContext(a.ctx, q, args...)
}
func (s *Service) inject(stage string) error {
	if s.fault != nil {
		return s.fault(stage)
	}
	return nil
}
func stamp(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }

// transaction uses S01's SAME exclusive writer as ledgered grant revocation.
// No domain ledger is fabricated for authentication/attribution metadata.
func (s *Service) transaction(ctx context.Context, change func(context.Context, *sql.Conn) error, readback func(context.Context, *sql.Conn) error) (status Status) {
	if ctx == nil || ctx.Err() != nil {
		return Failed
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l, err := s.store.Writer(ctx)
	if err != nil {
		return Unavailable
	}
	attempted := false
	status = Failed
	err = l.Do(ctx, func(c *sql.Conn) (e error) {
		defer func() {
			if recover() != nil {
				e = errors.New("credential transaction failed")
			}
		}()
		if e = s.inject("begin"); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
			return e
		}
		if e = change(ctx, c); e != nil {
			if errors.Is(e, ErrDenied) {
				status = Refused
			}
			return e
		}
		if e = s.inject("before-commit"); e != nil {
			return e
		}
		attempted = true
		if e = s.inject("commit"); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "COMMIT"); e != nil {
			return e
		}
		if e = s.inject("after-commit"); e != nil {
			return e
		}
		if e = s.inject("readback"); e != nil {
			return e
		}
		if e = readback(ctx, c); e != nil {
			return e
		}
		status = Confirmed
		return nil
	})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		x := l.Do(cleanup, func(c *sql.Conn) error {
			if e := s.inject("rollback"); e != nil {
				return e
			}
			_, e := c.ExecContext(cleanup, "ROLLBACK")
			if e != nil && e.Error() == "SQL logic error: cannot rollback - no transaction is active (1)" {
				return nil
			}
			return e
		})
		cancel()
		if x != nil {
			s.store.Fence()
			status = Unknown
		}
		if attempted {
			status = Unknown
		}
	}
	closeErr := l.Close()
	if s.inject("close") != nil || closeErr != nil {
		s.store.Fence()
		status = Unknown
	}
	return status
}

func currentScope(ctx context.Context, c *sql.Conn, b, i, g string) (actor, kind string, rights []string, err error) {
	var actualB, actualI string
	if !schema.ValidUUID(b) || !schema.ValidUUID(i) || !schema.ValidUUID(g) {
		err = ErrDenied
		return
	}
	if e := c.QueryRowContext(ctx, "SELECT board_id,incarnation FROM board WHERE singleton=1").Scan(&actualB, &actualI); e != nil || b != actualB || i != actualI {
		err = ErrDenied
		return
	}
	var enabled int
	if e := c.QueryRowContext(ctx, "SELECT g.actor_id,a.kind,g.enabled FROM grant_record g JOIN actor a USING(incarnation,actor_id) WHERE g.incarnation=? AND g.grant_id=?", i, g).Scan(&actor, &kind, &enabled); e != nil || enabled != 1 {
		err = ErrDenied
		return
	}
	rows, e := c.QueryContext(ctx, "SELECT right_name FROM grant_right WHERE incarnation=? AND grant_id=? ORDER BY right_name", i, g)
	if e != nil {
		err = e
		return
	}
	rights = []string{}
	for rows.Next() {
		var right string
		if e = rows.Scan(&right); e != nil {
			break
		}
		rights = append(rights, right)
	}
	err = errors.Join(e, rows.Err(), rows.Close())
	if err == nil {
		err = grants.ValidateRights(kind, rights)
	}
	if err != nil {
		err = ErrDenied
	}
	return
}

// Issue is an explicit native-owner request for a preexisting enabled grant.
// Terminal confirmation/private file publication belong to K03; transport
// authentication/Host/Origin/HTTPS belong to T01. Neither is inferred here.
func (s *Service) Issue(ctx context.Context, owner T.SecretToken, request T.CredentialIssueRequest) IssueResult {
	if s == nil {
		return IssueResult{Status: Unavailable}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var secret T.CredentialSecret
	var hash string
	status := s.transaction(ctx, func(ctx context.Context, c *sql.Conn) error {
		now := s.now().UTC().Truncate(time.Second)
		if _, err := s.native(owner, now); err != nil || string(request.InstallationID) != s.context.InstallationID {
			return ErrDenied
		}
		actor, kind, rights, err := currentScope(ctx, c, string(request.BoardID), string(request.Incarnation), string(request.GrantID))
		if err != nil {
			return err
		}
		var id [16]byte
		var bearer [32]byte
		if _, err = io.ReadFull(s.random, id[:]); err != nil {
			return errors.New("entropy unavailable")
		}
		if _, err = io.ReadFull(s.random, bearer[:]); err != nil {
			return errors.New("entropy unavailable")
		}
		id[6] = id[6]&15 | 64
		id[8] = id[8]&63 | 128
		credentialID := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
		token := base64.RawURLEncoding.EncodeToString(bearer[:])
		digest := sha256.Sum256(bearer[:])
		hash = hex.EncodeToString(digest[:])
		secret = T.CredentialSecret{APIVersion: 1, InstallationID: request.InstallationID, BoardID: request.BoardID, Incarnation: request.Incarnation, GrantID: request.GrantID, ActorID: A.UUID(actor), Kind: A.Kind(kind), Rights: T.Rights(rights), CredentialID: T.UUIDv4(credentialID), IssuedAt: A.Timestamp(stamp(now)), ExpiresAt: A.Timestamp(stamp(now.Add(30 * 24 * time.Hour))), Token: T.SecretToken(token)}
		if T.Validate(secret) != nil {
			return ErrDenied
		}
		if _, err = c.ExecContext(ctx, "DELETE FROM cli_credential WHERE incarnation=? AND grant_id=?", string(request.Incarnation), string(request.GrantID)); err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, "INSERT INTO cli_credential(incarnation,board_id,grant_id,actor_id,installation_id,origin,credential_id,credential_hash,issued_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)", string(secret.Incarnation), string(secret.BoardID), string(secret.GrantID), actor, string(secret.InstallationID), s.context.Origin, credentialID, hash, string(secret.IssuedAt), string(secret.ExpiresAt))
		return err
	}, func(ctx context.Context, c *sql.Conn) error {
		var count int
		err := c.QueryRowContext(ctx, "SELECT count(*) FROM cli_credential WHERE incarnation=? AND board_id=? AND grant_id=? AND actor_id=? AND installation_id=? AND origin=? AND credential_id=? AND credential_hash=? AND issued_at=? AND expires_at=?", string(secret.Incarnation), string(secret.BoardID), string(secret.GrantID), string(secret.ActorID), string(secret.InstallationID), s.context.Origin, string(secret.CredentialID), hash, string(secret.IssuedAt), string(secret.ExpiresAt)).Scan(&count)
		if err != nil || count != 1 {
			return ErrDenied
		}
		return nil
	})
	if status != Confirmed {
		return IssueResult{Status: status}
	}
	return IssueResult{Status: status, secret: &secret, Public: &T.CredentialResult{InstallationID: &secret.InstallationID, BoardID: &secret.BoardID, Incarnation: &secret.Incarnation, GrantID: &secret.GrantID, ActorID: &secret.ActorID, CredentialID: &secret.CredentialID, IssuedAt: &secret.IssuedAt, ExpiresAt: &secret.ExpiresAt, LocallyAvailable: false}}
}

// SelfRevoke removes ONLY the exact currently authenticated board CLI token.
// It never revokes a grant or another replacement bearer. Browser DELETE belongs
// to T01's independent in-memory session registry; native owner cannot use this.
func (s *Service) SelfRevoke(ctx context.Context, token T.SecretToken) Status {
	if s == nil {
		return Unavailable
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var hash string
	return s.transaction(ctx, func(ctx context.Context, c *sql.Conn) error {
		a := access{ctx, c}
		now := s.now().UTC()
		credential, err := s.cli(ctx, a, token, now)
		if err != nil {
			return ErrDenied
		}
		if _, _, _, err = currentScope(ctx, c, credential.BoardID, credential.Incarnation, credential.GrantID); err != nil {
			return ErrDenied
		}
		hash, err = tokenHash(token)
		if err != nil {
			return ErrDenied
		}
		result, err := c.ExecContext(ctx, "DELETE FROM cli_credential WHERE credential_hash=?", hash)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return ErrDenied
		}
		return nil
	}, func(ctx context.Context, c *sql.Conn) error {
		var n int
		err := c.QueryRowContext(ctx, "SELECT count(*) FROM cli_credential WHERE credential_hash=?", hash).Scan(&n)
		if err != nil || n != 0 {
			return ErrDenied
		}
		return nil
	})
}
