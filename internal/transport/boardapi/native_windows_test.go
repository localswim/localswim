//go:build windows && amd64 && k01_native

package boardapi

import (
	"bytes"
	"context"
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
	if os.Getenv("K01_NATIVE_OVERLAY") != "1" {
		t.Fatal("explicit private overlay required")
	}
	base := os.Getenv("S01_TEST_ROOT")
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if !filepath.IsAbs(base) || strings.HasPrefix(strings.ToLower(base)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("private synthetic root required")
	}
	root, e := os.MkdirTemp(base, "k01-")
	if e != nil {
		t.Fatal("private fixture unavailable")
	}
	t.Setenv("K01_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	check, e := setup.K01QualificationRoot()
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
	grantWire := f.wire(t, "", 0, 0, map[string]any{"granteeActorId": nativeActor, "kind": "human", "rights": []string{"approve", "comment", "create", "edit", "history-admin", "read", "read-own-outcome", "review-handoff"}})
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
func (f *nativeFixture) path() string     { return "/api/v1/boards/" + nativeBoard }
func (f *nativeFixture) cardPath() string { return f.path() + "/cards/" + f.card }
func terminal(t *testing.T, w *httptest.ResponseRecorder, status int) T.Terminal {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP %d want %d", w.Code, status)
	}
	out, e := T.Decode[T.Terminal](w.Body.Bytes())
	if e != nil {
		t.Fatal("terminal contract")
	}
	return out
}
func (f *nativeFixture) lookup(out T.Terminal) *httptest.ResponseRecorder {
	return f.call("GET", f.path()+"/operations/"+string(out.OpID)+"?incarnation="+string(out.Incarnation)+"&fingerprintVersion=1&fingerprint="+string(out.Fingerprint), f.token, nil, nil)
}
func (f *nativeFixture) count(t *testing.T, q string) int {
	t.Helper()
	l, e := f.store.Reader(nc(t))
	if e != nil {
		t.Fatal("count checkout")
	}
	n := 0
	e = l.Do(nc(t), func(c *sql.Conn) error { return c.QueryRowContext(nc(t), q).Scan(&n) })
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal("count read")
	}
	return n
}
func TestNativeMutationReadLookupRecovery(t *testing.T) {
	f := freshNative(t)
	w := f.call("GET", f.path(), f.token, nil, nil)
	view, e := T.Decode[T.BoardRead](w.Body.Bytes())
	if e != nil || w.Code != 200 || len(view.Result.Cards) != 1 || view.Result.Revision != 2 {
		t.Fatal("coherent board projection")
	}
	b := f.wire(t, "", 2, 0, map[string]any{"title": "Second", "description": ""})
	created := terminal(t, f.call("POST", f.path()+"/cards", f.token, b, nil), 201)
	replay := terminal(t, f.call("POST", f.path()+"/cards", f.token, b, nil), 200)
	if replay.Fingerprint != created.Fingerprint || *replay.Revision != 3 {
		t.Fatal("exact replay receipt")
	}
	resolved := terminal(t, f.lookup(created), 200)
	if resolved.OpID != created.OpID || resolved.Fingerprint != created.Fingerprint {
		t.Fatal("original lookup identity")
	}
	before := f.count(t, "SELECT count(*) FROM operation")
	path := f.path() + "/operations/" + string(created.OpID) + "?incarnation=" + nativeInc + "&fingerprintVersion=1&fingerprint=" + strings.Repeat("0", 64)
	w = f.call("GET", path, f.token, nil, nil)
	refused, e := T.Decode[T.NonterminalRefusal](w.Body.Bytes())
	if e != nil || w.Code != 409 || refused.Error.Code != "OPERATION_MISMATCH" {
		t.Fatal("lookup mismatch")
	}
	w = f.call("GET", strings.Replace(path, string(created.OpID), "90000000-0000-0000-0000-000000000001", 1), f.token, nil, nil)
	u, e := T.Decode[T.KnownIdentityUnknown](w.Body.Bytes())
	if e != nil || w.Code != 200 || u.Fingerprint != A.Digest(strings.Repeat("0", 64)) {
		t.Fatal("missing lookup original unknown")
	}
	if before != f.count(t, "SELECT count(*) FROM operation") {
		t.Fatal("lookup executed")
	}
	for _, bad := range [][]byte{[]byte(`{"input":{},"input":{}}`), []byte(`{"input":"\ud800"}`), append([]byte{0xff}, b...)} {
		w = f.call("POST", f.path()+"/cards", f.token, bad, nil)
		if w.Code != 400 {
			t.Fatal("malformed request")
		}
	}
	wrong := f.wire(t, "40000000-0000-0000-0000-000000000099", 3, 1, map[string]any{"title": "Wrong"})
	if f.call("PATCH", f.cardPath(), f.token, wrong, nil).Code != 400 {
		t.Fatal("route-target mismatch")
	}
	stale := terminal(t, f.call("PATCH", f.cardPath(), f.token, f.wire(t, f.card, 2, 1, map[string]any{"title": "Stale"}), nil), 200)
	if stale.Outcome != "not_committed" || stale.Code != "REVISION_CONFLICT" {
		t.Fatal("stale admission")
	}
	nochange := terminal(t, f.call("PATCH", f.cardPath(), f.token, f.wire(t, f.card, 3, 1, map[string]any{"title": "Synthetic"}), nil), 200)
	if nochange.Outcome != "not_committed" || nochange.Code != "NO_CHANGE" {
		t.Fatal("shared NO_CHANGE policy")
	}
	comment := terminal(t, f.call("POST", f.cardPath()+"/comments", f.token, f.wire(t, f.card, 3, 1, map[string]any{"text": "Text <script>\r\n"}), nil), 201)
	if *comment.Revision != 4 || *comment.CardRevision != 2 {
		t.Fatal("comment shared admission")
	}
	w = f.call("GET", f.cardPath(), f.token, nil, nil)
	card, e := T.Decode[T.CardRead](w.Body.Bytes())
	if e != nil || w.Code != 200 || len(card.Result.Comments) != 1 || len(card.Result.Actors) != 1 || len(card.Result.HistoryReferences) != 1 {
		t.Fatal("current card exact references")
	}
	w = f.call("GET", f.cardPath()+"/history", f.token, nil, nil)
	history, e := T.Decode[T.HistoryRead](w.Body.Bytes())
	if e != nil || w.Code != 200 || len(history.Result.Records) != 1 || history.Result.Records[0].State.Title != "Synthetic" || history.Result.CardRevision != 2 {
		t.Fatal("earlier state projection")
	}
	terminal(t, f.call("PATCH", f.cardPath(), f.token, f.wire(t, f.card, 4, 2, map[string]any{"lane": "In Progress"}), nil), 200)
	terminal(t, f.call("POST", f.cardPath()+"/review-handoff", f.token, f.wire(t, f.card, 5, 3, map[string]any{}), nil), 200)
	approved := terminal(t, f.call("POST", f.cardPath()+"/approve", f.token, f.wire(t, f.card, 6, 4, map[string]any{"policyVersion": 1}), nil), 200)
	if approved.Outcome != "committed" || *approved.Revision != 7 {
		t.Fatal("shared approve")
	}
	blocked := terminal(t, f.call("PATCH", f.cardPath(), f.token, f.wire(t, f.card, 7, 5, map[string]any{"title": "No reopen"}), nil), 200)
	if blocked.Outcome != "not_committed" {
		t.Fatal("Completed mutation admitted")
	}
	through := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	req, _ := json.Marshal(T.PreviewRequest{Incarnation: nativeInc, ExpectedRevision: 7, Command: "history.clear", Input: json.RawMessage(`{"throughTime":"` + through + `"}`)})
	w = f.call("POST", f.path()+"/recovery/preview", f.token, req, nil)
	preview, e := T.Decode[T.Preview](w.Body.Bytes())
	if e != nil || w.Code != 200 || preview.Inventory.Counts.HistorySnapshots != 4 {
		t.Fatal("standalone recovery preview")
	}
	if strings.Contains(w.Body.String(), "Synthetic") || strings.Contains(w.Body.String(), "Text <script>") {
		t.Fatal("preview prose disclosure")
	}
	confirm := f.wire(t, "", 7, 0, map[string]any{"throughTime": through})
	var fields map[string]any
	json.Unmarshal(confirm, &fields)
	fields["command"] = "history.clear"
	fields["previewHash"] = preview.PreviewHash
	confirm, _ = json.Marshal(fields)
	cleared := terminal(t, f.call("POST", f.path()+"/recovery/confirm", f.token, confirm, nil), 200)
	if cleared.Outcome != "committed" || *cleared.Revision != 8 || cleared.CardRevision != nil {
		t.Fatal("recovery confirm reused preview")
	}
	if terminal(t, f.call("POST", f.path()+"/recovery/confirm", f.token, confirm, nil), 200).Fingerprint != cleared.Fingerprint {
		t.Fatal("confirm replay")
	}
	// Cache loss cannot bypass S02's receipt-first resolution. A fresh adapter
	// has no preview, but matching terminal identity still resolves unchanged.
	f.handler, e = New(f.store, f.native, f.sessions)
	if e != nil {
		t.Fatal("new handler")
	}
	if terminal(t, f.call("POST", f.path()+"/recovery/confirm", f.token, confirm, nil), 200).Fingerprint != cleared.Fingerprint {
		t.Fatal("receipt-first after adapter restart")
	}
	resolved = terminal(t, f.lookup(created), 200)
	if resolved.Retention != "compact" || resolved.Outcome != "committed" {
		t.Fatal("compact lookup")
	}
	w = f.call("GET", f.cardPath()+"/history", f.token, nil, nil)
	history, e = T.Decode[T.HistoryRead](w.Body.Bytes())
	if e != nil || w.Code != 200 || len(history.Result.Records) != 4 || history.Result.Records[0].State != nil || len(history.Result.Coverage.Gaps) == 0 {
		t.Fatal("cleared history known gaps")
	}
	w = f.call("GET", f.cardPath(), f.token, nil, nil)
	card, e = T.Decode[T.CardRead](w.Body.Bytes())
	if e != nil || w.Code != 200 || card.Result.Card.Lane != "Completed" || card.Result.Card.Revision != 5 || card.Result.Card.CurrentApprovalID == nil || len(card.Result.Comments) != 1 {
		t.Fatal("recovery changed current content/approval")
	}
}

