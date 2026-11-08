//go:build windows && amd64 && l02_native && m11_cli_native && m12_cli_native

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/policyedit"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/local"
	"github.com/localswim/localswim/internal/workspace"
)

func TestInstallationActualBuiltCLI(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "default"
		if configured {
			name = "configured"
		}
		t.Run(name, func(t *testing.T) {
			p, input := recipientCLIProof(t)
			data := filepath.Join(p.root, "board")
			r, err := local.Start(t.Context(), local.Options{DataDir: data, Port: bridgePort(t)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			}()
			jsonArgs := []string{"--data", data, "--json"}
			if configured {
				show, err := T.Decode[T.BoardRead](p.call(jsonArgs, nil, 0, "", "board", "read"))
				if err != nil {
					t.Fatal(err)
				}
				definition := policyedit.DefaultDefinition("61000000-0000-4000-8000-000000000001", 2)
				definition.Lanes[0].Label = "Installation inspection queue"
				edit := map[string]any{"incarnation": show.Result.Identity.Incarnation, "opId": "61000000-0000-4000-8000-000000000002", "target": nil, "expectedRevision": show.Result.Revision, "expectedCardRevision": nil, "input": map[string]any{"expectedPolicyVersion": 1, "workflow": definition}}
				wire, err := json.Marshal(edit)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(p.root, "workflow.json")
				if err := os.WriteFile(path, wire, 0600); err != nil {
					t.Fatal(err)
				}
				p.call(jsonArgs, nil, 0, "", "workflow", "edit", "--input", path)
			}
			for _, saved := range []bool{false, true} {
				if saved {
					p.call(jsonArgs, nil, 0, "", "recipient", "configure", "--input", input)
				}
				board := p.call(jsonArgs, nil, 0, "", "board", "read")
				notices := p.call(jsonArgs, nil, 0, "", "notices", "status")
				policy := p.call(jsonArgs, nil, 0, "", "recipient", "status")
				status, err := T.Decode[T.RecipientResponse](policy)
				if err != nil {
					t.Fatal(err)
				}
				wire := p.call(jsonArgs, nil, 0, "", "recipient", "tool", "inspect")
				observation, err := T.Decode[T.InstallationInspectionResponse](wire)
				if err != nil || observation.Result.Identity != status.Result.Identity || observation.Result.RuntimeGeneration != status.Result.RuntimeGeneration {
					t.Fatal("actual inspection identity/generation")
				}
				readable := p.call([]string{"--data", data}, nil, 0, "", "recipient", "tool", "inspect")
				if !bytes.Contains(readable, []byte("Installation inspection for board/incarnation")) || !bytes.Contains(readable, []byte(status.Result.Identity.BoardID)) || bytes.Contains(readable, []byte("00000000-0000-7000-8000-000000000016")) {
					t.Fatal("readable inspection missing context or leaking recipient")
				}
				for _, extra := range [][]string{{"extra"}, {"--input", "-"}, {"--path", data}, {"--version", "1"}} {
					p.call(jsonArgs, nil, 1, "INVOCATION_OR_IO", append([]string{"recipient", "tool", "inspect"}, extra...)...)
				}
				if !bytes.Equal(board, p.call(jsonArgs, nil, 0, "", "board", "read")) || !bytes.Equal(notices, p.call(jsonArgs, nil, 0, "", "notices", "status")) || !bytes.Equal(policy, p.call(jsonArgs, nil, 0, "", "recipient", "status")) {
					t.Fatal("inspection or refused invocation changed board/notices/recipient")
				}
			}
		})
	}
}

