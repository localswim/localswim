//go:build windows && amd64 && l02_native

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/assets"
	T "github.com/localswim/localswim/internal/contract/transport"
	local "github.com/localswim/localswim/internal/transport/local"
)

func bridgeUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal("synthetic operation identity unavailable")
	}
	b[6], b[8] = b[6]&15|64, b[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func bridgePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("synthetic loopback port unavailable")
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func bridgeCLI(t *testing.T, binary, data string, input []byte, want int, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append([]string{"--data", data, "--json"}, args...)...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Stdin = bytes.NewReader(input)
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	err := command.Run()
	exit := 0
	if err != nil {
		failure, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal("actual CLI process unavailable")
		}
		exit = failure.ExitCode()
	}
	if exit != want {
		t.Fatalf("actual CLI exit %d, wanted %d", exit, want)
	}
	if bytes.Contains(output.Bytes(), []byte(data)) || bytes.Contains(diagnostic.Bytes(), []byte(data)) {
		t.Fatal("actual CLI disclosed its private data directory")
	}
	if info, err := local.ReadClient(data); err == nil && (bytes.Contains(output.Bytes(), []byte(info.Token)) || bytes.Contains(diagnostic.Bytes(), []byte(info.Token))) {
		t.Fatal("actual CLI disclosed its private automation capability")
	}
	return output.Bytes()
}

func bridgeRead(t *testing.T, binary, data string, info local.ClientInfo) T.BoardRead {
	t.Helper()
	board, err := T.Decode[T.BoardRead](bridgeCLI(t, binary, data, nil, 0, "board", "read"))
	if err != nil || string(board.Result.Identity.BoardID) != info.BoardID || string(board.Result.Identity.Incarnation) != info.Incarnation {
		t.Fatal("actual CLI did not read its selected board identity")
	}
	return board
}

func bridgeCreateWire(t *testing.T, info local.ClientInfo, revision any, title string) []byte {
	t.Helper()
	wire, err := json.Marshal(map[string]any{
		"incarnation": info.Incarnation, "opId": bridgeUUID(t), "target": nil,
		"expectedRevision": revision, "expectedCardRevision": nil,
		"input": map[string]string{"title": title, "description": "Fresh synthetic workspace bridge fixture", "priority": "P3"},
	})
	if err != nil {
		t.Fatal("synthetic create input unavailable")
	}
	return wire
}

