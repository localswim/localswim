//go:build windows && amd64 && l02_native && m11_cli_native

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	local "github.com/localswim/localswim/internal/transport/local"
	"github.com/localswim/localswim/internal/workspace"
)

const activityPrivateTitle = "M11_SYNTHETIC_PRIVATE_TITLE"

type activityCLIProof struct {
	t            *testing.T
	binary, root string
}

func (p activityCLIProof) call(prefix []string, input []byte, want int, diagnostic string, command ...string) []byte {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.t.Context(), 20*time.Second)
	defer cancel()
	args := append(append([]string(nil), prefix...), command...)
	cmd := exec.CommandContext(ctx, p.binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			p.t.Fatal("actual CLI subprocess unavailable")
		}
		if exit.ExitCode() != want {
			p.t.Fatalf("actual CLI exited %d; expected %d; diagnostic %s", exit.ExitCode(), want, activityProofDiagnostic(stderr.String()))
		}
	} else if want != 0 {
		p.t.Fatalf("actual CLI unexpectedly succeeded; expected exit %d", want)
	}
	if strings.TrimSpace(stderr.String()) != diagnostic {
		p.t.Fatalf("actual CLI diagnostic %s differs from expected %s", activityProofDiagnostic(stderr.String()), diagnostic)
	}
	if len(command) != 0 && command[0] == "activity" {
		for _, excluded := range []string{p.root, activityPrivateTitle, "Fresh synthetic workspace bridge fixture", `"affected"`, `"opId"`, `"title"`, `"description"`, `"grantId"`, `"token"`} {
			if strings.Contains(out.String(), excluded) || strings.Contains(stderr.String(), excluded) {
				p.t.Fatal("activity disclosed excluded material")
			}
		}
		if want != 0 {
			refusal, err := T.Decode[T.NonterminalRefusal](out.Bytes())
			if err != nil || refusal.Error.Code != diagnostic {
				p.t.Fatal("CLI failure was not a bounded machine refusal")
			}
		}
	}
	return out.Bytes()
}

func activityProofDiagnostic(v string) string {
	switch code := strings.TrimSpace(v); code {
	case "UNAVAILABLE", "UNAUTHENTICATED", "FORBIDDEN", "CURSOR_STALE", "CREDENTIAL_CONTEXT_MISMATCH", "CREDENTIAL_UNAVAILABLE", "INVOCATION_OR_IO":
		return code
	default:
		return "[unclassified]"
	}
}

func (p activityCLIProof) page(prefix []string, info local.ClientInfo, flags ...string) T.ActivityView {
	p.t.Helper()
	command := append([]string{"activity", "read"}, flags...)
	out := p.call(append(append([]string(nil), prefix...), "--json"), nil, 0, "", command...)
	got, err := T.Decode[T.ActivityRead](out)
	if err != nil || got.Result.Identity != (A.Identity{BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation)}) || bytes.Contains(out, []byte(info.Token)) {
		p.t.Fatal("actual CLI activity envelope did not match selected board")
	}
	return got.Result
}

func (p activityCLIProof) httpPage(info local.ClientInfo, values url.Values) T.ActivityView {
	p.t.Helper()
	values.Set("incarnation", info.Incarnation)
	req, err := http.NewRequestWithContext(p.t.Context(), http.MethodGet, info.Origin+"/api/v1/boards/"+info.BoardID+"/activity?"+values.Encode(), nil)
	if err != nil {
		p.t.Fatal("authoritative request unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+string(info.Token))
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	resp, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		p.t.Fatal("authoritative response unavailable")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, T.ActivityReadResponseLimit+1))
	got, decodeErr := T.Decode[T.ActivityRead](b)
	if err != nil || decodeErr != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		p.t.Fatal("authoritative activity response invalid")
	}
	return got.Result
}

func activityReadFixture(t *testing.T, data string) local.ClientInfo {
	t.Helper()
	info, err := local.ReadClient(data)
	if err != nil {
		t.Fatal("synthetic board rendezvous unavailable")
	}
	return info
}

