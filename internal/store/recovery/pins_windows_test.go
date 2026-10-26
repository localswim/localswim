//go:build windows && amd64

package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/writer"
)

func (f *fixture) attachmentOutcome(t *testing.T, at A.Timestamp, source, committed bool) contract.Intent {
	t.Helper()
	origin := inc
	if source {
		origin = sourceInc
	}
	digest := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if !committed {
		digest = strings.Repeat("f", 64)
	}
	wire, _ := json.Marshal(map[string]any{"opId": uuid(t), "incarnation": origin, "target": map[string]any{"cardId": card}, "expectedRevision": 3, "expectedCardRevision": 3, "input": map[string]any{"name": "Historical attachment", "mediaLabel": "", "sha256": digest, "byteLength": 0}})
	i, e := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "attachment.add", BoardID: bid, TargetID: card}, contract.AuthenticatedActor{ActorID: human})
	if e != nil {
		t.Fatal(e)
	}
	o, e := outcome.Base(i)
	if e != nil {
		t.Fatal(e)
	}
	o.TerminalAt = at
	if committed {
		o.TerminalClass = "COMMITTED"
		o.Code = "OK"
		r, cr := A.Positive(4), A.Positive(4)
		o.CommittedRevision = &r
		o.CommittedCardRevision = &cr
		v := A.Number(4)
		o.ResultRefs = []A.FactRef{{Kind: "attachment", ID: att}, {Kind: "card", ID: card, Revision: &v}}
		o.Full = &A.FullOutcome{Intent: i.CanonicalBytes(), Result: json.RawMessage(`{"attachmentId":"` + att + `","cardId":"` + card + `"}`)}
	} else {
		o.TerminalClass = "NOT_COMMITTED"
		o.Code = "FORBIDDEN"
		raw, _ := json.Marshal(deny("FORBIDDEN"))
		o.Full = &A.FullOutcome{Intent: i.CanonicalBytes(), Result: raw}
	}
	if e = A.Validate(o); e != nil {
		t.Fatal(e)
	}
	f.raw(t, func(c *sql.Conn) error {
		if !source {
			return outcome.Insert(tc(t), c, o)
		}
		// Source ledger fixtures use current storage FKs but immutable original
		// semantic identity. S02 Insert deliberately supports current authority only.
		ctx := tc(t)
		run := func(q string, args ...any) error { _, e := c.ExecContext(ctx, q, args...); return e }
		var rev, cardRev any
		if committed {
			rev = 4
			cardRev = 4
		}
		if e := run("INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision) VALUES(?,?,?,?,0,?,?,'attachment.add','card',?,3,3,?,?,?,?,?)", inc, bid, sourceInc, string(o.OpID), human, string(o.Fingerprint), card, string(o.TerminalClass), o.Code, string(at), rev, cardRev); e != nil {
			return e
		}
		if e := run("INSERT INTO operation_full VALUES(?,?,?,?,?,?)", inc, bid, sourceInc, string(o.OpID), string(o.Full.Intent), string(o.Full.Result)); e != nil {
			return e
		}
		if e := run("INSERT INTO byte_fact VALUES(?,?,?,?,?,0)", inc, bid, sourceInc, string(o.OpID), digest); e != nil {
			return e
		}
		if committed {
			if e := run("INSERT INTO operation_reference(incarnation,origin_board,origin_incarnation,op_id,kind,entity_id,revision) VALUES(?,?,?,?,'attachment',?,NULL)", inc, bid, sourceInc, string(o.OpID), att); e != nil {
				return e
			}
			if e := run("INSERT INTO operation_reference(incarnation,origin_board,origin_incarnation,op_id,kind,entity_id,revision) VALUES(?,?,?,?,'card',?,4)", inc, bid, sourceInc, string(o.OpID), card); e != nil {
				return e
			}
			return run("INSERT INTO operation_blob_pin VALUES(?,?,?,?,?,?,0)", inc, bid, sourceInc, string(o.OpID), att, digest)
		}
		return nil
	})
	return i
}

