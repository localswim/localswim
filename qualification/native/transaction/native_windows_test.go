//go:build nativequalification && windows && amd64

package transaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/attachments"
	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"golang.org/x/sys/windows"
	"modernc.org/sqlite"
)

const board = "10000000-0000-4000-8000-000000000001"
const inc = "20000000-0000-4000-8000-000000000001"
const actor = "30000000-0000-4000-8000-000000000001"
const grant = "30000000-0000-4000-8000-000000000002"
const admin = "30000000-0000-4000-8000-000000000003"
const adminGrant = "30000000-0000-4000-8000-000000000004"
const installation = "60000000-0000-4000-8000-000000000001"
const origin = "https://synthetic.localhost:47443"

var identity = schema.Identity{BoardID: board, Incarnation: inc}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func privateDir(t *testing.T) string {
	t.Helper()
	if runtime.Version() != "go1.27.3" {
		t.Fatal("exact qualified Go 1.27.3 required")
	}
	root := os.Getenv("V01_NATIVE_ROOT")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatal("V01_NATIVE_ROOT must name a canonical private local NTFS fixture root")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal("working directory unavailable")
	}
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(root)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("fixtures must be outside public repository")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal("fixture root unavailable")
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal("account unavailable")
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal("private descriptor unavailable")
	}
	for n := 0; n < 1000; n++ {
		p := filepath.Join(root, fmt.Sprintf("n1-%03d", n))
		err = windows.CreateDirectory(windows.StringToUTF16Ptr(p), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd})
		if err == nil {
			return p
		}
		if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			t.Fatal("private fixture creation failed")
		}
	}
	t.Fatal("fixture namespace exhausted")
	return ""
}

type fixture struct {
	s        *S.Store
	e        *writer.Engine
	g, owner *grants.Service
	path     string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{path: privateDir(t)}
	var err error
	f.s, err = S.Create(ctx(t), f.path, identity)
	if err != nil {
		t.Fatal("native store creation failed")
	}
	t.Cleanup(func() {
		if err := f.s.Close(); err != nil {
			t.Error("store cleanup failed")
		}
	})
	f.e = writer.New(f.s)
	raw(t, f.s, func(c *sql.Conn) error {
		for _, pair := range [][2]string{{actor, grant}, {admin, adminGrant}} {
			for _, q := range []string{
				`INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES('` + inc + `','` + pair[0] + `','human','Synthetic','` + board + `','` + inc + `')`,
				`INSERT INTO grant_fact VALUES('` + inc + `','` + pair[1] + `')`,
				`INSERT INTO grant_record VALUES('` + inc + `','` + pair[1] + `','` + pair[0] + `',1)`,
			} {
				if _, err := c.ExecContext(ctx(t), q); err != nil {
					return err
				}
			}
		}
		for _, r := range []string{"attach", "comment", "create", "edit", "read", "read-own-outcome"} {
			if _, err := c.ExecContext(ctx(t), "INSERT INTO grant_right VALUES(?,?,?)", inc, grant, r); err != nil {
				return err
			}
		}
		for _, r := range []string{"grant-admin", "read", "read-own-outcome"} {
			if _, err := c.ExecContext(ctx(t), "INSERT INTO grant_right VALUES(?,?,?)", inc, adminGrant, r); err != nil {
				return err
			}
		}
		return nil
	})
	// Synthetic authentication fixture only. Real G01 current grant/rights checks
	// still execute in each S02 transaction; this is not G02/TLS qualification.
	service := func(a, g string) *grants.Service {
		v, err := grants.New(installation, origin, func(_ context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
			var actual string
			err := r.Scan("SELECT actor_id FROM grant_record WHERE incarnation=? AND grant_id=?", []any{inc, g}, &actual)
			if actual != a {
				return grants.Credential{}, grants.ErrDenied
			}
			return grants.Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: actual, GrantID: g, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, err
		})
		if err != nil {
			t.Fatal("synthetic verifier composition failed")
		}
		return v
	}
	f.g = service(actor, grant)
	f.owner = service(admin, adminGrant)
	return f
}

