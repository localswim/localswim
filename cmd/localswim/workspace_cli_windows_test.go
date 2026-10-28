//go:build windows && amd64 && l02_native

package main

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	T "github.com/localswim/localswim/internal/contract/transport"
	local "github.com/localswim/localswim/internal/transport/local"
	"github.com/localswim/localswim/internal/workspace"
)

type workspaceBrokenOutput struct{}

func (workspaceBrokenOutput) Write([]byte) (int, error) {
	return 0, errors.New("synthetic output failure")
}

func TestWorkspaceCLIControlsAndBoardIsolation(t *testing.T) {
	base := os.Getenv("L02_TEST_ROOT")
	if !safeAbsolute(base) {
		t.Fatal("explicit private evidence root required")
	}
	root := filepath.Join(base, "cli-"+bridgeUUID(t)[:8])
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal("synthetic parent unavailable")
	}
	ws, project := filepath.Join(root, "workspace"), filepath.Join(root, "project")
	r, err := workspace.Start(t.Context(), workspace.Options{DataDir: ws, Port: bridgePort(t), Assets: http.NotFoundHandler()})
	if err != nil {
		t.Fatal("synthetic workspace unavailable")
	}
	defer func() {
		if r.Close() != nil {
			t.Error("workspace cleanup failed")
		}
	}()
	call := func(want int, projectKey, boardKey string, input []byte, command ...string) []byte {
		t.Helper()
		args := []string{"--workspace", ws}
		if projectKey != "" {
			args = append(args, "--project", projectKey)
		}
		if boardKey != "" {
			args = append(args, "--board", boardKey)
		}
		args = append(args, "--json")
		args = append(args, command...)
		var out, diagnostic bytes.Buffer
		if exit := run(t.Context(), args, bytes.NewReader(input), &out, &diagnostic); exit != want {
			t.Fatalf("focused CLI exit %d wanted %d", exit, want)
		}
		if bytes.Contains(out.Bytes(), []byte(root)) || bytes.Contains(diagnostic.Bytes(), []byte(root)) {
			t.Fatal("private root disclosure")
		}
		return out.Bytes()
	}
	projects, err := T.Decode[T.WorkspaceProjects](call(0, "", "", nil, "project", "list"))
	if err != nil || len(projects.Projects) != 0 {
		t.Fatal("empty project list unavailable")
	}
	created, err := T.Decode[T.WorkspaceControlResult](call(0, "", "", nil, "project", "create", "--key", "harbor", "--label", "Harbor", "--root", project))
	if err != nil || created.Status != "registered" {
		t.Fatal("project registration unavailable")
	}
	for _, key := range []string{"one", "two"} {
		v, err := T.Decode[T.WorkspaceControlResult](call(0, "harbor", "", nil, "board", "create", "--key", key, "--label", key, "--root", filepath.Join(project, key)))
		if err != nil || v.Status != "registered" {
			t.Fatal("explicit board create failed")
		}
	}
	read := func(key string) T.BoardRead {
		t.Helper()
		v, err := T.Decode[T.BoardRead](call(0, "harbor", key, nil, "board", "read"))
		if err != nil {
			t.Fatal("selected board read invalid")
		}
		return v
	}
	one, two := read("one"), read("two")
	if one.Result.Identity.BoardID == two.Result.Identity.BoardID {
		t.Fatal("independent boards collapsed")
	}
	info, err := local.ReadClient(filepath.Join(project, "one"))
	if err != nil {
		t.Fatal("synthetic board context unavailable")
	}
	wire := bridgeCreateWire(t, info, one.Result.Revision, "Scoped CLI fixture")
	terminal, err := T.Decode[T.Terminal](call(0, "harbor", "one", wire, "card", "create", "--input", "-"))
	if err != nil || terminal.Outcome != "committed" {
		t.Fatal("selected automation mutation unconfirmed")
	}
	if read("two").Result.Revision != two.Result.Revision {
		t.Fatal("sibling board revision changed")
	}
	status, err := T.Decode[T.WorkspaceControlResult](call(0, "harbor", "one", nil, "board", "status"))
	if err != nil || status.Status != "registered" || status.Identity == nil || status.Identity.BoardID != one.Result.Identity.BoardID {
		t.Fatal("focused control status lost identity")
	}
	list, err := T.Decode[T.WorkspaceBoards](call(0, "harbor", "", nil, "board", "list"))
	if err != nil || len(list.Boards) != 2 {
		t.Fatal("focused board list unavailable")
	}
	var diagnostic bytes.Buffer
	args := []string{"--workspace", ws, "--json", "project", "create", "--key", "second", "--label", "Second", "--root", filepath.Join(root, "second")}
	if exit := run(t.Context(), args, nil, workspaceBrokenOutput{}, &diagnostic); exit != 4 {
		t.Fatal("lost control result downgraded to ordinary IO failure")
	}
	projects, err = T.Decode[T.WorkspaceProjects](call(0, "", "", nil, "project", "list"))
	if err != nil || len(projects.Projects) != 2 {
		t.Fatal("lost control result replayed or lost its registration")
	}
}
