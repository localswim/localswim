//go:build windows && amd64 && l02_native && m11_cli_native && m12_cli_native

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/localswim/localswim/internal/board/policyedit"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/workspace"
)

// This uses the genuine workspace host with default and configured workflows.
// It exercises only GET, unconfigured preflight, and stale Stop; no command can
// reach the native runner. The controlled fixture covers actual POST decoding.
func TestIdentityProbeActualCLIWorkspaceSelection(t *testing.T) {
	p, input := recipientCLIProof(t)
	ws, project := filepath.Join(p.root, "workspace"), filepath.Join(p.root, "project")
	host, err := workspace.Start(t.Context(), workspace.Options{DataDir: ws, Port: bridgePort(t), Assets: http.NotFoundHandler()})
	if err != nil {
		t.Fatal("workspace fixture unavailable")
	}
	defer func() {
		if err := host.Close(); err != nil {
			t.Error("workspace fixture did not close")
		}
	}()
	p.call([]string{"--workspace", ws}, nil, 0, "", "project", "create", "--key", "demo", "--label", "Synthetic", "--root", project)
	for _, key := range []string{"default", "configured"} {
		t.Run(key, func(t *testing.T) {
			p := activityCLIProof{t, p.binary, p.root}
			p.call([]string{"--workspace", ws, "--project", "demo"}, nil, 0, "", "board", "create", "--key", key, "--label", key, "--root", filepath.Join(project, key))
			args := []string{"--workspace", ws, "--project", "demo", "--board", key, "--json"}
			if key == "configured" {
				board, err := T.Decode[T.BoardRead](p.call(args, nil, 0, "", "board", "read"))
				if err != nil {
					t.Fatal("selected workflow unavailable")
				}
				definition := policyedit.DefaultDefinition("85000000-0000-4000-8000-000000000003", 2)
				definition.Lanes[0].Label = "Configured identity CLI queue"
				wire, err := json.Marshal(map[string]any{"incarnation": board.Result.Identity.Incarnation, "opId": "85000000-0000-4000-8000-000000000004", "target": nil, "expectedRevision": board.Result.Revision, "expectedCardRevision": nil, "input": map[string]any{"expectedPolicyVersion": 1, "workflow": definition}})
				path := filepath.Join(p.root, "workflow.json")
				if err != nil || os.WriteFile(path, wire, 0600) != nil {
					t.Fatal("workflow fixture unavailable")
				}
				p.call(args, nil, 0, "", "workflow", "edit", "--input", path)
			}
			for _, action := range []string{"status", "probe-version", "stop"} {
				command := []string{"recipient", "tool", action}
				if action == "stop" {
					command = append(command, "--probe-generation", cliForeignGeneration)
				}
				p.call(args, nil, 3, "UNAVAILABLE", command...)
			}
			board := p.call(args, nil, 0, "", "board", "read")
			notices := p.call(args, nil, 0, "", "notices", "status")
			policyBytes := p.call(args, nil, 0, "", "recipient", "configure", "--input", input)
			policy, err := T.Decode[T.RecipientResponse](policyBytes)
			if err != nil || policy.Result.ConfigurationGeneration == nil {
				t.Fatal("selected policy unavailable")
			}
			status, err := T.Decode[T.ProbeControlResponse](p.call(args, nil, 0, "", "recipient", "tool", "status"))
			if err != nil || T.ValidateProbeControlContext(status, policy.Result.Identity, policy.Result.RuntimeGeneration, *policy.Result.ConfigurationGeneration) != nil || status.Result.Phase != "ready" {
				t.Fatal("status did not bind the exact selected workflow")
			}
			p.call(args, nil, 2, "CONTROL_STALE", "recipient", "tool", "stop", "--probe-generation", cliForeignGeneration)
			after, err := T.Decode[T.ProbeControlResponse](p.call(args, nil, 0, "", "recipient", "tool", "status"))
			if err != nil || after != status {
				t.Fatal("stale Stop changed the ready operation")
			}
			if !bytes.Equal(board, p.call(args, nil, 0, "", "board", "read")) || !bytes.Equal(notices, p.call(args, nil, 0, "", "notices", "status")) || !bytes.Equal(policyBytes, p.call(args, nil, 0, "", "recipient", "status")) {
				t.Fatal("control changed board, notices or saved recipient policy")
			}
		})
	}
}