func bridgeHTTP(t *testing.T, origin, path, method string, token T.SecretToken, wire []byte) int {
	t.Helper()
	q, err := http.NewRequestWithContext(t.Context(), method, origin+path, bytes.NewReader(wire))
	if err != nil {
		t.Fatal("synthetic HTTP request unavailable")
	}
	q.Header.Set("Authorization", "Bearer "+string(token))
	if wire != nil {
		q.Header.Set("Content-Type", "application/json")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(q)
	if err != nil {
		t.Fatal("actual loopback request unavailable")
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal("actual loopback response incomplete")
	}
	return response.StatusCode
}

// This exercises the M01.02 engine/host seam directly. It does not qualify the
// later workspace registry, CLI --workspace selector or multi-board browser UI.
func TestWorkspaceBridgeActualDataCLIIsolationAndStandaloneReopen(t *testing.T) {
	root, binary := os.Getenv("L02_TEST_ROOT"), os.Getenv("L02_BINARY")
	if !safeAbsolute(root) || !safeAbsolute(binary) {
		t.Fatal("explicit private root and compiled CLI required; no skipped qualification")
	}
	if stat, err := os.Stat(binary); err != nil || stat.IsDir() {
		t.Fatal("compiled CLI unavailable")
	}
	fixture, err := os.MkdirTemp(root, "workspace-cli-")
	if err != nil {
		t.Fatal("private synthetic fixture unavailable")
	}
	static, err := assets.Handler()
	if err != nil {
		t.Fatal("verified embedded assets unavailable")
	}
	workspaceID := bridgeUUID(t)
	host, err := local.StartWorkspaceHost(t.Context(), local.WorkspaceHostOptions{WorkspaceID: workspaceID, Port: bridgePort(t), Assets: static})
	if err != nil {
		t.Fatal("single workspace listener unavailable")
	}
	defer func() {
		if err := host.Close(); err != nil {
			t.Error("workspace host close failed")
		}
	}()
	// Both engines retain this same standalone port. Holding it open proves
	// neither engine tries to acquire a board-specific listener.
	standaloneListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("standalone port fixture unavailable")
	}
	defer standaloneListener.Close()
	standalonePort := standaloneListener.Addr().(*net.TCPAddr).Port
	dirs := []string{filepath.Join(fixture, "harbor-one"), filepath.Join(fixture, "harbor-two")}
	infos := make([]local.ClientInfo, 2)
	identities := make([][]byte, 2)
	for i, data := range dirs {
		engine, err := local.OpenEngine(t.Context(), local.EngineOptions{DataDir: data, Port: standalonePort, Origin: host.Origin(), WorkspaceID: workspaceID})
		if err != nil {
			t.Fatal("listener-free synthetic engine unavailable")
		}
		if err := host.Attach(engine); err != nil {
			_ = engine.Close()
			t.Fatal("workspace engine attach failed")
		}
		info, err := local.ReadClient(data)
		if err != nil || info.Origin != host.Origin() || info.Port != standalonePort || info.WorkspaceID != workspaceID || info.SessionPath() != "/api/v1/boards/"+info.BoardID+"/session" {
			t.Fatal("workspace metadata changed durable port or selected channel")
		}
		infos[i] = info
		identities[i], err = os.ReadFile(filepath.Join(data, "identity.json"))
		if err != nil {
			t.Fatal("durable synthetic identity unavailable")
		}
	}
	if infos[0].BoardID == infos[1].BoardID || infos[0].InstallationID == infos[1].InstallationID || infos[0].Token == infos[1].Token {
		t.Fatal("independent engines shared identity or authority")
	}
	before := []T.BoardRead{bridgeRead(t, binary, dirs[0], infos[0]), bridgeRead(t, binary, dirs[1], infos[1])}
	titles := []string{"First isolated board", "Second isolated board"}
	for i, title := range titles {
		other := 1 - i
		opposite := bridgeRead(t, binary, dirs[other], infos[other])
		wire := bridgeCreateWire(t, infos[i], before[i].Result.Revision, title)
		terminal, err := T.Decode[T.Terminal](bridgeCLI(t, binary, dirs[i], wire, 0, "card", "create", "--input", "-"))
		if err != nil || terminal.Outcome != "committed" || string(terminal.BoardID) != infos[i].BoardID || string(terminal.ActorID) != infos[i].ActorID {
			t.Fatal("ordinary built CLI did not commit to selected board")
		}
		selected := bridgeRead(t, binary, dirs[i], infos[i])
		unchanged := bridgeRead(t, binary, dirs[other], infos[other])
		if selected.Result.Revision != before[i].Result.Revision+1 || len(selected.Result.Cards) != 1 || selected.Result.Cards[0].Title != title || selected.Result.Cards[0].Priority != "P3" || unchanged.Result.Revision != opposite.Result.Revision || len(unchanged.Result.Cards) != len(opposite.Result.Cards) {
			t.Fatal("CLI selection crossed board state or revision boundaries")
		}
	}
	for i := range infos {
		other := 1 - i
		board := bridgeRead(t, binary, dirs[other], infos[other])
		base := "/api/v1/boards/" + infos[other].BoardID
		for _, path := range []string{base, base + "/session"} {
			if status := bridgeHTTP(t, host.Origin(), path, "GET", infos[i].Token, nil); status != http.StatusUnauthorized {
				t.Fatalf("other-board automation read status %d", status)
			}
		}
		wire := bridgeCreateWire(t, infos[other], board.Result.Revision, "Must not commit")
		if status := bridgeHTTP(t, host.Origin(), base+"/cards", "POST", infos[i].Token, wire); status != http.StatusUnauthorized {
			t.Fatalf("other-board automation mutation status %d", status)
		}
		if after := bridgeRead(t, binary, dirs[other], infos[other]); after.Result.Revision != board.Result.Revision || len(after.Result.Cards) != len(board.Result.Cards) {
			t.Fatal("refused cross-board credential changed selected state")
		}
	}
	bridgeMetadataRefusals(t, binary, dirs[0])
	if err := host.Close(); err != nil {
		t.Fatal("workspace shutdown did not release engines")
	}
	if err := standaloneListener.Close(); err != nil {
		t.Fatal("synthetic standalone port release failed")
	}
	for i, data := range dirs {
		if _, err := local.ReadClient(data); err == nil {
			t.Fatal("closed workspace retained usable rendezvous metadata")
		}
		standalone, err := local.Start(t.Context(), local.Options{DataDir: data, Assets: static})
		if err != nil {
			t.Fatal("standalone reopen failed after workspace drain")
		}
		func() {
			defer func() {
				if err := standalone.Close(); err != nil {
					t.Error("standalone reopened engine close failed")
				}
			}()
			info, err := local.ReadClient(data)
			identity, readErr := os.ReadFile(filepath.Join(data, "identity.json"))
			if err != nil || readErr != nil || !bytes.Equal(identity, identities[i]) || info.InstallationID != infos[i].InstallationID || info.BoardID != infos[i].BoardID || info.Incarnation != infos[i].Incarnation || info.ActorID != infos[i].ActorID || info.GrantID != infos[i].GrantID || info.Port != standalonePort || info.WorkspaceID != "" || info.Origin != fmt.Sprintf("http://127.0.0.1:%d", standalonePort) || info.SessionPath() != "/api/v1/session" || info.Token == infos[i].Token || info.Epoch == infos[i].Epoch {
				t.Fatal("standalone reopen changed identity, default port or legacy channel")
			}
			read := bridgeRead(t, binary, data, info)
			if read.Result.Revision != 1 || len(read.Result.Cards) != 1 || read.Result.Cards[0].Title != titles[i] || read.Result.Cards[0].Priority != "P3" {
				t.Fatal("workspace committed state lost on standalone reopen")
			}
			metadata, err := os.ReadFile(filepath.Join(data, "service.json"))
			var shape map[string]json.RawMessage
			if err != nil || json.Unmarshal(metadata, &shape) != nil || len(shape) != 4 || shape["identity"] == nil || shape["origin"] == nil || shape["epoch"] == nil || shape["botToken"] == nil {
				t.Fatal("standalone service metadata broke the legacy exact shape")
			}
		}()
	}
}

