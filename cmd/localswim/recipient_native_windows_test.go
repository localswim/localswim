//go:build windows && amd64 && l02_native && m11_cli_native && m12_cli_native

package main

import (
	"bytes"
	"encoding/json"
	"github.com/localswim/localswim/internal/board/policyedit"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/local"
	"golang.org/x/sys/windows"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type recipientDropOutput struct{}

func (recipientDropOutput) Write([]byte) (int, error) { return 0, nil }

func TestRecipientActualCLIForeignSidecar(t *testing.T) {
	p, input := recipientCLIProof(t)
	source := filepath.Join(p.root, "source")
	r, e := local.Start(t.Context(), local.Options{DataDir: source, Port: bridgePort(t)})
	if e != nil {
		t.Fatal(e)
	}
	p.call([]string{"--data", source, "--json"}, nil, 0, "", "recipient", "configure", "--input", input)
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	sidecar, e := os.ReadFile(filepath.Join(source, "recipient-policy.json"))
	if e != nil {
		t.Fatal(e)
	}
	destination := filepath.Join(p.root, "destination")
	port := bridgePort(t)
	r, e = local.Start(t.Context(), local.Options{DataDir: destination, Port: port})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(destination, "recipient-policy.json"), sidecar, 0600); e != nil {
		t.Fatal(e)
	}
	r, e = local.Start(t.Context(), local.Options{DataDir: destination, Port: port})
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	args := []string{"--data", destination, "--json"}
	wire := p.call(args, nil, 0, "", "recipient", "status")
	view, e := T.Decode[T.RecipientResponse](wire)
	if e != nil || view.Result.State != "unavailable" || view.Result.Reason == nil || *view.Result.Reason != "foreign" || view.Result.Input != nil || bytes.Contains(wire, []byte("00000000-0000-7000-8000-000000000016")) {
		t.Fatal("foreign private binding projected")
	}
	p.call(args, nil, 3, "UNAVAILABLE", "recipient", "configure", "--input", input)
	after, e := os.ReadFile(filepath.Join(destination, "recipient-policy.json"))
	if e != nil || !bytes.Equal(after, sidecar) {
		t.Fatal("foreign sidecar repaired")
	}
}
func TestRecipientActualCLIInputAndOutputFaults(t *testing.T) {
	p, input := recipientCLIProof(t)
	data := filepath.Join(p.root, "board")
	r, e := local.Start(t.Context(), local.Options{DataDir: data, Port: bridgePort(t)})
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	args := []string{"--data", data, "--json"}
	for _, bad := range [][]byte{[]byte(`{}`), append([]byte{0xef, 0xbb, 0xbf}, recipientCLIInput...), bytes.Replace(recipientCLIInput, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), {0xff}} {
		if e = os.WriteFile(input, bad, 0600); e != nil {
			t.Fatal(e)
		}
		p.call(args, nil, 2, "INVALID_INPUT", "recipient", "configure", "--input", input)
	}
	if e = os.WriteFile(input, bytes.Repeat([]byte(" "), 1025), 0600); e != nil {
		t.Fatal(e)
	}
	p.call(args, nil, 1, "UNSAFE_LOCAL_MATERIAL", "recipient", "configure", "--input", input)
	if e = os.WriteFile(input, recipientCLIInput, 0600); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(p.root, "input-link.json")
	if e = os.Link(input, alias); e != nil {
		t.Fatal(e)
	}
	p.call(args, nil, 1, "UNSAFE_LOCAL_MATERIAL", "recipient", "configure", "--input", input)
	if e = os.Remove(alias); e != nil {
		t.Fatal(e)
	}
	p.call(args, nil, 1, "UNSAFE_LOCAL_MATERIAL", "recipient", "configure", "--input", p.root)
	p.call(args, nil, 1, "UNSAFE_LOCAL_MATERIAL", "recipient", "configure", "--input", "relative.json")
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(input, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	p.call(args, nil, 1, "UNSAFE_LOCAL_MATERIAL", "recipient", "configure", "--input", input)
	current, e := T.Decode[T.RecipientResponse](p.call(args, nil, 0, "", "recipient", "status"))
	if e != nil || current.Result.State != "unconfigured" {
		t.Fatal("invalid input mutated state")
	}
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "readable-output-failure", true: "json-output-failure"}[jsonMode], func(t *testing.T) {
			p, input := recipientCLIProof(t)
			data := filepath.Join(p.root, "output-board")
			r, e := local.Start(t.Context(), local.Options{DataDir: data, Port: bridgePort(t)})
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			args := []string{"--data", data}
			if jsonMode {
				args = append(args, "--json")
			}
			args = append(args, "recipient", "configure", "--input", input)
			var diagnostic bytes.Buffer
			if exit := run(t.Context(), args, nil, recipientDropOutput{}, &diagnostic); exit != 4 || diagnostic.String() != "OUTCOME_UNKNOWN\n" {
				t.Fatal("output failure downgraded saved original")
			}
			v, e := T.Decode[T.RecipientResponse](p.call([]string{"--data", data, "--json"}, nil, 0, "", "recipient", "status"))
			if e != nil || v.Result.State != "configured" {
				t.Fatal("pure status did not inspect saved original")
			}
		})
	}
}

