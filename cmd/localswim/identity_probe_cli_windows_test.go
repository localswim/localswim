//go:build windows && amd64 && l02_native && m11_cli_native && m12_cli_native

package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/local"
)

const cliProbeGeneration = "85000000-0000-4000-8000-000000000001"
const cliForeignGeneration = "85000000-0000-4000-8000-000000000002"

type cliProbeScenario struct {
	stage, fault, phase string
	state, code         string
	httpStatus          int
}

func identityCLIWire(t *testing.T, value any) []byte {
	t.Helper()
	wire, err := T.Readable(value)
	if err != nil {
		t.Error("invalid controlled wire fixture")
	}
	return wire
}

// This fixture replaces every tool route and never calls the installed tool.
// The actual EXE still uses its ordinary local selector, descriptor, session,
// authorization and saved recipient policy paths.
func TestIdentityProbeActualCLIControlled(t *testing.T) {
	p, input := recipientCLIProof(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("owned fixture listener unavailable")
	}
	defer listener.Close()
	data := filepath.Join(p.root, "controlled")
	wid := bridgeUUID(t)
	engine, err := local.OpenEngine(t.Context(), local.EngineOptions{DataDir: data, Origin: "http://" + listener.Addr().String(), WorkspaceID: wid})
	if err != nil {
		t.Fatal("owned fixture engine unavailable")
	}
	defer engine.Close()
	info := activityReadFixture(t, data)
	identity := A.Identity{BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation)}
	mode := identityCLIWire(t, T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: T.UUIDv4(wid)})
	prefix := "/api/v1/boards/" + info.BoardID + "/recipient/"
	var policyReads, controls, posts, inspections, unexpected atomic.Int32
	var current atomic.Pointer[cliProbeScenario]
	var saved atomic.Pointer[T.RecipientView]
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/v1/runtime-mode" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(mode)
			return
		}
		scenario := current.Load()
		if path == prefix+"status" {
			policyReads.Add(1)
			if r.Method != http.MethodGet || r.ContentLength != 0 || r.URL.RawQuery != "incarnation="+info.Incarnation {
				t.Error("recipient status request changed")
			}
			if scenario != nil && scenario.stage == "policy" {
				v := saved.Load()
				if v == nil {
					t.Error("controlled policy response requested before fixture setup")
					return
				}
				identityCLIRespond(t, w, prefix, *scenario, identityCLIWire(t, T.RecipientResponse{APIVersion: 1, Scope: "recipient", Result: *v}))
				return
			}
		}
		if !strings.HasPrefix(path, prefix+"tool/") {
			engine.ServeHTTP(w, r)
			return
		}
		if scenario == nil || saved.Load() == nil {
			unexpected.Add(1)
			http.NotFound(w, r)
			return
		}
		v := saved.Load()
		runtime, config := v.RuntimeGeneration, *v.ConfigurationGeneration
		if r.Header.Get("Authorization") != "Bearer "+string(info.Token) || r.Header.Get("Accept") != "application/json" {
			t.Error("tool request did not retain the selected CLI credential and JSON negotiation")
		}
		generation := T.UUIDv4(cliProbeGeneration)
		respond := func(stage string, value any) {
			s := cliProbeScenario{}
			if scenario.stage == stage {
				s = *scenario
			}
			identityCLIRespond(t, w, prefix, s, identityCLIWire(t, value))
		}
		switch path {
		case prefix + "tool/inspect":
			inspections.Add(1)
			q := r.URL.Query()
			if r.Method != http.MethodGet || r.ContentLength != 0 || len(q) != 2 || len(q["incarnation"]) != 1 || len(q["expectedRuntimeGeneration"]) != 1 || q.Get("incarnation") != info.Incarnation || q.Get("expectedRuntimeGeneration") != string(runtime) {
				t.Error("metadata inspection protocol changed")
			}
			respond("inspect", T.InstallationInspectionResponse{APIVersion: 1, Scope: "installation-inspection", Result: T.InstallationInspectionResult{Identity: identity, RuntimeGeneration: runtime, State: "missing", Code: "TOOL_MISSING"}})
		case prefix + "tool/probe-control":
			controls.Add(1)
			q := r.URL.Query()
			if r.Method != http.MethodGet || r.ContentLength != 0 || len(r.URL.RawQuery) > T.ProbeControlQueryLimit || len(q) != 3 || len(q["incarnation"]) != 1 || len(q["expectedRuntimeGeneration"]) != 1 || len(q["expectedConfigurationGeneration"]) != 1 || q.Get("incarnation") != info.Incarnation || q.Get("expectedRuntimeGeneration") != string(runtime) || q.Get("expectedConfigurationGeneration") != string(config) {
				t.Error("unexpected control request")
			}
			phase := T.ProbeControlPhase("ready")
			if scenario.phase == "running" || scenario.phase == "draining" {
				phase = T.ProbeControlPhase(scenario.phase)
			}
			respond("control", T.ProbeControlResponse{APIVersion: 1, Scope: "identity-control", Result: T.ProbeControlView{Identity: identity, RuntimeGeneration: runtime, ConfigurationGeneration: config, ProbeGeneration: generation, Phase: phase, Joined: phase == "ready"}})
		case prefix + "tool/probe-version", prefix + "tool/stop":
			posts.Add(1)
			raw, err := io.ReadAll(io.LimitReader(r.Body, T.ProbeRequestLimit+1))
			request, decodeErr := T.Decode[T.ProbeRequest](raw)
			if err != nil || decodeErr != nil || r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" || request != (T.ProbeRequest{Incarnation: identity.Incarnation, ExpectedRuntimeGeneration: runtime, ExpectedConfigurationGeneration: config, ProbeGeneration: generation}) {
				t.Error("unexpected POST request")
			}
			if scenario.fault == "foreign-generation" {
				generation = cliForeignGeneration
			}
			if scenario.fault == "foreign-configuration" {
				config = cliForeignGeneration
			}
			if path == prefix+"tool/stop" {
				phase := T.ProbeStopPhase("joined")
				if scenario.phase == "incomplete" {
					phase = "incomplete"
				}
				respond("post", T.ProbeStopResponse{APIVersion: 1, Scope: "identity-stop", Result: T.ProbeStopView{Identity: identity, RuntimeGeneration: runtime, ConfigurationGeneration: config, ProbeGeneration: generation, Phase: phase, Joined: phase == "joined"}})
				return
			}
			state, code := scenario.state, scenario.code
			if state == "" {
				state, code = "supported", "OK"
			}
			result := T.ProbeResult{Version: 1, Identity: identity, RuntimeGeneration: runtime, ConfigurationGeneration: config, ProbeGeneration: generation, Kind: "identity", State: T.ProbeState(state), Code: T.ProbeCode(code), Joined: state != "incomplete"}
			if state == "supported" {
				version := T.ProbeToolVersion("1.2.3-test.1")
				result.ToolVersion = &version
			}
			respond("post", result)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		if e := <-done; e != http.ErrServerClosed {
			t.Error("owned fixture server did not close")
		}
	}()
	selection := []string{"--data", data, "--json"}
	stopArgs := []string{"--probe-generation", cliProbeGeneration}
	for _, action := range []string{"status", "probe-version", "stop"} {
		command := []string{"recipient", "tool", action}
		if action == "stop" {
			command = append(command, stopArgs...)
		}
		p.call(selection, nil, 3, "UNAVAILABLE", command...)
	}
	if policyReads.Load() != 3 || controls.Load()+posts.Load()+inspections.Load()+unexpected.Load() != 0 {
		t.Fatal("unconfigured recipient policy reached tool routes")
	}
	beforeBoard := p.call(selection, nil, 0, "", "board", "read")
	beforeNotices := p.call(selection, nil, 0, "", "notices", "status")
	beforePolicy := p.call(selection, nil, 0, "", "recipient", "configure", "--input", input)
	policy, err := T.Decode[T.RecipientResponse](beforePolicy)
	if err != nil || policy.Result.ConfigurationGeneration == nil {
		t.Fatal("controlled recipient setup failed")
	}
	saved.Store(&policy.Result)
	unknown := identityCLIWire(t, T.ProbeOutcomeUnknown{APIVersion: 1, Outcome: "unknown", Error: T.ProbeUnknownError{Code: "OUTCOME_UNKNOWN", Message: T.ProbeUnknownMessage}})

	check := func(t *testing.T, action string, s cliProbeScenario, want, controlCount, postCount int, diagnostic string, jsonMode bool) []byte {
		t.Helper()
		current.Store(&s)
		policyReads.Store(0)
		controls.Store(0)
		posts.Store(0)
		inspections.Store(0)
		args := selection
		if !jsonMode {
			args = selection[:2]
		}
		command := []string{"recipient", "tool", action}
		if action == "stop" {
			command = append(command, stopArgs...)
		}
		result := (activityCLIProof{t, p.binary, p.root}).call(args, nil, want, diagnostic, command...)
		wantInspect := 0
		if action == "inspect" {
			wantInspect = 1
		}
		if policyReads.Load() != 1 || controls.Load() != int32(controlCount) || posts.Load() != int32(postCount) || inspections.Load() != int32(wantInspect) || unexpected.Load() != 0 {
			t.Fatal("request sequence changed or replayed")
		}
		for _, leak := range []string{p.root, string(info.Token), string(recipientCLIInput), "PRIVATE_PROBE_DATA", "codex-cli ", "00000000-0000-7000-8000-000000000016"} {
			if bytes.Contains(result, []byte(leak)) {
				t.Fatal("actual CLI disclosed private fixture material")
			}
		}
		if diagnostic == "OUTCOME_UNKNOWN" {
			if jsonMode && !bytes.Equal(result, unknown) || !jsonMode && string(result) != T.ProbeUnknownMessage+"\n" {
				t.Fatal("actual CLI did not emit the exact client-only unknown output")
			}
		}
		if postCount == 0 && want != 0 && jsonMode {
			got, err := T.Decode[T.NonterminalRefusal](result)
			if err != nil || got.Error.Code != diagnostic || got.OpID != nil || got.Conflict != nil || got.Error.Field != nil {
				t.Fatal("preflight refusal disclosed facts or did not preserve its fixed code")
			}
		}
		if want == 2 && postCount == 1 && !jsonMode && string(result) != "Identity check request refused: "+diagnostic+".\n" {
			t.Fatal("readable POST refusal implied facts beyond the fixed refusal")
		}
		return result
	}

	for _, fault := range []string{"redirect", "encoding", "framing", "conflicting-transfer-length", "close-delimited", "declared-oversize", "wrong-type", "no-store-missing", "truncation"} {
		t.Run("W05/"+fault, func(t *testing.T) {
			check(t, "status", cliProbeScenario{stage: "control", fault: fault}, 3, 1, 0, "UNAVAILABLE", true)
			check(t, "probe-version", cliProbeScenario{stage: "control", fault: fault}, 3, 1, 0, "UNAVAILABLE", true)
			if fault == "conflicting-transfer-length" || fault == "close-delimited" {
				check(t, "stop", cliProbeScenario{stage: "policy", fault: fault}, 3, 0, 0, "UNAVAILABLE", true)
			}
			for _, action := range []string{"probe-version", "stop"} {
				n := 1
				if action == "stop" {
					n = 0
				}
				check(t, action, cliProbeScenario{stage: "post", fault: fault}, 4, n, 1, "OUTCOME_UNKNOWN", true)
			}
		})
	}
	refusals := []struct {
		status int
		code   string
	}{
		{400, "INVALID_INPUT"}, {401, "UNAUTHENTICATED"}, {403, "FORBIDDEN"},
		{404, "NOT_FOUND"}, {405, "INVALID_INPUT"}, {409, "CONTROL_STALE"},
		{409, "CONTROL_BUSY"}, {409, "IDENTITY_CHANGED"}, {503, "UNAVAILABLE"},
	}
	for _, pair := range refusals {
		t.Run("W07/"+pair.code, func(t *testing.T) {
			reply := cliProbeScenario{stage: "control", httpStatus: pair.status, code: pair.code}
			check(t, "probe-version", reply, 2, 1, 0, pair.code, true)
			reply.fault = "opid-null"
			check(t, "probe-version", reply, 3, 1, 0, "UNAVAILABLE", true)
			reply.stage, reply.fault = "policy", ""
			check(t, "stop", reply, 2, 0, 0, pair.code, true)
		})
		for _, action := range []string{"probe-version", "stop"} {
			caseID, want, diagnostic := "W09", 2, pair.code
			if pair.status == 503 {
				caseID, want, diagnostic = "W08", 4, "OUTCOME_UNKNOWN"
			}
			t.Run(caseID+"/"+action+"/"+pair.code, func(t *testing.T) {
				n := 1
				if action == "stop" {
					n = 0
				}
				for _, jsonMode := range []bool{true, false} {
					result := check(t, action, cliProbeScenario{stage: "post", httpStatus: pair.status, code: pair.code}, want, n, 1, diagnostic, jsonMode)
					if jsonMode && want == 2 {
						got, err := T.Decode[T.NonterminalRefusal](result)
						if err != nil || got.Error.Code != pair.code || got.Error.Message != "Request refused" || got.OpID != nil || got.Conflict != nil {
							t.Fatal("strict POST refusal not preserved")
						}
					}
				}
			})
		}
	}
	for _, fault := range []string{"lost", "malformed", "foreign-generation", "foreign-configuration", "wrong-pair", "unexpected-status", "over-limit", "duplicate-key"} {
		t.Run("W10/"+fault, func(t *testing.T) {
			check(t, "probe-version", cliProbeScenario{stage: "post", fault: fault}, 4, 1, 1, "OUTCOME_UNKNOWN", true)
			check(t, "stop", cliProbeScenario{stage: "post", fault: fault}, 4, 0, 1, "OUTCOME_UNKNOWN", true)
		})
	}
	t.Run("W11/metadata-only", func(t *testing.T) {
		out := check(t, "inspect", cliProbeScenario{}, 0, 0, 0, "", true)
		got, err := T.Decode[T.InstallationInspectionResponse](out)
		if err != nil || got.Result.Identity != identity || got.Result.RuntimeGeneration != policy.Result.RuntimeGeneration || got.Result.Code != "TOOL_MISSING" {
			t.Fatal("metadata inspection response changed")
		}
	})

	for _, phase := range []string{"ready", "running", "draining"} {
		t.Run("C10/status/"+phase, func(t *testing.T) {
			s := cliProbeScenario{phase: phase}
			for _, jsonMode := range []bool{true, false} {
				out := check(t, "status", s, 0, 1, 0, "", jsonMode)
				if !jsonMode {
					if !bytes.Contains(out, []byte(phase)) || !bytes.Contains(out, []byte(cliProbeGeneration)) {
						t.Fatal("readable control missing current observation")
					}
					continue
				}
				got, err := T.Decode[T.ProbeControlResponse](out)
				if err != nil || T.ValidateProbeControlContext(got, identity, policy.Result.RuntimeGeneration, *policy.Result.ConfigurationGeneration) != nil || string(got.Result.Phase) != phase {
					t.Fatal("validated current control missing")
				}
			}
			if phase != "ready" {
				check(t, "probe-version", s, 2, 1, 0, "CONTROL_BUSY", true)
			}
		})
	}
	for _, row := range []struct {
		state, code string
		exit        int
	}{
		{"supported", "OK", 0}, {"unsupported", "TOOL_UNSUPPORTED", 3}, {"unsupported", "PARSE_UNSUPPORTED", 3},
		{"unavailable", "TOOL_MISSING", 3}, {"unavailable", "PLATFORM_UNQUALIFIED", 3},
		{"unknown", "EXIT_FAILED", 4}, {"unknown", "PARSE_ERROR", 4}, {"unknown", "OUTPUT_LIMIT", 4},
		{"unknown", "TIMED_OUT", 4}, {"unknown", "CANCELLED", 4}, {"incomplete", "DRAIN_INCOMPLETE", 4},
	} {
		t.Run("C10/probe/"+row.code, func(t *testing.T) {
			diagnostic := row.code
			if row.exit == 0 {
				diagnostic = ""
			}
			for _, jsonMode := range []bool{true, false} {
				out := check(t, "probe-version", cliProbeScenario{stage: "post", state: row.state, code: row.code, fault: "exact-limit"}, row.exit, 1, 1, diagnostic, jsonMode)
				if bytes.Contains(out, []byte("1.2.3-test.1")) != (row.exit == 0) {
					t.Fatal("version disclosure did not match validated outcome")
				}
				if jsonMode {
					got, err := T.Decode[T.ProbeResult](out)
					if err != nil || T.ValidateProbeResultContext(got, identity, policy.Result.RuntimeGeneration, *policy.Result.ConfigurationGeneration, cliProbeGeneration) != nil || string(got.Code) != row.code {
						t.Fatal("validated probe result missing")
					}
				}
			}
		})
	}
	for _, phase := range []string{"joined", "incomplete"} {
		t.Run("C10/stop/"+phase, func(t *testing.T) {
			want, diagnostic := 0, ""
			if phase == "incomplete" {
				want, diagnostic = 4, "DRAIN_INCOMPLETE"
			}
			for _, jsonMode := range []bool{true, false} {
				out := check(t, "stop", cliProbeScenario{phase: phase}, want, 0, 1, diagnostic, jsonMode)
				if jsonMode {
					got, err := T.Decode[T.ProbeStopResponse](out)
					if err != nil || T.ValidateProbeStopContext(got, identity, policy.Result.RuntimeGeneration, *policy.Result.ConfigurationGeneration, cliProbeGeneration) != nil || string(got.Result.Phase) != phase {
						t.Fatal("validated Stop result missing")
					}
				}
			}
		})
	}
	t.Run("C10/invalid-arguments", func(t *testing.T) {
		policyReads.Store(0)
		controls.Store(0)
		posts.Store(0)
		for _, command := range [][]string{
			{"stop"}, {"stop", "--probe-generation", strings.ToUpper(cliProbeGeneration[:19] + "a" + cliProbeGeneration[20:])},
			{"stop", "--probe-generation", "85000000-0000-7000-8000-000000000001"},
			{"stop", "--probe-generation", cliProbeGeneration, "--probe-generation", cliProbeGeneration},
			{"status", "--probe-generation", cliProbeGeneration}, {"probe-version", "--path", p.root},
			{"probe-version", "--argv", "--version"}, {"probe-version", "--input", input},
		} {
			p.call(selection, nil, 1, "INVOCATION_OR_IO", append([]string{"recipient", "tool"}, command...)...)
		}
		if policyReads.Load()+controls.Load()+posts.Load() != 0 {
			t.Fatal("local syntax failure dispatched a request")
		}
	})
	if !bytes.Equal(beforeBoard, p.call(selection, nil, 0, "", "board", "read")) || !bytes.Equal(beforeNotices, p.call(selection, nil, 0, "", "notices", "status")) || !bytes.Equal(beforePolicy, p.call(selection, nil, 0, "", "recipient", "status")) {
		t.Fatal("controlled commands changed board, notices or saved recipient")
	}
}

