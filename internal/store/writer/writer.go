// Package writer owns serialized domain transaction and terminal reconciliation.
// It supplies no credential verification, grant policy or command implementation.
package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
)

// Read permits only one SELECT statement per call, under the transaction's
// bounded context. Close Rows before returning; never retain a seam or use it
// concurrently. Query deliberately has no SQL transaction/PRAGMA/raw API.
type Read interface {
	Query(string, ...any) (*sql.Rows, error)
	Scan(string, []any, ...any) error
}
type Write interface {
	Read
	Exec(string, ...any) (sql.Result, error)
}
type access struct {
	ctx context.Context
	c   *sql.Conn
}

func allowed(q string, words ...string) bool {
	if strings.ContainsAny(q, ";\x00") {
		return false
	}
	f := strings.Fields(q)
	if len(f) == 0 {
		return false
	}
	for _, w := range words {
		if strings.EqualFold(f[0], w) {
			return true
		}
	}
	return false
}
func (a access) Query(q string, args ...any) (*sql.Rows, error) {
	if !allowed(q, "SELECT") {
		return nil, errors.New("single SELECT required")
	}
	return a.c.QueryContext(a.ctx, q, args...)
}
func (a access) Scan(q string, args []any, dest ...any) error {
	if !allowed(q, "SELECT") {
		return errors.New("single SELECT required")
	}
	return a.c.QueryRowContext(a.ctx, q, args...).Scan(dest...)
}

type mutation struct{ access }

func (a mutation) Exec(q string, args ...any) (sql.Result, error) {
	if !allowed(q, "INSERT", "UPDATE", "DELETE") {
		return nil, errors.New("single mutation statement required")
	}
	return a.c.ExecContext(a.ctx, q, args...)
}

type Purpose string

const (
	// Submission verifies current authenticated identity and authority to record
	// its outcome. Command rights are checked separately by Admit in this txn.
	Submission Purpose = "submission"
	// OwnOutcome verifies exact current read-own-outcome authority for the
	// original actor/board/incarnation, including revocation/session admission.
	OwnOutcome Purpose = "read-own-outcome"
)

type LookupIdentity = outcome.LookupIdentity

// OperationIdentity contains only immutable command metadata. For retained
// evidence it comes from the stored row, including after full-payload pruning.
type OperationIdentity struct {
	Command       contract.Command
	Target        *contract.Target
	Preconditions contract.Preconditions
}
type AuthorityRequest struct {
	Identity  LookupIdentity
	Operation *OperationIdentity
}

func authorityRequest(k LookupIdentity, o *A.Operation) AuthorityRequest {
	r := AuthorityRequest{Identity: k}
	if o == nil {
		return r
	}
	m := &OperationIdentity{Command: contract.Command(o.Command), Preconditions: contract.Preconditions{ExpectedRevision: uint64(o.ExpectedRevision)}}
	if o.ExpectedCardRevision != nil {
		v := uint64(*o.ExpectedCardRevision)
		m.Preconditions.ExpectedCardRevision = &v
	}
	if o.Target != nil {
		m.Target = &contract.Target{}
		if o.Target.CardID != nil {
			m.Target.CardID = string(*o.Target.CardID)
		}
		if o.Target.GrantID != nil {
			m.Target.GrantID = string(*o.Target.GrantID)
		}
	}
	r.Operation = m
	return r
}

type Authority func(context.Context, Read, AuthorityRequest, Purpose) error
type Admission func(context.Context, Read, contract.Intent) (*T.RefusalResult, error)
type Effect struct {
	Result       json.RawMessage
	References   []A.FactRef
	CardRevision *A.Positive
}
type Apply func(context.Context, Write, contract.Intent, A.Timestamp) (Effect, error)
type Callbacks struct {
	Authority Authority
	Admit     Admission
	Apply     Apply
}

// Result is an internal sum; adapters choose exactly its nonnil member and the
// contextual D03 status. No SQL/internal callback error is exposed as prose.
type Result struct {
	Terminal *T.Terminal
	Unknown  *T.KnownIdentityUnknown
	Refused  *T.NonterminalRefusal
}
type Engine struct {
	store *S.Store
	now   func() time.Time
	fault func(string) error
}

var errSourceAuthority = errors.New("source authority unavailable")

