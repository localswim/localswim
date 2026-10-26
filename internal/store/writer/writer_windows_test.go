//go:build windows && amd64

package writer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
	"golang.org/x/sys/windows"
)

const board = "10000000-0000-0000-0000-000000000001"
const inc = "20000000-0000-0000-0000-000000000001"
const actor = "30000000-0000-0000-0000-000000000001"
const grant = "30000000-0000-0000-0000-000000000002"
const card = "40000000-0000-0000-0000-000000000001"
const op = "50000000-0000-0000-0000-000000000001"
const digest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

var sid = schema.Identity{BoardID: board, Incarnation: inc}

func testContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}
func fixture(t *testing.T) (*Engine, string) {
	t.Helper()
	root := os.Getenv("S01_TEST_ROOT")
	if root == "" {
		t.Skip("native synthetic fixtures require explicit private S01_TEST_ROOT")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("absolute private root required")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(filepath.Clean(root))+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("fixtures must be outside public repository")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")+"-"+hex.EncodeToString(nonce[:]))
	if err = windows.CreateDirectory(windows.StringToUTF16Ptr(p), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); err != nil {
		t.Fatal(err)
	}
	s, err := S.Create(testContext(t), p, sid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	e := New(s)
	e.now = func() time.Time { return time.Date(2026, 10, 26, 0, 0, 1, 998, time.UTC) }
	raw(t, e, func(c *sql.Conn) error {
		for _, q := range []string{
			`INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES('` + inc + `','` + actor + `','human','Synthetic','` + board + `','` + inc + `')`,
			`INSERT INTO grant_fact VALUES('` + inc + `','` + grant + `')`,
			`INSERT INTO grant_record VALUES('` + inc + `','` + grant + `','` + actor + `',1)`,
			`INSERT INTO grant_right VALUES('` + inc + `','` + grant + `','read-own-outcome')`,
		} {
			if _, err := c.ExecContext(testContext(t), q); err != nil {
				return err
			}
		}
		return nil
	})
	return e, p
}
func raw(t *testing.T, e *Engine, fn func(*sql.Conn) error) {
	t.Helper()
	l, err := e.store.Writer(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err = l.Do(testContext(t), fn); err != nil {
		t.Fatal(err)
	}
}
func value(t *testing.T, e *Engine, q string) int64 {
	t.Helper()
	l, err := e.store.Reader(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var n int64
	if err = l.Do(testContext(t), func(c *sql.Conn) error { return c.QueryRowContext(testContext(t), q).Scan(&n) }); err != nil {
		t.Fatal(err)
	}
	return n
}
func intent(t *testing.T, id string, revision uint64, title string) contract.Intent {
	t.Helper()
	wire := fmt.Sprintf(`{"opId":%q,"incarnation":%q,"target":null,"expectedRevision":%d,"expectedCardRevision":null,"input":{"title":%q,"description":"Synthetic"}}`, id, inc, revision, title)
	i, err := T.FreezeMutation([]byte(wire), contract.ResolvedRoute{Command: "card.create", BoardID: board}, contract.AuthenticatedActor{ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func lookupIdentity(t *testing.T, i contract.Intent) LookupIdentity {
	t.Helper()
	o, err := outcome.Base(i)
	if err != nil {
		t.Fatal(err)
	}
	return outcome.IdentityOf(o)
}
func authority(ctx context.Context, r Read, request AuthorityRequest, p Purpose) error {
	var n int
	q := `SELECT count(*) FROM grant_record g WHERE g.incarnation=? AND g.actor_id=? AND g.enabled=1`
	if p == OwnOutcome {
		q += ` AND EXISTS(SELECT 1 FROM grant_right x WHERE x.incarnation=g.incarnation AND x.grant_id=g.grant_id AND x.right_name='read-own-outcome')`
	}
	err := r.Scan(q, []any{request.Identity.Incarnation, request.Identity.ActorID}, &n)
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("synthetic current authority denied")
	}
	return nil
}
func callbacks() Callbacks {
	return Callbacks{Authority: authority, Admit: func(context.Context, Read, contract.Intent) (*T.RefusalResult, error) { return nil, nil }, Apply: create}
}
func create(ctx context.Context, w Write, i contract.Intent, at A.Timestamp) (Effect, error) {
	qs := []struct {
		q string
		a []any
	}{
		{`UPDATE board SET next_ticket=next_ticket+1,next_event=next_event+1 WHERE singleton=1`, nil},
		{`INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'card',?,?,?,?)`, []any{inc, card, card, board, inc}},
		{`INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,1,?)`, []any{inc, card}},
		{`INSERT INTO card(incarnation,card_id,revision,title,description) VALUES(?,?,1,'Synthetic','Synthetic')`, []any{inc, card}},
		{`INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,1,?,?,'card.create',1,?,'[]')`, []any{inc, board, inc, actor, op, string(at)}},
	}
	for _, q := range qs {
		if _, err := w.Exec(q.q, q.a...); err != nil {
			return Effect{}, err
		}
	}
	rev := A.Positive(1)
	n := A.Number(1)
	return Effect{Result: json.RawMessage(`{"cardId":"` + card + `"}`), References: []A.FactRef{{Kind: "card", ID: A.UUID(card), Revision: &n}}, CardRevision: &rev}, nil
}
func terminal(t *testing.T, r Result, class string) {
	t.Helper()
	if r.Terminal == nil || string(r.Terminal.Outcome) != class {
		t.Fatalf("expected %s terminal, got terminal=%v unknown=%v refused=%v", class, r.Terminal != nil, r.Unknown != nil, r.Refused != nil)
	}
	if err := T.Validate(*r.Terminal); err != nil {
		t.Fatal(err)
	}
}
func noDomain(t *testing.T, e *Engine) {
	t.Helper()
	for _, q := range []string{"SELECT revision FROM board", "SELECT count(*) FROM card", "SELECT count(*) FROM card_revision", "SELECT count(*) FROM activity", "SELECT count(*) FROM blob", "SELECT count(*) FROM operation"} {
		if value(t, e, q) != 0 {
			t.Fatal("partial domain/terminal effect:", q)
		}
	}
}

func TestTerminalIdentityAndCurrentAuthority(t *testing.T) {
	e, _ := fixture(t)
	i := intent(t, op, 0, "Synthetic")
	cb := callbacks()
	terminal(t, e.Submit(testContext(t), i, cb), "committed")
	cb.Apply = func(context.Context, Write, contract.Intent, A.Timestamp) (Effect, error) {
		t.Fatal("same ID replayed")
		return Effect{}, nil
	}
	terminal(t, e.Submit(testContext(t), i, cb), "committed")
	terminal(t, e.Lookup(testContext(t), lookupIdentity(t, i), authority), "committed")
	if r := e.Lookup(testContext(t), lookupIdentity(t, intent(t, op, 1, "Other")), authority); r.Refused == nil || r.Refused.Error.Code != "OPERATION_MISMATCH" {
		t.Fatal("identity mismatch classified original")
	}
	raw(t, e, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "UPDATE grant_record SET enabled=0")
		return err
	})
	if r := e.Lookup(testContext(t), lookupIdentity(t, i), authority); r.Refused == nil || r.Terminal != nil {
		t.Fatal("revoked authority saw terminal")
	}
	if r := e.Submit(testContext(t), i, cb); r.Refused == nil || r.Terminal != nil {
		t.Fatal("revoked submit saw terminal")
	}
	if r := e.Lookup(testContext(t), lookupIdentity(t, i), nil); r.Unknown == nil {
		t.Fatal("missing authority accepted")
	}
	if value(t, e, "SELECT revision FROM board") != 1 || value(t, e, "SELECT count(*) FROM operation") != 1 {
		t.Fatal("replay changed state")
	}
}

func TestRetainedSubmitRequiresOwnOutcomeNotSubmission(t *testing.T) {
	e, _ := fixture(t)
	i := intent(t, op, 0, "Synthetic")
	terminal(t, e.Submit(testContext(t), i, callbacks()), "committed")
	var submission, own, admission, application int
	cb := callbacks()
	cb.Authority = func(ctx context.Context, r Read, request AuthorityRequest, purpose Purpose) error {
		if purpose == Submission {
			submission++
			return errors.New("synthetic new submission denied")
		}
		own++
		return authority(ctx, r, request, purpose)
	}
	cb.Admit = func(context.Context, Read, contract.Intent) (*T.RefusalResult, error) {
		admission++
		return nil, errors.New("retained admission must not run")
	}
	cb.Apply = func(context.Context, Write, contract.Intent, A.Timestamp) (Effect, error) {
		application++
		return Effect{}, errors.New("retained application must not run")
	}
	terminal(t, e.Submit(testContext(t), i, cb), "committed")
	terminal(t, e.Lookup(testContext(t), lookupIdentity(t, i), cb.Authority), "committed")
	// Resolving a retained receipt needs no executable mutation extensions.
	terminal(t, e.Submit(testContext(t), i, Callbacks{Authority: cb.Authority}), "committed")
	if submission != 0 || own != 3 || admission != 0 || application != 0 {
		t.Fatal("retained identity used new-submission gates")
	}
	if r := e.Submit(testContext(t), intent(t, attachOp, 1, "Other"), cb); r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("new identity bypassed Submission")
	}
	if submission != 1 || admission != 0 || application != 0 {
		t.Fatal("new identity executed denied mutation")
	}
	cb.Authority = func(ctx context.Context, r Read, request AuthorityRequest, purpose Purpose) error {
		if purpose == OwnOutcome {
			return errors.New("synthetic own outcome denied")
		}
		return authority(ctx, r, request, purpose)
	}
	if r := e.Submit(testContext(t), i, cb); r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" || r.Terminal != nil {
		t.Fatal("retained identity bypassed OwnOutcome")
	}
	if r := e.Submit(testContext(t), intent(t, op, 1, "Other"), cb); r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("unauthorized mismatch disclosed retained classification")
	}
	if value(t, e, "SELECT revision FROM board") != 1 || value(t, e, "SELECT count(*) FROM operation") != 1 || admission != 0 || application != 0 {
		t.Fatal("resolution or denied new identity changed state")
	}
}

func TestPublicLookupDescriptorResolvesPrunedReceipt(t *testing.T) {
	e, _ := fixture(t)
	i := intent(t, op, 0, "Synthetic")
	terminal(t, e.Submit(testContext(t), i, callbacks()), "committed")
	// These are exactly route/query evidence plus credential-derived actor. The
	// descriptor carries no command, preconditions, target or semantic payload.
	k := LookupIdentity{BoardID: board, Incarnation: inc, OpID: op, ActorID: actor, FingerprintVersion: 1, Fingerprint: i.Fingerprint()}
	metadataChecks := 0
	auth := func(ctx context.Context, r Read, request AuthorityRequest, purpose Purpose) error {
		if purpose != OwnOutcome || request.Identity != k || request.Operation == nil {
			return errors.New("exact current lookup authority unavailable")
		}
		m := request.Operation
		if m.Command != "card.create" || m.Target != nil || m.Preconditions.ExpectedRevision != 0 || m.Preconditions.ExpectedCardRevision != nil {
			return errors.New("stored immutable command metadata unavailable")
		}
		metadataChecks++
		return authority(ctx, r, request, purpose)
	}
	terminal(t, e.Lookup(testContext(t), k, auth), "committed")
	raw(t, e, func(c *sql.Conn) error {
		for _, q := range []string{"BEGIN IMMEDIATE", "DELETE FROM operation_full", "UPDATE board SET revision=2", "UPDATE operation SET removed_at='2026-10-26T00:00:02Z',removal_reason='expired',removal_revision=2", "COMMIT"} {
			if _, err := c.ExecContext(testContext(t), q); err != nil {
				return err
			}
		}
		return nil
	})
	i = contract.Intent{} // No retained or in-memory Intent is available to lookup.
	r := e.Lookup(testContext(t), k, auth)
	terminal(t, r, "committed")
	if r.Terminal.Retention != "compact" || r.Terminal.Result != nil || metadataChecks != 2 {
		t.Fatal("compact lookup required/invented pruned semantic content")
	}
	if r.Terminal.Command != "card.create" || r.Terminal.ExpectedRevision != 0 || r.Terminal.Revision == nil || *r.Terminal.Revision != 1 {
		t.Fatal("compact lookup substituted current or supplied metadata")
	}
	for _, change := range []func(*LookupIdentity){
		func(k *LookupIdentity) { k.BoardID = "invalid" }, func(k *LookupIdentity) { k.Incarnation = "invalid" }, func(k *LookupIdentity) { k.OpID = "invalid" }, func(k *LookupIdentity) { k.ActorID = "invalid" }, func(k *LookupIdentity) { k.FingerprintVersion = 0 }, func(k *LookupIdentity) { k.Fingerprint = "invalid" }, func(k *LookupIdentity) { k.Fingerprint = strings.Repeat("A", 64) },
	} {
		bad := k
		change(&bad)
		r = e.Lookup(testContext(t), bad, authority)
		if r.Refused == nil || r.Refused.Error.Code != "INVALID_INPUT" || r.Terminal != nil {
			t.Fatal("malformed descriptor accepted")
		}
	}
	bad := k
	bad.Fingerprint = strings.Repeat("0", 64)
	if r = e.Lookup(testContext(t), bad, authority); r.Refused == nil || r.Refused.Error.Code != "OPERATION_MISMATCH" || r.Terminal != nil {
		t.Fatal("wrong fingerprint resolved")
	}
	bad = k
	bad.ActorID = grant
	if r = e.Lookup(testContext(t), bad, authority); r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" || r.Terminal != nil {
		t.Fatal("unauthorized actor resolved original")
	}
	bad = k
	bad.Incarnation = "20000000-0000-0000-0000-000000000099"
	if r = e.Lookup(testContext(t), bad, authority); r.Unknown == nil || r.Terminal != nil {
		t.Fatal("wrong incarnation resolved")
	}
	bad = k
	bad.BoardID = "10000000-0000-0000-0000-000000000099"
	if r = e.Lookup(testContext(t), bad, authority); r.Unknown == nil || r.Terminal != nil {
		t.Fatal("wrong board resolved")
	}
	bad = k
	bad.OpID = attachOp
	if r = e.Lookup(testContext(t), bad, authority); r.Unknown == nil || r.Terminal != nil {
		t.Fatal("missing operation resolved")
	}
	denyMetadata := func(ctx context.Context, r Read, request AuthorityRequest, purpose Purpose) error {
		if request.Operation == nil || request.Operation.Command != "grant.revoke" || request.Operation.Target == nil || request.Operation.Target.GrantID != grant {
			return errors.New("synthetic current owner command restriction")
		}
		return authority(ctx, r, request, purpose)
	}
	if r = e.Lookup(testContext(t), k, denyMetadata); r.Refused == nil || r.Terminal != nil {
		t.Fatal("stored command restriction bypassed")
	}
	raw(t, e, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "UPDATE grant_record SET enabled=0")
		return err
	})
	if r = e.Lookup(testContext(t), k, auth); r.Refused == nil || r.Terminal != nil {
		t.Fatal("compact descriptor bypassed current authority")
	}
}
func TestDurableRefusalFencesDelayedSameID(t *testing.T) {
	e, p := fixture(t)
	i := intent(t, op, 0, "Synthetic")
	cb := callbacks()
	cb.Admit = func(context.Context, Read, contract.Intent) (*T.RefusalResult, error) {
		return denial("FORBIDDEN", nil), nil
	}
	cb.Apply = func(context.Context, Write, contract.Intent, A.Timestamp) (Effect, error) {
		t.Fatal("refusal applied")
		return Effect{}, nil
	}
	terminal(t, e.Submit(testContext(t), i, cb), "not_committed")
	if value(t, e, "SELECT revision FROM board") != 0 || value(t, e, "SELECT count(*) FROM activity") != 0 {
		t.Fatal("refusal changed domain")
	}
	if err := e.store.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := S.Existing(testContext(t), p, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e = New(s)
	terminal(t, e.Submit(testContext(t), i, callbacks()), "not_committed")
	if value(t, e, "SELECT count(*) FROM card") != 0 {
		t.Fatal("delayed request executed")
	}
}
func TestApplicationAndCommitFaults(t *testing.T) {
	for _, stage := range []string{"application", "panic", "before-commit", "after-commit", "readback", "rollback"} {
		t.Run(stage, func(t *testing.T) {
			e, p := fixture(t)
			i := intent(t, op, 0, "Synthetic")
			cb := callbacks()
			if stage == "application" || stage == "panic" {
				cb.Apply = func(ctx context.Context, w Write, i contract.Intent, at A.Timestamp) (Effect, error) {
					v, err := create(ctx, w, i, at)
					if err != nil {
						return v, err
					}
					if stage == "panic" {
						panic("synthetic callback panic")
					}
					return v, errors.New("injected application failure")
				}
			}
			e.fault = func(point string) error {
				if point == stage || (stage == "rollback" && point == "before-commit") {
					return errors.New("synthetic fault")
				}
				return nil
			}
			r := e.Submit(testContext(t), i, cb)
			if stage == "after-commit" {
				terminal(t, r, "committed")
			} else if r.Unknown == nil {
				t.Fatal("fault did not preserve unknown")
			}
			if stage == "readback" {
				if value(t, e, "SELECT revision FROM board") != 1 {
					t.Fatal("commit lost")
				}
			} else if stage != "after-commit" {
				noDomain(t, e)
			}
			e.fault = nil
			if stage == "rollback" || stage == "readback" {
				if l, err := e.store.Writer(testContext(t)); !errors.Is(err, S.ErrFenced) {
					if l != nil {
						_ = l.Close()
					}
					t.Fatal("uncertain recovery did not fence")
				}
				if err := e.store.Close(); err != nil {
					t.Fatal(err)
				}
				s, err := S.Existing(testContext(t), p, sid)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				recovered := New(s).Lookup(testContext(t), lookupIdentity(t, i), authority)
				if stage == "readback" {
					terminal(t, recovered, "committed")
				} else if recovered.Unknown == nil {
					t.Fatal("missing recovered evidence inferred noncommit")
				}
			}
		})
	}
}

func TestNativeReaderBlocksCommitAndSerializedWriter(t *testing.T) {
	e, _ := fixture(t)
	beforeCommit, afterCommit := false, false
	e.fault = func(stage string) error {
		if stage == "before-commit" {
			beforeCommit = true
		}
		if stage == "after-commit" {
			afterCommit = true
		}
		return nil
	}
	l, err := e.store.Reader(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Do(testContext(t), func(c *sql.Conn) error {
		if _, err := c.ExecContext(testContext(t), "BEGIN"); err != nil {
			return err
		}
		var n int
		return c.QueryRowContext(testContext(t), "SELECT revision FROM board").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	r := e.Submit(testContext(t), intent(t, op, 0, "Synthetic"), callbacks())
	if r.Unknown == nil {
		t.Fatal("busy COMMIT asserted terminal")
	}
	if !beforeCommit || afterCommit {
		t.Fatal("native blocked COMMIT path was not exercised")
	}
	e.fault = nil
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	noDomain(t, e)
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan Result, 1)
	second := make(chan Result, 1)
	cb := callbacks()
	var calls atomic.Int32
	cb.Admit = func(context.Context, Read, contract.Intent) (*T.RefusalResult, error) {
		calls.Add(1)
		close(entered)
		<-release
		return denial("FORBIDDEN", nil), nil
	}
	go func() { first <- e.Submit(testContext(t), intent(t, op, 0, "Synthetic"), cb) }()
	<-entered
	cb2 := callbacks()
	cb2.Admit = func(context.Context, Read, contract.Intent) (*T.RefusalResult, error) {
		calls.Add(1)
		return denial("FORBIDDEN", nil), nil
	}
	go func() {
		second <- e.Submit(testContext(t), intent(t, "50000000-0000-0000-0000-000000000002", 0, "Synthetic"), cb2)
	}()
	select {
	case <-second:
		t.Fatal("writer serialization bypassed")
	case <-time.After(30 * time.Millisecond):
	}
	if calls.Load() != 1 {
		t.Fatal("second admission ran concurrently")
	}
	close(release)
	terminal(t, <-first, "not_committed")
	terminal(t, <-second, "not_committed")
}

func TestMissingCompactAndSourceEvidence(t *testing.T) {
	e, _ := fixture(t)
	i := intent(t, op, 0, "Synthetic")
	if r := e.Lookup(testContext(t), lookupIdentity(t, i), authority); r.Unknown == nil {
		t.Fatal("absence inferred noncommit")
	}
	cb := callbacks()
	cb.Admit = func(context.Context, Read, contract.Intent) (*T.RefusalResult, error) {
		return denial("FORBIDDEN", nil), nil
	}
	terminal(t, e.Submit(testContext(t), i, cb), "not_committed")
	raw(t, e, func(c *sql.Conn) error {
		for _, q := range []string{"BEGIN IMMEDIATE", "DELETE FROM operation_full", "UPDATE operation SET removed_at='2026-10-26T00:00:02Z',removal_reason='expired',removal_revision=1", "COMMIT"} {
			if _, err := c.ExecContext(testContext(t), q); err != nil {
				return err
			}
		}
		return nil
	})
	r := e.Lookup(testContext(t), lookupIdentity(t, i), authority)
	terminal(t, r, "not_committed")
	if r.Terminal.Retention != "compact" || r.Terminal.Result != nil {
		t.Fatal("pruned detail recreated")
	}
	wrongWire := fmt.Sprintf(`{"opId":%q,"incarnation":"20000000-0000-0000-0000-000000000099","target":null,"expectedRevision":0,"expectedCardRevision":null,"input":{"title":"Synthetic","description":"Synthetic"}}`, op)
	wrong, err := T.FreezeMutation([]byte(wrongWire), contract.ResolvedRoute{Command: "card.create", BoardID: board}, contract.AuthenticatedActor{ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	if r = e.Lookup(testContext(t), lookupIdentity(t, wrong), authority); r.Unknown == nil || r.Unknown.Error.Code != "SOURCE_AUTHORITY_UNAVAILABLE" {
		t.Fatal("wrong incarnation resolved")
	}
}

func TestSeamsFailClosed(t *testing.T) {
	e, _ := fixture(t)
	i := intent(t, op, 0, "Synthetic")
	if r := e.Submit(testContext(t), i, Callbacks{}); r.Unknown == nil {
		t.Fatal("missing extensions admitted")
	}
	cb := callbacks()
	cb.Admit = func(ctx context.Context, r Read, i contract.Intent) (*T.RefusalResult, error) {
		var n int
		if err := r.Scan("UPDATE board SET revision=1", nil, &n); err == nil {
			t.Fatal("admission mutation allowed")
		}
		return denial("FORBIDDEN", nil), nil
	}
	terminal(t, e.Submit(testContext(t), i, cb), "not_committed")
	cb = callbacks()
	cb.Apply = func(ctx context.Context, w Write, i contract.Intent, at A.Timestamp) (Effect, error) {
		for _, q := range []string{"BEGIN", "COMMIT", "ROLLBACK", "PRAGMA foreign_keys=OFF", "UPDATE board SET revision=99; COMMIT", "/* bypass */ COMMIT", "WITH x AS (SELECT 1) UPDATE board SET revision=99"} {
			if _, err := w.Exec(q); err == nil {
				t.Fatal("transaction-control escape accepted")
			}
		}
		if _, err := w.Query("PRAGMA query_only=OFF"); err == nil {
			t.Fatal("read seam allowed PRAGMA")
		}
		return Effect{}, errors.New("synthetic seam rejection")
	}
	if r := e.Submit(testContext(t), intent(t, attachOp, 0, "Synthetic"), cb); r.Unknown == nil {
		t.Fatal("failed extension classified noncommit")
	}
	if value(t, e, "SELECT revision FROM board") != 0 {
		t.Fatal("SQL escape changed state")
	}
}

const attach = "60000000-0000-0000-0000-000000000001"
const attachOp = "50000000-0000-0000-0000-000000000002"

func attachmentIntent(t *testing.T) contract.Intent {
	t.Helper()
	wire := fmt.Sprintf(`{"opId":%q,"incarnation":%q,"target":{"cardId":%q},"expectedRevision":1,"expectedCardRevision":1,"input":{"name":"Synthetic.txt","mediaLabel":"text/plain","byteLength":0,"sha256":%q}}`, attachOp, inc, card, digest)
	i, err := T.FreezeMutation([]byte(wire), contract.ResolvedRoute{Command: "attachment.add", BoardID: board, TargetID: card}, contract.AuthenticatedActor{ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func addAttachment(ctx context.Context, w Write, i contract.Intent, at A.Timestamp) (Effect, error) {
	qs := []struct {
		q string
		a []any
	}{
		{`UPDATE card SET revision=2 WHERE card_id=?`, []any{card}},
		{`INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,?,?)`, []any{inc, board, inc, card, actor, string(at), `{"ticket":1,"title":"Synthetic","description":"Synthetic","priority":"P3","lane":"Ready for Work","archived":false,"currentApprovalId":null}`}},
		{`INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'attachment',?,?,?,?)`, []any{inc, attach, card, board, inc}},
		{`INSERT INTO blob(incarnation,sha256,byte_length,bytes) VALUES(?,?,0,?)`, []any{inc, digest, []byte{}}},
		{`INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'Synthetic.txt','text/plain',?,0,?)`, []any{inc, attach, card, digest, string(at)}},
		{`INSERT INTO current_reference VALUES(?,?,'attachment',?)`, []any{inc, card, attach}},
		{`INSERT INTO current_attachment_pin VALUES(?,?,?,0)`, []any{inc, attach, digest}},
		{`UPDATE board SET next_event=next_event+1 WHERE singleton=1`, nil},
		{`INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,2,?,?,'attachment.add',2,?,'[]')`, []any{inc, board, inc, actor, attachOp, string(at)}},
	}
	for _, q := range qs {
		if _, err := w.Exec(q.q, q.a...); err != nil {
			return Effect{}, err
		}
	}
	rev := A.Positive(2)
	n := A.Number(2)
	return Effect{Result: json.RawMessage(`{"cardId":"` + card + `","attachmentId":"` + attach + `"}`), References: []A.FactRef{{Kind: "attachment", ID: A.UUID(attach)}, {Kind: "card", ID: A.UUID(card), Revision: &n}}, CardRevision: &rev}, nil
}
func TestAtomicHistoryBytesAndOperationPins(t *testing.T) {
	for _, stage := range []string{"application", "before-commit", "committed", "refused"} {
		t.Run(stage, func(t *testing.T) {
			e, _ := fixture(t)
			terminal(t, e.Submit(testContext(t), intent(t, op, 0, "Synthetic"), callbacks()), "committed")
			i := attachmentIntent(t)
			cb := callbacks()
			cb.Apply = addAttachment
			if stage == "application" {
				cb.Apply = func(ctx context.Context, w Write, i contract.Intent, at A.Timestamp) (Effect, error) {
					v, err := addAttachment(ctx, w, i, at)
					if err != nil {
						return v, err
					}
					return v, errors.New("injected complete domain staging failure")
				}
			}
			if stage == "before-commit" {
				e.fault = func(point string) error {
					if point == stage {
						return errors.New("injected staged terminal failure")
					}
					return nil
				}
			}
			if stage == "refused" {
				cb.Admit = func(context.Context, Read, contract.Intent) (*T.RefusalResult, error) {
					return denial("FORBIDDEN", nil), nil
				}
			}
			r := e.Submit(testContext(t), i, cb)
			if stage == "committed" {
				terminal(t, r, "committed")
				for _, table := range []string{"card_revision", "blob", "attachment", "byte_fact", "operation_blob_pin"} {
					if value(t, e, "SELECT count(*) FROM "+table) != 1 {
						t.Fatal("missing atomic retained effect", table)
					}
				}
				if value(t, e, "SELECT revision FROM board") != 2 {
					t.Fatal("board revision not once")
				}
			} else {
				if stage == "refused" {
					terminal(t, r, "not_committed")
					if value(t, e, "SELECT count(*) FROM byte_fact") != 1 {
						t.Fatal("refused attempted byte fact missing")
					}
				} else if r.Unknown == nil {
					t.Fatal("fault inferred noncommit")
				}
				for _, table := range []string{"card_revision", "blob", "attachment", "current_attachment_pin", "operation_blob_pin"} {
					if value(t, e, "SELECT count(*) FROM "+table) != 0 {
						t.Fatal("partial staged effect", table)
					}
				}
				if value(t, e, "SELECT revision FROM board") != 1 || value(t, e, "SELECT revision FROM card") != 1 || value(t, e, "SELECT count(*) FROM activity") != 1 {
					t.Fatal("partial current/activity effect")
				}
			}
		})
	}
}

func TestOwnRevocationRechecksAuthorityAfterCommit(t *testing.T) {
	e, _ := fixture(t)
	wire := fmt.Sprintf(`{"boardId":%q,"opId":%q,"incarnation":%q,"target":{"grantId":%q},"expectedRevision":0,"expectedCardRevision":null,"input":{}}`, board, op, inc, grant)
	i, err := T.FreezeMutation([]byte(wire), contract.ResolvedRoute{Command: "grant.revoke", BoardID: board, TargetID: grant}, contract.AuthenticatedActor{ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	cb := callbacks()
	cb.Apply = func(ctx context.Context, w Write, i contract.Intent, at A.Timestamp) (Effect, error) {
		_, err := w.Exec("UPDATE grant_record SET enabled=0 WHERE grant_id=?", grant)
		return Effect{Result: json.RawMessage(`{"grantId":"` + grant + `"}`), References: []A.FactRef{{Kind: "grant", ID: A.UUID(grant)}}}, err
	}
	r := e.Submit(testContext(t), i, cb)
	if r.Refused == nil || r.Terminal != nil {
		t.Fatal("own revocation exposed terminal without current authority")
	}
	if value(t, e, "SELECT count(*) FROM operation WHERE terminal_class='COMMITTED'") != 1 {
		t.Fatal("revocation not committed")
	}
	raw(t, e, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "UPDATE grant_record SET enabled=1")
		return err
	})
	terminal(t, e.Lookup(testContext(t), lookupIdentity(t, i), authority), "committed")
}

func TestSourceOnlyAndCorruptRetainedEvidenceStayUnknown(t *testing.T) {
	t.Run("source-only", func(t *testing.T) {
		e, _ := fixture(t)
		i := intent(t, op, 0, "Synthetic")
		raw(t, e, func(c *sql.Conn) error {
			_, err := c.ExecContext(testContext(t), `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,expected_revision,terminal_class,code,terminal_at,removed_at,removal_reason,removal_revision) VALUES(?,?,?, ?,0,?,?,'card.create',0,'NOT_COMMITTED','FORBIDDEN','2026-10-26T00:00:01Z','2026-10-26T00:00:02Z','expired',1)`, inc, board, "20000000-0000-0000-0000-000000000099", op, actor, i.Fingerprint())
			return err
		})
		if r := e.Lookup(testContext(t), lookupIdentity(t, i), authority); r.Unknown == nil || r.Terminal != nil {
			t.Fatal("archived source resolved destination operation")
		}
	})
	t.Run("corrupt-full", func(t *testing.T) {
		e, _ := fixture(t)
		i := intent(t, op, 0, "Synthetic")
		cb := callbacks()
		cb.Admit = func(context.Context, Read, contract.Intent) (*T.RefusalResult, error) {
			return denial("FORBIDDEN", nil), nil
		}
		terminal(t, e.Submit(testContext(t), i, cb), "not_committed")
		raw(t, e, func(c *sql.Conn) error {
			_, err := c.ExecContext(testContext(t), "DELETE FROM operation_full")
			return err
		})
		if r := e.Lookup(testContext(t), lookupIdentity(t, i), authority); r.Unknown == nil {
			t.Fatal("corrupt evidence resolved")
		}
		if r := e.Submit(testContext(t), i, callbacks()); r.Unknown == nil {
			t.Fatal("corrupt original reexecuted")
		}
		if value(t, e, "SELECT count(*) FROM card") != 0 {
			t.Fatal("corruption replayed command")
		}
	})
}