func TestNativeHistoryProvenanceTombstonesAndRecoveryRefusals(t *testing.T) {
	f := freshNative(t)
	second := terminal(t, f.call("POST", f.path()+"/cards", f.token, f.wire(t, "", 2, 0, map[string]any{"title": "Peer", "description": ""}), nil), 201)
	var peer string
	for _, ref := range second.ResultRefs {
		if ref.Kind == "card" {
			peer = string(ref.ID)
		}
	}
	link := terminal(t, f.call("POST", f.cardPath()+"/link-add", f.token, f.wire(t, f.card, 3, 1, map[string]any{"kind": "informational", "targetCardId": peer}), nil), 201)
	var linkID string
	for _, ref := range link.ResultRefs {
		if ref.Kind == "link" {
			linkID = string(ref.ID)
		}
	}
	terminal(t, f.call("POST", f.cardPath()+"/link-remove", f.token, f.wire(t, f.card, 4, 2, map[string]any{"linkId": linkID}), nil), 200)
	// Synthetic inherited history keeps the original incarnation/time/actor.
	sourceInc := "20000000-0000-0000-0000-000000000009"
	l, e := f.store.Writer(nc(t))
	if e != nil {
		t.Fatal("source fixture checkout")
	}
	e = l.Do(nc(t), func(c *sql.Conn) error {
		_, e := c.ExecContext(nc(t), `INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) SELECT incarnation,origin_board,?,card_id,card_revision,actor_id,'2020-01-02T03:04:05Z',state FROM card_revision WHERE incarnation=? AND card_id=? AND card_revision=1`, sourceInc, nativeInc, f.card)
		return e
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal("source fixture insert")
	}
	w := f.call("GET", f.cardPath()+"/history", f.token, nil, nil)
	h, e := T.Decode[T.HistoryRead](w.Body.Bytes())
	if e != nil || w.Code != 200 || len(h.Result.Records) != 3 || len(h.Result.Links) != 1 || h.Result.CardRevision != 3 {
		t.Fatal("history full exact references")
	}
	last := h.Result.Records[2]
	if string(last.Origin.Incarnation) != sourceInc || last.CommittedAt != "2020-01-02T03:04:05Z" || last.ActorID != nativeActor {
		t.Fatal("source provenance replaced")
	}
	through := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	req, _ := json.Marshal(T.PreviewRequest{Incarnation: nativeInc, ExpectedRevision: 5, Command: "history.clear", Input: json.RawMessage(`{"throughTime":"` + through + `"}`)})
	w = f.call("POST", f.path()+"/recovery/preview", f.token, req, nil)
	p, e := T.Decode[T.Preview](w.Body.Bytes())
	if e != nil || w.Code != 200 {
		t.Fatal("history preview")
	}
	confirm := f.wire(t, "", 5, 0, map[string]any{"throughTime": through})
	var m map[string]any
	json.Unmarshal(confirm, &m)
	m["command"] = "history.clear"
	m["previewHash"] = p.PreviewHash
	confirm, _ = json.Marshal(m)
	clear := terminal(t, f.call("POST", f.path()+"/recovery/confirm", f.token, confirm, nil), 200)
	if clear.Outcome != "committed" {
		t.Fatal("clear")
	}
	w = f.call("GET", f.cardPath()+"/history", f.token, nil, nil)
	h, e = T.Decode[T.HistoryRead](w.Body.Bytes())
	if e != nil || w.Code != 200 || len(h.Result.Links) != 0 || len(h.Result.Tombstones) != 1 || h.Result.Tombstones[0].ID != A.UUID(linkID) {
		t.Fatal("removed historical link tombstone")
	}
	if h.Result.Records[2].Origin.Incarnation != A.UUID(sourceInc) || h.Result.Records[2].CommittedAt != last.CommittedAt {
		t.Fatal("compaction provenance")
	}
	// A new identity with missing preview becomes a retained refusal; it is not
	// an adapter-generated claim of noncommit. Expired cache works the same way.
	m["opId"] = "50000000-0000-0000-0000-000000000099"
	m["expectedRevision"] = 6
	m["previewHash"] = strings.Repeat("a", 64)
	missing, _ := json.Marshal(m)
	no := terminal(t, f.call("POST", f.path()+"/recovery/confirm", f.token, missing, nil), 200)
	if no.Outcome != "not_committed" || no.Code != "PREVIEW_UNAVAILABLE" {
		t.Fatal("missing preview admission")
	}
	// Same valid op at a different source incarnation cannot resolve current
	// receipt or execute a new effect under a silently rewritten identity.
	lookup := f.path() + "/operations/" + string(clear.OpID) + "?incarnation=" + sourceInc + "&fingerprintVersion=1&fingerprint=" + string(clear.Fingerprint)
	w = f.call("GET", lookup, f.token, nil, nil)
	u, e := T.Decode[T.KnownIdentityUnknown](w.Body.Bytes())
	if e != nil || w.Code != 200 || u.Incarnation != A.UUID(sourceInc) || u.Error.Code != "SOURCE_AUTHORITY_UNAVAILABLE" {
		t.Fatal("original source lookup")
	}
}

func TestNativeBoundaryBrowserAndRevocation(t *testing.T) {
	f := freshNative(t)
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"host", func(r *http.Request) { r.Host = "wrong.localhost" }, 400}, {"TLS", func(r *http.Request) { r.TLS = nil }, 400}, {"origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"encoded", func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }, 400}, {"body", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("x")) }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := f.call("GET", f.path(), f.token, nil, tc.change); w.Code != tc.status {
				t.Fatal("boundary")
			}
		})
	}
	if f.call("GET", f.path(), f.owner, nil, nil).Code == 200 {
		t.Fatal("native owner read escalation")
	}
	if f.call("GET", f.path(), "", nil, nil).Code != 401 {
		t.Fatal("unauthenticated")
	}
	code, e := f.sessions.IssueCode(nc(t), f.owner, T.CodeRequest{InstallationID: T.UUIDv4(f.native.Context().InstallationID), BoardID: nativeBoard, Incarnation: nativeInc, GrantID: A.UUID(f.grant)})
	if e != nil {
		t.Fatal("browser code")
	}
	browser, e := f.sessions.Exchange(nc(t), code.Code)
	if e != nil {
		t.Fatal("browser exchange")
	}
	if f.call("GET", f.path(), browser.Token, nil, nil).Code != 403 {
		t.Fatal("browser fetch requirement")
	}
	if f.call("GET", f.path(), browser.Token, nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }).Code != 200 {
		t.Fatal("browser safe current read")
	}
	b := f.wire(t, f.card, 2, 1, map[string]any{"description": "Browser"})
	if f.call("PATCH", f.cardPath(), browser.Token, b, nil).Code != 403 {
		t.Fatal("browser write Origin")
	}
	terminal(t, f.call("PATCH", f.cardPath(), browser.Token, b, func(r *http.Request) { r.Header.Set("Origin", f.native.Context().Origin) }), 200)
	if f.sessions.Revoke(nc(t), "browser", browser.Token) != nil {
		t.Fatal("revoke")
	}
	if f.call("GET", f.path(), browser.Token, nil, func(r *http.Request) { r.Header.Set("Origin", f.native.Context().Origin) }).Code != 401 {
		t.Fatal("revoked browser")
	}
}

