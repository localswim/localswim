package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	L "github.com/localswim/localswim/internal/transport/local"
	"io"
	"os"
	"path/filepath"
)

func readWorkflowFile(path string) (*A.Workflow, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, errComposition
	}
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	ce := f.Close()
	if e != nil || ce != nil || len(b) > 65536 {
		return nil, errComposition
	}
	w, e := A.DecodeWorkflow(b)
	if e != nil || A.ValidateInitialWorkflow(w) != nil {
		return nil, errComposition
	}
	return w, nil
}

// Setup is native and deliberate; it never reads service credentials or retries.
func runLocalBoardSetup(ctx context.Context, args []string, out, diagnostic io.Writer) (bool, int) {
	if len(args) < 4 || args[0] != "--data" {
		return false, 0
	}
	cmd := args[2:]
	jsonMode := false
	if len(cmd) > 0 && cmd[0] == "--json" {
		jsonMode = true
		cmd = cmd[1:]
	}
	if len(cmd) < 2 || cmd[0] != "board" || (cmd[1] != "init" && cmd[1] != "inspect") {
		return false, 0
	}
	fail := func(code string, exit int) (bool, int) { return true, entryFailure(args, out, diagnostic, code, exit) }
	root, e := filepath.Abs(filepath.FromSlash(args[1]))
	if e != nil || !safeAbsolute(root) {
		return fail("INVOCATION_OR_IO", 1)
	}
	var result L.BoardDefinition
	if cmd[1] == "init" {
		if len(cmd) != 4 || cmd[2] != "--workflow" {
			return fail("INVOCATION_OR_IO", 1)
		}
		w, e := readWorkflowFile(cmd[3])
		if e != nil {
			return fail("INVOCATION_OR_IO", 1)
		}
		id, e := L.InitializeBoard(ctx, root, w)
		if e != nil {
			var cleanup L.WorkspaceCleanup
			if errors.As(e, &cleanup) {
				finishWorkspaceCLI(cleanup)
			}
			if errors.Is(e, L.ErrInitializationRefused) {
				return fail("INVALID_INPUT", 2)
			}
			return fail("OUTCOME_UNKNOWN", 4)
		}
		result = L.BoardDefinition{Identity: id, Workflow: w}
	} else {
		if len(cmd) != 2 {
			return fail("INVOCATION_OR_IO", 1)
		}
		result, e = L.InspectBoardDefinition(ctx, root, nil)
		if e != nil {
			var cleanup L.WorkspaceCleanup
			if errors.As(e, &cleanup) {
				finishWorkspaceCLI(cleanup)
			}
			return fail("UNAVAILABLE", 3)
		}
	}
	// Explicit field names remain consistent with WorkspaceIdentity.
	wire := struct {
		Status   string `json:"status"`
		Identity struct {
			InstallationID string `json:"installationId"`
			BoardID        string `json:"boardId"`
			Incarnation    string `json:"incarnation"`
		} `json:"identity"`
		Workflow *A.Workflow `json:"workflow,omitempty"`
	}{Status: "initialized", Workflow: result.Workflow}
	wire.Identity.InstallationID = result.Identity.InstallationID
	wire.Identity.BoardID = result.Identity.BoardID
	wire.Identity.Incarnation = result.Identity.Incarnation
	if out == nil {
		return true, 4
	}
	if !jsonMode {
		if _, e = fmt.Fprintf(out, "Board initialized %s/%s\n", result.Identity.BoardID, result.Identity.Incarnation); e != nil {
			return true, 4
		}
	}
	if e = json.NewEncoder(out).Encode(wire); e != nil {
		return true, 4
	}
	return true, 0
}