func identityCLIRespond(t *testing.T, w http.ResponseWriter, prefix string, s cliProbeScenario, wire []byte) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if s.httpStatus != 0 {
		status = s.httpStatus
		wire = identityCLIWire(t, T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: s.code, Message: "Request refused"}})
	}
	switch s.fault {
	case "redirect":
		status = http.StatusTemporaryRedirect
		w.Header().Set("Location", prefix+"tool/unexpected")
	case "encoding":
		w.Header().Set("Content-Encoding", "gzip")
	case "wrong-type":
		w.Header().Set("Content-Type", "text/plain")
	case "no-store-missing":
		w.Header().Del("Cache-Control")
	case "conflicting-transfer-length", "close-delimited":
		conn, out, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error("cannot create raw framing fixture")
			return
		}
		defer conn.Close()
		fmt.Fprint(out, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nCache-Control: no-store\r\nConnection: close\r\n")
		if s.fault == "conflicting-transfer-length" {
			fmt.Fprintf(out, "Transfer-Encoding: chunked\r\nContent-Length: 1\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", len(wire), wire)
		} else {
			fmt.Fprint(out, "\r\n")
			_, _ = out.Write(wire)
		}
		_ = out.Flush()
		return
	case "declared-oversize", "truncation", "framing", "lost":
		conn, out, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error("cannot create malformed framing fixture")
			return
		}
		defer conn.Close()
		if s.fault != "lost" {
			n := len(wire) + 1
			if s.fault == "declared-oversize" {
				n = T.ProbeResponseLimit + 1
			}
			fmt.Fprintf(out, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nCache-Control: no-store\r\nConnection: close\r\nContent-Length: %d\r\n", n)
			if s.fault == "framing" {
				fmt.Fprintf(out, "Content-Length: %d\r\n", n+1)
			}
			fmt.Fprint(out, "\r\n")
			_, _ = out.Write(wire)
			_ = out.Flush()
		}
		return
	case "malformed":
		wire = []byte("PRIVATE_PROBE_DATA")
	case "opid-null":
		wire = append([]byte(`{"opId":null,`), bytes.TrimSpace(wire)[1:]...)
	case "duplicate-key":
		key := `{"version":1,`
		if bytes.Contains(wire, []byte(`"apiVersion"`)) {
			key = `{"apiVersion":1,`
		}
		wire = append([]byte(key), bytes.TrimSpace(wire)[1:]...)
	case "wrong-pair", "unexpected-status":
		status = http.StatusConflict
		if s.fault == "unexpected-status" {
			status = http.StatusTeapot
		}
		wire = identityCLIWire(t, T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "UNAVAILABLE", Message: "Request refused"}})
	case "over-limit", "exact-limit":
		n := T.ProbeResponseLimit
		if s.fault == "over-limit" {
			n++
		}
		wire = append(wire, bytes.Repeat([]byte(" "), n-len(wire))...)
	}
	w.WriteHeader(status)
	_, _ = w.Write(wire)
}
