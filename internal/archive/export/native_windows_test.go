//go:build windows && amd64

package export

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/attachments"
	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/workflow"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
)

const testActor = "30000000-0000-0000-0000-000000000001"
const testGrant = "30000000-0000-0000-0000-000000000002"
const testInstall = "60000000-0000-4000-8000-000000000001"
const testOrigin = "https://synthetic.localhost:47443"

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func testUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type nativeFixture struct {
	s    *S.Store
	g    *grants.Service
	card string
	rev  uint64
}

func (f *nativeFixture) raw(t *testing.T, fn func(*sql.Conn) error) {
	t.Helper()
	l, e := f.s.Writer(testCtx(t))
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(testCtx(t), fn)
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
}
func newNative(t *testing.T) *nativeFixture {
	t.Helper()
	root := os.Getenv("S01_TEST_ROOT")
	if root == "" {
		t.Skip("native synthetic store requires private S01_TEST_ROOT")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if !filepath.IsAbs(root) || strings.HasPrefix(strings.ToLower(filepath.Clean(root))+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("private absolute fixture root required")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		t.Fatal(e)
	}
	defer token.Close()
	u, e := token.GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, testUUID(t)[:8])
	if e = windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); e != nil {
		t.Fatal(e)
	}
	s, e := S.Create(testCtx(t), dir, schema.Identity{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	f := &nativeFixture{s: s}
	f.raw(t, func(c *sql.Conn) error {
		ctx := testCtx(t)
		if _, e := c.ExecContext(ctx, "INSERT INTO actor VALUES(?,?,?,?,?,?)", string(testIdentity.Incarnation), testActor, "human", "Synthetic historical label", string(testIdentity.BoardID), string(testIdentity.Incarnation)); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, "INSERT INTO grant_fact VALUES(?,?)", string(testIdentity.Incarnation), testGrant); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, "INSERT INTO grant_record VALUES(?,?,?,1)", string(testIdentity.Incarnation), testGrant, testActor); e != nil {
			return e
		}
		for _, right := range []string{"create", "edit", "comment", "attach", "read", "read-own-outcome", "export", "history-admin"} {
			if _, e := c.ExecContext(ctx, "INSERT INTO grant_right VALUES(?,?,?)", string(testIdentity.Incarnation), testGrant, right); e != nil {
				return e
			}
		}
		return nil
	})
	f.g, e = grants.New(testInstall, testOrigin, func(ctx context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: testInstall, Origin: testOrigin, BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation), ActorID: testActor, GrantID: testGrant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	i := f.intent(t, "card.create", "", 0, 0, map[string]any{"title": "Synthetic", "description": "earlier prose \r\n Î© <&>"})
	result := writer.New(f.s).Submit(testCtx(t), i, cards.New(f.g).Callbacks(cards.Route{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation)}))
	assertCommit(t, result)
	for _, v := range result.Terminal.ResultRefs {
		if v.Kind == "card" {
			f.card = string(v.ID)
		}
	}
	f.rev = 1
	return f
}
func assertCommit(t *testing.T, r writer.Result) {
	t.Helper()
	if r.Terminal == nil || r.Terminal.Outcome != "committed" {
		t.Fatalf("synthetic command did not commit (terminal=%v refusal=%v unknown=%v)", r.Terminal != nil, r.Refused != nil, r.Unknown != nil)
	}
}
func (f *nativeFixture) intent(t *testing.T, cmd, id string, rev, cr uint64, input any) contract.Intent {
	t.Helper()
	var target, cardRev any
	if id != "" {
		target = map[string]string{"cardId": id}
		cardRev = cr
	}
	raw, e := json.Marshal(map[string]any{"opId": testUUID(t), "incarnation": testIdentity.Incarnation, "target": target, "expectedRevision": rev, "expectedCardRevision": cardRev, "input": input})
	if e != nil {
		t.Fatal(e)
	}
	i, e := T.FreezeMutation(raw, contract.ResolvedRoute{Command: contract.Command(cmd), BoardID: string(testIdentity.BoardID), TargetID: id}, contract.AuthenticatedActor{ActorID: testActor})
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func (f *nativeFixture) add(t *testing.T, data []byte) {
	t.Helper()
	i := f.intent(t, "attachment.add", f.card, f.rev, f.rev, map[string]any{"name": "../inert Î©.txt", "mediaLabel": "application/octet-stream", "sha256": digest(data), "byteLength": len(data)})
	u, e := attachments.Buffer(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	r, _ := attachments.New(f.s).Add(testCtx(t), f.g, attachments.Route{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation), CardID: f.card}, i, u)
	assertCommit(t, r)
	f.rev++
}
func (f *nativeFixture) request() Request {
	return Request{Identity: testIdentity, Revision: A.Number(f.rev), ExportedAt: testTime, Verify: func(ctx context.Context, r writer.Read) error {
		return f.g.Require(ctx, r, string(testIdentity.BoardID), string(testIdentity.Incarnation), testActor, "read", "export")
	}}
}
func (f *nativeFixture) prepare(t *testing.T) *Prepared {
	t.Helper()
	p, e := Prepare(testCtx(t), f.s, f.request())
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestNativeSnapshotProtectedBytesAndProse(t *testing.T) {
	f := newNative(t)
	raw := []byte{0, 255, 13, 10, 42}
	f.add(t, raw)
	f.add(t, raw)
	f.add(t, nil)
	i := f.intent(t, "comment.add", f.card, f.rev, f.rev, map[string]any{"text": "immutable comment Î©\n"})
	r := writer.New(f.s).Submit(testCtx(t), i, cards.New(f.g).Callbacks(cards.Route{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation), CardID: f.card}))
	assertCommit(t, r)
	f.rev++
	p := f.prepare(t)
	names, data := readArchive(t, p)
	if len(names) != 8 || !bytes.Equal(data["blobs/sha256-"+string(digest(raw))], raw) {
		t.Fatal("distinct raw inventory")
	}
	if v, ok := data["blobs/sha256-"+string(digest(nil))]; !ok || len(v) != 0 {
		t.Fatal("empty blob absent")
	}
	b, e := A.DecodeReadable[A.Board](data["board.json"])
	if e != nil {
		t.Fatal(e)
	}
	h, e := A.DecodeReadable[A.History](data["history.json"])
	if e != nil {
		t.Fatal(e)
	}
	o, e := A.DecodeReadable[A.Outcomes](data["outcomes.json"])
	if e != nil {
		t.Fatal(e)
	}
	a, e := A.DecodeReadable[A.Activity](data["activity.json"])
	if e != nil {
		t.Fatal(e)
	}
	m, e := A.DecodeReadable[A.Manifest](data["manifest.json"])
	if e != nil {
		t.Fatal(e)
	}
	pins, e := A.ValidateDocuments(m, b, h, a, o)
	if e != nil || len(pins) != 2 {
		t.Fatal("graph", e)
	}
	if b.Revision != A.Number(f.rev) || len(h.Records) != 4 || len(o.Operations) != 5 || len(b.Comments) != 1 || b.Cards[0].Description != "earlier prose \r\n Î© <&>" {
		t.Fatal("snapshot projection")
	}
	joined := bytes.Join([][]byte{data["board.json"], data["outcomes.json"]}, nil)
	for _, secret := range []string{testGrant, testOrigin, "grant_right", "credential"} {
		if bytes.Contains(joined, []byte(secret)) {
			t.Fatal("excluded authority material")
		}
	}
}

func TestNativeVerifierAndExactPreconditions(t *testing.T) {
	f := newNative(t)
	req := f.request()
	req.Verify = nil
	if p, e := Prepare(testCtx(t), f.s, req); e == nil || p != nil {
		t.Fatal("missing verifier")
	}
	req = f.request()
	called := false
	real := req.Verify
	req.Verify = func(ctx context.Context, r writer.Read) error { called = true; return real(ctx, r) }
	req.Revision++
	if p, e := Prepare(testCtx(t), f.s, req); !errors.Is(e, ErrPrecondition) || p != nil || called {
		t.Fatal("stale precondition", e)
	}
	req = f.request()
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(testCtx(t), "DELETE FROM grant_right WHERE right_name='export'")
		return e
	})
	if p, e := Prepare(testCtx(t), f.s, req); e == nil || p != nil {
		t.Fatal("revoked export exposed prepared bytes")
	}
}

