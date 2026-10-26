//go:build windows && amd64

package attachments

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
)

const board = "10000000-0000-0000-0000-000000000001"
const inc = "20000000-0000-0000-0000-000000000001"
const actor = "30000000-0000-0000-0000-000000000001"
const grant = "30000000-0000-0000-0000-000000000002"
const installation = "60000000-0000-4000-8000-000000000001"
const origin = "https://synthetic.localhost:47443"

type fixture struct {
	s      *S.Store
	a      *Service
	g      *grants.Service
	card   string
	create contract.Intent
}

func tc(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	return c
}
func nonce(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	return hex.EncodeToString(b[:])
}
func fresh(t *testing.T) *fixture {
	t.Helper()
	root := os.Getenv("S01_TEST_ROOT")
	if root == "" {
		t.Skip("synthetic native store requires explicit private S01_TEST_ROOT")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if !filepath.IsAbs(root) || strings.HasPrefix(strings.ToLower(root)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("private fixture root required")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, nonce(t)[:8])
	if e = windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); e != nil {
		t.Fatal(e)
	}
	s, e := S.Create(tc(t), dir, schema.Identity{BoardID: board, Incarnation: inc})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	f := &fixture{s: s, a: New(s)}
	f.raw(t, func(c *sql.Conn) error {
		for _, q := range []string{`INSERT INTO actor VALUES('` + inc + `','` + actor + `','human','Synthetic','` + board + `','` + inc + `')`, `INSERT INTO grant_fact VALUES('` + inc + `','` + grant + `')`, `INSERT INTO grant_record VALUES('` + inc + `','` + grant + `','` + actor + `',1)`} {
			if _, e := c.ExecContext(tc(t), q); e != nil {
				return e
			}
		}
		for _, r := range []string{"attach", "create", "read", "read-own-outcome"} {
			if _, e := c.ExecContext(tc(t), "INSERT INTO grant_right VALUES(?,?,?)", inc, grant, r); e != nil {
				return e
			}
		}
		return nil
	})
	f.g, e = grants.New(installation, origin, func(ctx context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
		var a string
		e := r.Scan("SELECT actor_id FROM grant_record WHERE incarnation=? AND grant_id=?", []any{inc, grant}, &a)
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: a, GrantID: grant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, e
	})
	if e != nil {
		t.Fatal(e)
	}
	f.create = f.intent(t, "card.create", "", 0, 0, map[string]any{"title": "Synthetic", "description": "prior text"})
	r := writer.New(s).Submit(tc(t), f.create, cards.New(f.g).Callbacks(cards.Route{BoardID: board, Incarnation: inc}))
	if r.Terminal == nil || r.Terminal.Outcome != "committed" {
		t.Fatal("card fixture failed")
	}
	for _, ref := range r.Terminal.ResultRefs {
		if ref.Kind == "card" {
			f.card = string(ref.ID)
		}
	}
	return f
}
func (f *fixture) raw(t *testing.T, fn func(*sql.Conn) error) {
	t.Helper()
	l, e := f.s.Writer(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(tc(t), fn)
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
}
func (f *fixture) value(t *testing.T, q string) uint64 {
	t.Helper()
	l, e := f.s.Reader(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	var n uint64
	e = l.Do(tc(t), func(c *sql.Conn) error { return c.QueryRowContext(tc(t), q).Scan(&n) })
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
	return n
}
func (f *fixture) intent(t *testing.T, cmd, id string, rev, cr uint64, input map[string]any) contract.Intent {
	t.Helper()
	op := nonce(t)
	op = op[:8] + "-" + op[8:12] + "-" + op[12:16] + "-" + op[16:20] + "-" + op[20:]
	var target, cardRev any
	if id != "" {
		target = map[string]string{"cardId": id}
		cardRev = cr
	}
	b, _ := json.Marshal(map[string]any{"opId": op, "incarnation": inc, "target": target, "expectedRevision": rev, "expectedCardRevision": cardRev, "input": input})
	i, e := T.FreezeMutation(b, contract.ResolvedRoute{Command: contract.Command(cmd), BoardID: board, TargetID: id}, contract.AuthenticatedActor{ActorID: actor})
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func (f *fixture) upload(t *testing.T, data []byte, rev uint64) contract.Intent {
	return f.intent(t, "attachment.add", f.card, rev, rev, map[string]any{"name": "../../untrusted.exe\r\n", "mediaLabel": "application/x-executable", "byteLength": len(data), "sha256": digest(data)})
}
func (f *fixture) add(t *testing.T, i contract.Intent, data []byte) (writer.Result, bool) {
	t.Helper()
	u, e := Buffer(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	return f.a.Add(tc(t), f.g, Route{board, inc, f.card}, i, u)
}
func committed(t *testing.T, r writer.Result) string {
	t.Helper()
	if r.Terminal == nil || r.Terminal.Outcome != "committed" {
		t.Fatalf("expected committed result: terminal=%v unknown=%v refusal=%v", r.Terminal != nil, r.Unknown != nil, r.Refused != nil)
	}
	for _, ref := range r.Terminal.ResultRefs {
		if ref.Kind == "attachment" {
			return string(ref.ID)
		}
	}
	t.Fatal("missing attachment ref")
	return ""
}
func TestAtomicRawZeroReuseReplaySnapshot(t *testing.T) {
	f := fresh(t)
	data := []byte{0, 255, 1, 13, 10, 42}
	i := f.upload(t, data, 1)
	r, new := f.add(t, i, data)
	id := committed(t, r)
	if !new {
		t.Fatal("fresh commit missing")
	}
	if r.Terminal.CardRevision == nil || *r.Terminal.CardRevision != 2 || len(r.Terminal.ByteFacts) != 1 {
		t.Fatal("typed receipt")
	}
	out, e := f.a.Read(tc(t), f.g, board, inc, actor, id)
	if e != nil || !bytes.Equal(out.Bytes, data) {
		t.Fatal("raw download", e)
	}
	replay, isNew := f.add(t, i, data)
	if isNew || committed(t, replay) != id || f.value(t, "SELECT revision FROM board") != 2 {
		t.Fatal("replayed effect")
	}
	second, _ := f.add(t, f.upload(t, data, 2), data)
	committed(t, second)
	zero, _ := f.add(t, f.upload(t, nil, 3), nil)
	zeroID := committed(t, zero)
	out, e = f.a.Read(tc(t), f.g, board, inc, actor, zeroID)
	if e != nil || len(out.Bytes) != 0 {
		t.Fatal("zero blob", e)
	}
	for table, want := range map[string]uint64{"attachment": 3, "blob": 2, "current_attachment_pin": 3, "operation_blob_pin": 3, "history_blob_pin": 3, "card_revision": 3} {
		if n := f.value(t, "SELECT count(*) FROM "+table); n != want {
			t.Fatal(table, n, want)
		}
	}
	f.raw(t, func(c *sql.Conn) error {
		var b, i, a, at, state string
		e := c.QueryRowContext(tc(t), "SELECT origin_board,origin_incarnation,actor_id,committed_at,state FROM card_revision WHERE card_revision=1").Scan(&b, &i, &a, &at, &state)
		if e != nil {
			return e
		}
		var s A.SnapshotState
		if e = json.Unmarshal([]byte(state), &s); e != nil {
			return e
		}
		if b != board || i != inc || a != actor || s.Description != "prior text" || s.Title != "Synthetic" {
			return errors.New("prior provenance altered")
		}
		return schema.Validate(tc(t), c, schema.Identity{BoardID: board, Incarnation: inc})
	})
}
func TestRawMismatchPreterminalAndRefusals(t *testing.T) {
	f := fresh(t)
	i := f.upload(t, []byte("a"), 1)
	for _, data := range [][]byte{nil, []byte("b"), []byte("aa")} {
		r, _ := f.add(t, i, data)
		if r.Refused == nil || r.Terminal != nil {
			t.Fatal("raw mismatch manufactured terminal")
		}
	}
	if f.value(t, "SELECT count(*) FROM operation") != 1 || f.value(t, "SELECT revision FROM board") != 1 {
		t.Fatal("preterminal side effects")
	}
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(tc(t), "UPDATE card SET lane='Completed' WHERE card_id=?", f.card)
		return e
	})
	r, _ := f.add(t, i, []byte("a"))
	if r.Terminal == nil || r.Terminal.Outcome != "not_committed" || r.Terminal.Code != "COMPLETED" {
		t.Fatal("Completed mutation admitted")
	}
	if f.value(t, "SELECT count(*) FROM blob") != 0 || f.value(t, "SELECT count(*) FROM attachment") != 0 || f.value(t, "SELECT revision FROM board") != 1 {
		t.Fatal("refusal side effects")
	}
}
func TestOwnOutcomeRevocationAndReadRights(t *testing.T) {
	f := fresh(t)
	data := []byte("binary")
	i := f.upload(t, data, 1)
	r, _ := f.add(t, i, data)
	id := committed(t, r)
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(tc(t), "DELETE FROM grant_right WHERE right_name IN ('attach','read')")
		return e
	})
	guard, e := recovery.RegistryFor(f.s).Guard(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	replay, _ := f.add(t, i, data)
	guard.Close()
	if committed(t, replay) != id {
		t.Fatal("matching receipt checked mutable attach right")
	}
	if _, e := f.a.Read(tc(t), f.g, board, inc, actor, id); !errors.Is(e, grants.ErrDenied) {
		t.Fatal("attach implies read", e)
	}
	f.raw(t, func(c *sql.Conn) error { _, e := c.ExecContext(tc(t), "UPDATE grant_record SET enabled=0"); return e })
	r, _ = f.add(t, i, data)
	if r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("revoked own outcome disclosed")
	}
}
func TestRollbackAndBlockedCommitNoOrphan(t *testing.T) {
	for _, mode := range []string{"write", "blocked-commit"} {
		t.Run(mode, func(t *testing.T) {
			f := fresh(t)
			data := []byte("no orphan")
			i := f.upload(t, data, 1)
			var held *S.Lease
			if mode == "write" {
				f.raw(t, func(c *sql.Conn) error {
					_, e := c.ExecContext(tc(t), "CREATE TRIGGER synthetic_fail BEFORE INSERT ON attachment BEGIN SELECT RAISE(ABORT,'synthetic fault'); END")
					return e
				})
			} else {
				var e error
				held, e = f.s.Reader(tc(t))
				if e != nil {
					t.Fatal(e)
				}
				if e = held.Do(tc(t), func(c *sql.Conn) error {
					_, e := c.ExecContext(tc(t), "BEGIN")
					if e != nil {
						return e
					}
					var n int
					return c.QueryRowContext(tc(t), "SELECT count(*) FROM card").Scan(&n)
				}); e != nil {
					t.Fatal(e)
				}
			}
			r, _ := f.add(t, i, data)
			if held != nil {
				if e := held.Close(); e != nil {
					t.Fatal(e)
				}
			}
			if r.Unknown == nil {
				t.Fatal("failed commit inferred definitive result")
			}
			for _, table := range []string{"blob", "attachment", "current_attachment_pin", "operation_blob_pin", "card_revision"} {
				if f.value(t, "SELECT count(*) FROM "+table) != 0 {
					t.Fatal("orphan", table)
				}
			}
			if f.value(t, "SELECT revision FROM board") != 1 || f.value(t, "SELECT revision FROM card") != 1 {
				t.Fatal("partial revision")
			}
		})
	}
}
func TestIntegrityCollisionAndDownloadFence(t *testing.T) {
	for _, read := range []bool{false, true} {
		t.Run(fmt.Sprint(read), func(t *testing.T) {
			f := fresh(t)
			data := []byte("good")
			i := f.upload(t, data, 1)
			r, _ := f.add(t, i, data)
			id := committed(t, r)
			// Synthetic corruption bypasses the immutable BLOB trigger deliberately.
			f.raw(t, func(c *sql.Conn) error {
				if _, e := c.ExecContext(tc(t), "DROP TRIGGER blob_immutable"); e != nil {
					return e
				}
				_, e := c.ExecContext(tc(t), "UPDATE blob SET bytes=?", []byte("evil"))
				return e
			})
			if read {
				if _, e := f.a.Read(tc(t), f.g, board, inc, actor, id); !errors.Is(e, ErrIntegrity) {
					t.Fatal("damaged download", e)
				}
			} else {
				r, _ = f.add(t, f.upload(t, data, 2), data)
				if r.Unknown == nil {
					t.Fatal("collision not unknown")
				}
			}
			if w, e := f.s.Writer(tc(t)); !errors.Is(e, S.ErrFenced) {
				if w != nil {
					w.Close()
				}
				t.Fatal("integrity failed to fence", e)
			}
			if _, e := recovery.RegistryFor(f.s).BeginExportSnapshot(tc(t)); e == nil {
				t.Fatal("export accepted integrity fence")
			}
		})
	}
}
func TestConcurrentSameRevisionAndExportPins(t *testing.T) {
	f := fresh(t)
	data := []byte("same bytes")
	a, b := f.upload(t, data, 1), f.upload(t, data, 1)
	var wg sync.WaitGroup
	out := make(chan writer.Result, 2)
	for _, i := range []contract.Intent{a, b} {
		wg.Add(1)
		go func(i contract.Intent) {
			defer wg.Done()
			u, e := Buffer(bytes.NewReader(data))
			if e != nil {
				panic(e)
			}
			r, _ := f.a.Add(context.Background(), f.g, Route{board, inc, f.card}, i, u)
			out <- r
		}(i)
	}
	wg.Wait()
	close(out)
	count := 0
	for r := range out {
		if r.Terminal != nil && r.Terminal.Outcome == "committed" {
			count++
		}
	}
	if count != 1 || f.value(t, "SELECT count(*) FROM attachment") != 1 {
		t.Fatal("concurrent CAS", count)
	}
	x, e := recovery.RegistryFor(f.s).BeginExportSnapshot(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	if len(x.Facts()) != 1 || x.Facts()[0].ByteLength != A.Number(len(data)) {
		t.Fatal("export pin snapshot")
	}
	if e = x.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestProtectedQuotaInclusiveAndDedup(t *testing.T) {
	f := fresh(t)
	var first []byte
	for n := 0; n < 7; n++ {
		size := MaxFileBytes
		if n == 6 {
			size = 4 * 1024 * 1024
		}
		data := bytes.Repeat([]byte{byte(n + 1)}, size)
		if n == 0 {
			first = data
		}
		r, _ := f.add(t, f.upload(t, data, uint64(n+1)), data)
		committed(t, r)
	}
	if f.value(t, "SELECT sum(byte_length) FROM blob") != 64*1024*1024 {
		t.Fatal("inclusive unique quota")
	}
	r, _ := f.add(t, f.upload(t, first, 8), first)
	committed(t, r)
	before := f.value(t, "SELECT count(*) FROM activity")
	r, _ = f.add(t, f.upload(t, []byte("x"), 9), []byte("x"))
	if r.Terminal == nil || r.Terminal.Outcome != "not_committed" || r.Terminal.Code != "BYTE_QUOTA_EXCEEDED" {
		t.Fatal("quota refusal")
	}
	if f.value(t, "SELECT revision FROM board") != 9 || f.value(t, "SELECT count(*) FROM blob") != 7 || f.value(t, "SELECT count(*) FROM attachment") != 8 || f.value(t, "SELECT count(*) FROM activity") != before || f.value(t, "SELECT count(*) FROM operation_blob_pin") != 8 {
		t.Fatal("quota refusal effects")
	}
}
func TestApprovalPriorReferencesAndCompactSourceProvenance(t *testing.T) {
	f := fresh(t)
	first, _ := f.add(t, f.upload(t, []byte("old"), 1), []byte("old"))
	old := committed(t, first)
	const approval = "80000000-0000-0000-0000-000000000001"
	f.raw(t, func(c *sql.Conn) error {
		for _, q := range []struct {
			q string
			a []any
		}{
			{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", []any{inc, approval, f.card, board, inc}},
			{"INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", []any{inc, approval, f.card, actor}},
			{"UPDATE card SET current_approval_id=? WHERE card_id=?", []any{approval, f.card}},
		} {
			if _, e := c.ExecContext(tc(t), q.q, q.a...); e != nil {
				return e
			}
		}
		return nil
	})
	r, _ := f.add(t, f.upload(t, []byte("new"), 2), []byte("new"))
	committed(t, r)
	if f.value(t, "SELECT count(*) FROM card WHERE current_approval_id IS NULL") != 1 || f.value(t, "SELECT count(*) FROM card_revision WHERE card_revision=2 AND json_extract(state,'$.currentApprovalId')='"+approval+"'") != 1 || f.value(t, "SELECT count(*) FROM revision_reference WHERE card_revision=2 AND kind='approval' AND entity_id='"+approval+"'") != 1 || f.value(t, "SELECT count(*) FROM revision_reference WHERE card_revision=2 AND kind='attachment' AND entity_id='"+old+"'") != 1 {
		t.Fatal("approval invalidation/prior reference loss")
	}
	const sourceBoard = "10000000-0000-0000-0000-000000000009"
	const sourceInc = "20000000-0000-0000-0000-000000000009"
	f.raw(t, func(c *sql.Conn) error {
		for _, q := range []struct {
			q string
			a []any
		}{
			{"UPDATE board SET revision=9", nil}, {"UPDATE card SET revision=4", nil},
			{`INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'card.edit','card',?,4,3,'COMMITTED','OK','2026-10-25T01:02:03Z',5,4,'2026-10-25T02:00:00Z','expired',6)`, []any{inc, sourceBoard, sourceInc, "50000000-0000-0000-0000-000000000099", actor, strings.Repeat("a", 64), f.card}},
			{"INSERT INTO operation_reference VALUES(?,?,?,?,'card',?,4)", []any{inc, sourceBoard, sourceInc, "50000000-0000-0000-0000-000000000099", f.card}},
		} {
			if _, e := c.ExecContext(tc(t), q.q, q.a...); e != nil {
				return e
			}
		}
		return nil
	})
	i := f.intent(t, "attachment.add", f.card, 9, 4, map[string]any{"name": "source prior", "mediaLabel": "", "byteLength": 0, "sha256": digest(nil)})
	r, _ = f.add(t, i, nil)
	committed(t, r)
	if f.value(t, "SELECT count(*) FROM card_revision WHERE card_revision=4 AND origin_board='"+sourceBoard+"' AND origin_incarnation='"+sourceInc+"' AND committed_at='2026-10-25T01:02:03Z'") != 1 || f.value(t, "SELECT count(*) FROM history_blob_pin WHERE card_revision=4 AND origin_board='"+sourceBoard+"'") != 2 {
		t.Fatal("compact source provenance/pins rewritten")
	}
}
func TestIdentityMismatchCountersAndImmutableAssociation(t *testing.T) {
	f := fresh(t)
	data := []byte("same")
	i := f.upload(t, data, 1)
	r, _ := f.add(t, i, data)
	id := committed(t, r)
	_, op, _, _, _, _ := i.Context()
	wire, _ := json.Marshal(map[string]any{"opId": op, "incarnation": inc, "target": map[string]string{"cardId": f.card}, "expectedRevision": 1, "expectedCardRevision": 1, "input": map[string]any{"name": "changed", "mediaLabel": "", "byteLength": len(data), "sha256": digest(data)}})
	changed, e := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "attachment.add", BoardID: board, TargetID: f.card}, contract.AuthenticatedActor{ActorID: actor})
	if e != nil {
		t.Fatal(e)
	}
	r, _ = f.add(t, changed, data)
	if r.Refused == nil || r.Refused.Error.Code != "OPERATION_MISMATCH" {
		t.Fatal("operation mismatch")
	}
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(tc(t), "UPDATE attachment SET name='changed' WHERE attachment_id=?", id)
		if e == nil {
			return errors.New("immutable association modified")
		}
		return nil
	})
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(tc(t), "UPDATE board SET next_event=?", contract.MaxInteger)
		return e
	})
	r, _ = f.add(t, f.upload(t, data, 2), data)
	if r.Terminal == nil || r.Terminal.Code != "COUNTER_OVERFLOW" {
		t.Fatal("event overflow")
	}
	if f.value(t, "SELECT revision FROM board") != 2 || f.value(t, "SELECT count(*) FROM attachment") != 1 {
		t.Fatal("counter overflow effects")
	}
}
func TestDownloadAuthorizationAndBytesShareRevokeSnapshot(t *testing.T) {
	f := fresh(t)
	data := []byte("one coherent snapshot")
	r, _ := f.add(t, f.upload(t, data, 1), data)
	id := committed(t, r)
	entered, release := make(chan struct{}), make(chan struct{})
	g, e := grants.New(installation, origin, func(ctx context.Context, rd writer.Read, n time.Time) (grants.Credential, error) {
		var enabled int
		if e := rd.Scan("SELECT enabled FROM grant_record WHERE incarnation=? AND grant_id=?", []any{inc, grant}, &enabled); e != nil {
			return grants.Credential{}, e
		}
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return grants.Credential{}, ctx.Err()
		}
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: actor, GrantID: grant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	type answer struct {
		d Download
		e error
	}
	download := make(chan answer, 1)
	go func() { d, e := f.a.Read(context.Background(), g, board, inc, actor, id); download <- answer{d, e} }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("read snapshot not entered")
	}
	writeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		l, e := f.s.Writer(ctx)
		if e != nil {
			writeDone <- e
			return
		}
		e = l.Do(ctx, func(c *sql.Conn) error { _, e := c.ExecContext(ctx, "UPDATE grant_record SET enabled=0"); return e })
		ce := l.Close()
		if e == nil {
			e = ce
		}
		writeDone <- e
	}()
	select {
	case e := <-writeDone:
		close(release)
		t.Fatal("revoke passed active reader snapshot", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case a := <-download:
		if a.e != nil || !bytes.Equal(a.d.Bytes, data) {
			t.Fatal("split authorization/data snapshot", a.e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download blocked")
	}
	select {
	case e := <-writeDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoke blocked")
	}
	if _, e := f.a.Read(tc(t), f.g, board, inc, actor, id); !errors.Is(e, grants.ErrDenied) {
		t.Fatal("subsequent read survived revoke", e)
	}
}
func TestUploadReaderMutationDuringAdmissionKeepsHealthyStore(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprint(reuse), func(t *testing.T) {
			f := fresh(t)
			data := []byte("good")
			revision := uint64(1)
			if reuse {
				r, _ := f.add(t, f.upload(t, data, revision), data)
				committed(t, r)
				revision++
			}
			upload, e := Buffer(bytes.NewReader(data))
			if e != nil {
				t.Fatal(e)
			}
			probes := 0
			g, e := grants.New(installation, origin, func(ctx context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
				// Runs after Add has checked the exact private buffer's hash. The old
				// bytes.Reader WriterTo path would pass that storage to the hostile Writer.
				cursor := upload.Reader()
				if direct, ok := cursor.(io.WriterTo); ok {
					_, _ = direct.WriteTo(&mutatingWriter{})
					return grants.Credential{}, errors.New("reader exposes WriterTo")
				}
				poison := &mutatingWriter{}
				copied, e := io.Copy(poison, cursor)
				if e != nil || copied != 4 || poison.calls != 1 {
					return grants.Credential{}, errors.New("mutation probe did not execute")
				}
				probes++
				var a string
				e = r.Scan("SELECT actor_id FROM grant_record WHERE incarnation=? AND grant_id=?", []any{inc, grant}, &a)
				return grants.Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: a, GrantID: grant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, e
			})
			if e != nil {
				t.Fatal(e)
			}
			result, fresh := f.a.Add(tc(t), g, Route{board, inc, f.card}, f.upload(t, data, revision), upload)
			id := committed(t, result)
			if !fresh || probes < 2 || string(upload.data) != "good" {
				t.Fatal("immutable upload admission", fresh, probes)
			}
			download, e := f.a.Read(tc(t), f.g, board, inc, actor, id)
			if e != nil || !bytes.Equal(download.Bytes, data) {
				t.Fatal("healthy bytes changed", e)
			}
			if f.value(t, "SELECT count(*) FROM blob") != 1 {
				t.Fatal("dedup/new BLOB count")
			}
			lease, e := f.s.Writer(tc(t))
			if e != nil {
				t.Fatal("healthy store falsely fenced", e)
			}
			if e = lease.Close(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestUploadRetainingSourceDuringAdmissionKeepsHealthyStore(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprint(reuse), func(t *testing.T) {
			f := fresh(t)
			data := []byte("good")
			revision := uint64(1)
			if reuse {
				r, _ := f.add(t, f.upload(t, data, revision), data)
				committed(t, r)
				revision++
			}
			source := &retainingReader{data: data}
			upload, e := Buffer(source)
			if e != nil {
				t.Fatal(e)
			}
			probes := 0
			g, e := grants.New(installation, origin, func(ctx context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
				// Runs after Add's hash check; the source retained only bounded scratch.
				source.mutate()
				probes++
				var a string
				e = r.Scan("SELECT actor_id FROM grant_record WHERE incarnation=? AND grant_id=?", []any{inc, grant}, &a)
				return grants.Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: a, GrantID: grant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, e
			})
			if e != nil {
				t.Fatal(e)
			}
			result, fresh := f.a.Add(tc(t), g, Route{board, inc, f.card}, f.upload(t, data, revision), upload)
			id := committed(t, result)
			if !fresh || probes < 2 || string(upload.data) != "good" {
				t.Fatal("source retention reached immutable upload", fresh, probes)
			}
			download, e := f.a.Read(tc(t), f.g, board, inc, actor, id)
			if e != nil || !bytes.Equal(download.Bytes, data) {
				t.Fatal("healthy bytes changed", e)
			}
			if f.value(t, "SELECT count(*) FROM blob") != 1 {
				t.Fatal("dedup/new BLOB count")
			}
			lease, e := f.s.Writer(tc(t))
			if e != nil {
				t.Fatal("healthy store falsely fenced", e)
			}
			if e = lease.Close(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