func identityUnknown(o A.Operation, err error) Result {
	if errors.Is(err, errSourceAuthority) {
		return unknown(o, "SOURCE_AUTHORITY_UNAVAILABLE")
	}
	return unknown(o, "OUTCOME_UNKNOWN")
}

func New(s *S.Store) *Engine { return &Engine{store: s, now: time.Now} }
func refusal(code string) Result {
	return Result{Refused: &T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request cannot be admitted", Field: nil}, Conflict: nil}}
}
func unknown(o A.Operation, code string) Result {
	return Result{Unknown: &T.KnownIdentityUnknown{APIVersion: 1, Outcome: "unknown", BoardID: o.Origin.BoardID, Incarnation: o.Origin.Incarnation, OpID: o.OpID, ActorID: o.ActorID, FingerprintVersion: 1, Fingerprint: o.Fingerprint, Error: T.Error{Code: code, Message: "Original outcome is unavailable", Field: nil}}}
}
func (e *Engine) inject(stage string) error {
	if e.fault != nil {
		return e.fault(stage)
	}
	return nil
}
func bound(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, errors.New("context required")
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	return c, cancel, nil
}
func identity(ctx context.Context, c *sql.Conn, o A.Operation) error {
	var b, i string
	if err := c.QueryRowContext(ctx, "SELECT board_id,incarnation FROM board WHERE singleton=1").Scan(&b, &i); err != nil {
		return err
	}
	if b == string(o.Origin.BoardID) && i != string(o.Origin.Incarnation) {
		return errSourceAuthority
	}
	if b != string(o.Origin.BoardID) {
		return errors.New("board authority unavailable")
	}
	return nil
}
func (e *Engine) rollback(c *sql.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := e.inject("rollback")
	if err == nil {
		_, err = c.ExecContext(ctx, "ROLLBACK")
	}
	if err != nil {
		e.store.Fence()
	}
	return err
}

// Submit does not dispatch unless every extension is supplied. A callback being
// present is not authentication proof: G01 must independently verify the live
// credential and current authority each time Authority/Admit is invoked. Never
// construct this proof from the Intent actor, cached rights or an earlier check.
func (e *Engine) Submit(ctx context.Context, i contract.Intent, cb Callbacks) Result {
	o, err := outcome.Base(i)
	if err != nil {
		return refusal("INVALID_INPUT")
	}
	if e == nil || e.store == nil || cb.Authority == nil {
		return unknown(o, "OUTCOME_UNKNOWN")
	}
	ctx, cancel, err := bound(ctx)
	if err != nil {
		return unknown(o, "OUTCOME_UNKNOWN")
	}
	defer cancel()
	l, err := e.store.Writer(ctx)
	if err != nil {
		return unknown(o, "OUTCOME_UNKNOWN")
	}
	var direct *Result
	err = l.Do(ctx, func(c *sql.Conn) (transactionErr error) {
		defer func() {
			if recover() != nil {
				transactionErr = errors.New("transaction callback failed")
			}
		}()
		if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		if err := identity(ctx, c, o); err != nil {
			r := identityUnknown(o, err)
			direct = &r
			return e.rollback(c)
		}
		r := access{ctx, c}
		old, err := outcome.Load(ctx, c, o)
		if !errors.Is(err, sql.ErrNoRows) {
			// A retained identity resolves under current own-outcome authority,
			// independently of whether a new submission would be admitted. Also
			// require this check before disclosing a retained-key mismatch/error.
			var retained *A.Operation
			if err == nil {
				retained = &old
			}
			if authErr := cb.Authority(ctx, r, authorityRequest(outcome.IdentityOf(o), retained), OwnOutcome); authErr != nil {
				v := refusal("FORBIDDEN")
				direct = &v
				return e.rollback(c)
			}
			if errors.Is(err, outcome.ErrMismatch) {
				v := refusal("OPERATION_MISMATCH")
				direct = &v
				return e.rollback(c)
			}
			if err != nil {
				return err
			}
			t, err := outcome.Terminal(old)
			if err != nil {
				return err
			}
			v := Result{Terminal: &t}
			direct = &v
			return e.rollback(c)
		}
		if cb.Admit == nil || cb.Apply == nil {
			return errors.New("new submission callbacks required")
		}
		if err := cb.Authority(ctx, r, authorityRequest(outcome.IdentityOf(o), &o), Submission); err != nil {
			v := refusal("FORBIDDEN")
			direct = &v
			return e.rollback(c)
		}
		at := A.Timestamp(e.now().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z"))
		deny, err := preconditions(ctx, c, o)
		if err != nil {
			return err
		}
		if deny == nil {
			deny, err = cb.Admit(ctx, r, i)
			if err != nil {
				return err
			}
		}
		o.TerminalAt = at
		if deny != nil {
			if deny.Error.Code == "" || deny.Error.Code == "OK" {
				return errors.New("invalid refusal")
			}
			raw, err := json.Marshal(deny)
			if err != nil {
				return err
			}
			o.TerminalClass = "NOT_COMMITTED"
			o.Code = deny.Error.Code
			o.Full = &A.FullOutcome{Intent: i.CanonicalBytes(), Result: raw}
		} else {
			if _, err := c.ExecContext(ctx, "UPDATE board SET revision=revision+1 WHERE singleton=1"); err != nil {
				return err
			}
			v, err := cb.Apply(ctx, mutation{r}, i, at)
			if err != nil {
				return err
			}
			o.TerminalClass = "COMMITTED"
			o.Code = "OK"
			rev := A.Positive(uint64(o.ExpectedRevision) + 1)
			o.CommittedRevision = &rev
			o.CommittedCardRevision = v.CardRevision
			o.ResultRefs = v.References
			o.Full = &A.FullOutcome{Intent: i.CanonicalBytes(), Result: append([]byte(nil), v.Result...)}
			var current uint64
			if err = c.QueryRowContext(ctx, "SELECT revision FROM board WHERE singleton=1").Scan(&current); err != nil || current != uint64(rev) {
				return errors.New("invalid board revision effect")
			}
			if err = cardEffect(ctx, c, o); err != nil {
				return err
			}
		}
		if err = outcome.Insert(ctx, c, o); err != nil {
			return err
		}
		if err = schema.Validate(ctx, c, schema.Identity{BoardID: string(o.Origin.BoardID), Incarnation: string(o.Origin.Incarnation)}); err != nil {
			return err
		}
		if err = e.inject("before-commit"); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		return e.inject("after-commit")
	})
	if err != nil {
		// This may fail after COMMIT because no transaction remains. Cleanup's
		// independent explicit rollback establishes that case; fence only genuine
		// uncertain rollback, not the known no-active-transaction diagnostic.
		_ = l.Do(context.Background(), func(c *sql.Conn) error {
			if e.inject("rollback") != nil {
				e.store.Fence()
				return errors.New("rollback uncertainty")
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, x := c.ExecContext(cleanup, "ROLLBACK")
			if x != nil && x.Error() != "SQL logic error: cannot rollback - no transaction is active (1)" {
				e.store.Fence()
			}
			return x
		})
	}
	closeErr := l.Close()
	if direct != nil && err == nil && closeErr == nil {
		return *direct
	}
	// COMMIT, cancellation, application and cleanup failures are never inferred
	// noncommit. Only a fresh exact retained receipt may resolve the uncertainty.
	return e.lookup(ctx, outcome.IdentityOf(o), &o, cb.Authority, true)
}

func denial(code string, conflict *T.Conflict) *T.RefusalResult {
	return &T.RefusalResult{Error: T.Error{Code: code, Message: "Mutation refused", Field: nil}, Conflict: conflict}
}
func preconditions(ctx context.Context, c *sql.Conn, o A.Operation) (*T.RefusalResult, error) {
	var rev uint64
	if err := c.QueryRowContext(ctx, "SELECT revision FROM board WHERE singleton=1").Scan(&rev); err != nil {
		return nil, err
	}
	conflict := &T.Conflict{Incarnation: o.Origin.Incarnation, Revision: A.Number(rev)}
	if rev != uint64(o.ExpectedRevision) {
		return denial("REVISION_CONFLICT", conflict), nil
	}
	if rev == contract.MaxInteger {
		return denial("COUNTER_OVERFLOW", nil), nil
	}
	if o.Target != nil && o.Target.CardID != nil {
		var card uint64
		err := c.QueryRowContext(ctx, "SELECT revision FROM card WHERE incarnation=? AND card_id=?", string(o.Origin.Incarnation), string(*o.Target.CardID)).Scan(&card)
		if errors.Is(err, sql.ErrNoRows) {
			return denial("NOT_FOUND", nil), nil
		}
		if err != nil {
			return nil, err
		}
		v := A.Positive(card)
		conflict.CardID = o.Target.CardID
		conflict.CardRevision = &v
		if o.ExpectedCardRevision == nil || card != uint64(*o.ExpectedCardRevision) {
			return denial("REVISION_CONFLICT", conflict), nil
		}
		if card == contract.MaxInteger {
			return denial("COUNTER_OVERFLOW", nil), nil
		}
	}
	return nil, nil
}
func cardEffect(ctx context.Context, c *sql.Conn, o A.Operation) error {
	if (o.Target == nil || o.Target.CardID == nil) && o.Command != "card.create" {
		if o.CommittedCardRevision != nil {
			return errors.New("unexpected card revision")
		}
		return nil
	}
	var id string
	want := uint64(1)
	if o.Command == "card.create" {
		for _, r := range o.ResultRefs {
			if r.Kind == "card" {
				id = string(r.ID)
			}
		}
	} else {
		id = string(*o.Target.CardID)
		want = uint64(*o.ExpectedCardRevision) + 1
	}
	var got uint64
	if err := c.QueryRowContext(ctx, "SELECT revision FROM card WHERE incarnation=? AND card_id=?", string(o.Origin.Incarnation), id).Scan(&got); err != nil {
		return err
	}
	if got != want || o.CommittedCardRevision == nil || uint64(*o.CommittedCardRevision) != want {
		return errors.New("invalid card revision effect")
	}
	return nil
}

// Lookup is read-only. Its current-authority check and ledger read share one
// reader snapshot; archived outcomes can never resolve original source evidence.
func (e *Engine) Lookup(ctx context.Context, k LookupIdentity, auth Authority) Result {
	return e.lookup(ctx, k, nil, auth, false)
}
func (e *Engine) lookup(ctx context.Context, k LookupIdentity, original *A.Operation, auth Authority, reconcile bool) Result {
	if err := k.Validate(); err != nil {
		return refusal("INVALID_INPUT")
	}
	o := k.Evidence()
	u := unknown(o, "OUTCOME_UNKNOWN")
	if e == nil || e.store == nil || auth == nil {
		return u
	}
	ctx, cancel, err := bound(ctx)
	if err != nil {
		if reconcile {
			e.store.Fence()
		}
		return u
	}
	defer cancel()
	l, err := e.store.Reader(ctx)
	if err != nil {
		if reconcile {
			e.store.Fence()
		}
		return u
	}
	result := u
	err = l.Do(ctx, func(c *sql.Conn) (lookupErr error) {
		defer func() {
			if recover() != nil {
				lookupErr = errors.New("authority callback failed")
			}
		}()
		if _, err := c.ExecContext(ctx, "BEGIN"); err != nil {
			return err
		}
		if err := identity(ctx, c, o); err != nil {
			result = identityUnknown(o, err)
			return nil
		}
		old, loadErr := outcome.Lookup(ctx, c, k)
		if loadErr == nil && original != nil {
			loadErr = outcome.Match(old, *original)
		}
		var retained *A.Operation
		if loadErr == nil {
			retained = &old
		}
		if err := auth(ctx, access{ctx, c}, authorityRequest(k, retained), OwnOutcome); err != nil {
			result = refusal("FORBIDDEN")
			return nil
		}
		if err := e.inject("readback"); err != nil {
			return err
		}
		if errors.Is(loadErr, sql.ErrNoRows) {
			return nil
		}
		if errors.Is(loadErr, outcome.ErrMismatch) {
			result = refusal("OPERATION_MISMATCH")
			return nil
		}
		if loadErr != nil {
			return loadErr
		}
		t, err := outcome.Terminal(old)
		if err != nil {
			return err
		}
		result = Result{Terminal: &t}
		return nil
	})
	closeErr := l.Close()
	if err != nil || closeErr != nil {
		if reconcile {
			e.store.Fence()
		}
		return u
	}
	return result
}
