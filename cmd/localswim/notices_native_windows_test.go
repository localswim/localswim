//go:build windows && amd64 && l02_native && m11_cli_native && m12_cli_native

package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	P "github.com/localswim/localswim/internal/contract/noticetext"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/local"
	"github.com/localswim/localswim/internal/workspace"
)

func TestNoticesActualBuiltCLI(t *testing.T) {
	base, binary := os.Getenv("M12_CLI_ROOT"), os.Getenv("M12_CLI_BINARY")
	if !safeAbsolute(base) || !safeAbsolute(binary) {
		t.Fatal("explicit private synthetic root and built executable required")
	}
	root, err := os.MkdirTemp(base, "c1-")
	if err != nil {
		t.Fatal("private synthetic root unavailable")
	}
	t.Cleanup(func() {
		if os.RemoveAll(root) != nil {
			t.Error("synthetic data cleanup failed")
		}
	})
	p := activityCLIProof{t, binary, root}
	stop := func(r interface{ Close() error }) {
		if r.Close() != nil {
			t.Error("synthetic host did not drain")
		}
	}
	read := func(prefix []string, info local.ClientInfo, action string, flags ...string) T.NoticeView {
		t.Helper()
		wire := p.call(append(append([]string(nil), prefix...), "--json"), nil, 0, "", append([]string{"notices", action}, flags...)...)
		view, err := T.Decode[T.NoticeResponse](wire)
		if err != nil || view.Result.Identity != (A.Identity{BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation)}) {
			t.Fatal("notice result lost selected identity")
		}
		canonical, err := A.Canonical(view)
		if err != nil || !bytes.Equal(wire, canonical) {
			t.Fatal("actual CLI --json notice output is not exact compact D02 C")
		}
		checkNoticeRedaction(t, wire, root, info)
		return view.Result
	}
	data := filepath.Join(root, "s")
	standalone, err := local.Start(t.Context(), local.Options{DataDir: data, Assets: http.NotFoundHandler()})
	if err != nil {
		t.Fatal("default-port standalone runtime unavailable")
	}
	defer func() {
		if standalone != nil {
			stop(standalone)
		}
	}()
	info := activityReadFixture(t, data)
	args := []string{"--data", data}
	if info.Port != 8792 || read(args, info, "status").State != "unconfigured" {
		t.Fatal("default standalone status invalid")
	}
	target := read(args, info, "configure").TargetID
	if target == nil || !reflect.DeepEqual(read(args, info, "configure").TargetID, target) ||
		read(args, info, "start").State != "running" || read(args, info, "stop").State != "stopped" {
		t.Fatal("fixed target or clean controls failed")
	}

	httpRequest := func(method, path string, wire []byte, cookie *http.Cookie) ([]byte, []*http.Cookie) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, info.Origin+path, bytes.NewReader(wire))
		if err != nil {
			t.Fatal("synthetic request unavailable")
		}
		if wire != nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", info.Origin)
			req.Header.Set("X-Localswim-Client", "browser")
		} else {
			req.Header.Set("Authorization", "Bearer "+string(info.Token))
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		transport := &http.Transport{Proxy: nil}
		defer transport.CloseIdleConnections()
		res, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal("authoritative HTTP request unavailable")
		}
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, T.NoticeResponseLimit+1))
		if err != nil || (res.StatusCode != 200 && res.StatusCode != 201) {
			t.Fatalf("authoritative HTTP response invalid, status %d", res.StatusCode)
		}
		return body, res.Cookies()
	}
	_, cookies := httpRequest("POST", "/api/v1/local/bootstrap", []byte("{}"), nil)
	if len(cookies) != 1 {
		t.Fatal("synthetic human session unavailable")
	}
	cards := "/api/v1/boards/" + info.BoardID + "/cards"
	wire, _ := httpRequest("POST", cards, bridgeCreateWire(t, info, 0, activityPrivateTitle), cookies[0])
	if committed, err := T.Decode[T.Terminal](wire); err != nil || committed.Outcome != "committed" {
		t.Fatal("synthetic downtime event did not commit")
	}
	stop(standalone)
	standalone, err = local.Start(t.Context(), local.Options{DataDir: data, Assets: http.NotFoundHandler()})
	if err != nil {
		t.Fatal("ordinary standalone reopen unavailable")
	}
	info = activityReadFixture(t, data)
	if reopened := read(args, info, "start"); reopened.State != "running" || !reflect.DeepEqual(reopened.TargetID, target) {
		t.Fatal("reopen did not use its fixed target")
	}
	until := time.Now().Add(12 * time.Second)
	var inbox T.NoticeView
	for {
		inbox = read(args, info, "inbox")
		if len(inbox.Entries) == 1 && inbox.Pending == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("exact catch-up receipt did not arrive")
		}
		time.Sleep(80 * time.Millisecond)
	}
	pair := inbox.Entries[0]
	if pair.Class != "human" || pair.TargetID != *target || pair.LowerEvent != 0 || pair.UpperEvent == nil ||
		*pair.UpperEvent != 1 || pair.EventCount == nil || *pair.EventCount != 1 {
		t.Fatal("catch-up receipt did not identify the downtime event")
	}
	if pair.Payload == nil || pair.Payload.Kind != "complete" || P.Validate(*pair.Payload) != nil ||
		pair.Payload.Facts.Label != "Board "+info.BoardID || len(pair.Payload.Facts.Tickets) != 1 || pair.Payload.Facts.Tickets[0] != 1 {
		t.Fatal("actual CLI inbox lost the original rich captured payload")
	}
	originalPayload := P.Clone(pair.Payload)
	text := func(prefix []string, pair T.NoticePair, want int, code string) []byte {
		t.Helper()
		got := p.call(prefix, nil, want, code, "notices", "text", "--target", string(pair.TargetID), "--delivery", string(pair.DeliveryID))
		if want == 0 && (pair.Payload == nil || !bytes.Equal(got, []byte(pair.Payload.Text))) || want != 0 && len(got) != 0 {
			t.Fatal("raw text output changed original bytes or leaked on refusal")
		}
		return got
	}
	text(args, pair, 0, "")
	if got := p.call(append(append([]string(nil), args...), "--json"), nil, 1, "INVOCATION_OR_IO", "notices", "text", "--target", string(pair.TargetID), "--delivery", string(pair.DeliveryID)); len(got) != 0 {
		t.Fatal("incompatible JSON contaminated raw stdout")
	}
	// A later current-board mutation cannot alter the retained original.
	_, cookies = httpRequest("POST", "/api/v1/local/bootstrap", []byte("{}"), nil)
	httpRequest("POST", cards, bridgeCreateWire(t, info, 1, "Different current card"), cookies[0])
	text(args, pair, 0, "")
	if read(args, info, "stop").State != "stopped" {
		t.Fatal("reopened monitor did not stop cleanly")
	}
	// Reopen the actual host; raw text still exposes the saved original.
	stop(standalone)
	standalone, err = local.Start(t.Context(), local.Options{DataDir: data, Assets: http.NotFoundHandler()})
	if err != nil {
		t.Fatal("original reopen failed")
	}
	info = activityReadFixture(t, data)
	text(args, pair, 0, "")
	inbox = read(args, info, "inbox")
	hostWire, _ := httpRequest("GET", "/api/v1/boards/"+info.BoardID+"/notices/inbox?incarnation="+url.QueryEscape(info.Incarnation), nil, nil)
	host, err := T.Decode[T.NoticeResponse](hostWire)
	if err != nil || !reflect.DeepEqual(host.Result, inbox) {
		t.Fatal("CLI inbox differs from authoritative host records")
	}
	if len(inbox.Entries) != 1 || !P.Equal(inbox.Entries[0].Payload, originalPayload) {
		t.Fatal("actual CLI repeated read changed the original captured payload")
	}
	flags := []string{"--target-id", string(pair.TargetID), "--delivery-id", string(pair.DeliveryID)}
	lookup := read(args, info, "lookup", flags...)
	if lookup.Lookup == nil || lookup.Lookup.State != "accepted" || !reflect.DeepEqual(lookup.Lookup.Receipt, &pair) {
		t.Fatal("lookup did not identify the exact accepted receipt")
	}
	if !P.Equal(lookup.Lookup.Receipt.Payload, originalPayload) {
		t.Fatal("actual CLI lookup changed original rich payload")
	}
	readable := p.call(args, nil, 0, "", append([]string{"notices", "lookup"}, flags...)...)
	checkNoticeRedaction(t, readable, root, info)
	for _, want := range []string{info.BoardID + "/" + info.Incarnation, "Lookup: accepted", string(pair.TargetID), string(pair.DeliveryID), string(pair.AcceptedAt), "human, events (0, 1], count 1"} {
		if !bytes.Contains(readable, []byte(want)) {
			t.Fatal("readable lookup differs from JSON receipt")
		}
	}

	ws, project := filepath.Join(root, "w"), filepath.Join(root, "p")
	w, err := workspace.Start(t.Context(), workspace.Options{DataDir: ws, Port: bridgePort(t), Assets: http.NotFoundHandler()})
	if err != nil {
		t.Fatal("configured-port workspace runtime unavailable")
	}
	defer stop(w)
	workspaceArgs := []string{"--workspace", ws}
	projectArgs := append(append([]string(nil), workspaceArgs...), "--project", "demo")
	member := filepath.Join(project, "b")
	p.call(workspaceArgs, nil, 0, "", "project", "create", "--key", "demo", "--label", "Synthetic", "--root", project)
	p.call(projectArgs, nil, 0, "", "board", "create", "--key", "board", "--label", "Synthetic", "--root", member)
	memberInfo := activityReadFixture(t, member)
	memberArgs := append(append([]string(nil), projectArgs...), "--board", "board")
	if read(memberArgs, memberInfo, "status").State != "unconfigured" {
		t.Fatal("workspace CLI did not resolve fresh member")
	}
	configured := read(memberArgs, memberInfo, "configure")
	if configured.TargetID == nil || *configured.TargetID == *target ||
		read(memberArgs, memberInfo, "start").State != "running" ||
		len(read(memberArgs, memberInfo, "inbox").Entries) != 0 {
		t.Fatal("workspace control or inbox crossed scope")
	}
	text(memberArgs, pair, 3, "UNAVAILABLE")
	// Capture a human interval through the configured workspace member.
	savedInfo := info
	info = memberInfo
	bootstrapWire, _ := T.Readable(T.BoardBootstrapRequest{APIVersion: 1, InstallationID: T.UUIDv4(info.InstallationID), Incarnation: A.UUID(info.Incarnation)})
	_, memberCookies := httpRequest("POST", "/api/v1/boards/"+info.BoardID+"/local/bootstrap", bootstrapWire, nil)
	httpRequest("POST", "/api/v1/boards/"+info.BoardID+"/cards", bridgeCreateWire(t, info, 0, "Configured current card"), memberCookies[0])
	info = savedInfo
	deadline := time.Now().Add(12 * time.Second)
	var memberInbox T.NoticeView
	for {
		memberInbox = read(memberArgs, memberInfo, "inbox")
		if len(memberInbox.Entries) > 0 && memberInbox.Pending == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("configured notice unavailable")
		}
		time.Sleep(80 * time.Millisecond)
	}
	if memberInbox.Entries[0].Payload == nil || memberInbox.Entries[0].Payload.Facts == nil || memberInbox.Entries[0].Payload.Facts.Label != "Synthetic" {
		t.Fatal("configured original label missing")
	}
	text(memberArgs, memberInbox.Entries[0], 0, "")
	foreign := read(memberArgs, memberInfo, "lookup", flags...)
	if foreign.Lookup == nil || foreign.Lookup.State != "unknown" || foreign.Lookup.Receipt != nil ||
		read(memberArgs, memberInfo, "stop").State != "stopped" {
		t.Fatal("workspace exposed a foreign receipt or did not stop")
	}
	// Register the same stopped physical member into a fresh configured
	// workspace with a renamed label using existing product config operations.
	// No current-config rename API exists; do not edit private catalog files.
	memberOriginal := memberInbox.Entries[0]
	stop(w)
	renamedWS := filepath.Join(root, "renamed-workspace")
	renamedHost, renameErr := workspace.Start(t.Context(), workspace.Options{DataDir: renamedWS, Port: bridgePort(t), Assets: http.NotFoundHandler()})
	if renameErr != nil {
		t.Fatal("renamed workspace unavailable")
	}
	defer stop(renamedHost)
	p.call([]string{"--workspace", renamedWS}, nil, 0, "", "project", "create", "--key", "demo", "--label", "Renamed project", "--root", project)
	p.call([]string{"--workspace", renamedWS, "--project", "demo"}, nil, 0, "", "board", "register", "--key", "board", "--label", "Renamed current board", "--root", member)
	renamedArgs := []string{"--workspace", renamedWS, "--project", "demo", "--board", "board"}
	renameDeadline := time.Now().Add(10 * time.Second)
	for {
		current, e := local.ReadClient(member)
		if e == nil && current.Origin == renamedHost.Origin() {
			break
		}
		if time.Now().After(renameDeadline) {
			t.Fatal("renamed registered member did not activate")
		}
		time.Sleep(80 * time.Millisecond)
	}
	text(renamedArgs, memberOriginal, 0, "")
	renamedInfo := activityReadFixture(t, member)
	renamedInbox := read(renamedArgs, renamedInfo, "inbox")
	if len(renamedInbox.Entries) != 1 || !P.Equal(renamedInbox.Entries[0].Payload, memberOriginal.Payload) {
		t.Fatal("renamed configured reopen rewrote original")
	}
	stop(standalone)
	standalone = nil
	text(args, pair, 3, "UNAVAILABLE")
	refusal := p.call(append(args, "--json"), nil, 3, "UNAVAILABLE", "notices", "inbox")
	if v, err := T.Decode[T.NonterminalRefusal](refusal); err != nil || v.Error.Code != "UNAVAILABLE" {
		t.Fatal("closed host did not yield bounded refusal")
	}
	refusal = p.call(append(args, "--json"), nil, 3, "UNAVAILABLE", "notices", "recover")
	if v, err := T.Decode[T.NonterminalRefusal](refusal); err != nil || v.Error.Code != "UNAVAILABLE" {
		t.Fatal("recovery fell back to closed host's local state")
	}
	activityModifyStoppedFixture(t, data, info, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", info.Incarnation, info.GrantID)
	standalone, err = local.Start(t.Context(), local.Options{DataDir: data, Assets: http.NotFoundHandler()})
	if err != nil {
		t.Fatal("authority fixture reopen unavailable")
	}
	text(args, pair, 2, "FORBIDDEN")
	refusal = p.call(append(args, "--json"), nil, 2, "FORBIDDEN", "notices", "configure")
	if v, err := T.Decode[T.NonterminalRefusal](refusal); err != nil || v.Error.Code != "FORBIDDEN" {
		t.Fatal("denied authority did not yield bounded refusal")
	}
	refusal = p.call(append(args, "--json"), nil, 2, "FORBIDDEN", "notices", "recover")
	if v, err := T.Decode[T.NonterminalRefusal](refusal); err != nil || v.Error.Code != "FORBIDDEN" {
		t.Fatal("denied recovery did not yield bounded refusal")
	}
	t.Log("C1 built CLI: all six actions, default standalone and configured workspace selection, clean reopen catch-up, exact host receipt parity, foreign lookup, authority refusal, no stopped-host fallback, readable and JSON redaction")
	t.Run("stale-control", func(t *testing.T) {
		p.t = t
		testNoticeCLIControlBoundary(t, p, "start", "stale")
	})
}