func activityInventory(t *testing.T, data string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	for _, folder := range []string{"board", "cli-evidence"} {
		err := filepath.WalkDir(filepath.Join(data, folder), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			// Native exclusion files are opened without share-read; the durable
			// database, sidecars and every readable receipt remain in the snapshot.
			if d.Name() == "board.lock" || d.Name() == "evidence.lock" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = sha256.Sum256(b)
			return nil
		})
		if err != nil {
			t.Fatal("synthetic board and receipt inventory unavailable")
		}
	}
	return out
}

func activityModifyStoppedFixture(t *testing.T, data string, info local.ClientInfo, statement string, args ...any) {
	t.Helper()
	store, err := S.Existing(t.Context(), filepath.Join(data, "board"), schema.Identity{BoardID: info.BoardID, Incarnation: info.Incarnation})
	if err != nil {
		t.Fatal("exclusive stopped synthetic store unavailable")
	}
	defer func() {
		if store.Close() != nil {
			t.Error("stopped synthetic store cleanup failed")
		}
	}()
	lease, err := store.Writer(t.Context())
	if err != nil {
		t.Fatal("stopped synthetic writer unavailable")
	}
	err = lease.Do(t.Context(), func(c *sql.Conn) error {
		result, err := c.ExecContext(t.Context(), statement, args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n != 1 {
			return fmt.Errorf("expected one fixture row")
		}
		return err
	})
	closeErr := lease.Close()
	if err != nil || closeErr != nil {
		t.Fatal("stopped synthetic fixture change failed")
	}
}

func TestActivityActualBuiltCLI(t *testing.T) {
	base, binary := os.Getenv("M11_CLI_ROOT"), os.Getenv("M11_CLI_BINARY")
	if !safeAbsolute(base) || !safeAbsolute(binary) {
		t.Fatal("explicit private synthetic root and built executable required")
	}
	root, err := os.MkdirTemp(base, "a-")
	if err != nil {
		t.Fatal("private synthetic root unavailable")
	}
	t.Cleanup(func() {
		if os.RemoveAll(root) != nil {
			t.Error("synthetic data cleanup failed")
		}
	})
	p := activityCLIProof{t, binary, root}
	data := filepath.Join(root, "s")
	startLocal := func(port int) *local.Runtime {
		r, err := local.Start(t.Context(), local.Options{DataDir: data, Port: port, Assets: http.NotFoundHandler()})
		if err != nil {
			t.Fatal("ordinary standalone runtime unavailable")
		}
		return r
	}
	stop := func(r interface{ Close() error }) {
		if r.Close() != nil {
			t.Fatal("synthetic host did not drain")
		}
	}
	standalone := startLocal(bridgePort(t))
	defer func() { stop(standalone) }()
	info := activityReadFixture(t, data)
	args := []string{"--data", data}
	jsonArgs := append(append([]string(nil), args...), "--json")
	initial := activityInventory(t, data)
	if page := p.page(args, info); page.Coverage.Status != "complete" || len(page.Events) != 0 || page.NextCursor != nil {
		t.Fatal("ordinary complete empty page invalid")
	}
	if !bytes.Contains(p.call(args, nil, 0, "", "activity", "read"), []byte("No activity in this interval.")) || !reflect.DeepEqual(initial, activityInventory(t, data)) {
		t.Fatal("ordinary empty label or read conservation failed")
	}
	for revision := 0; revision < 21; revision++ {
		wire := bridgeCreateWire(t, info, revision, fmt.Sprintf("%s_%d", activityPrivateTitle, revision))
		out := p.call(jsonArgs, wire, 0, "", "card", "create", "--input", "-")
		result, err := T.Decode[T.Terminal](out)
		if err != nil || result.Outcome != "committed" {
			t.Fatal("ordinary synthetic automation commit missing")
		}
	}
	afterWrites := activityInventory(t, data)
	first := p.page(args, info)
	if len(first.Events) != 20 || first.NextCursor == nil || first.Coverage.Status != "complete" {
		t.Fatal("ordinary first page lacked committed pagination")
	}
	from, through, cursor := string(first.From), string(first.Through), *first.NextCursor
	flags := []string{"--from", from, "--through", through}
	if !reflect.DeepEqual(first, p.page(args, info, flags...)) || !reflect.DeepEqual(first, p.httpPage(info, url.Values{"from": {from}, "through": {through}})) {
		t.Fatal("exact-bound CLI first page differs from authoritative page")
	}
	readable := p.call(args, nil, 0, "", append([]string{"activity", "read"}, flags...)...)
	for _, text := range []string{info.BoardID + "/" + info.Incarnation + "/21", "card.create", "actor automation (" + info.ActorID + ")", "Next cursor: " + cursor} {
		if !bytes.Contains(readable, []byte(text)) {
			t.Fatal("readable page omitted provenance, attribution or continuation")
		}
	}
	last := p.page(args, info, "--cursor", cursor)
	if !reflect.DeepEqual(last, p.httpPage(info, url.Values{"cursor": {cursor}})) || len(last.Events) != 1 || last.NextCursor != nil || last.From != first.From || last.Through != first.Through || !reflect.DeepEqual(last.Coverage, first.Coverage) {
		t.Fatal("ordinary CLI next page differs from captured authoritative interval")
	}
	if out := p.call(args, nil, 0, "", "activity", "read", "--cursor", cursor); !bytes.Contains(out, []byte(info.BoardID+"/"+info.Incarnation+"/1 card.create")) || bytes.Contains(out, []byte("Next cursor:")) {
		t.Fatal("readable continuation did not show the final retained event")
	}
	for i, event := range append(append([]T.ActivityEvent(nil), first.Events...), last.Events...) {
		if event.Origin != first.Identity || int(event.EventID) != 21-i || int(event.BoardRevision) != 21-i || event.ActorKind != "automation" || event.ActorID == nil || string(*event.ActorID) != info.ActorID {
			t.Fatal("committed tuples differ across CLI pages")
		}
	}
	p.call(jsonArgs, nil, 1, "INVOCATION_OR_IO", "activity", "read", "--from", from)
	future := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	p.call(jsonArgs, nil, 2, "INVALID_INPUT", "activity", "read", "--from", future.Add(-time.Hour).Format(time.RFC3339), "--through", future.Format(time.RFC3339))
	if !reflect.DeepEqual(afterWrites, activityInventory(t, data)) {
		t.Fatal("standalone reads modified committed data or operation receipts")
	}
	stop(standalone)
	standalone = startLocal(0)
	info = activityReadFixture(t, data)
	p.call(jsonArgs, nil, 2, "CURSOR_STALE", "activity", "read", "--cursor", cursor)
	if fresh := p.page(args, info, flags...); fresh.NextCursor == nil || *fresh.NextCursor == cursor || !reflect.DeepEqual(fresh.Events, first.Events) || !reflect.DeepEqual(fresh.Coverage, first.Coverage) {
		t.Fatal("deliberate standalone reopen did not restore the inspection")
	}
	if out := p.call(args, nil, 0, "", append([]string{"activity", "read"}, flags...)...); !bytes.Contains(out, []byte("Next cursor:")) || bytes.Contains(out, []byte(cursor)) {
		t.Fatal("readable fresh inspection did not offer a new continuation")
	}
	if !reflect.DeepEqual(afterWrites, activityInventory(t, data)) {
		t.Fatal("standalone restart and reads changed committed data or receipts")
	}
	t.Log("ordinary standalone: complete empty, 21 committed events across 2 pages, exact-bound parity, malformed invocation, pre-restart cursor refusal, deliberate reopen, unchanged state and receipts")

	ws, project := filepath.Join(root, "w"), filepath.Join(root, "p")
	startWorkspace := func(port int) *workspace.Runtime {
		r, err := workspace.Start(t.Context(), workspace.Options{DataDir: ws, Port: port, Assets: http.NotFoundHandler()})
		if err != nil {
			t.Fatal("ordinary workspace runtime unavailable")
		}
		return r
	}
	w := startWorkspace(bridgePort(t))
	defer func() { stop(w) }()
	workspaceArgs := []string{"--workspace", ws}
	projectArgs := append(append([]string(nil), workspaceArgs...), "--project", "demo")
	workspaceBoard := filepath.Join(project, "b")
	p.call(workspaceArgs, nil, 0, "", "project", "create", "--key", "demo", "--label", "Synthetic", "--root", project)
	p.call(projectArgs, nil, 0, "", "board", "create", "--key", "board", "--label", "Synthetic", "--root", workspaceBoard)
	workspaceInfo := activityReadFixture(t, workspaceBoard)
	wargs := append(append([]string(nil), projectArgs...), "--board", "board")
	wjson := append(append([]string(nil), wargs...), "--json")
	p.call(wjson, bridgeCreateWire(t, workspaceInfo, 0, activityPrivateTitle), 0, "", "card", "create", "--input", "-")
	if workspaceInfo.BoardID == info.BoardID || workspaceInfo.Incarnation == info.Incarnation || len(p.page(wargs, workspaceInfo).Events) != 1 {
		t.Fatal("ordinary workspace board selection was not independent")
	}
	p.call(wjson, nil, 2, "CURSOR_STALE", "activity", "read", "--cursor", cursor)
	stop(w)
	floorTime := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	floor := floorTime.Format(time.RFC3339)
	activityModifyStoppedFixture(t, workspaceBoard, workspaceInfo, "UPDATE board SET activity_floor=? WHERE board_id=? AND incarnation=?", floor, workspaceInfo.BoardID, workspaceInfo.Incarnation)
	w = startWorkspace(0)
	// Workspace registration activates members asynchronously. An ordinary
	// registry status call is not a credential fallback; wait for member metadata.
	workspaceInfo = activityWaitFixture(t, workspaceBoard)
	workspaceBaseline := activityInventory(t, workspaceBoard)
	gapped := p.page(wargs, workspaceInfo)
	if gapped.Coverage.Status != "gapped" || len(gapped.Events) != 1 || gapped.Coverage.ActivityFloor == nil || string(*gapped.Coverage.ActivityFloor) != floor || !reflect.DeepEqual(gapped, p.httpPage(workspaceInfo, url.Values{"from": {string(gapped.From)}, "through": {string(gapped.Through)}})) {
		t.Fatal("gapped workspace page failed authoritative parity with retained rows")
	}
	if out := p.call(wargs, nil, 0, "", "activity", "read"); !bytes.Contains(out, []byte("Coverage: gapped")) || !bytes.Contains(out, []byte("card.create")) {
		t.Fatal("gapped readable activity hid coverage or retained row")
	}
	expiredFrom := floorTime.Add(-time.Hour).Format(time.RFC3339)
	expiredFlags := []string{"--from", expiredFrom, "--through", floor}
	expired := p.page(wargs, workspaceInfo, expiredFlags...)
	if expired.Coverage.Status != "expired" || len(expired.Events) != 0 || !reflect.DeepEqual(expired, p.httpPage(workspaceInfo, url.Values{"from": {expiredFrom}, "through": {floor}})) {
		t.Fatal("expired workspace coverage did not match authoritative page")
	}
	if out := p.call(wargs, nil, 0, "", append([]string{"activity", "read"}, expiredFlags...)...); !bytes.Contains(out, []byte("Coverage: expired")) || !bytes.Contains(out, []byte("No retained activity")) {
		t.Fatal("expired readable activity did not distinguish missing evidence")
	}
	if !reflect.DeepEqual(workspaceBaseline, activityInventory(t, workspaceBoard)) {
		t.Fatal("workspace reads modified committed data or operation receipts")
	}
	stop(w)
	activityModifyStoppedFixture(t, workspaceBoard, workspaceInfo, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", workspaceInfo.Incarnation, workspaceInfo.GrantID)
	w = startWorkspace(0)
	_ = activityWaitFixture(t, workspaceBoard)
	refusalBaseline := activityInventory(t, workspaceBoard)
	// The workspace catalog hides an inadmissible member before the activity
	// route. Standalone admission below exercises the explicit route refusal.
	p.call(wjson, nil, 3, "UNAVAILABLE", "activity", "read")
	if !reflect.DeepEqual(refusalBaseline, activityInventory(t, workspaceBoard)) {
		t.Fatal("workspace refusal modified committed data or operation receipts")
	}
	t.Log("ordinary workspace selection: independent board and foreign cursor refusal; controlled stopped-fixture floor and read-right removal: gapped retained, expired empty, inaccessible selection unavailable, parity and unchanged state and receipts")
	stop(standalone)
	activityModifyStoppedFixture(t, data, info, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", info.Incarnation, info.GrantID)
	standalone = startLocal(0)
	noReadBaseline := activityInventory(t, data)
	p.call(jsonArgs, nil, 2, "FORBIDDEN", "activity", "read")
	if !reflect.DeepEqual(noReadBaseline, activityInventory(t, data)) {
		t.Fatal("standalone authority refusal modified data or operation receipts")
	}
	t.Log("controlled stopped standalone grant without read: current authority refused with FORBIDDEN and no committed change or receipt")
	t.Run("controlled-activity-response", func(t *testing.T) {
		p.t = t
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal("controlled loopback listener unavailable")
		}
		defer listener.Close()
		id := bridgeUUID(t)
		fixture := filepath.Join(root, "f")
		engine, err := local.OpenEngine(t.Context(), local.EngineOptions{DataDir: fixture, Origin: "http://" + listener.Addr().String(), WorkspaceID: id})
		if err != nil {
			t.Fatal("controlled synthetic engine unavailable")
		}
		defer stop(engine)
		meta := activityReadFixture(t, fixture)
		mode, err := T.Readable(T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: T.UUIDv4(id)})
		if err != nil {
			t.Fatal("controlled mode response unavailable")
		}
		server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch req.URL.Path {
			case "/api/v1/runtime-mode":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(mode)
			case "/api/v1/boards/" + meta.BoardID + "/activity":
				w.Header().Set("Content-Type", "application/json")
				if req.URL.Query().Has("cursor") {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, `{"apiVersion":1,"outcome":"refused","error":{"code":"UNAVAILABLE","message":"`+activityPrivateTitle+`"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"apiVersion":1,"scope":"activity","result":{"title":"`+activityPrivateTitle+`"}}`)
			default:
				engine.ServeHTTP(w, req)
			}
		})}
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		defer func() {
			if server.Close() != nil || <-done != http.ErrServerClosed {
				t.Error("controlled loopback did not close")
			}
		}()
		fixtureArgs := []string{"--data", fixture, "--json"}
		before := activityInventory(t, fixture)
		p.call(fixtureArgs, nil, 3, "UNAVAILABLE", "activity", "read")
		p.call(fixtureArgs, nil, 3, "UNAVAILABLE", "activity", "read", "--cursor", "AA.AA")
		if !reflect.DeepEqual(before, activityInventory(t, fixture)) {
			t.Fatal("unavailable and malformed responses changed state or receipts")
		}
		t.Log("controlled loopback responses: malformed HTTP 200 and bounded HTTP 503 both unavailable; no private bytes displayed")
	})
}

func activityWaitFixture(t *testing.T, data string) local.ClientInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := local.ReadClient(data); err == nil {
			return info
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("workspace member did not activate")
	return local.ClientInfo{}
}