func raw(t *testing.T, s *S.Store, fn func(*sql.Conn) error) {
	t.Helper()
	l, err := s.Writer(ctx(t))
	if err != nil {
		t.Fatal("writer unavailable")
	}
	defer l.Close()
	if err = l.Do(ctx(t), fn); err != nil {
		t.Fatal("fixture SQL failed")
	}
}
func scalar(t *testing.T, s *S.Store, q string) int64 {
	t.Helper()
	l, err := s.Reader(ctx(t))
	if err != nil {
		t.Fatal("reader unavailable")
	}
	defer l.Close()
	var n int64
	if l.Do(ctx(t), func(c *sql.Conn) error { return c.QueryRowContext(ctx(t), q).Scan(&n) }) != nil {
		t.Fatal("readback failed")
	}
	return n
}
func text(t *testing.T, s *S.Store, q string) string {
	t.Helper()
	l, err := s.Reader(ctx(t))
	if err != nil {
		t.Fatal("reader unavailable")
	}
	defer l.Close()
	var value string
	if l.Do(ctx(t), func(c *sql.Conn) error { return c.QueryRowContext(ctx(t), q).Scan(&value) }) != nil {
		t.Fatal("text readback failed")
	}
	return value
}
func op(n int) string { return fmt.Sprintf("50000000-0000-4000-8000-%012d", n) }
func freeze(t *testing.T, cmd contract.Command, target string, rev, cr uint64, n int, input any) contract.Intent {
	t.Helper()
	var targetValue, cardRevision any
	a := actor
	if target != "" {
		targetValue = map[string]any{"cardId": target}
		cardRevision = cr
	}
	if cmd == "grant.revoke" {
		a = admin
		targetValue = map[string]any{"grantId": target}
		cardRevision = nil
	}
	wire := map[string]any{"opId": op(n), "incarnation": inc, "target": targetValue, "expectedRevision": rev, "expectedCardRevision": cardRevision, "input": input}
	if cmd == "grant.revoke" {
		wire["boardId"] = board
	}
	b, err := json.Marshal(wire)
	if err != nil {
		t.Fatal("fixture encoding")
	}
	i, err := T.FreezeMutation(b, contract.ResolvedRoute{Command: cmd, BoardID: board, TargetID: target}, contract.AuthenticatedActor{ActorID: a})
	if err != nil {
		t.Fatal("fixture intent rejected")
	}
	return i
}
func cb(f *fixture, id string) writer.Callbacks {
	return cards.New(f.g).Callbacks(cards.Route{BoardID: board, Incarnation: inc, CardID: id})
}
func terminal(t *testing.T, r writer.Result, want string) *T.Terminal {
	t.Helper()
	if r.Terminal == nil || string(r.Terminal.Outcome) != want || T.Validate(*r.Terminal) != nil {
		t.Fatalf("expected validated %s terminal", want)
	}
	return r.Terminal
}
func lookup(t *testing.T, f *fixture, i contract.Intent) writer.Result {
	t.Helper()
	o, err := outcome.Base(i)
	if err != nil {
		t.Fatal("lookup fixture")
	}
	return f.e.Lookup(ctx(t), outcome.IdentityOf(o), f.g.Authority)
}
func create(t *testing.T, f *fixture, n int) string {
	t.Helper()
	i := freeze(t, "card.create", "", 0, 0, n, map[string]any{"title": "Alpha", "description": "First draft"})
	r := terminal(t, f.e.Submit(ctx(t), i, cb(f, "")), "committed")
	for _, ref := range r.ResultRefs {
		if ref.Kind == "card" {
			return string(ref.ID)
		}
	}
	t.Fatal("missing card reference")
	return ""
}
func counts(t *testing.T, s *S.Store) map[string]int64 {
	t.Helper()
	m := map[string]int64{}
	for _, table := range []string{"entity", "ticket", "card", "comment", "link", "approval", "current_reference", "card_revision", "revision_reference", "history_blob_pin", "activity", "operation", "operation_full", "operation_reference", "blob", "attachment", "current_attachment_pin", "operation_blob_pin", "byte_fact"} {
		m[table] = scalar(t, s, "SELECT count(*) FROM "+table)
	}
	m["revision"] = scalar(t, s, "SELECT revision FROM board")
	return m
}
func unchanged(t *testing.T, s *S.Store, before map[string]int64) {
	t.Helper()
	after := counts(t, s)
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("partial effect in %s", k)
		}
	}
}