func TestNativeConcurrentClearKeepsOneSnapshot(t *testing.T) {
	f := newNative(t)
	f.add(t, []byte("protected while clear waits"))
	service := recovery.New(f.s, f.g)
	through := A.Timestamp(time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05Z")) // Select original records inclusively using the actual latest terminal time.
	f.raw(t, func(c *sql.Conn) error {
		return c.QueryRowContext(testCtx(t), "SELECT max(terminal_at) FROM operation").Scan(&through)
	})
	input, _ := json.Marshal(A.ClearInput{ThroughTime: through})
	entered, release := make(chan struct{}), make(chan struct{})
	req := f.request()
	real := req.Verify
	req.Verify = func(ctx context.Context, r writer.Read) error {
		if e := real(ctx, r); e != nil {
			return e
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	type preparedResult struct {
		p *Prepared
		e error
	}
	prepared := make(chan preparedResult, 1)
	go func() { p, e := Prepare(testCtx(t), f.s, req); prepared <- preparedResult{p, e} }()
	<-entered
	preview, e := service.Preview(testCtx(t), recovery.Route{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation)}, testActor, A.PreviewRequest{Incarnation: testIdentity.Incarnation, ExpectedRevision: A.Number(f.rev), Command: "history.clear", Input: input})
	if e != nil {
		t.Fatal(e)
	}
	confirm := A.Confirmation{OpID: A.UUID(testUUID(t)), Incarnation: testIdentity.Incarnation, ExpectedRevision: A.Number(f.rev), Command: "history.clear", Input: input, PreviewHash: preview.PreviewHash}
	wire, _ := json.Marshal(confirm)
	intent, e := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "history.clear", BoardID: string(testIdentity.BoardID)}, contract.AuthenticatedActor{ActorID: testActor})
	if e != nil {
		t.Fatal(e)
	}
	cleared := make(chan writer.Result, 1)
	go func() {
		cleared <- service.Confirm(testCtx(t), recovery.Route{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation)}, intent)
	}()
	// COMMIT cannot finish while the prepared export still owns its reader.
	select {
	case <-cleared:
		t.Fatal("clear crossed active snapshot")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	got := <-prepared
	if got.e != nil {
		t.Fatal(got.e)
	}
	assertCommit(t, <-cleared)
	_, data := readArchive(t, got.p)
	old, e := A.DecodeReadable[A.History](data["history.json"])
	if e != nil || len(old.Records) != 1 || old.Records[0].State == nil {
		t.Fatal("clear changed pinned history", e)
	}
	f.rev++
	_, newData := readArchive(t, f.prepare(t))
	now, e := A.DecodeReadable[A.History](newData["history.json"])
	if e != nil || now.Records[0].State != nil || now.Records[0].Removal == nil {
		t.Fatal("clear did not compact future snapshot", e)
	}
	o, e := A.DecodeReadable[A.Outcomes](newData["outcomes.json"])
	if e != nil {
		t.Fatal(e)
	}
	compact := 0
	for _, v := range o.Operations {
		if v.Full == nil {
			compact++
		}
	}
	if compact != 2 {
		t.Fatal("compact facts lost")
	}
}

