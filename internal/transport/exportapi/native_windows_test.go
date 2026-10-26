//go:build windows && amd64 && h02_native

package exportapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/attachments"
	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

const nativeBoard = "10000000-0000-0000-0000-000000000001"
const nativeInc = "20000000-0000-0000-0000-000000000001"
const nativeActor = "30000000-0000-0000-0000-000000000001"

type nativeFixture struct {
	store        *S.Store
	native       *credentials.Service
	sessions     *session.Service
	handler      *Handler
	owner, token T.SecretToken
	card, grant  string
	next         int
}

func nc(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func freshNative(t *testing.T) *nativeFixture {
	t.Helper()
	if os.Getenv("H02_NATIVE_OVERLAY") != "1" {
		t.Fatal("explicit private overlay required")
	}
	base := os.Getenv("S01_TEST_ROOT")
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if !filepath.IsAbs(base) || strings.HasPrefix(strings.ToLower(base)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("private synthetic root required")
	}
	root, e := os.MkdirTemp(base, "h02-")
	if e != nil {
		t.Fatal("private fixture unavailable")
	}
	t.Setenv("H02_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	check, e := setup.H02QualificationRoot()
	if e != nil || check != filepath.Join(root, "installation") {
		t.Fatal("overlay sentinel required")
	}
	m, e := setup.New(nil)
	if e != nil || m.Init(nil).Exit != 0 {
		t.Fatal("synthetic setup unavailable")
	}
	owner, e := m.LoadNativeOwner()
	if e != nil {
		t.Fatal("synthetic owner unavailable")
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal("synthetic ACL unavailable")
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal("synthetic ACL unavailable")
	}
	dir := filepath.Join(root, "board")
	if windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}) != nil {
		t.Fatal("private board directory unavailable")
	}
	s, e := S.Create(nc(t), dir, schema.Identity{BoardID: nativeBoard, Incarnation: nativeInc})
	if e != nil {
		t.Fatal("synthetic store unavailable")
	}
	n, e := credentials.SourceRoot(s)
	if e != nil {
		t.Fatal("native source unavailable")
	}
	sessions, e := session.New(s, n)
	if e != nil {
		t.Fatal("sessions unavailable")
	}
	h, e := New(s, n, sessions)
	if e != nil {
		t.Fatal("board adapter unavailable")
	}
	f := &nativeFixture{store: s, native: n, sessions: sessions, handler: h, owner: T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer "))}
	t.Cleanup(func() {
		sessions.Close()
		n.Close()
		if s.Close() != nil {
			t.Error("synthetic store cleanup")
		}
	})
	grantWire := f.wire(t, "", 0, 0, map[string]any{"granteeActorId": nativeActor, "kind": "human", "rights": []string{"attach", "comment", "create", "edit", "export", "read", "read-own-outcome"}})
	i, e := T.FreezeMutation(grantWire, contract.ResolvedRoute{Command: "grant.create", BoardID: nativeBoard}, contract.AuthenticatedActor{ActorID: n.Context().OwnerActorID})
	if e != nil {
		t.Fatal("grant fixture intent")
	}
	out := n.SubmitNative(nc(t), f.owner, i, grants.Route{BoardID: nativeBoard, Incarnation: nativeInc})
	if out.Result == nil || out.Result.Terminal == nil || out.Result.Terminal.Outcome != "committed" {
		t.Fatal("grant fixture effect")
	}
	for _, ref := range out.Result.Terminal.ResultRefs {
		if ref.Kind == "grant" {
			f.grant = string(ref.ID)
		}
	}
	issued := n.Issue(nc(t), f.owner, T.CredentialIssueRequest{InstallationID: T.UUIDv4(n.Context().InstallationID), BoardID: A.UUID(nativeBoard), Incarnation: A.UUID(nativeInc), GrantID: A.UUID(f.grant)})
	raw, e := issued.MarshalEphemeral()
	if e != nil {
		t.Fatal("synthetic CLI issuance")
	}
	secret, e := T.Decode[T.CredentialSecret](raw)
	if e != nil {
		t.Fatal("synthetic CLI shape")
	}
	f.token = secret.Token
	g, e := sessions.Admission("cli", f.token)
	if e != nil {
		t.Fatal("CLI admission")
	}
	i, e = T.FreezeMutation(f.wire(t, "", 1, 0, map[string]any{"title": "Synthetic", "description": "private text"}), contract.ResolvedRoute{Command: "card.create", BoardID: nativeBoard}, contract.AuthenticatedActor{ActorID: nativeActor})
	if e != nil {
		t.Fatal("card fixture intent")
	}
	r := writer.New(s).Submit(nc(t), i, cards.New(g).Callbacks(cards.Route{BoardID: nativeBoard, Incarnation: nativeInc}))
	if r.Terminal == nil || r.Terminal.Outcome != "committed" {
		t.Fatal("card fixture effect")
	}
	for _, ref := range r.Terminal.ResultRefs {
		if ref.Kind == "card" {
			f.card = string(ref.ID)
		}
	}
	return f
}
func (f *nativeFixture) wire(t *testing.T, target string, rev, cr uint64, input any) []byte {
	t.Helper()
	f.next++
	var selected, cardRev any
	if target != "" {
		selected = map[string]string{"cardId": target}
		cardRev = cr
	}
	fields := map[string]any{"incarnation": nativeInc, "opId": fmt.Sprintf("50000000-0000-0000-0000-%012d", f.next), "target": selected, "expectedRevision": rev, "expectedCardRevision": cardRev, "input": input}
	if m, ok := input.(map[string]any); ok {
		if _, grant := m["granteeActorId"]; grant {
			fields["boardId"] = nativeBoard
		}
	}
	b, e := json.Marshal(fields)
	if e != nil {
		t.Fatal("wire fixture")
	}
	return b
}

func (f *nativeFixture) call(method, path string, token T.SecretToken, b []byte, change func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://"+f.native.Context().Authority+path, bytes.NewReader(b))
	r.URL.Scheme = ""
	r.URL.Host = ""
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+string(token))
	}
	r.Header.Set("Content-Type", "application/json")
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f *nativeFixture) path() string { return "/api/v1/boards/" + nativeBoard + "/exports" }
func (f *nativeFixture) request(rev int) []byte {
	return []byte(`{"incarnation":"` + nativeInc + `","expectedRevision":` + strconv.Itoa(rev) + `,"input":{}}`)
}
func (f *nativeFixture) raw(t *testing.T, q string, args ...any) {
	t.Helper()
	l, e := f.store.Writer(nc(t))
	if e != nil {
		t.Fatal("fixture writer")
	}
	e = l.Do(nc(t), func(c *sql.Conn) error { _, e := c.ExecContext(nc(t), q, args...); return e })
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal("fixture mutation")
	}
}
func (f *nativeFixture) add(t *testing.T, data []byte) {
	t.Helper()
	d := sha256.Sum256(data)
	g, e := f.sessions.Admission("cli", f.token)
	if e != nil {
		t.Fatal("admission")
	}
	i, e := T.FreezeMutation(f.wire(t, f.card, 2, 1, map[string]any{"name": "../inert.bin", "mediaLabel": "application/octet-stream", "sha256": hex.EncodeToString(d[:]), "byteLength": len(data)}), contract.ResolvedRoute{Command: "attachment.add", BoardID: nativeBoard, TargetID: f.card}, contract.AuthenticatedActor{ActorID: nativeActor})
	if e != nil {
		t.Fatal("attachment intent")
	}
	u, e := attachments.Buffer(bytes.NewReader(data))
	if e != nil {
		t.Fatal("upload fixture")
	}
	out, _ := attachments.New(f.store).Add(nc(t), g, attachments.Route{BoardID: nativeBoard, Incarnation: nativeInc, CardID: f.card}, i, u)
	if out.Terminal == nil || out.Terminal.Outcome != "committed" {
		t.Fatal("attachment fixture")
	}
}
func archiveBytes(t *testing.T, b []byte, blob []byte) {
	t.Helper()
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		t.Fatal("complete stored ZIP")
	}
	if len(z.File) != 7 || z.File[0].Name != "manifest.json" || z.File[len(z.File)-1].Name != "COMPLETE" {
		t.Fatal("exact member inventory/order")
	}
	m := map[string][]byte{}
	for _, f := range z.File {
		if f.Method != zip.Store {
			t.Fatal("compressed member")
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal("member open")
		}
		v, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal("member read")
		}
		if _, ok := m[f.Name]; ok {
			t.Fatal("duplicate member")
		}
		m[f.Name] = v
	}
	d := sha256.Sum256(m["manifest.json"])
	if string(m["COMPLETE"]) != hex.EncodeToString(d[:])+"\n" {
		t.Fatal("COMPLETE manifest digest")
	}
	manifest, e := A.DecodeReadable[A.Manifest](m["manifest.json"])
	if e != nil || manifest.Source.BoardID != nativeBoard || manifest.Revision != 3 {
		t.Fatal("source identity/revision")
	}
	if len(manifest.Inventory) != 5 {
		t.Fatal("manifest exact inventory")
	}
	for _, v := range manifest.Inventory {
		b, ok := m[v.Path]
		d := sha256.Sum256(b)
		if !ok || A.Number(len(b)) != v.ByteLength || A.Digest(hex.EncodeToString(d[:])) != v.SHA256 {
			t.Fatal("inventory length/digest")
		}
	}
	d = sha256.Sum256(blob)
	if !bytes.Equal(m["blobs/sha256-"+hex.EncodeToString(d[:])], blob) {
		t.Fatal("raw binary bytes")
	}
	for _, name := range []string{"board.json", "history.json", "activity.json", "outcomes.json"} {
		if len(m[name]) == 0 {
			t.Fatal("missing document")
		}
	}
	if bytes.Contains(b, []byte(string(fixtureSecretMarker))) {
		t.Fatal("credential disclosure")
	}
}