func TestConcurrentWritersAndDurableRefusal(t *testing.T) {
	f := newFixture(t)
	start := make(chan struct{})
	results := make(chan writer.Result, 2)
	a := freeze(t, "card.create", "", 0, 0, 1, map[string]any{"title": "Alpha", "description": "One"})
	b := freeze(t, "card.create", "", 0, 0, 2, map[string]any{"title": "Beta", "description": "Two"})
	for _, i := range []contract.Intent{a, b} {
		go func(i contract.Intent) { <-start; results <- f.e.Submit(ctx(t), i, cb(f, "")) }(i)
	}
	close(start)
	committed, refused := 0, 0
	for n := 0; n < 2; n++ {
		r := <-results
		if r.Terminal == nil {
			t.Fatal("concurrent operation unresolved")
		}
		switch r.Terminal.Outcome {
		case "committed":
			committed++
		case "not_committed":
			refused++
		}
	}
	if committed != 1 || refused != 1 || scalar(t, f.s, "SELECT revision FROM board") != 1 || scalar(t, f.s, "SELECT count(*) FROM card") != 1 || scalar(t, f.s, "SELECT count(*) FROM activity") != 1 {
		t.Fatal("concurrent exact-revision admission failed")
	}
	for _, i := range []contract.Intent{a, b} {
		before := counts(t, f.s)
		old := lookup(t, f, i)
		if old.Terminal == nil {
			t.Fatal("original terminal missing")
		}
		again := f.e.Submit(ctx(t), i, cb(f, ""))
		terminal(t, again, string(old.Terminal.Outcome))
		unchanged(t, f.s, before)
	}
}

func TestRevocationSerializesBeforeQueuedWriter(t *testing.T) {
	f := newFixture(t)
	i := freeze(t, "grant.revoke", grant, 0, 0, 1, map[string]any{})
	rcb := f.owner.GrantCallbacks(grants.Route{BoardID: board, Incarnation: inc, GrantID: grant})
	apply := rcb.Apply
	entered := make(chan struct{})
	release := make(chan struct{})
	rcb.Apply = func(c context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		v, e := apply(c, w, i, at)
		close(entered)
		<-release
		return v, e
	}
	done := make(chan writer.Result, 1)
	go func() { done <- f.e.Submit(ctx(t), i, rcb) }()
	<-entered
	writeDone := make(chan writer.Result, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		writeDone <- f.e.Submit(ctx(t), freeze(t, "card.create", "", 1, 0, 2, map[string]any{"title": "Denied", "description": "Retained input"}), cb(f, ""))
	}()
	<-started
	close(release)
	terminal(t, <-done, "committed")
	r := <-writeDone
	if r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" || scalar(t, f.s, "SELECT count(*) FROM card") != 0 || scalar(t, f.s, "SELECT enabled FROM grant_record WHERE grant_id='"+grant+"'") != 0 {
		t.Fatal("queued writer used revoked authority")
	}
	if scalar(t, f.s, "SELECT revision FROM board") != 1 || scalar(t, f.s, "SELECT count(*) FROM operation") != 1 {
		t.Fatal("revocation was not single atomic mutation")
	}
}

func TestNativeCommitBusyRollsBackWholeCardHistory(t *testing.T) {
	f := newFixture(t)
	id := create(t, f, 1)
	before := counts(t, f.s)
	r, err := f.s.Reader(ctx(t))
	if err != nil {
		t.Fatal("reader lease")
	}
	if r.Do(ctx(t), func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx(t), "BEGIN"); e != nil {
			return e
		}
		var n int
		return c.QueryRowContext(ctx(t), "SELECT count(*) FROM card").Scan(&n)
	}) != nil {
		t.Fatal("native reader lock")
	}
	i := freeze(t, "card.edit", id, 1, 1, 2, map[string]any{"description": "Uncommitted replacement"})
	callbacks := cb(f, id)
	apply := callbacks.Apply
	staged := false
	callbacks.Apply = func(c context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		v, err := apply(c, w, i, at)
		staged = err == nil
		return v, err
	}
	result := f.e.Submit(ctx(t), i, callbacks)
	if r.Close() != nil {
		t.Fatal("reader cleanup")
	}
	if !staged || result.Unknown == nil || result.Terminal != nil {
		t.Fatal("real reader-blocked COMMIT inferred terminal")
	}
	unchanged(t, f.s, before)
	if text(t, f.s, "SELECT description FROM card") != "First draft" {
		t.Fatal("failed commit changed current prose")
	}
	if lookup(t, f, i).Unknown == nil {
		t.Fatal("absent failed-commit evidence inferred noncommit")
	}
	// Recovery succeeded, so the next deliberately distinct identity can proceed.
	terminal(t, f.e.Submit(ctx(t), freeze(t, "card.edit", id, 1, 1, 3, map[string]any{"description": "Reviewed draft"}), cb(f, id)), "committed")
}