func TestNativeTamperedBlobRefusesAndFences(t *testing.T) {
	f := newNative(t)
	f.add(t, []byte("abc"))
	f.raw(t, func(c *sql.Conn) error {
		if _, e := c.ExecContext(testCtx(t), "DROP TRIGGER blob_immutable"); e != nil {
			return e
		}
		_, e := c.ExecContext(testCtx(t), "UPDATE blob SET bytes=?", []byte("xyz"))
		return e
	})
	if p, e := Prepare(testCtx(t), f.s, f.request()); e == nil || p != nil {
		t.Fatal("tampered bytes completed")
	}
	if l, e := f.s.Writer(testCtx(t)); !errors.Is(e, S.ErrFenced) {
		if l != nil {
			l.Close()
		}
		t.Fatal("integrity failure did not fence", e)
	}
}

func TestNativeHistoricalAndOutcomeOnlyPinsAndSourceProvenance(t *testing.T) {
	f := newNative(t)
	historical, operationOnly := []byte("history only bytes"), []byte("full COMMITTED outcome only bytes")
	f.add(t, historical)
	f.add(t, operationOnly)
	oldInc := "20000000-0000-0000-0000-000000000002"
	missingCard := "40000000-0000-0000-0000-000000000099"
	attempted := digest([]byte("attempted bytes never stored"))
	sourceOp := testUUID(t)
	f.raw(t, func(c *sql.Conn) error {
		ctx := testCtx(t)
		run := func(q string, args ...any) error { _, e := c.ExecContext(ctx, q, args...); return e }
		if e := run("BEGIN IMMEDIATE"); e != nil {
			return e
		}
		if e := run("DELETE FROM current_attachment_pin"); e != nil {
			return e
		}
		if e := run("DELETE FROM current_reference WHERE kind='attachment'"); e != nil {
			return e
		}
		// Compact source refusal preserves attempted references/facts, with no
		// fabricated entity or BLOB. It remains outside the destination ledger.
		if e := run(`INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'attachment.add','card',?,1,1,'NOT_COMMITTED','NOT_FOUND',?,?,'cleared',3)`, string(testIdentity.Incarnation), string(testIdentity.BoardID), oldInc, sourceOp, testActor, string(digest([]byte("inert compact source fingerprint"))), missingCard, string(testTime), string(testTime)); e != nil {
			return e
		}
		if e := run("INSERT INTO byte_fact VALUES(?,?,?,?,?,?)", string(testIdentity.Incarnation), string(testIdentity.BoardID), oldInc, sourceOp, string(attempted), len("attempted bytes never stored")); e != nil {
			return e
		}
		if e := run("INSERT INTO lineage VALUES(?,?,?,?,?)", string(testIdentity.Incarnation), string(testIdentity.BoardID), oldInc, string(digest([]byte("synthetic source manifest"))), 3); e != nil {
			return e
		}
		return run("COMMIT")
	})
	_, data := readArchive(t, f.prepare(t))
	if !bytes.Equal(data["blobs/sha256-"+string(digest(historical))], historical) || !bytes.Equal(data["blobs/sha256-"+string(digest(operationOnly))], operationOnly) {
		t.Fatal("non-current protected bytes omitted")
	}
	if _, ok := data["blobs/sha256-"+string(attempted)]; ok {
		t.Fatal("compact refusal pinned attempted bytes")
	}
	b, e := A.DecodeReadable[A.Board](data["board.json"])
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Cards[0].AttachmentIDs) != 0 {
		t.Fatal("fixture retained current refs")
	}
	o, e := A.DecodeReadable[A.Outcomes](data["outcomes.json"])
	if e != nil || len(o.SourceArchives) != 1 || len(o.SourceArchives[0].Operations) != 1 || o.SourceArchives[0].Operations[0].OpID != A.UUID(sourceOp) {
		t.Fatal("source outcomes merged", e)
	}
	m, e := A.DecodeReadable[A.Manifest](data["manifest.json"])
	if e != nil || len(m.Lineage) != 1 || m.Lineage[0].Identity.Incarnation != A.UUID(oldInc) {
		t.Fatal("source lineage lost", e)
	}
}

