//go:build windows && amd64 && k02_native

package core

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
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/boardapi"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

type nativeProvider struct {
	manager   *setup.Manager
	metadata  CredentialMetadata
	token     T.SecretToken
	lastError error
}

func (p *nativeProvider) Open(_ context.Context, s Selectors) (*Connection, error) {
	c, e := p.manager.ReadContext()
	if e != nil {
		return nil, e
	}
	if s != p.metadata.Selected {
		return nil, &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
	}
	connection, err := NewBoardConnection(c, p.metadata, p.token)
	if err == nil {
		do := connection.do
		connection.do = func(r *http.Request) (*http.Response, error) {
			response, err := do(r)
			p.lastError = err
			return response, err
		}
	}
	return connection, err
}
func (p *nativeProvider) NativeOwner(_ context.Context, _ evidence.Record) (*Connection, error) {
	n, e := p.manager.LoadNativeOwner()
	if e != nil {
		return nil, e
	}
	return NewNativeOwnerConnection(n)
}

type nativeRun struct {
	runner      Runner
	provider    *nativeProvider
	root        string
	grantRecord evidence.Record
	grantPath   string
	next        int
	lost        atomic.Bool
	mutations   atomic.Int32
}

func nativeContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func newNative(t *testing.T) *nativeRun {
	t.Helper()
	if os.Getenv("K02_NATIVE_OVERLAY") != "1" {
		t.Fatal("explicit private overlay required")
	}
	base := os.Getenv("S01_TEST_ROOT")
	if !filepath.IsAbs(base) {
		t.Fatal("private synthetic root required")
	}
	root, e := os.MkdirTemp(base, "k02-core-")
	if e != nil {
		t.Fatal("private root unavailable")
	}
	t.Setenv("K02_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	sentinel, e := setup.K02QualificationRoot()
	if e != nil || sentinel != filepath.Join(root, "installation") {
		t.Fatal("compiled root-only overlay sentinel required")
	}
	m, e := setup.New(nil)
	if e != nil || m.Init(nil).Exit != 0 {
		t.Fatal("synthetic native material unavailable")
	}
	owner, e := m.LoadNativeOwner()
	if e != nil {
		t.Fatal("synthetic owner unavailable")
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal("private account unavailable")
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal("private ACL unavailable")
	}
	dir := filepath.Join(root, "board")
	if windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}) != nil {
		t.Fatal("private board directory unavailable")
	}
	s, e := S.Create(nativeContext(t), dir, schema.Identity{BoardID: testBoard, Incarnation: testInc})
	if e != nil {
		t.Fatal("synthetic store unavailable")
	}
	n, e := credentials.SourceRoot(s)
	if e != nil {
		t.Fatal("real native source unavailable")
	}
	sessions, e := session.New(s, n)
	if e != nil {
		t.Fatal("sessions unavailable")
	}
	board, e := boardapi.New(s, n, sessions)
	if e != nil {
		t.Fatal("board routes unavailable")
	}
	sessionHTTP, e := H.New(n, sessions)
	if e != nil {
		t.Fatal("session routes unavailable")
	}
	t.Cleanup(func() {
		sessions.Close()
		n.Close()
		if s.Close() != nil {
			t.Error("synthetic store cleanup")
		}
	})
	store, e := evidence.NewWindowsStore(filepath.Join(root, "evidence"))
	if e != nil {
		t.Fatal("private evidence unavailable")
	}
	f := &nativeRun{root: root, next: 1}
	grantWire := f.wire(t, "", 0, 0, map[string]any{"granteeActorId": testActor, "kind": "human", "rights": []string{"approve", "comment", "create", "edit", "read", "read-own-outcome", "review-handoff"}})
	var grant map[string]any
	_ = json.Unmarshal(grantWire, &grant)
	grant["boardId"] = testBoard
	grantWire, _ = json.Marshal(grant)
	gr, e := evidence.Freeze(evidence.Context{InstallationID: n.Context().InstallationID, Origin: n.Context().Origin}, contract.ResolvedRoute{Command: "grant.create", BoardID: testBoard}, contract.AuthenticatedActor{ActorID: n.Context().OwnerActorID}, grantWire)
	if e != nil {
		t.Fatal("grant fixture freeze")
	}
	path, e := store.Save(gr)
	if e != nil {
		t.Fatal("grant fixture private evidence")
	}
	f.grantRecord = gr
	f.grantPath = path
	i, e := T.FreezeMutation(grantWire, gr.Route(), contract.AuthenticatedActor{ActorID: n.Context().OwnerActorID})
	if e != nil {
		t.Fatal("grant fixture intent")
	}
	out := n.SubmitNative(nativeContext(t), T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer ")), i, grants.Route{BoardID: testBoard, Incarnation: testInc})
	if out.Result == nil || out.Result.Terminal == nil || out.Result.Terminal.Outcome != "committed" {
		t.Fatal("grant fixture admission")
	}
	grantID := ""
	for _, ref := range out.Result.Terminal.ResultRefs {
		if ref.Kind == "grant" {
			grantID = string(ref.ID)
		}
	}
	issued := n.Issue(nativeContext(t), T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer ")), T.CredentialIssueRequest{InstallationID: T.UUIDv4(n.Context().InstallationID), BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), GrantID: A.UUID(grantID)})
	raw, e := issued.MarshalEphemeral()
	if e != nil {
		t.Fatal("synthetic credential issuance")
	}
	secret, e := T.Decode[T.CredentialSecret](raw)
	if e != nil {
		t.Fatal("synthetic credential shape")
	}
	expires, _ := time.Parse("2006-01-02T15:04:05Z", string(secret.ExpiresAt))
	p := &nativeProvider{manager: m, metadata: CredentialMetadata{Selected: Selectors{n.Context().InstallationID, testBoard, testInc, grantID}, ActorID: testActor, ExpiresAt: expires}, token: secret.Token}
	f.provider = p
	f.runner = Runner{p, store}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/session") {
			sessionHTTP.ServeHTTP(w, r)
			return
		}
		if r.Method != "GET" {
			f.mutations.Add(1)
		}
		if r.Method != "GET" && f.lost.Swap(false) {
			capture := httptest.NewRecorder()
			board.ServeHTTP(capture, r)
			if capture.Code != 201 {
				t.Error("lost response fixture did not commit")
			}
			h, ok := w.(http.Hijacker)
			if !ok {
				t.Error("loss fixture unavailable")
				return
			}
			conn, _, err := h.Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		board.ServeHTTP(w, r)
	})
	server, e := H.Listen(nativeContext(t), n, handler)
	if e != nil {
		t.Fatal("exclusive dual loopback TLS unavailable")
	}
	t.Cleanup(server.Close)
	return f
}
func (f *nativeRun) wire(t *testing.T, target string, revision, cardRevision uint64, input any) []byte {
	t.Helper()
	f.next++
	var selected, card any
	if target != "" {
		selected = map[string]string{"cardId": target}
		card = cardRevision
	}
	b, e := json.Marshal(map[string]any{"opId": fmt.Sprintf("60000000-0000-0000-0000-%012d", f.next), "incarnation": testInc, "target": selected, "expectedRevision": revision, "expectedCardRevision": card, "input": input})
	if e != nil {
		t.Fatal("input unavailable")
	}
	return b
}
func (f *nativeRun) args(cmd ...string) []string {
	s := f.provider.metadata.Selected
	return append([]string{"--json", "--installation", s.InstallationID, "--board", s.BoardID, "--incarnation", s.Incarnation, "--grant", s.GrantID}, cmd...)
}
func (f *nativeRun) call(t *testing.T, want int, wire []byte, args ...string) []byte {
	t.Helper()
	var out, diag bytes.Buffer
	exit := f.runner.Run(nativeContext(t), f.args(args...), bytes.NewReader(wire), &out, &diag)
	if exit != want {
		var errno syscall.Errno
		if errors.As(f.provider.lastError, &errno) {
			t.Logf("native socket error %d", uint32(errno))
		} else if f.provider.lastError != nil {
			t.Logf("native response error type %T", f.provider.lastError)
		}
		t.Fatalf("native exit %d want %d category %s", exit, want, diag.String())
	}
	if bytes.Contains(out.Bytes(), []byte(string(f.provider.token))) || bytes.Contains(diag.Bytes(), []byte(string(f.provider.token))) {
		t.Fatal("secret escaped")
	}
	return out.Bytes()
}
func nativeTerminal(t *testing.T, b []byte) T.Terminal {
	t.Helper()
	x, e := T.Decode[T.Terminal](b)
	if e != nil {
		t.Fatal("terminal unavailable")
	}
	return x
}
func TestNativePinnedCLIJourneyAndOriginalOnlyReconciliation(t *testing.T) {
	f := newNative(t)
	if _, e := T.Decode[T.BoardRead](f.call(t, 0, nil, "board", "read")); e != nil {
		t.Fatal("board read")
	}
	created := nativeTerminal(t, f.call(t, 0, f.wire(t, "", 1, 0, map[string]any{"title": "Synthetic", "description": "<retained>\r\nΩ"}), "card", "create", "--input", "-"))
	card := string(created.ResultRefs[0].ID)
	second := nativeTerminal(t, f.call(t, 0, f.wire(t, "", 2, 0, map[string]any{"title": "Peer", "description": ""}), "card", "create", "--input", "-"))
	peer := string(second.ResultRefs[0].ID)
	f.call(t, 0, f.wire(t, card, 3, 1, map[string]any{"description": "Edited", "lane": "In Progress"}), "card", "edit", card, "--input", "-")
	f.call(t, 0, f.wire(t, card, 4, 2, map[string]any{"text": "Private synthetic comment"}), "comment", "add", card, "--input", "-")
	linked := nativeTerminal(t, f.call(t, 0, f.wire(t, card, 5, 3, map[string]any{"kind": "informational", "targetCardId": peer}), "card", "link-add", card, "--input", "-"))
	link := ""
	for _, ref := range linked.ResultRefs {
		if ref.Kind == "link" {
			link = string(ref.ID)
		}
	}
	f.call(t, 0, f.wire(t, card, 6, 4, map[string]any{"linkId": link}), "card", "link-remove", card, "--input", "-")
	f.call(t, 0, f.wire(t, card, 7, 5, map[string]any{}), "card", "review-handoff", card, "--input", "-")
	f.call(t, 0, f.wire(t, card, 8, 6, map[string]any{"policyVersion": 1}), "card", "approve", card, "--input", "-")
	f.call(t, 0, nil, "card", "read", card)
	f.call(t, 0, nil, "card", "history", card)
	// After a real commit, disconnect before any response bytes reach the CLI.
	f.lost.Store(true)
	wire := f.wire(t, "", 9, 0, map[string]any{"title": "Response lost", "description": ""})
	unknown := f.call(t, 4, wire, "card", "create", "--input", "-")
	u, e := T.Decode[T.KnownIdentityUnknown](unknown)
	if e != nil {
		t.Fatal("lost response identity")
	}
	before := f.mutations.Load()
	path := filepath.Join(f.root, "evidence", string(u.OpID)+".json")
	f.call(t, 0, nil, "operation", "read", string(u.OpID), "--evidence", path)
	if f.mutations.Load() != before {
		t.Fatal("lookup replayed mutation")
	}
	f.provider.metadata.ActorID = "00000000-0000-4000-8000-000000000099"
	f.call(t, 4, nil, "operation", "read", string(u.OpID), "--evidence", path)
	f.provider.metadata.ActorID = testActor
	// The actual current native-owner source can reconcile its original grant,
	// while the core refuses native-owner substitution for ordinary card intent.
	var out bytes.Buffer
	a := []string{"--json", "--board", testBoard, "operation", "read", f.grantRecord.Identity().OpID, "--evidence", f.grantPath, "--native-owner"}
	if f.runner.Run(nativeContext(t), a, nil, &out, io.Discard) != 0 {
		t.Fatal("native original grant lookup")
	}
	a = []string{"--json", "--board", testBoard, "operation", "read", string(u.OpID), "--evidence", path, "--native-owner"}
	out.Reset()
	if f.runner.Run(nativeContext(t), a, nil, &out, io.Discard) != 4 {
		t.Fatal("native owner broadened to domain outcome")
	}
	c, e := f.provider.manager.ReadContext()
	if e != nil {
		t.Fatal("native context")
	}
	c.IssuerSHA256 = strings.Repeat("0", 64)
	if _, e = NewBoardConnection(c, f.provider.metadata, f.provider.token); e == nil {
		t.Fatal("unvalidated issuer pin accepted")
	}
}
