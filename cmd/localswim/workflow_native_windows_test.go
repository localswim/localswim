//go:build windows && amd64 && l02_native

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/localswim/localswim/internal/board/policyedit"
	T "github.com/localswim/localswim/internal/contract/transport"
	local "github.com/localswim/localswim/internal/transport/local"
)

func TestWorkflowActualCLIPreviewEdit(t *testing.T) {
	binary, root := os.Getenv("L02_BINARY"), os.Getenv("L02_TEST_ROOT")
	if !safeAbsolute(binary) || !safeAbsolute(root) {
		t.Fatal("explicit binary and private test root required")
	}
	data, err := os.MkdirTemp(root, "workflow-cli-")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	runtime, err := local.Start(t.Context(), local.Options{DataDir: filepath.Join(data, "board"), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runCLI := func(command ...string) []byte {
		t.Helper()
		args := append([]string{"--data", filepath.Join(data, "board"), "--json"}, command...)
		cmd := exec.CommandContext(t.Context(), binary, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("actual CLI failed for %s", command[1])
		}
		return stdout.Bytes()
	}
	show, err := T.Decode[T.BoardRead](runCLI("workflow", "show"))
	if err != nil || show.Result.Revision != 0 || show.Result.Workflow != nil {
		t.Fatal("actual CLI initial context unavailable")
	}
	candidate := policyedit.DefaultDefinition("51000000-0000-4000-8000-000000000001", 2)
	candidate.Lanes[0].Label = "Queue"
	previewRequest := T.WorkflowPreviewRequest{APIVersion: 1, BoardID: show.Result.Identity.BoardID, Incarnation: show.Result.Identity.Incarnation, ExpectedRevision: 0, ExpectedPolicyVersion: 1, Workflow: candidate}
	previewBytes, _ := json.Marshal(previewRequest)
	previewPath := filepath.Join(data, "preview.json")
	if os.WriteFile(previewPath, previewBytes, 0600) != nil {
		t.Fatal("preview input unavailable")
	}
	preview, err := T.Decode[T.WorkflowPreview](runCLI("workflow", "preview", "--input", previewPath))
	if err != nil || !preview.WouldChange || !reflect.DeepEqual(preview.Candidate, candidate) {
		t.Fatal("actual CLI preview changed candidate")
	}
	unchanged, err := T.Decode[T.BoardRead](runCLI("workflow", "show"))
	if err != nil || unchanged.Result.Revision != 0 || unchanged.Result.Workflow != nil {
		t.Fatal("actual CLI preview mutated board")
	}
	editBytes, _ := json.Marshal(map[string]any{"incarnation": show.Result.Identity.Incarnation, "opId": "51000000-0000-4000-8000-000000000002", "target": nil, "expectedRevision": 0, "expectedCardRevision": nil, "input": map[string]any{"expectedPolicyVersion": 1, "workflow": candidate}})
	editPath := filepath.Join(data, "edit.json")
	if os.WriteFile(editPath, editBytes, 0600) != nil {
		t.Fatal("edit input unavailable")
	}
	edited, err := T.Decode[T.Terminal](runCLI("workflow", "edit", "--input", editPath))
	if err != nil || edited.Command != "workflow.edit" || edited.Outcome != "committed" {
		t.Fatal("actual CLI edit failed")
	}
	after, err := T.Decode[T.BoardRead](runCLI("workflow", "show"))
	if err != nil || after.Result.PolicyVersion != 2 || len(after.Result.WorkflowPolicies) != 2 || !reflect.DeepEqual(after.Result.Workflow, &candidate) {
		t.Fatal("actual CLI policy context missing after save")
	}
}