func TestFullCommittedOutcomePinsMetadataAndBytes(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			f := newFixture(t)
			f.seed(t, A.Timestamp(baseTime.Add(-3*time.Hour).Format(stamp)), true)
			i := f.attachmentOutcome(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), source, true)
			p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Add(-2 * time.Hour).Format(stamp))})
			if len(p.Inventory.Bytes.DomainAfter) != 1 || len(p.Inventory.Bytes.Reclaimable) != 0 {
				t.Fatal("surviving full committed operation did not pin")
			}
			terminal(t, confirm(t, f, p), "OK")
			if f.value(t, "SELECT count(*) FROM attachment") != 1 || f.value(t, "SELECT count(*) FROM blob") != 1 {
				t.Fatal("full outcome metadata/bytes pruned")
			}
			p = f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
			if len(p.Inventory.Bytes.Reclaimable) != 1 {
				t.Fatal("last full pin not reclaimable")
			}
			terminal(t, confirm(t, f, p), "OK")
			if f.value(t, "SELECT count(*) FROM attachment") != 0 || f.value(t, "SELECT count(*) FROM blob") != 0 || f.value(t, "SELECT count(*) FROM byte_fact") != 1 {
				t.Fatal("compact facts should survive without metadata/bytes")
			}
			o, _ := outcome.Base(i)
			r := f.svc.engine.Lookup(tc(t), outcome.IdentityOf(o), f.svc.grants.Authority)
			if source {
				if r.Unknown == nil || r.Unknown.Error.Code != "SOURCE_AUTHORITY_UNAVAILABLE" {
					t.Fatal("source receipt became destination authority")
				}
			} else {
				terminal(t, r, "OK")
				if r.Terminal.Retention != "compact" || len(r.Terminal.ByteFacts) != 1 {
					t.Fatal("compact receipt byte evidence lost")
				}
			}
		})
	}
}

func TestRefusedSourceAndCurrentByteFactsNeverPin(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), false)
	f.attachmentOutcome(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), false, false)
	f.attachmentOutcome(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), true, false)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	if p.Inventory.Counts.OutcomePayloads != 2 || len(p.Inventory.Bytes.DomainBefore) != 0 || len(p.Inventory.Bytes.Reclaimable) != 0 {
		t.Fatal("refused byte fact pinned nonexistent bytes")
	}
	terminal(t, confirm(t, f, p), "OK")
	if f.value(t, "SELECT count(*) FROM byte_fact") != 2 || f.value(t, "SELECT count(*) FROM entity WHERE kind='attachment'") != 0 {
		t.Fatal("refused attempted reference fabricated entity or lost facts")
	}
}

func TestCurrentAndSurvivingHistoryPinsFormDistinctUnion(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), true)
	f.raw(t, func(c *sql.Conn) error {
		ctx := tc(t)
		if _, e := c.ExecContext(ctx, "INSERT INTO current_reference VALUES(?,?,'attachment',?)", inc, card, att); e != nil {
			return e
		}
		_, e := c.ExecContext(ctx, "INSERT INTO current_attachment_pin SELECT incarnation,attachment_id,sha256,byte_length FROM attachment")
		return e
	})
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	if len(p.Inventory.Bytes.DomainBefore) != 1 || len(p.Inventory.Bytes.DomainAfter) != 1 || len(p.Inventory.Bytes.Reclaimable) != 0 {
		t.Fatal("distinct current/history union")
	}
	terminal(t, confirm(t, f, p), "OK")
	if f.value(t, "SELECT count(*) FROM attachment") != 1 || f.value(t, "SELECT count(*) FROM blob") != 1 {
		t.Fatal("current bytes expired")
	}
}

func TestRegistryGuardCancellationAndTwoReaderQueue(t *testing.T) {
	f := newFixture(t)
	g := testGuard(t, f.svc.registry)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e := f.svc.registry.Guard(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("pin guard ignored deadline")
	}
	g.Close()
	x, e := f.svc.registry.BeginExportSnapshot(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { x.Close() })
	y, e := f.svc.registry.BeginExportSnapshot(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { y.Close() })
	queued := make(chan *ExportSnapshot, 1)
	failure := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		z, e := f.svc.registry.BeginExportSnapshot(ctx)
		if e != nil {
			failure <- e
			return
		}
		queued <- z
	}()
	// S01 bounds the two occupied readers; releasing a reader requires no
	// writer/registry and must let the queued writer make progress.
	closed := make(chan error, 1)
	go func() { closed <- x.Close() }()
	var z *ExportSnapshot
	select {
	case z = <-queued:
	case e = <-failure:
		t.Fatal(e)
	case <-time.After(5 * time.Second):
		t.Fatal("queued export/close deadlock")
	}
	t.Cleanup(func() { z.Close() })
	select {
	case e = <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first export close deadlock")
	}
	if e = y.Close(); e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestMatchingOutcomeDoesNotAcquireRegistryOrCache(t *testing.T) {
	f := newFixture(t)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	i := intent(t, p, human, uuid(t))
	terminal(t, f.svc.Confirm(tc(t), route, i), "OK")
	f.svc.previews = nil
	g := testGuard(t, f.svc.registry)
	defer g.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	terminal(t, f.svc.Confirm(ctx, route, i), "OK")
	f.exec(t, "DELETE FROM grant_right WHERE right_name='read-own-outcome'")
	r := f.svc.Confirm(ctx, route, i)
	if r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("retained outcome bypassed current own-outcome right")
	}
}