func TestNativeSQLiteFullRollsBackAttachmentBytes(t *testing.T) {
	f := newFixture(t)
	id := create(t, f, 1)
	before := counts(t, f.s)
	raw(t, f.s, func(c *sql.Conn) error {
		var pages int
		if e := c.QueryRowContext(ctx(t), "PRAGMA page_count").Scan(&pages); e != nil {
			return e
		}
		var maximum int
		return c.QueryRowContext(ctx(t), fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Scan(&maximum)
	})
	data := bytes.Repeat([]byte("invented attachment bytes\n"), 131072)
	h := sha256.Sum256(data)
	// Establish the actual native SQLite error independently; an unavailable
	// provider or a generic refusal must not masquerade as SQLITE_FULL evidence.
	raw(t, f.s, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx(t), "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx(t), "INSERT INTO blob(incarnation,sha256,byte_length,bytes) VALUES(?,?,?,?)", inc, hex.EncodeToString(h[:]), len(data), data)
		var native *sqlite.Error
		if !errors.As(err, &native) || native.Code() != 13 {
			return errors.New("native SQLITE_FULL checkpoint not reached")
		}
		// SQLITE_FULL may have rolled back the transaction already.
		_, rollback := c.ExecContext(ctx(t), "ROLLBACK")
		if rollback != nil && rollback.Error() != "SQL logic error: cannot rollback - no transaction is active (1)" {
			return rollback
		}
		return nil
	})
	unchanged(t, f.s, before)
	i := freeze(t, "attachment.add", id, 1, 1, 2, map[string]any{"name": "note.txt", "mediaLabel": "text/plain", "byteLength": len(data), "sha256": hex.EncodeToString(h[:])})
	u, err := attachments.Buffer(bytes.NewReader(data))
	if err != nil {
		t.Fatal("private attachment buffer")
	}
	r, _ := attachments.New(f.s).Add(ctx(t), f.g, attachments.Route{BoardID: board, Incarnation: inc, CardID: id}, i, u)
	if r.Unknown == nil || r.Terminal != nil {
		t.Fatal("native SQLITE_FULL inferred terminal")
	}
	unchanged(t, f.s, before)
	if text(t, f.s, "SELECT description FROM card") != "First draft" {
		t.Fatal("native full changed current state")
	}
}

func TestExplicitFenceRequiresValidatedReopen(t *testing.T) {
	f := newFixture(t)
	create(t, f, 1)
	before := counts(t, f.s)
	f.s.Fence()
	if _, err := f.s.Writer(ctx(t)); !errors.Is(err, S.ErrFenced) {
		t.Fatal("fence admitted writer")
	}
	r := f.e.Submit(ctx(t), freeze(t, "card.create", "", 1, 0, 2, map[string]any{"title": "Fenced", "description": "Input"}), cb(f, ""))
	if r.Unknown == nil {
		t.Fatal("fenced submission did not remain unknown")
	}
	unchanged(t, f.s, before)
	if f.s.Close() != nil {
		t.Fatal("close fenced store")
	}
	var err error
	f.s, err = S.Existing(ctx(t), f.path, identity)
	if err != nil {
		t.Fatal("validated reopen failed")
	}
	f.e = writer.New(f.s)
	unchanged(t, f.s, before)
}

func TestNativePrivateEvidenceACLAndReadback(t *testing.T) {
	p := privateDir(t)
	store, err := evidence.NewWindowsStore(p)
	if err != nil {
		t.Fatal("private evidence store")
	}
	i := freeze(t, "card.create", "", 0, 0, 1, map[string]any{"title": "Alpha", "description": "private sentinel prose"})
	_ = i // The separately frozen evidence below must agree with this intent.
	wire, _ := json.Marshal(map[string]any{"opId": op(1), "incarnation": inc, "target": nil, "expectedRevision": 0, "expectedCardRevision": nil, "input": map[string]any{"title": "Alpha", "description": "private sentinel prose"}})
	r, err := evidence.Freeze(evidence.Context{InstallationID: installation, Origin: origin}, contract.ResolvedRoute{Command: "card.create", BoardID: board}, contract.AuthenticatedActor{ActorID: actor}, wire)
	if err != nil {
		t.Fatal("evidence freeze")
	}
	if !bytes.Equal(r.Semantic(), i.CanonicalBytes()) || r.Fingerprint() != i.Fingerprint() {
		t.Fatal("private evidence changed semantic identity")
	}
	path, err := store.Save(r)
	if err != nil {
		t.Fatal("native flush/readback failed")
	}
	loaded, err := store.Load(path)
	if err != nil || !bytes.Equal(loaded.Bytes(), r.Bytes()) {
		t.Fatal("independent readback mismatch")
	}
	for _, path := range []string{p, path} {
		verifyACL(t, path)
	}
	if _, err = store.Save(r); err == nil {
		t.Fatal("immutable evidence overwritten")
	}
	// Product store validates unsafe ACLs again on read, rather than trusting
	// creation-time validation. No descriptor or private path enters logs.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal("unsafe descriptor fixture")
	}
	dacl, _, _ := sd.DACL()
	if windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil) != nil {
		t.Fatal("unsafe ACL fixture failed")
	}
	if _, err = store.Load(path); err == nil {
		t.Fatal("unsafe evidence ACL accepted")
	}
}
func verifyACL(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal("ACL readback failed")
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal("account readback failed")
	}
	owner, _, err := sd.Owner()
	if err != nil || !owner.Equals(u.User.Sid) {
		t.Fatal("unexpected private owner")
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("private DACL not protected")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 2 {
		t.Fatal("private ACE count")
	}
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	seen := map[string]bool{}
	for n := uint32(0); n < uint32(dacl.AceCount); n++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, n, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x1f01ff {
			t.Fatal("private ACE mismatch")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(u.User.Sid) && !sid.Equals(system) {
			t.Fatal("unexpected private principal")
		}
		seen[sid.String()] = true
	}
	if len(seen) != 2 {
		t.Fatal("private principal set mismatch")
	}
}

