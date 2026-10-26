//go:build windows && amd64 && k03adminqualification

package admin

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/localswim/localswim/internal/board/credentials"
	G "github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/boardapi"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/grantapi"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

type noTrust struct{}

func (noTrust) Membership([]byte) (bool, error) { return false, nil }
func (noTrust) Enroll([]byte) error             { return fmt.Errorf("synthetic no trust mutation") }
func (noTrust) Remove([]byte) error             { return fmt.Errorf("synthetic no trust mutation") }

type recordedEvidence struct {
	evidence.Store
	last  string
	saves int
}

func (s *recordedEvidence) Save(r evidence.Record) (string, error) {
	p, e := s.Store.Save(r)
	if e == nil {
		s.last = p
		s.saves++
	}
	return p, e
}

func nativePrivateDirectory(t *testing.T, path string) {
	t.Helper()
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal("native user unavailable")
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal("private descriptor unavailable")
	}
	if windows.CreateDirectory(windows.StringToUTF16Ptr(path), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}) != nil {
		t.Fatal("private directory unavailable")
	}
}

func TestNativeAdminProtectedCredentialTLSAndRecoveryJourney(t *testing.T) {
	base := os.Getenv("K03_ADMIN_TEST_BASE")
	if !filepath.IsAbs(base) {
		t.Fatal("explicit unique private runtime required")
	}
	root, e := os.MkdirTemp(base, "admin-journey-")
	if e != nil {
		t.Fatal("synthetic runtime unavailable")
	}
	setupRoot := filepath.Join(root, "installation")
	t.Setenv("K03_ADMIN_TEST_ROOT", setupRoot)
	// This symbol exists only in the private root-only compile overlay. Without
	// the overlay this tagged suite cannot build or touch production LocalAppData.
	if setup.K03AdminQualificationRoot() != setupRoot {
		t.Fatal("compiled root-only overlay required")
	}
	m, e := setup.New(noTrust{})
	if e != nil || m.Init(nil).Exit != 0 {
		t.Fatal("synthetic setup unavailable")
	}
	local, e := m.ReadContext()
	if e != nil {
		t.Fatal("synthetic context unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	boardRoot := filepath.Join(root, "board")
	nativePrivateDirectory(t, boardRoot)
	db, e := S.Create(ctx, boardRoot, schema.Identity{BoardID: boardID, Incarnation: incID})
	if e != nil {
		t.Fatal("synthetic board unavailable")
	}
	defer db.Close()
	n, e := credentials.SourceRoot(db)
	if e != nil {
		t.Fatal("native source unavailable")
	}
	defer n.Close()
	sessions, e := session.New(db, n)
	if e != nil {
		t.Fatal("sessions unavailable")
	}
	defer sessions.Close()
	board, e := boardapi.New(db, n, sessions)
	if e != nil {
		t.Fatal("board adapter unavailable")
	}
	grants, e := grantapi.New(db, n, sessions)
	if e != nil {
		t.Fatal("grant adapter unavailable")
	}
	sessionHTTP, e := H.New(n, sessions)
	if e != nil {
		t.Fatal("session adapter unavailable")
	}
	var lose atomic.Bool
	var raceRevoke atomic.Bool
	var racingWire []byte
	var mutations atomic.Int32
	var sessionReads atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "GET" && req.URL.Path == "/api/v1/session" {
			sessionReads.Add(1)
		}
		var next http.Handler = board
		if strings.HasPrefix(req.URL.Path, "/api/v1/session") {
			next = sessionHTTP
		} else if strings.HasPrefix(req.URL.Path, "/api/v1/grants") {
			next = grants
		}
		if req.Method != "GET" {
			mutations.Add(1)
		}
		if strings.HasSuffix(req.URL.Path, "/recovery/confirm") && raceRevoke.Swap(false) {
			owner, err := m.LoadNativeOwner()
			if err != nil {
				t.Error("synthetic current owner unavailable")
				return
			}
			intent, err := T.FreezeMutation(racingWire, contract.ResolvedRoute{Command: "grant.revoke", BoardID: boardID, TargetID: selectedRaceGrant(racingWire)}, contract.AuthenticatedActor{ActorID: owner.Context.OwnerActorID})
			if err != nil {
				t.Error("synthetic revocation intent unavailable")
				return
			}
			out := n.SubmitNative(ctx, T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer ")), intent, G.Route{BoardID: boardID, Incarnation: incID, GrantID: selectedRaceGrant(racingWire)})
			if out.Result == nil || out.Result.Terminal == nil || out.Result.Terminal.Outcome != "committed" {
				t.Error("racing current-right revocation failed")
				return
			}
		}
		if req.Method != "GET" && lose.Swap(false) {
			capture := httptest.NewRecorder()
			next.ServeHTTP(capture, req)
			if capture.Code != 200 && capture.Code != 201 {
				t.Error("synthetic loss did not reach successful admission")
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		next.ServeHTTP(w, req)
	})
	server, e := H.Listen(ctx, n, handler)
	if e != nil {
		t.Fatal("exclusive dual-loopback TLS unavailable")
	}
	defer server.Close()
	store, e := evidence.NewWindowsStore(filepath.Join(root, "evidence"))
	if e != nil {
		t.Fatal("native evidence unavailable")
	}
	recorded := &recordedEvidence{Store: store}
	provider := Provider{Manager: m}
	sink := &consoleFake{}
	r := Runner{Setup: m, Manager: m, Provider: provider, Owner: provider, Evidence: recorded, terminal: func(io.Reader) bool { return true }, console: func(io.Reader) (codeConsole, error) { return sink, nil }}
	var output, diagnostic bytes.Buffer
	var secretFragments []string
	run := func(args []string, line string, want int) []byte {
		t.Helper()
		output.Reset()
		diagnostic.Reset()
		exit := r.Run(ctx, args, strings.NewReader(line), &output, &diagnostic)
		if exit != want {
			t.Fatalf("native administrative operation exit %d expected %d", exit, want)
		}
		for _, secret := range append(append([]string(nil), secretFragments...), sink.codes...) {
			if bytes.Contains(output.Bytes(), []byte(secret)) || bytes.Contains(diagnostic.Bytes(), []byte(secret)) {
				t.Fatal("ephemeral secret reached public output")
			}
		}
		return bytes.Clone(output.Bytes())
	}
	shortPromptRun := func(args []string, line, mode string) {
		t.Helper()
		beforeHTTP, beforeEvidence, beforeCodes := mutations.Load(), recorded.saves, len(sink.codes)
		input := &observedInput{source: strings.NewReader(line)}
		display := &promptOutput{mode: mode}
		if r.Run(ctx, args, input, io.Discard, display) != 2 || input.reads != 0 || mutations.Load() != beforeHTTP || recorded.saves != beforeEvidence || len(sink.codes) != beforeCodes {
			t.Fatal("incomplete native required prompt consumed input or reached mutation effects")
		}
	}
	nextOp := 1
	wire := func(command string, revision uint64, target any, input any) []byte {
		t.Helper()
		nextOp++
		v := map[string]any{"opId": fmt.Sprintf("10000000-0000-4000-8000-%012x", nextOp), "boardId": boardID, "incarnation": incID, "target": target, "expectedRevision": revision, "expectedCardRevision": nil, "input": input}
		if command == "history.clear" || command == "retention.set" {
			delete(v, "boardId")
		}
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal("synthetic command unavailable")
		}
		return b
	}
	rights := []string{"grant-admin", "history-admin", "read", "read-own-outcome"}
	grantWire := wire("grant.create", 0, nil, map[string]any{"granteeActorId": actorID, "kind": "human", "rights": rights})
	grantPath := inputFile(t, grantWire)
	nativeArgs := []string{"--json", "--board", boardID, "grant", "create", "--input", grantPath, "--native-owner"}
	challenge := "enroll-human " + local.InstallationID + " " + boardID + " " + incID + " " + actorID + " " + strings.Join(rights, ",") + "\n"
	shortPromptRun(nativeArgs, challenge, "zero")
	shortPromptRun(nativeArgs, challenge, "partial")
	if exit := r.Run(ctx, nativeArgs, strings.NewReader("wrong\n"), io.Discard, io.Discard); exit != 2 || recorded.saves != 0 || mutations.Load() != 0 {
		t.Fatal("human gate preceded evidence or HTTP")
	}
	terminal, e := T.Decode[T.Terminal](run(nativeArgs, challenge, 0))
	if e != nil || terminal.Outcome != "committed" {
		t.Fatal("native ledgered human grant unavailable")
	}
	selectedGrant := ""
	for _, ref := range terminal.ResultRefs {
		if ref.Kind == "grant" {
			selectedGrant = string(ref.ID)
		}
	}
	if selectedGrant == "" {
		t.Fatal("grant result identity missing")
	}
	issueArgs := []string{"--json", "--board", boardID, "credential", "issue", "--incarnation", incID, "--grant", selectedGrant}
	issueChallenge := "issue-cli " + local.InstallationID + " " + boardID + " " + incID + " " + selectedGrant + "\n"
	shortPromptRun(issueArgs, issueChallenge, "zero")
	shortPromptRun(issueArgs, issueChallenge, "partial")
	issued, e := T.Decode[T.CredentialCLI](run(issueArgs, issueChallenge, 0))
	if e != nil || !issued.Result.LocallyAvailable {
		t.Fatal("verified native credential publication unavailable")
	}
	selected := core.Selectors{InstallationID: local.InstallationID, BoardID: boardID, Incarnation: incID, GrantID: selectedGrant}
	if r.Run(ctx, issueArgs, strings.NewReader(issueChallenge), failedOutput{}, io.Discard) != 4 {
		t.Fatal("issue output loss downgraded possible publication")
	}
	c, e := core.Open(ctx, provider, selected)
	if e != nil {
		t.Fatal("real exact-file/current-session provider unavailable")
	}
	c.Close()
	material, e := m.OpenCredential(selection(selected))
	if e != nil {
		t.Fatal("protected credential audit unavailable")
	}
	if material.WithToken(func(token T.SecretToken) error { secretFragments = append(secretFragments, string(token)); return nil }) != nil {
		t.Fatal("guarded credential audit unavailable")
	}
	material.Close()
	ordinaryArgs := []string{"--json", "--installation", local.InstallationID, "--board", boardID, "--incarnation", incID, "--grant", selectedGrant}
	bad := selected
	bad.GrantID = grantID
	if _, e := core.Open(ctx, provider, bad); e == nil {
		t.Fatal("provider searched alternate grant")
	}
	if _, e := core.Open(ctx, provider, core.Selectors{BoardID: boardID}); e == nil {
		t.Fatal("provider inferred selection")
	}
	// Real browser-code issuance reaches only a synthetic in-memory console. It
	// does not touch the actual CONOUT$ device or establish human login/consent.
	codeArgs := []string{"--json", "--board", boardID, "session", "code", "--incarnation", incID, "--grant", selectedGrant}
	codeChallenge := "issue-code " + local.InstallationID + " " + boardID + " " + incID + " " + selectedGrant + "\n"
	shortPromptRun(codeArgs, codeChallenge, "zero")
	shortPromptRun(codeArgs, codeChallenge, "partial")
	codeOut := run(codeArgs, codeChallenge, 0)
	if len(sink.codes) != 1 || bytes.Contains(codeOut, []byte(sink.codes[0])) || bytes.Contains(diagnostic.Bytes(), []byte(sink.codes[0])) {
		t.Fatal("code channel isolation failed")
	}
	// Ordinary grant administration uses the same current grant-admin actor,
	// shared immutable evidence engine and actual server admission.
	automation := wire("grant.create", 1, nil, map[string]any{"granteeActorId": "00000000-0000-4000-8000-000000000abc", "kind": "automation", "rights": []string{"read"}})
	autoOut := run(append(append([]string(nil), ordinaryArgs...), "grant", "create", "--input", inputFile(t, automation)), "", 0)
	autoTerminal, e := T.Decode[T.Terminal](autoOut)
	if e != nil || autoTerminal.ActorID != A.UUID(actorID) {
		t.Fatal("ordinary current actor not bound")
	}
	// Original preview is captured once, then deliberately confirmed unchanged.
	previewReq := []byte(`{"incarnation":"` + incID + `","expectedRevision":2,"command":"retention.set","input":{"historyDays":90,"activityDays":365,"outcomeDays":180}}`)
	previewOut := run(append(append([]string(nil), ordinaryArgs...), "recovery", "preview", "--input", inputFile(t, previewReq)), "", 0)
	p, e := A.Decode[A.Preview](previewOut)
	if e != nil {
		t.Fatal("original preview unavailable")
	}
	confirmation := A.Confirmation{OpID: "10000000-0000-4000-8000-000000000050", Incarnation: A.UUID(incID), ExpectedRevision: 2, Command: "retention.set", Input: p.Input, PreviewHash: p.PreviewHash}
	confirmBytes, e := A.Readable(confirmation)
	if e != nil {
		t.Fatal("synthetic confirmation unavailable")
	}
	confirmArgs := append(append([]string(nil), ordinaryArgs...), "recovery", "confirm", "--input", inputFile(t, confirmBytes), "--preview", inputFile(t, previewOut))
	confirmChallenge := "confirm-recovery " + local.InstallationID + " " + boardID + " " + incID + " " + string(p.PreviewHash) + "\n"
	shortPromptRun(confirmArgs, confirmChallenge, "zero")
	shortPromptRun(confirmArgs, confirmChallenge, "partial")
	before := mutations.Load()
	lose.Store(true)
	unknownOut := run(confirmArgs, confirmChallenge, 4)
	if mutations.Load() != before+1 {
		t.Fatal("confirmation retried")
	}
	unknown, e := T.Decode[T.KnownIdentityUnknown](unknownOut)
	if e != nil || unknown.OpID != confirmation.OpID {
		t.Fatal("lost confirmation changed original identity")
	}
	lookupArgs := append(append([]string(nil), ordinaryArgs...), "operation", "read", string(confirmation.OpID), "--evidence", recorded.last)
	var lookup bytes.Buffer
	if (core.Runner{Provider: provider, Evidence: recorded}).Run(ctx, lookupArgs, nil, &lookup, io.Discard) != 0 {
		t.Fatal("original confirmation lookup unavailable")
	}
	resolved, e := T.Decode[T.Terminal](lookup.Bytes())
	if e != nil || resolved.Fingerprint != unknown.Fingerprint || resolved.Outcome != "committed" {
		t.Fatal("original receipt mismatch")
	}
	// Issuance response loss leaves local credentials fenced even though the
	// server replaced the bearer; deliberate NEW issue is the only recovery here.
	before = mutations.Load()
	lose.Store(true)
	run(issueArgs, issueChallenge, 4)
	if mutations.Load() != before+1 {
		t.Fatal("issue retried")
	}
	if _, e := core.Open(ctx, provider, selected); e == nil {
		t.Fatal("unknown issue reused predecessor")
	}
	run(issueArgs, issueChallenge, 0)
	// A current authenticated refusal does not broaden rights or mint another
	// grant; the enabled original credential remains independently selected.
	wrongIssue := append([]string(nil), issueArgs...)
	wrongIssue[len(wrongIssue)-1] = grantID
	run(wrongIssue, "issue-cli "+local.InstallationID+" "+boardID+" "+incID+" "+grantID+"\n", 2)
	// Self-revoke uses the loaded exact bearer and a durable local marker. Lost
	// server response is unknown; no ordinary local use or automatic retry follows.
	revokeArgs := []string{"--json", "--board", boardID, "credential", "revoke", "--incarnation", incID, "--grant", selectedGrant}
	before = mutations.Load()
	lose.Store(true)
	revokeUnknown := run(revokeArgs, "", 4)
	if mutations.Load() != before+1 {
		v, _ := T.Decode[T.CredentialCLI](revokeUnknown)
		code := "unavailable"
		if v.Error != nil {
			code = v.Error.Code
		}
		t.Fatalf("self revoke HTTP delta %d expected 1 category %s", mutations.Load()-before, code)
	}
	if _, e := core.Open(ctx, provider, selected); e == nil {
		t.Fatal("unknown revoke reopened local use")
	}
	before = mutations.Load()
	beforeReads := sessionReads.Load()
	run(revokeArgs, "", 4)
	if mutations.Load() != before || sessionReads.Load() != beforeReads {
		t.Fatal("disabled repeat revocation dispatched")
	}
	run(issueArgs, issueChallenge, 0)
	run(revokeArgs, "", 0)
	if _, e := core.Open(ctx, provider, selected); e == nil {
		t.Fatal("confirmed revoke available locally")
	}
	// A native grant lost response retains the owner actor and original evidence.
	lostGrant := wire("grant.create", 3, nil, map[string]any{"granteeActorId": "00000000-0000-4000-8000-000000000def", "kind": "automation", "rights": []string{"read"}})
	before = mutations.Load()
	lose.Store(true)
	lostOut := run([]string{"--json", "--board", boardID, "grant", "create", "--input", inputFile(t, lostGrant), "--native-owner"}, "", 4)
	if mutations.Load() != before+1 {
		t.Fatal("grant replayed")
	}
	lostIdentity, e := T.Decode[T.KnownIdentityUnknown](lostOut)
	if e != nil {
		t.Fatal("lost grant evidence unavailable")
	}
	lookup.Reset()
	if (core.Runner{Provider: provider, Evidence: recorded}).Run(ctx, []string{"--json", "--board", boardID, "operation", "read", string(lostIdentity.OpID), "--evidence", recorded.last, "--native-owner"}, nil, &lookup, io.Discard) != 0 {
		t.Fatal("narrow native original grant lookup unavailable")
	}
	lostTerminal, e := T.Decode[T.Terminal](lookup.Bytes())
	if e != nil {
		t.Fatal("lost grant terminal unavailable")
	}
	newGrant := ""
	for _, ref := range lostTerminal.ResultRefs {
		if ref.Kind == "grant" {
			newGrant = string(ref.ID)
		}
	}
	if newGrant == "" {
		t.Fatal("lost grant result reference absent")
	}
	revokeGrantWire := wire("grant.revoke", 4, map[string]string{"grantId": newGrant}, map[string]any{})
	run([]string{"--json", "--board", boardID, "grant", "revoke", newGrant, "--input", inputFile(t, revokeGrantWire), "--native-owner"}, "", 0)
	// Revoke the current actor after its GETsession preflight and original preview,
	// but before confirmation admission. No preview refresh or native fallback can
	// turn this refusal into proof about the submitted operation's outcome.
	run(issueArgs, issueChallenge, 0)
	previewReq = bytes.Replace(previewReq, []byte(`"expectedRevision":2`), []byte(`"expectedRevision":5`), 1)
	previewOut = run(append(append([]string(nil), ordinaryArgs...), "recovery", "preview", "--input", inputFile(t, previewReq)), "", 0)
	p, e = A.Decode[A.Preview](previewOut)
	if e != nil {
		t.Fatal("racing original preview unavailable")
	}
	confirmation = A.Confirmation{OpID: "10000000-0000-4000-8000-000000000051", Incarnation: A.UUID(incID), ExpectedRevision: 5, Command: "retention.set", Input: p.Input, PreviewHash: p.PreviewHash}
	confirmBytes, e = A.Readable(confirmation)
	if e != nil {
		t.Fatal("racing confirmation unavailable")
	}
	confirmArgs = append(append([]string(nil), ordinaryArgs...), "recovery", "confirm", "--input", inputFile(t, confirmBytes), "--preview", inputFile(t, previewOut))
	racingWire = wire("grant.revoke", 5, map[string]string{"grantId": selectedGrant}, map[string]any{})
	raceRevoke.Store(true)
	confirmChallenge = "confirm-recovery " + local.InstallationID + " " + boardID + " " + incID + " " + string(p.PreviewHash) + "\n"
	before = mutations.Load()
	run(confirmArgs, confirmChallenge, 4)
	if mutations.Load() != before+1 {
		t.Fatal("current-right denial refreshed or replayed confirmation")
	}
	lookup.Reset()
	lookupArgs = append(append([]string(nil), ordinaryArgs...), "operation", "read", string(confirmation.OpID), "--evidence", recorded.last)
	if (core.Runner{Provider: provider, Evidence: recorded}).Run(ctx, lookupArgs, nil, &lookup, io.Discard) != 4 {
		t.Fatal("revoked actor lookup classified original outcome")
	}
	// Ephemeral codes must never appear in any durable submission record.
	entries, e := os.ReadDir(filepath.Join(root, "evidence"))
	if e != nil {
		t.Fatal("evidence audit unavailable")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		b, e := os.ReadFile(filepath.Join(root, "evidence", entry.Name()))
		if e != nil {
			t.Fatal("evidence read unavailable")
		}
		for _, secret := range append(append([]string(nil), secretFragments...), sink.codes...) {
			if bytes.Contains(b, []byte(secret)) {
				t.Fatal("ephemeral secret leaked into evidence")
			}
		}
	}
	if recorded.saves != 6 {
		t.Fatalf("unexpected immutable submission count %d", recorded.saves)
	}
}

func selectedRaceGrant(wire []byte) string {
	v, e := contract.DecodeJSON(wire)
	if e != nil {
		return ""
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	target, ok := m["target"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := target["grantId"].(string)
	return id
}