func TestDifferentCurrentlyAuthorizedHumanCannotConfirmPreview(t *testing.T) {
	f := newFixture(t)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	other := uuid(t)
	f.raw(t, func(c *sql.Conn) error {
		ctx := tc(t)
		if _, e := c.ExecContext(ctx, "INSERT INTO actor VALUES(?,?,'human','Second synthetic administrator',?,?)", inc, other, bid, inc); e != nil {
			return e
		}
		_, e := c.ExecContext(ctx, "UPDATE grant_record SET actor_id=?", other)
		return e
	})
	f.who = other
	terminal(t, confirm(t, f, p), "PREVIEW_MISMATCH")
	if f.value(t, "SELECT revision FROM board") != 0 {
		t.Fatal("another human confirmed original actor's loss set")
	}
}

func TestRouteIncarnationAndOperationIdentityMismatch(t *testing.T) {
	f := newFixture(t)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	op := uuid(t)
	i := intent(t, p, human, op)
	terminal(t, f.svc.Confirm(tc(t), route, i), "OK")
	bad := route
	bad.BoardID = human
	if r := f.svc.Confirm(tc(t), bad, i); r.Refused == nil || r.Refused.Error.Code != "INVALID_INPUT" {
		t.Fatal("retained route mismatch accepted")
	}
	p.Input = json.RawMessage(`{"throughTime":"2026-10-25T12:00:00Z"}`)
	r := f.svc.Confirm(tc(t), route, intent(t, p, human, op))
	if r.Refused == nil || r.Refused.Error.Code != "OPERATION_MISMATCH" {
		t.Fatal("operation identity mismatch changed original receipt")
	}
	p.Incarnation = sourceInc
	r = f.svc.Confirm(tc(t), Route{bid, sourceInc}, intent(t, p, human, uuid(t)))
	if r.Unknown == nil || r.Unknown.Error.Code != "SOURCE_AUTHORITY_UNAVAILABLE" {
		t.Fatal("source identity fabricated noncommit")
	}
}

func TestPreviewCacheIsImmutableAndOriginalSelectionDoesNotGrow(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-24*time.Hour+time.Second).Format(stamp)), true)
	p := f.preview(t, "retention.set", A.Retention{HistoryDays: 1, ActivityDays: 1, OutcomeDays: 1})
	if p.Inventory.Counts.HistorySnapshots != 0 {
		t.Fatal("boundary selected early")
	}
	h := p.PreviewHash
	original, e := f.svc.cached(h)
	if e != nil {
		t.Fatal(e)
	}
	p.Input[0] = ' '
	p.Inventory.Bytes.DomainBefore[0].ByteLength = 1
	again, e := f.svc.cached(h)
	if e != nil || !reflectPreview(original, again) {
		t.Fatal("returned preview mutated immutable cache")
	}
	f.clock = f.clock.Add(2 * time.Second)
	terminal(t, confirm(t, f, again), "OK")
	if f.value(t, "SELECT count(*) FROM card_revision WHERE state IS NOT NULL") != 2 {
		t.Fatal("confirm added newly eligible records beyond original inventory")
	}
}
func reflectPreview(a, b A.Preview) bool {
	x, _ := A.Canonical(a)
	y, _ := A.Canonical(b)
	return string(x) == string(y)
}

