//go:build windows && amd64 && k03_archive_native

package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	V "github.com/localswim/localswim/internal/archive/validate"
	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/attachments"
	"github.com/localswim/localswim/internal/transport/boardapi"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/exportapi"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

type nativeProvider struct {
	manager  *setup.Manager
	metadata core.CredentialMetadata
	token    T.SecretToken
}

func (p *nativeProvider) Open(_ context.Context, s core.Selectors) (*core.Connection, error) {
	if s != p.metadata.Selected {
		return nil, &core.Failure{Code: "CREDENTIAL_CONTEXT_MISMATCH", Exit: 2}
	}
	c, e := p.manager.ReadContext()
	if e != nil {
		return nil, e
	}
	return core.NewBoardConnection(c, p.metadata, p.token)
}
func (p *nativeProvider) NativeOwner(context.Context, evidence.Record) (*core.Connection, error) {
	return nil, errors.New("native fallback forbidden")
}

type nativeJourney struct {
	root                                                  string
	runner                                                Runner
	provider                                              *nativeProvider
	next                                                  uint64
	store                                                 *S.Store
	uploads, exports                                      atomic.Int32
	lost, badDownload, badExport, wrongExport, badPackage atomic.Bool
}

func nativeCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func (f *nativeJourney) wire(t *testing.T, target string, revision, cardRevision uint64, input any) []byte {
	t.Helper()
	f.next++
	var selected, card any
	if target != "" {
		selected = map[string]string{"cardId": target}
		card = cardRevision
	}
	b, e := json.Marshal(map[string]any{"opId": fmt.Sprintf("60000000-0000-4000-8000-%012d", f.next), "incarnation": testInc, "target": selected, "expectedRevision": revision, "expectedCardRevision": card, "input": input})
	if e != nil {
		t.Fatal("wire fixture")
	}
	return b
}
func (f *nativeJourney) args(cmd ...string) []string {
	s := f.provider.metadata.Selected
	return append([]string{"--json", "--installation", s.InstallationID, "--board", s.BoardID, "--incarnation", s.Incarnation, "--grant", s.GrantID}, cmd...)
}
func (f *nativeJourney) call(t *testing.T, want int, wire []byte, args ...string) []byte {
	t.Helper()
	var out, diagnostic bytes.Buffer
	exit := f.runner.Run(nativeCtx(t), f.args(args...), bytes.NewReader(wire), &out, &diagnostic)
	if exit != want {
		t.Fatalf("adapter exit %d want %d", exit, want)
	}
	if bytes.Contains(out.Bytes(), []byte(string(f.provider.token))) || bytes.Contains(diagnostic.Bytes(), []byte(string(f.provider.token))) || bytes.Contains(out.Bytes(), []byte(f.root)) {
		t.Fatal("private material escaped")
	}
	return out.Bytes()
}
func terminal(t *testing.T, raw []byte) T.Terminal {
	t.Helper()
	v, e := T.Decode[T.Terminal](raw)
	if e != nil {
		t.Fatal("terminal invalid")
	}
	return v
}
func newJourney(t *testing.T) *nativeJourney {
	return newJourneyRights(t, []string{"attach", "create", "export", "read", "read-own-outcome"})
}
func newJourneyRights(t *testing.T, rights []string) *nativeJourney {
	t.Helper()
	root := nativeTestRoot(t)
	t.Setenv("K03_ARCHIVE_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	m, e := setup.New(nil)
	if e != nil || m.Init(nil).Exit != 0 {
		t.Fatal("private synthetic setup")
	}
	owner, e := m.LoadNativeOwner()
	if e != nil {
		t.Fatal("private synthetic owner")
	}
	nfs, e := newLocalFS()
	if e != nil {
		t.Fatal("private file policy")
	}
	boardDir := filepath.Join(root, "board")
	if windows.CreateDirectory(localPointer(boardDir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: nfs.sd}) != nil {
		t.Fatal("private board directory")
	}
	s, e := S.Create(nativeCtx(t), boardDir, schema.Identity{BoardID: testBoard, Incarnation: testInc})
	if e != nil {
		t.Fatal("private physical4 store")
	}
	n, e := credentials.SourceRoot(s)
	if e != nil {
		t.Fatal("native source")
	}
	sessions, e := session.New(s, n)
	if e != nil {
		t.Fatal("sessions")
	}
	board, e := boardapi.New(s, n, sessions)
	if e != nil {
		t.Fatal("K01")
	}
	attach, e := attachments.New(s, n, sessions)
	if e != nil {
		t.Fatal("A01")
	}
	exports, e := exportapi.New(s, n, sessions)
	if e != nil {
		t.Fatal("H02")
	}
	sessionHTTP, e := H.New(n, sessions)
	if e != nil {
		t.Fatal("T01")
	}
	t.Cleanup(func() {
		sessions.Close()
		n.Close()
		if s.Close() != nil {
			t.Error("store close")
		}
	})
	store, e := evidence.NewWindowsStore(filepath.Join(root, "evidence"))
	if e != nil {
		t.Fatal("K02 evidence")
	}
	f := &nativeJourney{root: root, next: 1, store: s}
	grantWire := f.wire(t, "", 0, 0, map[string]any{"granteeActorId": testActor, "kind": "human", "rights": rights})
	var grant map[string]any
	_ = json.Unmarshal(grantWire, &grant)
	grant["boardId"] = testBoard
	grantWire, _ = json.Marshal(grant)
	i, e := T.FreezeMutation(grantWire, contract.ResolvedRoute{Command: "grant.create", BoardID: testBoard}, contract.AuthenticatedActor{ActorID: n.Context().OwnerActorID})
	if e != nil {
		t.Fatal("grant intent")
	}
	gr := n.SubmitNative(nativeCtx(t), T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer ")), i, grants.Route{BoardID: testBoard, Incarnation: testInc})
	if gr.Result == nil || gr.Result.Terminal == nil || gr.Result.Terminal.Outcome != "committed" {
		t.Fatal("grant fixture admission")
	}
	grantID := ""
	for _, ref := range gr.Result.Terminal.ResultRefs {
		if ref.Kind == "grant" {
			grantID = string(ref.ID)
		}
	}
	issued := n.Issue(nativeCtx(t), T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer ")), T.CredentialIssueRequest{InstallationID: T.UUIDv4(n.Context().InstallationID), BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), GrantID: A.UUID(grantID)})
	raw, e := issued.MarshalEphemeral()
	if e != nil {
		t.Fatal("credential fixture issue")
	}
	secret, e := T.Decode[T.CredentialSecret](raw)
	if e != nil {
		t.Fatal("credential fixture shape")
	}
	expires, _ := time.Parse(time.RFC3339, string(secret.ExpiresAt))
	p := &nativeProvider{manager: m, metadata: core.CredentialMetadata{Selected: core.Selectors{InstallationID: n.Context().InstallationID, BoardID: testBoard, Incarnation: testInc, GrantID: grantID}, ActorID: testActor, ExpiresAt: expires}, token: secret.Token}
	f.provider = p
	f.runner = Runner{Provider: p, Evidence: store, ProtectedRoots: []string{boardDir}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/session") {
			sessionHTTP.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/exports") {
			f.exports.Add(1)
			if f.badPackage.Swap(false) {
				capture := httptest.NewRecorder()
				exports.ServeHTTP(capture, r)
				for k, v := range capture.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(capture.Code)
				body := bytes.Clone(capture.Body.Bytes())
				if len(body) > 80 {
					body[80] ^= 1
				}
				_, _ = w.Write(body)
				return
			}
			if f.wrongExport.Swap(false) {
				wire, _ := io.ReadAll(r.Body)
				q, e := T.Decode[T.ExportRequest](wire)
				if e != nil {
					t.Error("export request shape")
				}
				q.ExpectedRevision++
				body, _ := T.Readable(q)
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			if f.badExport.Swap(false) {
				capture := httptest.NewRecorder()
				exports.ServeHTTP(capture, r)
				for k, v := range capture.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(capture.Code)
				b := capture.Body.Bytes()
				if len(b) > 1 {
					b = b[:len(b)-1]
				}
				_, _ = w.Write(b)
				return
			}
			exports.ServeHTTP(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/attachments") {
			if r.Method == "POST" {
				f.uploads.Add(1)
			}
			if r.Method == "POST" && f.lost.Swap(false) {
				capture := httptest.NewRecorder()
				attach.ServeHTTP(capture, r)
				if capture.Code != 201 {
					t.Error("loss fixture did not commit")
				}
				h, ok := w.(http.Hijacker)
				if !ok {
					t.Error("disconnect unavailable")
					return
				}
				conn, _, e := h.Hijack()
				if e == nil {
					conn.Close()
				}
				return
			}
			if r.Method == "GET" && f.badDownload.Swap(false) {
				capture := httptest.NewRecorder()
				attach.ServeHTTP(capture, r)
				for k, v := range capture.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(capture.Code)
				b := bytes.Clone(capture.Body.Bytes())
				if len(b) > 0 {
					b[0] ^= 255
				}
				_, _ = w.Write(b)
				return
			}
			attach.ServeHTTP(w, r)
			return
		}
		board.ServeHTTP(w, r)
	})
	server, e := H.Listen(nativeCtx(t), n, handler)
	if e != nil {
		t.Fatal("exclusive pinned TLS listener unavailable")
	}
	t.Cleanup(server.Close)
	return f
}

func (f *nativeJourney) createCard(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	if (core.Runner{Provider: f.provider, Evidence: f.runner.Evidence}).Run(nativeCtx(t), f.args("card", "create", "--input", "-"), bytes.NewReader(f.wire(t, "", 1, 0, map[string]any{"title": "Synthetic", "description": ""})), &out, io.Discard) != 0 {
		t.Fatal("source card create")
	}
	return string(terminal(t, out.Bytes()).ResultRefs[0].ID)
}

type changedSourceStore struct {
	base evidence.Store
	path string
	fail bool
}

func (s changedSourceStore) Save(r evidence.Record) (string, error) {
	if s.fail {
		return "", errors.New("synthetic evidence unavailable")
	}
	if os.WriteFile(s.path, []byte("changed externally after freeze"), 0600) != nil {
		return "", errors.New("source mutation fixture unavailable")
	}
	return s.base.Save(r)
}
func (s changedSourceStore) Load(p string) (evidence.Record, error) { return s.base.Load(p) }
func (s changedSourceStore) Resolve(r evidence.Record, t T.Terminal) error {
	return s.base.Resolve(r, t)
}

func TestNativeMalformedStaleEvidenceFailureAndImmutableUploadBytes(t *testing.T) {
	f := newJourney(t)
	card := f.createCard(t)
	raw := []byte("frozen original")
	source := filepath.Join(f.root, "raw.bin")
	if nativeSave(nativeCtx(t), source, raw, []string{}, func(string) error { return nil }) != saveConfirmed {
		t.Fatal("raw source")
	}
	wire := f.wire(t, card, 2, 1, map[string]any{"name": "frozen.bin", "mediaLabel": "application/octet-stream", "sha256": sha(raw), "byteLength": len(raw)})
	f.call(t, 2, append(bytes.Clone(wire), []byte(`{}`)...), "attachment", "add", card, "--input", "-", "--file", source)
	f.call(t, 2, bytes.Replace(wire, []byte(sha(raw)), []byte(strings.Repeat("0", 64)), 1), "attachment", "add", card, "--input", "-", "--file", source)
	if f.uploads.Load() != 0 {
		t.Fatal("invalid bytes or metadata transmitted")
	}
	base := f.runner.Evidence
	f.runner.Evidence = changedSourceStore{base: base, fail: true}
	f.call(t, 1, wire, "attachment", "add", card, "--input", "-", "--file", source)
	if f.uploads.Load() != 0 {
		t.Fatal("unsafe evidence transmitted")
	}
	f.runner.Evidence = base
	stale := f.wire(t, card, 1, 1, map[string]any{"name": "stale.bin", "mediaLabel": "application/octet-stream", "sha256": sha(raw), "byteLength": len(raw)})
	refusal := terminal(t, f.call(t, 2, stale, "attachment", "add", card, "--input", "-", "--file", source))
	if refusal.Outcome != "not_committed" || refusal.Code != "REVISION_CONFLICT" {
		t.Fatal("stale attachment not definitively refused")
	}
	f.runner.Evidence = changedSourceStore{base: base, path: source}
	added := terminal(t, f.call(t, 0, wire, "attachment", "add", card, "--input", "-", "--file", source))
	if f.uploads.Load() != 2 {
		t.Fatal("upload replay or omission")
	}
	attachment := ""
	for _, ref := range added.ResultRefs {
		if ref.Kind == "attachment" {
			attachment = string(ref.ID)
		}
	}
	destination := filepath.Join(f.root, "frozen.bin")
	f.call(t, 0, nil, "attachment", "download", attachment, "--destination", destination)
	got, e := nativeRead(nativeCtx(t), destination, maxFile)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("source reread or unfrozen upload")
	}
}

func TestNativeCurrentRightsAndIdentityFailClosed(t *testing.T) {
	f := newJourneyRights(t, []string{"create", "read", "read-own-outcome"})
	card := f.createCard(t)
	source := filepath.Join(f.root, "raw.bin")
	raw := []byte("denied")
	if nativeSave(nativeCtx(t), source, raw, []string{}, func(string) error { return nil }) != saveConfirmed {
		t.Fatal("source fixture")
	}
	wire := f.wire(t, card, 2, 1, map[string]any{"name": "denied.bin", "mediaLabel": "application/octet-stream", "sha256": sha(raw), "byteLength": len(raw)})
	denied := terminal(t, f.call(t, 2, wire, "attachment", "add", card, "--input", "-", "--file", source))
	if denied.Outcome != "not_committed" || denied.Code != "FORBIDDEN" {
		t.Fatal("attach right not enforced")
	}
	destination := filepath.Join(f.root, "denied.zip")
	f.call(t, 2, nil, "export", "create", "--destination", destination)
	if _, e := os.Lstat(destination); !os.IsNotExist(e) {
		t.Fatal("export without right published")
	}
	before := f.uploads.Load()
	f.provider.metadata.ActorID = "00000000-0000-4000-8000-000000000099"
	f.call(t, 2, wire, "attachment", "add", card, "--input", "-", "--file", source)
	if f.uploads.Load() != before {
		t.Fatal("actor mismatch dispatched")
	}
}

func TestNativeAttachmentDownloadExportOriginalOnlyAndRestore(t *testing.T) {
	f := newJourney(t)
	// The source card uses the existing K02 adapter and the same authenticated
	// provider/evidence, rather than direct database fixture mutation.
	var created bytes.Buffer
	if (core.Runner{Provider: f.provider, Evidence: f.runner.Evidence}).Run(nativeCtx(t), f.args("card", "create", "--input", "-"), bytes.NewReader(f.wire(t, "", 1, 0, map[string]any{"title": "Synthetic", "description": ""})), &created, io.Discard) != 0 {
		t.Fatal("source card create")
	}
	card := string(terminal(t, created.Bytes()).ResultRefs[0].ID)
	raw := []byte{0, 255, 13, 10, 1}
	source := filepath.Join(f.root, "source.bin")
	if nativeSave(nativeCtx(t), source, raw, []string{}, func(string) error { return nil }) != saveConfirmed {
		t.Fatal("raw source publication")
	}
	wire := f.wire(t, card, 2, 1, map[string]any{"name": "inert <name>.bin", "mediaLabel": "application/octet-stream", "sha256": sha(raw), "byteLength": len(raw)})
	added := terminal(t, f.call(t, 0, wire, "attachment", "add", card, "--input", "-", "--file", source))
	attachment := ""
	for _, ref := range added.ResultRefs {
		if ref.Kind == "attachment" {
			attachment = string(ref.ID)
		}
	}
	if attachment == "" || f.uploads.Load() != 1 {
		t.Fatal("A01 upload count or identity")
	}
	if changed, e := os.ReadFile(source); e != nil || !bytes.Equal(changed, raw) {
		t.Fatal("source upload bytes changed")
	}
	download := filepath.Join(f.root, "download.bin")
	f.call(t, 0, nil, "attachment", "download", attachment, "--destination", download)
	got, e := nativeRead(nativeCtx(t), download, maxFile)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("download mismatch")
	}
	outputLost := filepath.Join(f.root, "output-lost.bin")
	if f.runner.Run(nativeCtx(t), f.args("attachment", "download", attachment, "--destination", outputLost), nil, failedOutput{}, io.Discard) != 4 {
		t.Fatal("download/output failure not separate")
	}
	got, e = nativeRead(nativeCtx(t), outputLost, maxFile)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("completed download downgraded after output failure")
	}
	f.call(t, 1, nil, "attachment", "download", attachment, "--destination", download)
	f.badDownload.Store(true)
	tampered := filepath.Join(f.root, "tampered.bin")
	f.call(t, 3, nil, "attachment", "download", attachment, "--destination", tampered)
	if _, e = os.Lstat(tampered); !os.IsNotExist(e) {
		t.Fatal("tampered bytes published")
	}
	// New operation IDs are deliberate test actions. A lost upload response is
	// reconciled only with its original private immutable evidence.
	f.lost.Store(true)
	lostWire := f.wire(t, card, 3, 2, map[string]any{"name": "lost.bin", "mediaLabel": "application/octet-stream", "sha256": sha(raw), "byteLength": len(raw)})
	unknown, e := T.Decode[T.KnownIdentityUnknown](f.call(t, 4, lostWire, "attachment", "add", card, "--input", "-", "--file", source))
	if e != nil {
		t.Fatal("original unknown unavailable")
	}
	paths, e := filepath.Glob(filepath.Join(f.root, "evidence", "*.json"))
	if e != nil {
		t.Fatal("evidence inventory")
	}
	evidencePath := ""
	for _, path := range paths {
		record, e := f.runner.Evidence.Load(path)
		if e == nil && record.Identity().OpID == string(unknown.OpID) {
			evidencePath = path
		}
	}
	if evidencePath == "" {
		t.Fatal("original durable evidence missing")
	}
	var proof bytes.Buffer
	if (core.Runner{Provider: f.provider, Evidence: f.runner.Evidence}).Run(nativeCtx(t), f.args("operation", "read", string(unknown.OpID), "--evidence", evidencePath), nil, &proof, io.Discard) != 0 {
		t.Fatal("original reconciliation failed")
	}
	if terminal(t, proof.Bytes()).Fingerprint != unknown.Fingerprint || f.uploads.Load() != 2 {
		t.Fatal("reconciliation replayed or rebound")
	}
	exportPath := filepath.Join(f.root, "snapshot.zip")
	status, e := T.Decode[T.ExportStatus](f.call(t, 0, nil, "export", "create", "--destination", exportPath))
	if e != nil || status.Outcome != "confirmed" || status.Result.Destination != nil {
		t.Fatal("export public status")
	}
	packageBytes, e := nativeRead(nativeCtx(t), exportPath, V.MaxArchiveBytes)
	if e != nil {
		t.Fatal("export local bytes")
	}
	packageFile, e := V.Read(nativeCtx(t), bytes.NewReader(packageBytes))
	if e != nil {
		t.Fatal("A03 rejected H02 package")
	}
	manifest, e := packageFile.Manifest()
	if e != nil || manifest.Revision != 4 {
		t.Fatal("snapshot revision not bound")
	}
	f.wrongExport.Store(true)
	refusedPath := filepath.Join(f.root, "stale.zip")
	f.call(t, 2, nil, "export", "create", "--destination", refusedPath)
	if _, e = os.Lstat(refusedPath); !os.IsNotExist(e) {
		t.Fatal("revision refusal published")
	}
	f.badExport.Store(true)
	badPath := filepath.Join(f.root, "truncated.zip")
	f.call(t, 1, nil, "export", "create", "--destination", badPath)
	if _, e = os.Lstat(badPath); !os.IsNotExist(e) {
		t.Fatal("truncated export published")
	}
	f.badPackage.Store(true)
	invalidPath := filepath.Join(f.root, "invalid-package.zip")
	f.call(t, 1, nil, "export", "create", "--destination", invalidPath)
	if _, e = os.Lstat(invalidPath); !os.IsNotExist(e) {
		t.Fatal("invalid whole archive published")
	}
	outputLostExport := filepath.Join(f.root, "output-lost.zip")
	if f.runner.Run(nativeCtx(t), f.args("export", "create", "--destination", outputLostExport), nil, failedOutput{}, io.Discard) != 4 {
		t.Fatal("export/output failure not separate")
	}
	retained, e := nativeRead(nativeCtx(t), outputLostExport, V.MaxArchiveBytes)
	if e != nil {
		t.Fatal("completed export lost")
	}
	if _, e = V.Read(nativeCtx(t), bytes.NewReader(retained)); e != nil {
		t.Fatal("completed export downgraded after output failure")
	}
	if f.exports.Load() != 5 {
		t.Fatal("automatic export retry")
	}
	// A restored destination is separate from the source archive directory.
	outDir := filepath.Join(f.root, "out")
	if os.Mkdir(outDir, 0700) != nil {
		t.Fatal("restore output parent")
	}
	destination := filepath.Join(outDir, "restored")
	var restored, diagnostic bytes.Buffer
	exit := f.runner.Run(nativeCtx(t), []string{"--json", "restore", "create", "--source", exportPath, "--destination", destination}, nil, &restored, &diagnostic)
	if exit != 4 {
		t.Fatal("source-parent overlap must refuse restore")
	}
}