func TestNativeCommittedProcessTerminationAndRestart(t *testing.T) {
	if os.Getenv("V01_CHILD_MODE") == "committed" {
		childCommitted(t)
		return
	}
	f := newFixture(t)
	if f.s.Close() != nil {
		t.Fatal("fixture release")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeCommittedProcessTerminationAndRestart$")
	cmd.Env = append(os.Environ(), "V01_CHILD_MODE=committed", "V01_CHILD_BOARD="+f.path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal("child output pipe")
	}
	cmd.Stderr = cmd.Stdout
	if cmd.Start() != nil {
		t.Fatal("native child process unavailable")
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	ready := make(chan bool, 1)
	go func() { var b [1]byte; n, e := stdout.Read(b[:]); ready <- n == 1 && e == nil && b[0] == 'R' }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child commit checkpoint unavailable")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("child commit checkpoint timeout")
	}
	if cmd.Process.Kill() != nil {
		t.Fatal("process termination failed")
	}
	if cmd.Wait() == nil {
		t.Fatal("termination did not interrupt process")
	}
	f.s, err = S.Existing(ctx(t), f.path, identity)
	if err != nil {
		t.Fatal("native restart refused committed store")
	}
	f.e = writer.New(f.s)
	if text(t, f.s, "SELECT description FROM card") != "Reviewed draft" || scalar(t, f.s, "SELECT count(*) FROM card_revision") != 1 || scalar(t, f.s, "SELECT count(*) FROM operation") != 2 || !strings.Contains(text(t, f.s, "SELECT state FROM card_revision"), "First draft") {
		t.Fatal("committed current/history/receipt did not survive process termination")
	}
	comment := freeze(t, "comment.add", text(t, f.s, "SELECT card_id FROM card"), 2, 2, 3, map[string]any{"text": "Check once"})
	terminal(t, f.e.Submit(ctx(t), comment, cb(f, text(t, f.s, "SELECT card_id FROM card"))), "committed")
	if f.s.Close() != nil {
		t.Fatal("restart close")
	}
	f.s, err = S.Existing(ctx(t), f.path, identity)
	if err != nil {
		t.Fatal("restart open")
	}
	f.e = writer.New(f.s)
	before := counts(t, f.s)
	terminal(t, lookup(t, f, comment), "committed")
	unchanged(t, f.s, before)
	if scalar(t, f.s, "SELECT count(*) FROM comment") != 1 {
		t.Fatal("original lookup duplicated comment")
	}
}
func childCommitted(t *testing.T) {
	s, err := S.Existing(ctx(t), os.Getenv("V01_CHILD_BOARD"), identity)
	if err != nil {
		t.Fatal("child store open")
	}
	f := &fixture{s: s, e: writer.New(s)}
	g, err := grants.New(installation, origin, func(_ context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
		var a string
		err := r.Scan("SELECT actor_id FROM grant_record WHERE grant_id=?", []any{grant}, &a)
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: a, GrantID: grant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, err
	})
	if err != nil {
		t.Fatal("child verifier")
	}
	f.g = g
	id := create(t, f, 1)
	terminal(t, f.e.Submit(ctx(t), freeze(t, "card.edit", id, 1, 1, 2, map[string]any{"description": "Reviewed draft"}), cb(f, id)), "committed")
	if _, err = os.Stdout.Write([]byte("R")); err != nil {
		t.Fatal("child checkpoint")
	}
	select {}
}
