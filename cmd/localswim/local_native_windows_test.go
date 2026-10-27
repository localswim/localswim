//go:build windows && amd64 && l02_native

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	local "github.com/localswim/localswim/internal/transport/local"
)

type localServeOutput struct {
	mu    sync.Mutex
	ready chan string
	once  sync.Once
}

func (w *localServeOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if strings.HasPrefix(string(p), "Localswim http://127.0.0.1:") {
		w.once.Do(func() {
			w.ready <- strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(string(p), "Localswim ")), "/")
		})
	}
	return len(p), nil
}

func TestLocalEntryRealBrowserAndCLIReopen(t *testing.T) {
	root, node, browser, binary := os.Getenv("L02_TEST_ROOT"), os.Getenv("L02_NODE"), os.Getenv("L02_BROWSER"), os.Getenv("L02_BINARY")
	for _, p := range []string{root, node, browser, binary} {
		if !safeAbsolute(p) {
			t.Fatal("explicit local test paths required; no skipped qualification")
		}
	}
	var nonce [6]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		t.Fatal("fixture identity unavailable")
	}
	data := filepath.Join(root, "entry-"+hex.EncodeToString(nonce[:]))
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal("test port unavailable")
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	start := func(explicitPort bool) (string, func()) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan int, 1)
		output := &localServeOutput{ready: make(chan string, 1)}
		args := []string{"serve", "--data", data}
		if explicitPort {
			args = append(args, "--port", fmt.Sprint(port))
		}
		go func() { done <- run(ctx, args, nil, output, os.Stderr) }()
		stop := func() {
			cancel()
			select {
			case exit := <-done:
				if exit != 0 {
					t.Errorf("normal local entry exit %d", exit)
				}
			case <-time.After(15 * time.Second):
				t.Error("local entry failed to drain")
			}
		}
		select {
		case origin := <-output.ready:
			return origin, stop
		case exit := <-done:
			cancel()
			t.Fatalf("local entry refused, exit %d", exit)
		case <-time.After(20 * time.Second):
			cancel()
			t.Fatal("local entry did not become ready")
		}
		return "", func() {}
	}
	origin, stop := start(true)
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	before, e := local.ReadClient(data)
	if e != nil {
		t.Fatal("private local client context unavailable")
	}
	cliRead := func() []byte {
		var output, diagnostic bytes.Buffer
		if exit := run(t.Context(), []string{"--data", data, "--json", "board", "read"}, nil, &output, &diagnostic); exit != 0 {
			t.Fatalf("ordinary CLI read exit %d; fixed diagnostic %q; public envelope %s", exit, diagnostic.String(), output.String())
		}
		return output.Bytes()
	}
	if !json.Valid(cliRead()) {
		t.Fatal("CLI did not return a JSON board")
	}
	script, e := filepath.Abs("testdata/local-browser-smoke.mjs")
	if e != nil {
		t.Fatal("browser fixture unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Env = append(os.Environ(), "L02_ORIGIN="+origin, "L02_BROWSER="+browser, "L02_BINARY="+binary, "L02_DATA="+data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual product browser smoke failed: %s", output)
	}
	var result struct {
		BrowserCreated       string `json:"browserCreated"`
		CLICreated           string `json:"cliCreated"`
		BrowserMutationCount int    `json:"browserMutationCount"`
	}
	if json.Unmarshal(output, &result) != nil || result.BrowserCreated == "" || result.CLICreated == "" || result.BrowserMutationCount != 1 {
		t.Fatal("browser/CLI evidence missing")
	}
	confirmed := cliRead()
	if !bytes.Contains(confirmed, []byte(result.BrowserCreated)) || !bytes.Contains(confirmed, []byte(result.CLICreated)) {
		t.Fatal("UI/CLI confirmed state diverged")
	}
	stop()
	stop = nil
	origin2, stop2 := start(false)
	stop = stop2
	after, e := local.ReadClient(data)
	if e != nil || origin2 != origin || before.InstallationID != after.InstallationID || before.BoardID != after.BoardID || before.Incarnation != after.Incarnation || before.ActorID != after.ActorID || before.GrantID != after.GrantID || before.Token == after.Token || before.Epoch == after.Epoch {
		t.Fatal("reopen replaced identity or reused old capability")
	}
	confirmed = cliRead()
	if !bytes.Contains(confirmed, []byte(result.BrowserCreated)) || !bytes.Contains(confirmed, []byte(result.CLICreated)) {
		t.Fatal("committed data lost after local service reopen")
	}
}
