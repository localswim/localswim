//go:build windows && amd64 && a01_native

package attachments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

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
	if os.Getenv("A01_NATIVE_OVERLAY") != "1" {
		t.Fatal("explicit private overlay required")
	}
	base := os.Getenv("S01_TEST_ROOT")
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if !filepath.IsAbs(base) || strings.HasPrefix(strings.ToLower(base)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("private synthetic root required")
	}
	root, e := os.MkdirTemp(base, "a01-")
	if e != nil {
		t.Fatal("private fixture unavailable")
	}
	t.Setenv("A01_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	check, e := setup.A01QualificationRoot()
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
		t.Fatal("attachment adapter unavailable")
	}
	f := &nativeFixture{store: s, native: n, sessions: sessions, handler: h, owner: T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer "))}
	t.Cleanup(func() {
		sessions.Close()
		n.Close()
		if s.Close() != nil {
			t.Error("synthetic store cleanup")
		}
	})
	grantWire := f.wire(t, "", 0, 0, map[string]any{"granteeActorId": nativeActor, "kind": "human", "rights": []string{"attach", "create", "read", "read-own-outcome"}})
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
func (f *nativeFixture) upload(t *testing.T, data []byte, rev, cr uint64) ([]byte, string) {
	m := f.wire(t, f.card, rev, cr, map[string]any{"name": "../../private-name.exe\r\n<script>", "mediaLabel": "text/html", "byteLength": len(data), "sha256": fmt.Sprintf("%x", sha256.Sum256(data))})
	return multipartFixture(t, []string{"metadata", "file"}, m, data, "")
}
func (f *nativeFixture) path() string {
	return "/api/v1/boards/" + nativeBoard + "/cards/" + f.card + "/attachments"
}
func (f *nativeFixture) call(method, path, media string, token T.SecretToken, body []byte, mutate func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://"+f.native.Context().Authority+path, bytes.NewReader(body))
	r.URL.Scheme = ""
	r.URL.Host = ""
	r.Header.Set("Authorization", "Bearer "+string(token))
	if media != "" {
		r.Header.Set("Content-Type", media)
	}
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f *nativeFixture) value(t *testing.T, q string) int {
	t.Helper()
	l, e := f.store.Reader(nc(t))
	if e != nil {
		t.Fatal("count checkout")
	}
	var n int
	e = l.Do(nc(t), func(c *sql.Conn) error { return c.QueryRowContext(nc(t), q).Scan(&n) })
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal("count snapshot")
	}
	return n
}
func nativeTerminal(t *testing.T, w *httptest.ResponseRecorder, status int) T.Terminal {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP status %d want %d", w.Code, status)
	}
	r, e := T.Decode[T.Terminal](w.Body.Bytes())
	if e != nil {
		t.Fatal("typed terminal")
	}
	return r
}
func attachmentID(t *testing.T, r T.Terminal) string {
	t.Helper()
	for _, ref := range r.ResultRefs {
		if ref.Kind == "attachment" {
			return string(ref.ID)
		}
	}
	t.Fatal("missing attachment result ref")
	return ""
}
func TestNativeMountedUploadDownloadAndReplay(t *testing.T) {
	f := freshNative(t)
	data := []byte{0, 255, 1, 13, 10, 65}
	b, m := f.upload(t, data, 2, 1)
	first := nativeTerminal(t, f.call("POST", f.path(), m, f.token, b, nil), 201)
	id := attachmentID(t, first)
	replay := nativeTerminal(t, f.call("POST", f.path(), m, f.token, b, nil), 200)
	if replay.Fingerprint != first.Fingerprint || f.value(t, "SELECT count(*) FROM attachment") != 1 {
		t.Fatal("retained operation replay")
	}
	w := f.call("GET", "/api/v1/boards/"+nativeBoard+"/attachments/"+id, "", f.token, nil, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) || w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Disposition") != `attachment; filename="attachment-`+id+`.bin"` || w.Header().Get("Content-Encoding") != "" {
		t.Fatal("forced raw download")
	}
	if strings.Contains(w.Header().Get("Content-Disposition"), "private-name") || strings.Contains(w.Header().Get("Content-Disposition"), ".exe") {
		t.Fatal("untrusted download name")
	}
	owner := f.call("GET", "/api/v1/boards/"+nativeBoard+"/attachments/"+id, "", f.owner, nil, nil)
	if owner.Code == 200 {
		t.Fatal("native owner ordinary bypass")
	}
}
func TestNativeBoundaryMultipartAndMetadataRefusals(t *testing.T) {
	f := freshNative(t)
	data := []byte("strict")
	b, m := f.upload(t, data, 2, 1)
	before := f.value(t, "SELECT count(*) FROM operation")
	for _, tt := range []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"host", func(r *http.Request) { r.Host = "wrong.localhost" }, 400}, {"TLS", func(r *http.Request) { r.TLS = nil }, 400}, {"origin", func(r *http.Request) { r.Header.Set("Origin", "https://wrong.localhost") }, 403}, {"duplicate origin", func(r *http.Request) {
			r.Header.Add("Origin", f.native.Context().Origin)
			r.Header.Add("Origin", f.native.Context().Origin)
		}, 403}, {"duplicate bearer", func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+string(f.token)) }, 401}, {"cookies no bearer", func(r *http.Request) {
			r.Header.Del("Authorization")
			r.Header.Set("Cookie", "credential="+string(f.token))
		}, 401}, {"encoded", func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }, 400}, {"query", func(r *http.Request) { r.URL.RawQuery = "private-secret" }, 400}, {"body target path", func(r *http.Request) { r.URL.Path = strings.Replace(r.URL.Path, f.card, nativeActor, 1) }, 400}, {"wrong media", func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := f.call("POST", f.path(), m, f.token, b, tt.mutate)
			if w.Code != tt.status {
				t.Fatal(w.Code)
			}
			if _, e := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); e != nil {
				t.Fatal("safe refusal shape")
			}
			if strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), string(f.token)) || strings.Contains(w.Body.String(), "private-name") {
				t.Fatal("private error echo")
			}
		})
	}
	for _, tt := range []struct {
		name     string
		metadata []byte
		file     []byte
	}{
		{"UTF8", []byte{255}, data}, {"duplicate JSON", []byte(`{"opId":"a","opId":"b"}`), data}, {"unknown actor", f.wire(t, f.card, 2, 1, map[string]any{"name": "x", "mediaLabel": "", "byteLength": len(data), "sha256": fmt.Sprintf("%x", sha256.Sum256(data)), "actorId": nativeActor}), data}, {"wrong bytes", f.wire(t, f.card, 2, 1, map[string]any{"name": "x", "mediaLabel": "", "byteLength": len(data), "sha256": fmt.Sprintf("%x", sha256.Sum256(data))}), []byte("STRICT")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, m := multipartFixture(t, []string{"metadata", "file"}, tt.metadata, tt.file, "")
			w := f.call("POST", f.path(), m, f.token, b, nil)
			if w.Code != 400 {
				t.Fatal(w.Code)
			}
		})
	}
	if f.value(t, "SELECT count(*) FROM operation") != before || f.value(t, "SELECT count(*) FROM attachment") != 0 || f.value(t, "SELECT revision FROM board") != 2 {
		t.Fatal("preterminal refusal changed store")
	}
}
func TestNativeBrowserCurrentReadAndRevocation(t *testing.T) {
	f := freshNative(t)
	req := T.CodeRequest{InstallationID: T.UUIDv4(f.native.Context().InstallationID), BoardID: A.UUID(nativeBoard), Incarnation: A.UUID(nativeInc), GrantID: A.UUID(f.grant)}
	code, e := f.sessions.IssueCode(nc(t), f.owner, req)
	if e != nil {
		t.Fatal("browser code")
	}
	browser, e := f.sessions.Exchange(nc(t), code.Code)
	if e != nil {
		t.Fatal("browser exchange")
	}
	b, m := f.upload(t, nil, 2, 1)
	if w := f.call("POST", f.path(), m, browser.Token, b, nil); w.Code != 401 && w.Code != 403 {
		t.Fatal("absent origin browser write")
	}
	r := nativeTerminal(t, f.call("POST", f.path(), m, browser.Token, b, func(r *http.Request) { r.Header.Set("Origin", f.native.Context().Origin) }), 201)
	id := attachmentID(t, r)
	path := "/api/v1/boards/" + nativeBoard + "/attachments/" + id
	w := f.call("GET", path, "", browser.Token, nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") })
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("browser zero download")
	}
	if e = f.sessions.Revoke(nc(t), "browser", browser.Token); e != nil {
		t.Fatal("browser revoke")
	}
	w = f.call("GET", path, "", browser.Token, nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") })
	if w.Code == 200 {
		t.Fatal("revoked browser download")
	}
}
func TestPinnedHTTPSAttachmentRawRoundTrip(t *testing.T) {
	f := freshNative(t)
	server, e := H.Listen(nc(t), f.native, f.handler)
	if e != nil {
		t.Fatal("synthetic HTTPS listener")
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
	data := []byte{0, 255, 13, 10, 4, 5}
	b, m := f.upload(t, data, 2, 1)
	req, e := http.NewRequest("POST", f.native.Context().Origin+f.path(), io.NopCloser(bytes.NewReader(b)))
	if e != nil {
		t.Fatal("upload request")
	}
	req.Header.Set("Content-Type", m)
	req.Header.Set("Authorization", "Bearer "+string(f.token))
	response, e := client.Do(req)
	if e != nil {
		var errno syscall.Errno
		if errors.As(e, &errno) {
			t.Fatalf("pinned upload socket error %d", uint32(errno))
		}
		t.Fatalf("pinned upload error type %T", e)
	}
	raw, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || response.StatusCode != 201 {
		t.Fatal("pinned upload response")
	}
	terminal, e := T.Decode[T.Terminal](raw)
	if e != nil {
		t.Fatal("pinned upload terminal")
	}
	id := attachmentID(t, terminal)
	req, e = http.NewRequest("GET", f.native.Context().Origin+"/api/v1/boards/"+nativeBoard+"/attachments/"+id, nil)
	if e != nil {
		t.Fatal("download request")
	}
	req.Header.Set("Authorization", "Bearer "+string(f.token))
	response, e = client.Do(req)
	if e != nil {
		t.Fatal("pinned download")
	}
	raw, e = io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || response.StatusCode != 200 || !bytes.Equal(raw, data) || response.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatal("pinned byte exact download")
	}
}