func TestNoticeRecoverActualBuiltCLIBoundary(t *testing.T) {
	base, binary := os.Getenv("M12_CLI_ROOT"), os.Getenv("M12_CLI_BINARY")
	if !safeAbsolute(base) || !safeAbsolute(binary) {
		t.Fatal("explicit private synthetic root and built executable required")
	}
	root, err := os.MkdirTemp(base, "r1-")
	if err != nil {
		t.Fatal("private synthetic root unavailable")
	}
	t.Cleanup(func() {
		if os.RemoveAll(root) != nil {
			t.Error("synthetic data cleanup failed")
		}
	})
	for _, mode := range []string{"stale", "lost-after-admission", "wrong-scope"} {
		t.Run(mode, func(t *testing.T) {
			testNoticeCLIControlBoundary(t, activityCLIProof{t, binary, root}, "recover", mode)
		})
	}
}

func testNoticeCLIControlBoundary(t *testing.T, p activityCLIProof, action, scenario string) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("controlled loopback listener unavailable")
	}
	defer listener.Close()
	data := filepath.Join(p.root, "g-"+scenario)
	workspaceID := bridgeUUID(t)
	engine, err := local.OpenEngine(t.Context(), local.EngineOptions{DataDir: data, Origin: "http://" + listener.Addr().String(), WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal("controlled synthetic engine unavailable")
	}
	defer engine.Close()
	info := activityReadFixture(t, data)
	mode, err := T.Readable(T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: T.UUIDv4(workspaceID)})
	if err != nil {
		t.Fatal("controlled mode response unavailable")
	}
	path := "/api/v1/boards/" + info.BoardID + "/notices/"
	var statusCalls, actionCalls, admittedStatus atomic.Int32
	var wrongScope atomic.Bool
	foreignBoard := A.UUID(bridgeUUID(t))
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v1/runtime-mode":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(mode)
			return
		case path + "status":
			statusCalls.Add(1)
			if wrongScope.Load() {
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, req)
				view, err := T.Decode[T.NoticeResponse](recorder.Body.Bytes())
				view.Result.Identity.BoardID = foreignBoard
				wire, encodeErr := T.Readable(view)
				if err != nil || encodeErr != nil || recorder.Code != http.StatusOK {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(wire)
				admittedStatus.Store(http.StatusOK)
				return
			}
		case path + action:
			if actionCalls.Add(1) == 1 {
				if scenario == "lost-after-admission" {
					recorder := httptest.NewRecorder()
					engine.ServeHTTP(recorder, req)
					admittedStatus.Store(int32(recorder.Code))
					conn, _, err := http.NewResponseController(w).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				body, err := io.ReadAll(io.LimitReader(req.Body, T.NoticeRequestLimit+1))
				if err != nil {
					w.WriteHeader(500)
					return
				}
				// Admit a competing Start using the current generation before
				// handing the CLI request to the same real engine.
				competing := req.Clone(req.Context())
				competing.URL.Path = path + "start"
				competing.Body = io.NopCloser(bytes.NewReader(body))
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, competing)
				admittedStatus.Store(int32(recorder.Code))
				req.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
		engine.ServeHTTP(w, req)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		if server.Close() != nil || <-done != http.ErrServerClosed {
			t.Error("controlled loopback did not close")
		}
	}()
	args := []string{"--data", data, "--json"}
	p.call(args, nil, 0, "", "notices", "configure")
	statusCalls.Store(0)
	if scenario == "wrong-scope" {
		wrongScope.Store(true)
		wire := p.call(args, nil, 3, "UNAVAILABLE", "notices", action)
		refusal, err := T.Decode[T.NonterminalRefusal](wire)
		if err != nil || refusal.Error.Code != "UNAVAILABLE" || actionCalls.Load() != 0 ||
			statusCalls.Load() != 1 || admittedStatus.Load() != http.StatusOK ||
			bytes.Contains(wire, []byte(foreignBoard)) {
			t.Fatal("wrong selected-board status was not rejected without a submission")
		}
		t.Log("C1 controlled wrong-scope status: one status, zero submissions")
		return
	}
	if scenario == "lost-after-admission" {
		wire := p.call(args, nil, 4, "OUTCOME_UNKNOWN", "notices", action)
		unknown, err := T.Decode[T.NoticeOutcomeUnknown](wire)
		if err != nil || unknown.Outcome != "unknown" || unknown.Error.Code != "OUTCOME_UNKNOWN" ||
			!strings.Contains(unknown.Error.Message, "Read notices status") {
			t.Fatal("CLI did not retain uncertainty after admitted action lost its response")
		}
		checkNoticeRedaction(t, wire, p.root, info)
	} else {
		wire := p.call(args, nil, 2, "CONTROL_STALE", "notices", action)
		refusal, err := T.Decode[T.NonterminalRefusal](wire)
		if err != nil || refusal.Error.Code != "CONTROL_STALE" {
			t.Fatal("real admission did not refuse the stale CLI generation")
		}
	}
	if admittedStatus.Load() != 200 || actionCalls.Load() != 1 || statusCalls.Load() != 1 {
		t.Fatal("controlled admission, status count or single POST invariant failed")
	}
	wire := p.call(args, nil, 0, "", "notices", "status")
	status, err := T.Decode[T.NoticeResponse](wire)
	want := T.NoticePhase("running")
	if scenario == "lost-after-admission" {
		want = "stopped"
	}
	if err != nil || status.Result.State != want {
		t.Fatal("deliberate status did not confirm the authoritative state")
	}
	p.call(args, nil, 0, "", "notices", "stop")
	t.Log("C1 controlled timing: one status and one submission through real engine admission, with no replay")
}

func checkNoticeRedaction(t *testing.T, wire []byte, root string, info local.ClientInfo) {
	t.Helper()
	for _, secret := range []string{root, strings.ReplaceAll(root, "\\", "\\\\"), string(info.Token), info.GrantID, activityPrivateTitle, "Fresh synthetic workspace bridge fixture"} {
		if secret != "" && bytes.Contains(wire, []byte(secret)) {
			t.Fatal("notice output disclosed private fixture material")
		}
	}
}
