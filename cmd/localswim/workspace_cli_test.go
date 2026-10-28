package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
)

type retryWorkspaceOwner struct{ failures, attempts int }

func (o *retryWorkspaceOwner) Close() error {
	o.attempts++
	if o.attempts <= o.failures {
		return errors.New("synthetic close failure")
	}
	return nil
}

type blockedWorkspaceOwner struct {
	calls            int
	entered, release chan struct{}
}

func (o *blockedWorkspaceOwner) Close() error {
	o.calls++
	if o.calls == 1 {
		return errors.New("synthetic close failure")
	}
	close(o.entered)
	<-o.release
	return nil
}

func TestWorkspaceCleanupDoesNotHoldMutexDuringNativeClose(t *testing.T) {
	if !retryWorkspaceCLICleanup() {
		t.Fatal("prior cleanup unresolved")
	}
	owner := &blockedWorkspaceOwner{entered: make(chan struct{}), release: make(chan struct{})}
	finishWorkspaceCLI(owner)
	done := make(chan bool, 1)
	go func() { done <- retryWorkspaceCLICleanup() }()
	select {
	case <-owner.entered:
	case <-time.After(time.Second):
		t.Fatal("cleanup retry did not start")
	}
	concurrent := make(chan bool, 1)
	go func() { concurrent <- retryWorkspaceCLICleanup() }()
	select {
	case ready := <-concurrent:
		if ready {
			t.Error("active cleanup barrier bypassed")
		}
	case <-time.After(time.Second):
		t.Error("native close held global cleanup mutex")
	}
	close(owner.release)
	if !<-done {
		t.Fatal("completed cleanup remained pending")
	}
}

func TestWorkspaceCleanupDoesNotRetainDuplicateOwner(t *testing.T) {
	owner := &retryWorkspaceOwner{failures: 2}
	finishWorkspaceCLI(owner)
	finishWorkspaceCLI(owner)
	workspaceCLICleanup.Lock()
	count := len(workspaceCLICleanup.clients)
	workspaceCLICleanup.Unlock()
	if count != 1 {
		t.Fatal("same cleanup owner retained twice")
	}
	if !retryWorkspaceCLICleanup() {
		t.Fatal("duplicate owner cleanup unavailable")
	}
}

func TestWorkspaceCLIUnresolvedCleanupRetainsCustody(t *testing.T) {
	if !retryWorkspaceCLICleanup() {
		t.Fatal("prior cleanup unresolved")
	}
	owner := &retryWorkspaceOwner{failures: 2}
	finishWorkspaceCLI(owner)
	if owner.attempts != 1 || retryWorkspaceCLICleanup() {
		t.Fatal("failed close custody lost or ignored")
	}
	if !retryWorkspaceCLICleanup() || owner.attempts != 3 {
		t.Fatal("retained cleanup was not retried")
	}
	if !retryWorkspaceCLICleanup() || owner.attempts != 3 {
		t.Fatal("released owner remained armed")
	}
}

func TestWorkspaceOriginalCleanupBarrierRemainsUnknown(t *testing.T) {
	if !retryWorkspaceCLICleanup() {
		t.Fatal("prior cleanup unresolved")
	}
	owner := &retryWorkspaceOwner{failures: 2}
	finishWorkspaceCLI(owner)
	var out, diagnostic bytes.Buffer
	q := localInvocation{workspace: `C:\synthetic\never-open`, project: "harbor", board: "main", args: []string{"--json", "operation", "read", "00000000-0000-4000-8000-000000000001", "--evidence", `C:\synthetic\original.json`}}
	if exit := runWorkspaceCLI(context.Background(), q, nil, &out, &diagnostic); exit != 4 {
		t.Fatal("cleanup refusal downgraded original uncertainty")
	}
	if !retryWorkspaceCLICleanup() {
		t.Fatal("synthetic cleanup not released")
	}
}

func TestWorkspaceCLIExplicitGrammar(t *testing.T) {
	base := []string{"--workspace", `C:\synthetic\workspace`}
	for _, tail := range [][]string{
		{"project", "list"},
		{"--json", "project", "create", "--key", "harbor", "--label", "Harbor", "--root", `C:\synthetic\harbor`},
		{"board", "list"},
		{"--project", "harbor", "board", "list"},
		{"--project", "harbor", "board", "create", "--key", "main", "--label", "Main", "--root", `C:\synthetic\harbor\main`},
		{"--project", "harbor", "board", "register", "--key", "main", "--label", "Main", "--root", `C:\synthetic\harbor\main`},
		{"--project", "harbor", "--board", "main", "board", "status"},
		{"--project", "harbor", "--board", "main", "--json", "board", "read"},
		{"--project", "harbor", "--board", "main", "card", "create", "--input", "-"},
		{"--project", "harbor", "--board", "main", "attachment", "add", "00000000-0000-4000-8000-000000000001", "--input", "-", "--file", `C:\synthetic\attachment.bin`},
		{"--project", "harbor", "--board", "main", "export", "create", "--destination", `C:\synthetic\export.zip`},
		{"--project", "harbor", "--board", "main", "operation", "read", "00000000-0000-4000-8000-000000000001", "--evidence", `C:\synthetic\original.json`},
	} {
		if _, err := parseLocal(append(append([]string{}, base...), tail...)); err != nil {
			t.Fatalf("explicit grammar refused: %v", tail)
		}
	}
	for _, tail := range [][]string{
		{"board", "read"}, {"--project", "harbor", "board", "read"}, {"--board", "main", "card", "create", "--input", "-"},
		{"--project", "harbor", "project", "list"}, {"--board", "main", "board", "list"},
		{"--project", "harbor", "--board", "main", "board", "create", "--key", "other", "--label", "Other", "--root", `C:\synthetic\other`},
		{"board", "register", "--key", "main", "--label", "Main", "--root", `C:\synthetic\main`},
		{"--project", "harbor", "board", "status", "--root", `C:\synthetic\main`},
		{"project", "create", "--key", "harbor", "--root", `C:\synthetic\harbor`},
		{"project", "list", "--key", "unused"}, {"--data", `C:\synthetic\board`, "project", "list"},
		{"--project", "harbor", "--project", "other", "board", "list"}, {"--project", "../bad", "board", "list"},
		{"--project", "harbor", "--board", "main", "board", "read", "--actor", "human"},
		{"--project", "harbor", "--board", "main", "operation", "read", "x", "--native-owner"},
		{"project", "list", "--json"},
		{"--project", "harbor", "--board", "main", "card", "create"},
		{"--project", "harbor", "--board", "main", "unknown", "command"},
		{"--project", "harbor", "--board", "main", "export", "create"},
	} {
		if _, err := parseLocal(append(append([]string{}, base...), tail...)); err == nil {
			t.Fatalf("unsafe/ambiguous grammar accepted: %v", tail)
		}
	}
}