// Confirm and preview share only registry observation, never reader handles.
// A queued preview cannot consume a reader slot while S02 reconciles a commit.
func TestQueuedPreviewCannotStarveS02Reconciliation(t *testing.T) {
	f := newFixture(t)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	entered := make(chan struct{})
	resume := make(chan struct{})
	f.svc.fault = func(stage string) error {
		if stage == "after-prune" {
			close(entered)
			<-resume
		}
		return nil
	}
	done := make(chan writer.Result, 1)
	i := intent(t, p, human, uuid(t))
	go func() { done <- f.svc.Confirm(context.Background(), route, i) }()
	<-entered
	previews := make(chan error, 2)
	for n := 0; n < 2; n++ {
		go func() {
			raw, _ := A.Canonical(A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
			_, e := f.svc.Preview(context.Background(), route, human, A.PreviewRequest{Incarnation: inc, ExpectedRevision: 0, Command: "history.clear", Input: raw})
			previews <- e
		}()
	}
	close(resume)
	select {
	case r := <-done:
		terminal(t, r, "OK")
	case <-time.After(5 * time.Second):
		t.Fatal("queued previews starved terminal reconciliation")
	}
	for n := 0; n < 2; n++ {
		select {
		case e := <-previews:
			var pe *PreviewError
			if !errors.As(e, &pe) || pe.Code != "REVISION_CONFLICT" {
				t.Fatal("queued preview did not observe current revision", e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued preview deadlock")
		}
	}
}

func TestActualSQLiteDomainAndExportQuotaUnion(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), false)
	insert := func(c *sql.Conn, id string, data []byte) error {
		ctx := tc(t)
		h := fmt.Sprintf("%x", sha256.Sum256(data))
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO blob VALUES(?,?,?,?)", []any{inc, h, len(data), data}},
			{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,created_at) VALUES(?,'attachment',?,?,?,?,?)", []any{inc, id, card, bid, inc, baseTime.Format(stamp)}},
			{"INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'Synthetic quota bytes','',?,?,?)", []any{inc, id, card, h, len(data), baseTime.Format(stamp)}},
			{"INSERT INTO current_reference VALUES(?,?,'attachment',?)", []any{inc, card, id}},
			{"INSERT INTO current_attachment_pin VALUES(?,?,?,?)", []any{inc, id, h, len(data)}},
		} {
			if _, e := c.ExecContext(ctx, q.sql, q.args...); e != nil {
				return e
			}
		}
		return nil
	}
	oldID := uuid(t)
	f.raw(t, func(c *sql.Conn) error { return insert(c, oldID, []byte{77}) })
	x, e := f.svc.registry.BeginExportSnapshot(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { x.Close() })
	// The old reader is closed before pin-unregistration's writer admission;
	// its pending registration still protects the actual old one-byte BLOB.
	if e = x.reader.Close(); e != nil {
		t.Fatal(e)
	}
	f.raw(t, func(c *sql.Conn) error {
		ctx := tc(t)
		if _, e := c.ExecContext(ctx, "DELETE FROM current_attachment_pin WHERE attachment_id=?", oldID); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, "DELETE FROM current_reference WHERE entity_id=?", oldID); e != nil {
			return e
		}
		for n := 0; n < 7; n++ {
			size := 10485760
			if n == 6 {
				size = 4194304
			}
			if e := insert(c, uuid(t), bytes.Repeat([]byte{byte(n + 1)}, size)); e != nil {
				return e
			}
		}
		return nil
	})
	if f.value(t, "SELECT sum(byte_length) FROM current_attachment_pin") != 67108864 {
		t.Fatal("actual inclusive domain quota fixture")
	}
	g := testGuard(t, f.svc.registry)
	l, e := f.store.Reader(tc(t))
	if e != nil {
		g.Close()
		t.Fatal(e)
	}
	e = l.Do(tc(t), func(c *sql.Conn) error { return g.CheckQuota(readAccess{tc(t), c}) })
	l.Close()
	g.Close()
	if e == nil {
		t.Fatal("active export extra byte bypassed distinct union quota")
	}
	raw, _ := A.Canonical(A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	if _, e = f.svc.Preview(tc(t), route, human, A.PreviewRequest{Incarnation: inc, ExpectedRevision: 4, Command: "history.clear", Input: raw}); e == nil {
		t.Fatal("over-quota preview accepted")
	}
	if e = x.Close(); e != nil {
		t.Fatal(e)
	}
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	if p.Inventory.Bytes.DomainBeforeBytes != 67108864 || len(p.Inventory.Bytes.DomainBefore) != 7 || f.value(t, "SELECT count(*) FROM blob") != 7 {
		t.Fatal("last export pin cleanup or exact 64MiB union")
	}
}

func TestMaintenanceVerifierHasOnlyReadAccess(t *testing.T) {
	f := newFixture(t)
	verify := func(ctx context.Context, r writer.Read) (MaintenancePrincipal, error) {
		if _, ok := r.(writer.Write); ok {
			return MaintenancePrincipal{}, errors.New("write facet leaked")
		}
		return maintenance(ctx, r)
	}
	result, e := f.svc.Expire(tc(t), route, verify)
	if e != nil || result.Unknown || result.Revision == nil {
		t.Fatal("maintenance read facet", e)
	}
}

func TestPreviewVerifierPanicReleasesReaderAndGuard(t *testing.T) {
	f := newFixture(t)
	g, e := grants.New(install, webOrigin, func(context.Context, writer.Read, time.Time) (grants.Credential, error) {
		panic("synthetic verifier failure")
	})
	if e != nil {
		t.Fatal(e)
	}
	s := New(f.store, g)
	raw, _ := A.Canonical(A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	if _, e = s.Preview(tc(t), route, human, A.PreviewRequest{Incarnation: inc, Command: "history.clear", Input: raw}); e == nil {
		t.Fatal("verifier panic accepted")
	}
	guard := testGuard(t, s.registry)
	guard.Close()
	x, e := s.registry.BeginExportSnapshot(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = x.Close(); e != nil {
		t.Fatal(e)
	}
}