func bridgeMetadataRefusals(t *testing.T, binary, data string) {
	t.Helper()
	path := filepath.Join(data, "service.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("synthetic workspace metadata unavailable")
	}
	defer func() {
		if os.WriteFile(path, original, 0600) != nil {
			t.Error("synthetic workspace metadata restoration failed")
		}
	}()
	var shape map[string]json.RawMessage
	if json.Unmarshal(original, &shape) != nil || len(shape) != 6 || string(shape["metadataVersion"]) != "2" || shape["host"] == nil {
		t.Fatal("workspace service metadata missing the strict v2 alternative")
	}
	cases := []struct {
		name string
		edit func(map[string]json.RawMessage)
	}{
		{"unsupported-version", func(m map[string]json.RawMessage) { m["metadataVersion"] = []byte("3") }},
		{"missing-tag", func(m map[string]json.RawMessage) { delete(m, "metadataVersion") }},
		{"missing-host", func(m map[string]json.RawMessage) { delete(m, "host") }},
		{"unknown-field", func(m map[string]json.RawMessage) { m["sessionUrl"] = []byte(`"http://127.0.0.1:8792/arbitrary"`) }},
		{"unknown-host-field", func(m map[string]json.RawMessage) {
			m["host"] = []byte(`{"mode":"workspace","workspaceId":"10000000-0000-4000-8000-000000000001","sessionPath":"/arbitrary"}`)
		}},
		{"wrong-host-mode", func(m map[string]json.RawMessage) {
			m["host"] = []byte(`{"mode":"standalone","workspaceId":"10000000-0000-4000-8000-000000000001"}`)
		}},
		{"noncanonical-origin", func(m map[string]json.RawMessage) { m["origin"] = []byte(`"http://localhost:8792"`) }},
		{"arbitrary-origin-path", func(m map[string]json.RawMessage) { m["origin"] = []byte(`"http://127.0.0.1:8792/api/v1/session"`) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var modified map[string]json.RawMessage
			if json.Unmarshal(original, &modified) != nil {
				t.Fatal("metadata fixture decode failed")
			}
			test.edit(modified)
			wire, err := json.Marshal(modified)
			if err != nil || os.WriteFile(path, wire, 0600) != nil {
				t.Fatal("metadata refusal fixture unavailable")
			}
			if _, err := local.ReadClient(data); err == nil {
				t.Fatal("malformed workspace metadata accepted")
			}
		})
	}
	for _, wire := range [][]byte{
		append([]byte(`{"metadataVersion":2,`), original[1:]...),
		append(append([]byte(nil), original...), []byte(strings.Repeat(" ", 65537))...),
	} {
		if os.WriteFile(path, wire, 0600) != nil {
			t.Fatal("bounded metadata fixture unavailable")
		}
		if _, err := local.ReadClient(data); err == nil {
			t.Fatal("duplicate or oversized metadata accepted")
		}
	}
	// A well-formed forged workspace UUID must fail live context verification.
	shape["host"] = []byte(fmt.Sprintf(`{"mode":"workspace","workspaceId":%q}`, bridgeUUID(t)))
	forged, err := json.Marshal(shape)
	if err != nil || os.WriteFile(path, forged, 0600) != nil {
		t.Fatal("live workspace mismatch fixture unavailable")
	}
	refusal, err := T.Decode[T.NonterminalRefusal](bridgeCLI(t, binary, data, nil, 2, "board", "read"))
	if err != nil || refusal.Outcome != "refused" || refusal.Error.Code != "CREDENTIAL_CONTEXT_MISMATCH" {
		t.Fatal("forged workspace metadata did not fail live context verification")
	}
}