func TestWorkspaceStaleRevisionRemainsDistinct(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if workspaceFailure([]string{"--json"}, &out, &diagnostic, &core.Failure{Code: "CONFIG_CHANGED", Exit: 2}) != 2 {
		t.Fatal("stale revision exit changed")
	}
	v, err := T.Decode[T.NonterminalRefusal](out.Bytes())
	if err != nil || v.Error.Code != "CONFIG_CHANGED" || diagnostic.String() != "CONFIG_CHANGED\n" {
		t.Fatal("stale configuration collapsed into generic unavailability")
	}
}

func TestWorkspaceMalformedDomainRefusesBeforePrivateOrServiceIO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	for _, command := range [][]string{{"card", "create"}, {"export", "create"}, {"attachment", "add", "00000000-0000-4000-8000-000000000001", "--input", "-"}, {"unknown", "command"}} {
		args := append([]string{"--workspace", root, "--project", "harbor", "--board", "main", "--json"}, command...)
		var out, diagnostic bytes.Buffer
		if exit := run(context.Background(), args, nil, &out, &diagnostic); exit != 1 {
			t.Fatal("malformed command reached unavailable private/service context")
		}
		v, err := T.Decode[T.NonterminalRefusal](out.Bytes())
		if err != nil || v.Error.Code != "INVOCATION_OR_IO" {
			t.Fatal("owning grammar refusal lost")
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("malformed command created private state")
	}
}

func TestWorkspaceExpectedIdentityIsBoundedAndStrict(t *testing.T) {
	file := filepath.Join(t.TempDir(), "expected.json")
	for i, wire := range []string{
		`{"installationId":"00000000-0000-4000-8000-000000000001","boardId":"00000000-0000-4000-8000-000000000002","incarnation":"00000000-0000-4000-8000-000000000003"}`,
		`{"installationId":"00000000-0000-4000-8000-000000000001","boardId":"00000000-0000-4000-8000-000000000002","incarnation":"00000000-0000-4000-8000-000000000003","grant":"forbidden"}`,
		string(bytes.Repeat([]byte(" "), 4097)),
	} {
		if err := os.WriteFile(file, []byte(wire), 0600); err != nil {
			t.Fatal("fixture unavailable")
		}
		c := workspaceCommand{expectedFile: file}
		err := c.readExpectedIdentity()
		if i == 0 {
			if err != nil || c.expected == nil {
				t.Fatal("valid expected tuple refused")
			}
		} else if err == nil {
			t.Fatal("unsafe expected tuple accepted")
		}
	}
}

func TestWorkspaceTextKeepsKeysLabelsAndStatus(t *testing.T) {
	var out bytes.Buffer
	v := T.WorkspaceBoards{ConfigRevision: 4, Boards: []T.WorkspaceMember{{ProjectKey: "harbor", BoardKey: "main", Label: "Main", Availability: "ready"}}}
	if writeWorkspaceText(&out, v) != nil || !bytes.Contains(out.Bytes(), []byte("harbor/main\t\"Main\"\tready")) {
		t.Fatal("focused board identity hidden")
	}
	key := T.WorkspaceKey("main")
	out.Reset()
	if writeWorkspaceText(&out, T.WorkspaceControlResult{Status: "initialized-unregistered", ProjectKey: "harbor", BoardKey: &key, ConfigRevision: 4}) != nil || !bytes.Contains(out.Bytes(), []byte("initialized-unregistered harbor/main")) {
		t.Fatal("inspection status hidden")
	}
}

func TestWorkspaceTextMakesLabelsInert(t *testing.T) {
	var out bytes.Buffer
	v := T.WorkspaceProjects{ConfigRevision: 1, Projects: []T.WorkspaceProject{{Key: "harbor", Label: "title\x1b[31m\tspoof\nnext\u202e"}}}
	if writeWorkspaceText(&out, v) != nil || bytes.ContainsAny(out.Bytes(), "\x1b\u202e") || bytes.Count(out.Bytes(), []byte("\n")) != 2 || !bytes.Contains(out.Bytes(), []byte(`\x1b`)) {
		t.Fatal("workspace label controlled terminal output")
	}
}