func TestNativePinnedHTTPSBoardRoundTrip(t *testing.T) {
	f := freshNative(t)
	server, e := H.Listen(nc(t), f.native, f.handler)
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
	req, e := http.NewRequest("GET", f.native.Context().Origin+f.path(), nil)
	if e != nil {
		t.Fatal("TLS request")
	}
	req.Header.Set("Authorization", "Bearer "+string(f.token))
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
	if e != nil || response.StatusCode != 200 {
		t.Fatal("TLS body")
	}
	view, e := T.Decode[T.BoardRead](b)
	if e != nil || view.Result.Identity.BoardID != nativeBoard || len(view.Result.Cards) != 1 {
		t.Fatal("TLS DTO")
	}
}

func (f *nativeFixture) issueGrant(t *testing.T, revision uint64, kind string, rights []string) (T.SecretToken, T.Terminal) {
	t.Helper()
	i, e := T.FreezeMutation(f.wire(t, "", revision, 0, map[string]any{"granteeActorId": "30000000-0000-0000-0000-000000000009", "kind": kind, "rights": rights}), contract.ResolvedRoute{Command: "grant.create", BoardID: nativeBoard}, contract.AuthenticatedActor{ActorID: f.native.Context().OwnerActorID})
	if e != nil {
		t.Fatal("restricted grant intent")
	}
	out := f.native.SubmitNative(nc(t), f.owner, i, grants.Route{BoardID: nativeBoard, Incarnation: nativeInc})
	if out.Result == nil || out.Result.Terminal == nil || out.Result.Terminal.Outcome != "committed" {
		t.Fatal("restricted grant")
	}
	id := ""
	for _, ref := range out.Result.Terminal.ResultRefs {
		if ref.Kind == "grant" {
			id = string(ref.ID)
		}
	}
	issued := f.native.Issue(nc(t), f.owner, T.CredentialIssueRequest{InstallationID: T.UUIDv4(f.native.Context().InstallationID), BoardID: nativeBoard, Incarnation: nativeInc, GrantID: A.UUID(id)})
	b, e := issued.MarshalEphemeral()
	if e != nil {
		t.Fatal("restricted credential")
	}
	secret, e := T.Decode[T.CredentialSecret](b)
	if e != nil {
		t.Fatal("restricted credential shape")
	}
	return secret.Token, *out.Result.Terminal
}