const fixtureSecretMarker = "Authorization: Bearer "

func TestNativeExportRouteStoredInventoryAndBrowser(t *testing.T) {
	f := freshNative(t)
	raw := []byte{0, 255, 254, 13, 10, 42, 0}
	f.add(t, raw)
	w := f.call("POST", f.path(), f.token, f.request(3), nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" || w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal("ZIP delivery headers")
	}
	archiveBytes(t, w.Body.Bytes(), raw)
	code, e := f.sessions.IssueCode(nc(t), f.owner, T.CodeRequest{InstallationID: T.UUIDv4(f.native.Context().InstallationID), BoardID: nativeBoard, Incarnation: nativeInc, GrantID: A.UUID(f.grant)})
	if e != nil {
		t.Fatal("browser code")
	}
	browser, e := f.sessions.Exchange(nc(t), code.Code)
	if e != nil {
		t.Fatal("browser exchange")
	}
	w = f.call("POST", f.path(), browser.Token, f.request(3), func(r *http.Request) { r.Header.Set("Origin", f.native.Context().Origin) })
	if w.Code != 200 {
		t.Fatal("browser export")
	}
	archiveBytes(t, w.Body.Bytes(), raw)
	if f.call("POST", f.path(), browser.Token, f.request(3), nil).Code != 403 {
		t.Fatal("absent browser write origin")
	}
}
func TestNativePrestreamRefusals(t *testing.T) {
	f := freshNative(t)
	for _, tc := range []struct {
		path   string
		body   []byte
		change func(*http.Request)
		status int
	}{
		{f.path(), f.request(1), nil, 409},
		{f.path(), bytes.ReplaceAll(f.request(2), []byte(nativeInc), []byte("20000000-0000-0000-0000-000000000099")), nil, 409},
		{strings.Replace(f.path(), nativeBoard, "10000000-0000-0000-0000-000000000099", 1), f.request(2), nil, 403},
		{f.path(), []byte(`{"input":{},"input":{}}`), nil, 400},
		{f.path(), f.request(2), func(r *http.Request) { r.Host = "wrong.localhost:47443" }, 400},
		{f.path(), f.request(2), func(r *http.Request) { r.Header.Set("Origin", "https://wrong.localhost:47443") }, 403},
		{f.path(), f.request(2), func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 400},
		{f.path(), f.request(2), func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+string(f.token)) }, 401},
	} {
		w := f.call("POST", tc.path, f.token, tc.body, tc.change)
		if w.Code != tc.status || w.Header().Get("Content-Type") != "application/json" || bytes.HasPrefix(w.Body.Bytes(), []byte("PK")) {
			t.Fatalf("prestream refusal HTTP %d want %d", w.Code, tc.status)
		}
		if _, e := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); e != nil {
			t.Fatal("refusal envelope")
		}
	}
	for _, right := range []string{"export", "read"} {
		f.raw(t, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name=?", nativeInc, f.grant, right)
		if f.call("POST", f.path(), f.token, f.request(2), nil).Code != 403 {
			t.Fatal("missing ordinary right")
		}
		f.raw(t, "INSERT INTO grant_right VALUES(?,?,?)", nativeInc, f.grant, right)
	}
	if f.call("POST", f.path(), f.owner, f.request(2), nil).Code < 400 {
		t.Fatal("native owner gained ordinary export")
	}
	if f.call("POST", f.path(), T.SecretToken(strings.Repeat("A", 43)), f.request(2), nil).Code < 400 {
		t.Fatal("unknown credential")
	}
	f.raw(t, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", nativeInc, f.grant)
	if f.call("POST", f.path(), f.token, f.request(2), func(r *http.Request) { r.Header.Set("Origin", f.native.Context().Origin) }).Code != 401 {
		t.Fatal("revoked grant")
	}
}

func TestNativePinnedVerifierRechecksCurrentAuthority(t *testing.T) {
	for _, change := range []string{"right", "grant", "credential"} {
		t.Run(change, func(t *testing.T) {
			f := freshNative(t)
			current, e := f.sessions.Current(nc(t), "cli", f.token)
			if e != nil {
				t.Fatal("initial authentication")
			}
			switch change {
			case "right":
				f.raw(t, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='export'", nativeInc, f.grant)
			case "grant":
				f.raw(t, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", nativeInc, f.grant)
			case "credential":
				if f.sessions.Revoke(nc(t), "cli", f.token) != nil {
					t.Fatal("credential revocation")
				}
			}
			p, e := f.handler.prepare(nc(t), nativeBoard, "cli", f.token, current, T.ExportRequest{Incarnation: nativeInc, ExpectedRevision: 2})
			if p != nil || !errors.Is(e, grants.ErrDenied) {
				t.Fatal("cached current authority admitted snapshot")
			}
		})
	}
}

type brokenWriter struct {
	header        http.Header
	status, limit int
	data          []byte
	cancel        context.CancelFunc
	short         bool
}

func (w *brokenWriter) Header() http.Header { return w.header }
func (w *brokenWriter) WriteHeader(s int)   { w.status = s }
func (w *brokenWriter) Write(p []byte) (int, error) {
	if w.cancel != nil {
		w.cancel()
		return 0, context.Canceled
	}
	n := len(p)
	if n > w.limit-len(w.data) {
		n = w.limit - len(w.data)
	}
	if n < 0 {
		n = 0
	}
	w.data = append(w.data, p[:n]...)
	if w.short {
		return n, nil
	}
	if n < len(p) {
		return n, io.ErrClosedPipe
	}
	return n, nil
}
func TestNativeInterruptedDeliveryAbortsWithoutSuccessTrailer(t *testing.T) {
	f := freshNative(t)
	blob := bytes.Repeat([]byte{0, 255, 13, 10}, 20000)
	f.add(t, blob)
	current, e := f.sessions.Current(nc(t), "cli", f.token)
	if e != nil {
		t.Fatal("current")
	}
	p, e := f.handler.prepare(nc(t), nativeBoard, "cli", f.token, current, T.ExportRequest{Incarnation: nativeInc, ExpectedRevision: 3})
	if e != nil {
		t.Fatal("prepared")
	}
	for _, mode := range []string{"zero", "partial", "short", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(nc(t))
			defer cancel()
			w := &brokenWriter{header: http.Header{}, limit: 5000}
			if mode == "zero" {
				w.limit = 0
			}
			if mode == "short" {
				w.short = true
			}
			if mode == "cancel" {
				w.cancel = cancel
			}
			var caught any
			func() {
				defer func() { caught = recover() }()
				deliver(w, httptest.NewRequest("POST", base, nil).WithContext(ctx), p)
			}()
			if caught != http.ErrAbortHandler || w.status != 200 || w.Header().Get("Content-Type") != "application/zip" || len(w.data) >= int(p.ByteLength()) {
				t.Fatal("partial delivery not aborted")
			}
			if _, e := zip.NewReader(bytes.NewReader(w.data), int64(len(w.data))); e == nil {
				t.Fatal("partial accepted as ZIP")
			}
			for _, bad := range []string{`"outcome"`, `"confirmed"`, `"offered"`, `"refused"`} {
				if bytes.Contains(w.data, []byte(bad)) {
					t.Fatal("JSON trailer/status")
				}
			}
		})
	}
	var complete bytes.Buffer
	if p.Write(nc(t), &complete) != nil {
		t.Fatal("independent successful delivery")
	}
	archiveBytes(t, complete.Bytes(), blob)
}
func TestNativePinnedHTTPSExport(t *testing.T) {
	f := freshNative(t)
	raw := bytes.Repeat([]byte{0, 255, 13, 10, 7}, 16000)
	f.add(t, raw)
	server, e := H.Listen(nc(t), f.native, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Synthetic-Interrupt") == "1" {
			f.handler.ServeHTTP(&interruptHTTP{ResponseWriter: w, remaining: 5000}, r)
			return
		}
		f.handler.ServeHTTP(w, r)
	}))
	if e != nil {
		t.Fatal("synthetic TLS listener")
	}
	t.Cleanup(server.Close)
	cfg, e := f.native.Context().TLSConfig()
	if e != nil {
		t.Fatal("issuer pin")
	}
	tr := &http.Transport{TLSClientConfig: cfg, Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprint(f.native.Context().Port)))
	}}
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	req, e := http.NewRequest("POST", f.native.Context().Origin+f.path(), bytes.NewReader(f.request(3)))
	if e != nil {
		t.Fatal("TLS request")
	}
	req.Header.Set("Authorization", "Bearer "+string(f.token))
	req.Header.Set("Content-Type", "application/json")
	response, e := client.Do(req)
	if e != nil {
		var errno syscall.Errno
		if errors.As(e, &errno) {
			t.Fatalf("pinned TLS socket error %d", uint32(errno))
		}
		t.Fatalf("pinned TLS response type %T", e)
	}
	b, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/zip" {
		t.Fatal("TLS body")
	}
	archiveBytes(t, b, raw)
	// The real server must terminate the response with an incomplete body,
	// rather than append JSON or finish a nominally successful ZIP response.
	req, e = http.NewRequest("POST", f.native.Context().Origin+f.path(), bytes.NewReader(f.request(3)))
	if e != nil {
		t.Fatal("interrupted TLS request")
	}
	req.Header.Set("Authorization", "Bearer "+string(f.token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Synthetic-Interrupt", "1")
	response, e = client.Do(req)
	if e != nil {
		t.Fatal("interrupted response headers unavailable")
	}
	b, e = io.ReadAll(response.Body)
	response.Body.Close()
	if e == nil || response.StatusCode != 200 || len(b) != 5000 || response.ContentLength <= int64(len(b)) {
		t.Fatal("HTTP truncation not observable")
	}
	if _, e := zip.NewReader(bytes.NewReader(b), int64(len(b))); e == nil {
		t.Fatal("interrupted response accepted as archive")
	}
	if bytes.HasSuffix(b, []byte("Request refused\n")) || bytes.Contains(b, []byte(`"outcome": "offered"`)) || bytes.Contains(b, []byte(`"outcome": "confirmed"`)) {
		t.Fatal("interrupted HTTP fabricated success or JSON trailer")
	}
}

type interruptHTTP struct {
	http.ResponseWriter
	remaining int
}

func (w *interruptHTTP) Write(p []byte) (int, error) {
	n := len(p)
	if n > w.remaining {
		n = w.remaining
	}
	w.remaining -= n
	written, e := w.ResponseWriter.Write(p[:n])
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	if e != nil {
		return written, e
	}
	if n != len(p) {
		return written, io.ErrClosedPipe
	}
	return written, nil
}