func TestNativeMissingProtectedBlobRefusesAndFences(t *testing.T) {
	f := newNative(t)
	f.add(t, []byte("missing immutable bytes"))
	f.raw(t, func(c *sql.Conn) error {
		ctx := testCtx(t)
		if _, e := c.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); e != nil {
			return e
		}
		_, e := c.ExecContext(ctx, "DELETE FROM blob")
		return e
	})
	if p, e := Prepare(testCtx(t), f.s, f.request()); e == nil || p != nil {
		t.Fatal("missing protected bytes completed")
	}
	if l, e := f.s.Writer(testCtx(t)); !errors.Is(e, S.ErrFenced) {
		if l != nil {
			l.Close()
		}
		t.Fatal("missing bytes did not fence", e)
	}
}

func TestNativeOversizeMetadataRefusesBeforeTextProjection(t *testing.T) {
	f := newNative(t)
	f.raw(t, func(c *sql.Conn) error {
		_, e := c.ExecContext(testCtx(t), "UPDATE actor SET label=printf('%.*c',33554433,'x')")
		return e
	})
	if p, e := Prepare(testCtx(t), f.s, f.request()); !errors.Is(e, ErrLimit) || p != nil {
		t.Fatal("oversize metadata did not refuse", e)
	}
	l, e := f.s.Writer(testCtx(t))
	if e != nil {
		t.Fatal("legitimate oversize metadata fenced store", e)
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestNativeDeclaredBlobLengthMismatchRefusesAndFences(t *testing.T) {
	f := newNative(t)
	f.add(t, []byte("abc"))
	f.raw(t, func(c *sql.Conn) error {
		ctx := testCtx(t)
		for _, q := range []string{"PRAGMA ignore_check_constraints=ON", "DROP TRIGGER blob_immutable", "UPDATE blob SET bytes=x'0102'"} {
			if _, e := c.ExecContext(ctx, q); e != nil {
				return e
			}
		}
		return nil
	})
	if p, e := Prepare(testCtx(t), f.s, f.request()); e == nil || p != nil {
		t.Fatal("length corruption completed")
	}
	if l, e := f.s.Writer(testCtx(t)); !errors.Is(e, S.ErrFenced) {
		if l != nil {
			l.Close()
		}
		t.Fatal("length corruption did not fence", e)
	}
}

func TestNativeRemovedLinkTombstoneSurvivesClearAndExport(t *testing.T) {
	f := newNative(t)
	create := f.intent(t, "card.create", "", f.rev, 0, map[string]any{"title": "Synthetic prerequisite", "description": ""})
	route := cards.Route{BoardID: string(testIdentity.BoardID), Incarnation: string(testIdentity.Incarnation)}
	result := writer.New(f.s).Submit(testCtx(t), create, cards.New(f.g).Callbacks(route))
	assertCommit(t, result)
	f.rev++
	var peer string
	for _, ref := range result.Terminal.ResultRefs {
		if ref.Kind == "card" {
			peer = string(ref.ID)
		}
	}
	wroute := workflow.Route{BoardID: route.BoardID, Incarnation: route.Incarnation, CardID: f.card}
	add := f.intent(t, "link.add", f.card, f.rev, 1, map[string]any{"kind": "informational", "targetCardId": peer})
	result = writer.New(f.s).Submit(testCtx(t), add, workflow.New(f.g).Callbacks(wroute))
	assertCommit(t, result)
	f.rev++
	var link string
	for _, ref := range result.Terminal.ResultRefs {
		if ref.Kind == "link" {
			link = string(ref.ID)
		}
	}
	remove := f.intent(t, "link.remove", f.card, f.rev, 2, map[string]any{"linkId": link})
	assertCommit(t, writer.New(f.s).Submit(testCtx(t), remove, workflow.New(f.g).Callbacks(wroute)))
	f.rev++
	var through A.Timestamp
	f.raw(t, func(c *sql.Conn) error {
		return c.QueryRowContext(testCtx(t), "SELECT max(terminal_at) FROM operation").Scan(&through)
	})
	input, _ := json.Marshal(A.ClearInput{ThroughTime: through})
	service := recovery.New(f.s, f.g)
	rroute := recovery.Route{BoardID: route.BoardID, Incarnation: route.Incarnation}
	preview, e := service.Preview(testCtx(t), rroute, testActor, A.PreviewRequest{Incarnation: testIdentity.Incarnation, ExpectedRevision: A.Number(f.rev), Command: "history.clear", Input: input})
	if e != nil {
		t.Fatal(e)
	}
	wire, _ := json.Marshal(A.Confirmation{OpID: A.UUID(testUUID(t)), Incarnation: testIdentity.Incarnation, ExpectedRevision: A.Number(f.rev), Command: "history.clear", Input: input, PreviewHash: preview.PreviewHash})
	confirm, e := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "history.clear", BoardID: route.BoardID}, contract.AuthenticatedActor{ActorID: testActor})
	if e != nil {
		t.Fatal(e)
	}
	assertCommit(t, service.Confirm(testCtx(t), rroute, confirm))
	f.rev++
	var storedCreator string
	f.raw(t, func(c *sql.Conn) error {
		return c.QueryRowContext(testCtx(t), "SELECT creator_id FROM entity WHERE kind='link' AND entity_id=? AND removed_at IS NOT NULL", link).Scan(&storedCreator)
	})
	if storedCreator != testActor {
		t.Fatal("real workflow did not retain physical creator attribution")
	}
	_, data := readArchive(t, f.prepare(t))
	b, e := A.DecodeReadable[A.Board](data["board.json"])
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Tombstones) != 1 || b.Tombstones[0].Kind != "link" || b.Tombstones[0].ID != A.UUID(link) || b.Tombstones[0].ActorID != nil || b.Tombstones[0].CreatedAt == nil || b.Tombstones[0].CreatedIn != testIdentity {
		t.Fatal("removed link archive attribution")
	}
	if len(b.Links) != 0 || len(b.Cards[0].LinkIDs) != 0 {
		t.Fatal("cleared removed link became current")
	}

	// Synthetic retained source facts exercise the same projection across lineage;
	// comment creators remain archive attribution, while link creators are omitted.
	oldInc := "20000000-0000-0000-0000-000000000002"
	sourceLink, sourceComment := testUUID(t), testUUID(t)
	f.raw(t, func(c *sql.Conn) error {
		for _, v := range []struct{ kind, id string }{{"link", sourceLink}, {"comment", sourceComment}} {
			if _, e := c.ExecContext(testCtx(t), "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at,removed_at,removal_reason) VALUES(?,?,?,?,?,?,?,?,?,'cleared')", route.Incarnation, v.kind, v.id, f.card, route.BoardID, oldInc, testActor, string(through), string(through)); e != nil {
				return e
			}
		}
		_, e := c.ExecContext(testCtx(t), "INSERT INTO lineage VALUES(?,?,?,?,?)", route.Incarnation, route.BoardID, oldInc, string(digest([]byte("synthetic cleared source manifest"))), 3)
		return e
	})
	_, data = readArchive(t, f.prepare(t))
	b, e = A.DecodeReadable[A.Board](data["board.json"])
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Tombstones) != 3 {
		t.Fatal("source tombstones omitted")
	}
	for _, tombstone := range b.Tombstones {
		if tombstone.ID == A.UUID(sourceLink) && (tombstone.ActorID != nil || tombstone.CreatedIn.Incarnation != A.UUID(oldInc)) {
			t.Fatal("source link attribution")
		}
		if tombstone.ID == A.UUID(sourceComment) && (tombstone.ActorID == nil || *tombstone.ActorID != testActor || tombstone.CreatedIn.Incarnation != A.UUID(oldInc)) {
			t.Fatal("source comment attribution")
		}
	}
}