func TestInstallationActualWorkspaceCLI(t *testing.T) {
	p, _ := recipientCLIProof(t)
	ws, project := filepath.Join(p.root, "workspace"), filepath.Join(p.root, "project")
	host, err := workspace.Start(t.Context(), workspace.Options{DataDir: ws, Port: bridgePort(t), Assets: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
	}()
	p.call([]string{"--workspace", ws}, nil, 0, "", "project", "create", "--key", "demo", "--label", "Synthetic", "--root", project)
	for _, key := range []string{"default", "configured"} {
		p.call([]string{"--workspace", ws, "--project", "demo"}, nil, 0, "", "board", "create", "--key", key, "--label", key, "--root", filepath.Join(project, key))
		selection := []string{"--workspace", ws, "--project", "demo", "--board", key, "--json"}
		if key == "configured" {
			before, err := T.Decode[T.BoardRead](p.call(selection, nil, 0, "", "board", "read"))
			if err != nil {
				t.Fatal(err)
			}
			definition := policyedit.DefaultDefinition("62000000-0000-4000-8000-000000000001", 2)
			definition.Lanes[0].Label = "Configured inspection queue"
			wire, err := json.Marshal(map[string]any{"incarnation": before.Result.Identity.Incarnation, "opId": "62000000-0000-4000-8000-000000000002", "target": nil, "expectedRevision": before.Result.Revision, "expectedCardRevision": nil, "input": map[string]any{"expectedPolicyVersion": 1, "workflow": definition}})
			if err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(p.root, "workspace-workflow.json")
			if err := os.WriteFile(input, wire, 0600); err != nil {
				t.Fatal(err)
			}
			p.call(selection, nil, 0, "", "workflow", "edit", "--input", input)
		}
		before := p.call(selection, nil, 0, "", "board", "read")
		status, err := T.Decode[T.RecipientResponse](p.call(selection, nil, 0, "", "recipient", "status"))
		if err != nil {
			t.Fatal(err)
		}
		observation, err := T.Decode[T.InstallationInspectionResponse](p.call(selection, nil, 0, "", "recipient", "tool", "inspect"))
		if err != nil || observation.Result.Identity != status.Result.Identity || observation.Result.RuntimeGeneration != status.Result.RuntimeGeneration {
			t.Fatal("workspace selection not bound")
		}
		readable := p.call(selection[:len(selection)-1], nil, 0, "", "recipient", "tool", "inspect")
		if !bytes.Contains(readable, []byte(status.Result.Identity.BoardID)) || !bytes.Equal(before, p.call(selection, nil, 0, "", "board", "read")) {
			t.Fatal("workspace read changed or foreign")
		}
	}
}

func TestInstallationActualCLIControlledReads(t *testing.T) {
	for _, scenario := range []string{"missing", "unqualified", "unavailable", "denied", "stale", "busy", "invalid", "wrong-generation", "malformed", "wrong-media", "lost-body"} {
		t.Run(scenario, func(t *testing.T) {
			p, _ := recipientCLIProof(t)
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			data := filepath.Join(p.root, "controlled")
			wid := bridgeUUID(t)
			engine, err := local.OpenEngine(t.Context(), local.EngineOptions{DataDir: data, Origin: "http://" + listener.Addr().String(), WorkspaceID: wid})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			info := activityReadFixture(t, data)
			mode, _ := T.Readable(T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: T.UUIDv4(wid)})
			prefix := "/api/v1/boards/" + info.BoardID + "/recipient/"
			var statusReads, inspections atomic.Int32
			server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.URL.Path == "/api/v1/runtime-mode" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(mode)
					return
				}
				if q.URL.Path == prefix+"status" {
					statusReads.Add(1)
				}
				if q.URL.Path != prefix+"tool/inspect" {
					engine.ServeHTTP(w, q)
					return
				}
				inspections.Add(1)
				if q.Method != "GET" || q.ContentLength > 0 || len(q.URL.Query()) != 2 {
					t.Error("inspection request shape")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				state, code := "missing", "TOOL_MISSING"
				if scenario == "unqualified" {
					state, code = "unqualified", "INSPECTION_UNKNOWN"
				}
				generation := T.UUIDv4(q.URL.Query().Get("expectedRuntimeGeneration"))
				if scenario == "wrong-generation" {
					generation = T.UUIDv4(bridgeUUID(t))
				}
				wire, _ := T.Readable(T.InstallationInspectionResponse{APIVersion: 1, Scope: "installation-inspection", Result: T.InstallationInspectionResult{Identity: A.Identity{BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation)}, RuntimeGeneration: generation, State: T.InstallationInspectionState(state), Code: T.InstallationInspectionCode(code)}})
				if scenario == "malformed" {
					wire = []byte("PRIVATE_MALFORMED")
				}
				if scenario == "wrong-media" {
					w.Header().Set("Content-Type", "text/plain")
				}
				status, refusal := 0, ""
				switch scenario {
				case "unavailable":
					status, refusal = 503, "UNAVAILABLE"
				case "denied":
					status, refusal = 403, "FORBIDDEN"
				case "stale":
					status, refusal = 409, "CONTROL_STALE"
				case "busy":
					status, refusal = 409, "CONTROL_BUSY"
				case "invalid":
					status, refusal = 400, "INVALID_INPUT"
				}
				if status != 0 {
					wire, _ = T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: refusal, Message: "Request refused"}})
					w.WriteHeader(status)
				}
				if scenario == "lost-body" {
					w.Header().Set("Content-Length", "500")
					w.WriteHeader(200)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					if c, _, e := http.NewResponseController(w).Hijack(); e == nil {
						_ = c.Close()
					}
					return
				}
				_, _ = w.Write(wire)
			})}
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			defer func() {
				_ = server.Close()
				if e := <-done; e != http.ErrServerClosed {
					t.Error(e)
				}
			}()
			selection := []string{"--data", data, "--json"}
			before := p.call(selection, nil, 0, "", "board", "read")
			want, diagnostic := 3, "UNAVAILABLE"
			if scenario == "missing" || scenario == "unqualified" {
				want, diagnostic = 0, ""
			}
			switch scenario {
			case "unavailable":
				want, diagnostic = 2, "UNAVAILABLE"
			case "denied":
				want, diagnostic = 2, "FORBIDDEN"
			case "stale":
				want, diagnostic = 2, "CONTROL_STALE"
			case "busy":
				want, diagnostic = 2, "CONTROL_BUSY"
			case "invalid":
				want, diagnostic = 2, "INVALID_INPUT"
			}
			wire := p.call(selection, nil, want, diagnostic, "recipient", "tool", "inspect")
			if statusReads.Load() != 1 || inspections.Load() != 1 || bytes.Contains(wire, []byte("PRIVATE_MALFORMED")) || !bytes.Equal(before, p.call(selection, nil, 0, "", "board", "read")) {
				t.Fatal("controlled read replay/leak/mutation")
			}
		})
	}
}