func TestNativeCurrentRightsAndNativeLookupScope(t *testing.T) {
	f := freshNative(t)
	token, grant := f.issueGrant(t, 2, "automation", []string{"comment", "read-own-outcome"})
	if f.call("GET", f.path(), token, nil, nil).Code != 403 {
		t.Fatal("absent read right")
	}
	path := f.path() + "/operations/" + string(grant.OpID) + "?incarnation=" + nativeInc + "&fingerprintVersion=1&fingerprint=" + string(grant.Fingerprint)
	own := terminal(t, f.call("GET", path, f.owner, nil, nil), 200)
	if own.Fingerprint != grant.Fingerprint {
		t.Fatal("native own grant lookup")
	}
	if f.call("GET", path, token, nil, nil).Code != 409 {
		t.Fatal("another actor's grant outcome")
	}
	comment := terminal(t, f.call("POST", f.cardPath()+"/comments", token, f.wire(t, f.card, 3, 1, map[string]any{"text": "Automation comment"}), nil), 201)
	if comment.ActorID != "30000000-0000-0000-0000-000000000009" {
		t.Fatal("actor not credential derived")
	}
	path = f.path() + "/operations/" + string(comment.OpID) + "?incarnation=" + nativeInc + "&fingerprintVersion=1&fingerprint=" + string(comment.Fingerprint)
	if f.call("GET", path, f.owner, nil, nil).Code != 403 {
		t.Fatal("native owner domain lookup escalation")
	}
	no := terminal(t, f.call("POST", f.cardPath()+"/approve", token, f.wire(t, f.card, 4, 2, map[string]any{"policyVersion": 1}), nil), 200)
	if no.Outcome != "not_committed" || no.Code != "FORBIDDEN" {
		t.Fatal("automation approval rights")
	}
	var grantID string
	for _, ref := range grant.ResultRefs {
		if ref.Kind == "grant" {
			grantID = string(ref.ID)
		}
	}
	revoke := f.wire(t, "", 4, 0, map[string]any{})
	var m map[string]any
	json.Unmarshal(revoke, &m)
	m["boardId"] = nativeBoard
	m["target"] = map[string]string{"grantId": grantID}
	revoke, _ = json.Marshal(m)
	i, e := T.FreezeMutation(revoke, contract.ResolvedRoute{Command: "grant.revoke", BoardID: nativeBoard, TargetID: grantID}, contract.AuthenticatedActor{ActorID: f.native.Context().OwnerActorID})
	if e != nil {
		t.Fatal("revoke intent")
	}
	out := f.native.SubmitNative(nc(t), f.owner, i, grants.Route{BoardID: nativeBoard, Incarnation: nativeInc, GrantID: grantID})
	if out.Result == nil || out.Result.Terminal == nil || out.Result.Terminal.Outcome != "committed" {
		t.Fatal("grant revocation")
	}
	if f.call("GET", f.path(), token, nil, func(r *http.Request) { r.Header.Set("Origin", f.native.Context().Origin) }).Code != 401 {
		t.Fatal("revoked current credential")
	}
}

func TestNativeMalformedHistoryFailsWholeRead(t *testing.T) {
	f := freshNative(t)
	terminal(t, f.call("PATCH", f.cardPath(), f.token, f.wire(t, f.card, 2, 1, map[string]any{"description": "Next"}), nil), 200)
	l, e := f.store.Writer(nc(t))
	if e != nil {
		t.Fatal("damaged fixture checkout")
	}
	e = l.Do(nc(t), func(c *sql.Conn) error {
		_, e := c.ExecContext(nc(t), `INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) SELECT incarnation,origin_board,'20000000-0000-0000-0000-000000000009',card_id,card_revision,actor_id,committed_at,'{}' FROM card_revision WHERE incarnation=? AND card_id=?`, nativeInc, f.card)
		return e
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal("damaged fixture insert")
	}
	w := f.call("GET", f.cardPath()+"/history", f.token, nil, nil)
	ref, e := T.Decode[T.NonterminalRefusal](w.Body.Bytes())
	if e != nil || w.Code != 503 || ref.Error.Code != "UNAVAILABLE" || strings.Contains(w.Body.String(), "records") {
		t.Fatal("damaged history fabricated partial result")
	}
}