var recipientCLIInput = []byte(`{"version":1,"recipient":{"kind":"codex-thread","threadId":"00000000-0000-7000-8000-000000000016"},"policy":"conservative-metadata-v1"}`)

func recipientCLIProof(t *testing.T) (activityCLIProof, string) {
	t.Helper()
	base, binary := os.Getenv("M12_CLI_ROOT"), os.Getenv("M12_CLI_BINARY")
	if !safeAbsolute(base) || !safeAbsolute(binary) {
		t.Fatal("explicit private root and actual binary required")
	}
	root, e := os.MkdirTemp(base, "recipient-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(root); e != nil {
			t.Error("owned recipient fixture cleanup failed")
		}
	})
	input := filepath.Join(root, "input.json")
	if e = os.WriteFile(input, recipientCLIInput, 0600); e != nil {
		t.Fatal(e)
	}
	return activityCLIProof{t, binary, root}, input
}
func TestRecipientActualBuiltCLI(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "default-workflow"
		if configured {
			name = "configured-workflow"
		}
		t.Run(name, func(t *testing.T) {
			p, input := recipientCLIProof(t)
			data := filepath.Join(p.root, "board")
			port := bridgePort(t)
			r, e := local.Start(t.Context(), local.Options{DataDir: data, Port: port})
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if e := r.Close(); e != nil {
					t.Error(e)
				}
			}()
			args := []string{"--data", data}
			jsonArgs := []string{"--data", data, "--json"}
			if configured {
				show, e := T.Decode[T.BoardRead](p.call(jsonArgs, nil, 0, "", "workflow", "show"))
				if e != nil {
					t.Fatal(e)
				}
				candidate := policyedit.DefaultDefinition("51000000-0000-4000-8000-000000000001", 2)
				candidate.Lanes[0].Label = "Recipient configured queue"
				edit := map[string]any{"incarnation": show.Result.Identity.Incarnation, "opId": "51000000-0000-4000-8000-000000000002", "target": nil, "expectedRevision": show.Result.Revision, "expectedCardRevision": nil, "input": map[string]any{"expectedPolicyVersion": 1, "workflow": candidate}}
				wire, _ := json.Marshal(edit)
				file := filepath.Join(p.root, "workflow.json")
				if os.WriteFile(file, wire, 0600) != nil {
					t.Fatal("workflow input")
				}
				p.call(jsonArgs, nil, 0, "", "workflow", "edit", "--input", file)
				after, e := T.Decode[T.BoardRead](p.call(jsonArgs, nil, 0, "", "workflow", "show"))
				if e != nil || after.Result.Workflow == nil || after.Result.PolicyVersion != 2 {
					t.Fatal("configured workflow missing")
				}
			}
			notices := p.call(jsonArgs, nil, 0, "", "notices", "status")
			board := p.call(jsonArgs, nil, 0, "", "board", "read")
			initial, e := T.Decode[T.RecipientResponse](p.call(jsonArgs, nil, 0, "", "recipient", "status"))
			if e != nil || initial.Result.State != "unconfigured" {
				t.Fatal("initial status")
			}
			saved, e := T.Decode[T.RecipientResponse](p.call(jsonArgs, nil, 0, "", "recipient", "configure", "--input", input))
			if e != nil || saved.Result.State != "configured" || saved.Result.Input == nil || string(saved.Result.Input.Recipient.ThreadID) != "00000000-0000-7000-8000-000000000016" {
				t.Fatal("actual save")
			}
			readable := p.call(args, nil, 0, "", "recipient", "status")
			if !bytes.Contains(readable, []byte("Saved; delivery is not activated")) || !bytes.Contains(readable, []byte(saved.Result.Input.Recipient.ThreadID)) {
				t.Fatal("readable status")
			}
			p.call(jsonArgs, nil, 2, "CONFIGURATION_FIXED", "recipient", "configure", "--input", input)
			p.call(args, nil, 2, "CONFIGURATION_FIXED", "recipient", "configure", "--input", input)
			afterInput, _ := os.ReadFile(input)
			if !bytes.Equal(afterInput, recipientCLIInput) || !bytes.Equal(notices, p.call(jsonArgs, nil, 0, "", "notices", "status")) || !bytes.Equal(board, p.call(jsonArgs, nil, 0, "", "board", "read")) {
				t.Fatal("input/notice/board conservation")
			}
			if e = r.Close(); e != nil {
				t.Fatal(e)
			}
			r, e = local.Start(t.Context(), local.Options{DataDir: data, Port: port})
			if e != nil {
				t.Fatal(e)
			}
			reopened, e := T.Decode[T.RecipientResponse](p.call(jsonArgs, nil, 0, "", "recipient", "status"))
			if e != nil || reopened.Result.RuntimeGeneration == saved.Result.RuntimeGeneration || *reopened.Result.ConfigurationGeneration != *saved.Result.ConfigurationGeneration || *reopened.Result.RequestID != *saved.Result.RequestID || *reopened.Result.Input != *saved.Result.Input {
				t.Fatal("reopen lost immutable configuration")
			}
			p.call(jsonArgs, recipientCLIInput, 1, "INVOCATION_OR_IO", "recipient", "configure", "--input", "-")
			p.call(jsonArgs, nil, 1, "INVOCATION_OR_IO", "recipient", "start")
		})
	}
}
func TestRecipientActualCLIControlledOutcomes(t *testing.T) {
	for _, scenario := range []string{"lost-after-save", "wrong-request200", "malformed200", "unknown200", "stale", "denied", "generic503"} {
		t.Run(scenario, func(t *testing.T) {
			p, input := recipientCLIProof(t)
			listener, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			data := filepath.Join(p.root, "board")
			wid := bridgeUUID(t)
			engine, e := local.OpenEngine(t.Context(), local.EngineOptions{DataDir: data, Origin: "http://" + listener.Addr().String(), WorkspaceID: wid})
			if e != nil {
				t.Fatal(e)
			}
			defer engine.Close()
			info := activityReadFixture(t, data)
			mode, _ := T.Readable(T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: T.UUIDv4(wid)})
			prefix := "/api/v1/boards/" + info.BoardID + "/recipient/"
			var gets, posts atomic.Int32
			server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.URL.Path == "/api/v1/runtime-mode" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(mode)
					return
				}
				if q.URL.Path == prefix+"status" {
					gets.Add(1)
				}
				if q.URL.Path == prefix+"configure" {
					posts.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					switch scenario {
					case "lost-after-save", "wrong-request200":
						rr := httptest.NewRecorder()
						engine.ServeHTTP(rr, q)
						if rr.Code != 200 {
							t.Error("controlled real save failed")
							w.WriteHeader(500)
							return
						}
						if scenario == "lost-after-save" {
							conn, _, e := http.NewResponseController(w).Hijack()
							if e == nil {
								_ = conn.Close()
							}
							return
						}
						v, e := T.Decode[T.RecipientResponse](rr.Body.Bytes())
						if e != nil {
							t.Error(e)
							return
						}
						other := T.UUIDv4(bridgeUUID(t))
						v.Result.RequestID = &other
						wire, _ := T.Readable(v)
						_, _ = w.Write(wire)
						return
					case "stale", "denied":
						code, status := "CONTROL_STALE", 409
						if scenario == "denied" {
							code, status = "FORBIDDEN", 403
						}
						wire, _ := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}})
						w.WriteHeader(status)
						_, _ = w.Write(wire)
						return
					case "unknown200":
						rr := httptest.NewRecorder()
						read := q.Clone(q.Context())
						read.Method = "GET"
						read.URL.Path = prefix + "status"
						read.URL.RawQuery = "incarnation=" + info.Incarnation
						read.Body = nil
						engine.ServeHTTP(rr, read)
						_, _ = w.Write(rr.Body.Bytes())
						return
					case "malformed200":
						_, _ = io.WriteString(w, "PRIVATE_MALFORMED")
						return
					case "generic503":
						w.WriteHeader(503)
						_, _ = io.WriteString(w, `{}`)
						return
					}
				}
				engine.ServeHTTP(w, q)
			})}
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			defer func() {
				_ = server.Close()
				if e := <-done; e != http.ErrServerClosed {
					t.Error(e)
				}
			}()
			args := []string{"--data", data, "--json"}
			want, code := 4, "OUTCOME_UNKNOWN"
			if scenario == "stale" {
				want, code = 2, "CONTROL_STALE"
			}
			if scenario == "denied" {
				want, code = 2, "FORBIDDEN"
			}
			wire := p.call(args, nil, want, code, "recipient", "configure", "--input", input)
			if gets.Load() != 1 || posts.Load() != 1 || bytes.Contains(wire, []byte("00000000-0000-7000-8000-000000000016")) {
				t.Fatal("replay or private ambiguous output")
			}
			if want == 4 {
				if _, e := T.Decode[T.RecipientOutcomeUnknown](wire); e != nil {
					t.Fatal(e)
				}
			}
			status, e := T.Decode[T.RecipientResponse](p.call(args, nil, 0, "", "recipient", "status"))
			if e != nil || posts.Load() != 1 {
				t.Fatal("status replayed")
			}
			if scenario == "lost-after-save" || scenario == "wrong-request200" {
				if status.Result.State != "configured" {
					t.Fatal("pure positive inspection missing")
				}
			} else if status.Result.State != "unconfigured" {
				t.Fatal("controlled no-save changed state")
			}
		})
	}
}
