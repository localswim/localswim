package core

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	T "github.com/localswim/localswim/internal/contract/transport"
)

type inspectionUnreadInput struct{}

func (inspectionUnreadInput) Read([]byte) (int, error) { panic("inspection read stdin") }

func inspectionWire(t *testing.T, state, code string) []byte {
	t.Helper()
	v := T.InstallationInspectionResponse{APIVersion: 1, Scope: "installation-inspection", Result: T.InstallationInspectionResult{Identity: recipientTestView().Identity, RuntimeGeneration: testGrant, State: T.InstallationInspectionState(state), Code: T.InstallationInspectionCode(code)}}
	wire, err := T.Readable(v)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func inspectionStatus(t *testing.T) []byte {
	t.Helper()
	v := recipientTestView()
	v.State, v.ConfigurationRevision = "configured", 1
	generation, request := T.UUIDv4(testBoard), T.UUIDv4(testActor)
	input := recipientTestInput
	v.ConfigurationGeneration, v.RequestID, v.Input = &generation, &request, &input
	wire, err := T.Readable(T.RecipientResponse{APIVersion: 1, Scope: "recipient", Result: v})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func inspectionRunner(t *testing.T, call func(*http.Request) (int, []byte, error)) (Runner, *memoryEvidence) {
	t.Helper()
	evidence := &memoryEvidence{}
	return Runner{Provider: &provider{conn: recipientTestConnection(t, call)}, Evidence: evidence}, evidence
}

func assertInspectionRead(t *testing.T, r *http.Request, status bool) {
	t.Helper()
	if r.Method != http.MethodGet || r.Body != nil || r.GetBody != nil {
		t.Fatal("inspection must only issue bodyless, non-replayable GETs")
	}
	if status {
		if r.URL.RequestURI() != recipientPath(selected, "status") {
			t.Fatal("wrong status route")
		}
		return
	}
	q := r.URL.Query()
	if r.URL.Path != recipientPath(selected, "tool/inspect") || len(q) != 2 || len(q["incarnation"]) != 1 || q.Get("incarnation") != testInc || len(q["expectedRuntimeGeneration"]) != 1 || q.Get("expectedRuntimeGeneration") != testGrant {
		t.Fatal("inspection did not bind the one status observation")
	}
}

func TestInstallationInspectionGrammar(t *testing.T) {
	q, code, exit := parse(args("recipient", "tool", "inspect"))
	if code != "" || exit != 0 || q.kind != installationInspectKind || q.method != http.MethodGet || q.input != "" {
		t.Fatal("valid inspect grammar")
	}
	for _, tail := range [][]string{{"recipient", "tool"}, {"recipient", "tool", "version"}, {"recipient", "tool", "inspect", "extra"}, {"recipient", "tool", "inspect", "operation", "read"}, {"recipient", "tool", "version", "operation", "read"}, {"recipient", "tool", "inspect", "--input", "-"}, {"recipient", "tool", "inspect", "--path", "private"}, {"recipient", "tool", "inspect", "--version", "1"}, {"recipient", "tool", "inspect", "--native-owner"}, {"recipient", "tool", "inspect", "--json"}} {
		t.Run(strings.Join(tail, " "), func(t *testing.T) {
			p := &provider{}
			var out, diagnostic bytes.Buffer
			if got := (Runner{Provider: p}).Run(t.Context(), args(tail...), inspectionUnreadInput{}, &out, &diagnostic); got != 1 || p.opened != 0 {
				t.Fatal("invalid grammar reached provider")
			}
		})
	}
}

func TestInstallationInspectionStatesAndPrivacy(t *testing.T) {
	for _, pair := range [][2]string{{"candidate", "CANDIDATE_PRESENT"}, {"missing", "TOOL_MISSING"}, {"unqualified", "PLATFORM_UNQUALIFIED"}, {"unqualified", "SOURCE_UNQUALIFIED"}, {"unqualified", "PATH_UNQUALIFIED"}, {"unqualified", "PUBLISHER_UNQUALIFIED"}, {"unqualified", "FILE_CHANGED"}, {"unqualified", "INSPECTION_UNKNOWN"}, {"unqualified", "INSPECTION_TIMED_OUT"}} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(pair[1]+map[bool]string{false: "/readable", true: "/json"}[jsonMode], func(t *testing.T) {
				calls := 0
				wire := inspectionWire(t, pair[0], pair[1])
				runner, evidence := inspectionRunner(t, func(r *http.Request) (int, []byte, error) {
					calls++
					assertInspectionRead(t, r, calls == 1)
					if calls == 1 {
						return 200, inspectionStatus(t), nil
					}
					return 200, wire, nil
				})
				argv := prefix()[1:]
				if jsonMode {
					argv = prefix()
				}
				var out, diagnostic bytes.Buffer
				if exit := runner.Run(t.Context(), append(argv, "recipient", "tool", "inspect"), inspectionUnreadInput{}, &out, &diagnostic); exit != 0 || calls != 2 || diagnostic.Len() != 0 {
					t.Fatalf("exit/calls %d/%d", exit, calls)
				}
				if jsonMode && !bytes.Equal(out.Bytes(), wire) {
					t.Fatal("JSON observation changed")
				}
				if !jsonMode && (!strings.Contains(out.String(), testBoard+"/"+testInc) || !strings.Contains(out.String(), pair[1])) {
					t.Fatal("readable identity/code missing")
				}
				if strings.Contains(out.String(), string(recipientTestInput.Recipient.ThreadID)) || strings.Contains(out.String(), "SYNTHETIC_SECRET") || strings.Contains(out.String(), "conservative-metadata") || evidence.saved+evidence.loaded+evidence.resolved != 0 {
					t.Fatal("inspection leaked policy or touched original evidence")
				}
			})
		}
	}
}

func TestInstallationInspectionRefusalsAndInvalidBodies(t *testing.T) {
	valid := inspectionWire(t, "candidate", "CANDIDATE_PRESENT")
	for _, scenario := range []string{"invalid", "unauthenticated", "denied", "stale", "busy", "identity", "unavailable", "not-found", "method", "wrong-pair", "private-message", "opid-null", "wrong-board", "wrong-incarnation", "wrong-generation", "wrong-state-code", "unknown-key", "duplicate-key", "null", "bom", "invalid-utf8", "trailing", "oversized", "lost"} {
		t.Run(scenario, func(t *testing.T) {
			calls, want := 0, 3
			runner, evidence := inspectionRunner(t, func(r *http.Request) (int, []byte, error) {
				calls++
				assertInspectionRead(t, r, calls == 1)
				if calls == 1 {
					return 200, inspectionStatus(t), nil
				}
				refusals := map[string]struct {
					status int
					code   string
				}{"invalid": {400, "INVALID_INPUT"}, "unauthenticated": {401, "UNAUTHENTICATED"}, "denied": {403, "FORBIDDEN"}, "stale": {409, "CONTROL_STALE"}, "busy": {409, "CONTROL_BUSY"}, "identity": {409, "IDENTITY_CHANGED"}, "unavailable": {503, "UNAVAILABLE"}, "not-found": {404, "NOT_FOUND"}, "method": {405, "INVALID_INPUT"}}
				if refusal, ok := refusals[scenario]; ok {
					want = 2
					wire, err := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: refusal.code, Message: "Request refused"}})
					if err != nil {
						t.Fatal(err)
					}
					return refusal.status, wire, nil
				}
				wire := append([]byte(nil), valid...)
				switch scenario {
				case "wrong-pair", "private-message", "opid-null":
					message := "Request refused"
					if scenario == "private-message" {
						message = "PRIVATE_VALUE"
					}
					wire, _ = T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "CONTROL_BUSY", Message: message}})
					if scenario == "opid-null" {
						wire = bytes.Replace(wire, []byte(`"outcome":`), []byte(`"opId":null,"outcome":`), 1)
					}
					if scenario == "wrong-pair" {
						return 400, wire, nil
					}
					return 409, wire, nil
				case "wrong-board":
					wire = bytes.Replace(wire, []byte(testBoard), []byte(testActor), 1)
				case "wrong-incarnation":
					wire = bytes.Replace(wire, []byte(testInc), []byte(testActor), 1)
				case "wrong-generation":
					wire = bytes.Replace(wire, []byte(testGrant), []byte(testActor), 1)
				case "wrong-state-code":
					wire = bytes.Replace(wire, []byte(`"candidate"`), []byte(`"missing"`), 1)
				case "unknown-key":
					wire = bytes.Replace(wire, []byte(`"apiVersion":`), []byte(`"privatePath":"PRIVATE_VALUE","apiVersion":`), 1)
				case "duplicate-key":
					wire = bytes.Replace(wire, []byte(`"apiVersion":`), []byte(`"apiVersion":1,"apiVersion":`), 1)
				case "null":
					wire = []byte(`null`)
				case "bom":
					wire = append([]byte{0xef, 0xbb, 0xbf}, wire...)
				case "invalid-utf8":
					wire = []byte{0xff}
				case "trailing":
					wire = append(wire, []byte(`{}`)...)
				case "oversized":
					wire = append(wire, bytes.Repeat([]byte(" "), T.InstallationInspectionResponseLimit)...)
				case "lost":
					return 0, nil, errors.New("PRIVATE_VALUE")
				}
				return 200, wire, nil
			})
			var out, diagnostic bytes.Buffer
			exit := runner.Run(t.Context(), args("recipient", "tool", "inspect"), inspectionUnreadInput{}, &out, &diagnostic)
			if exit != want || calls != 2 || evidence.saved+evidence.loaded+evidence.resolved != 0 || strings.Contains(out.String()+diagnostic.String(), "PRIVATE_VALUE") || strings.Contains(out.String(), "CANDIDATE_PRESENT") {
				t.Fatalf("failure semantics: exit=%d want=%d calls=%d", exit, want, calls)
			}
		})
	}
}

func TestInstallationStatusFailureStopsAndOutputNeverRetries(t *testing.T) {
	for _, statusFailure := range []bool{false, true} {
		for _, jsonMode := range []bool{false, true} {
			calls := 0
			runner, evidence := inspectionRunner(t, func(r *http.Request) (int, []byte, error) {
				calls++
				if calls == 1 {
					if statusFailure {
						return 200, []byte(`{}`), nil
					}
					return 200, inspectionStatus(t), nil
				}
				return 200, inspectionWire(t, "missing", "TOOL_MISSING"), nil
			})
			argv := prefix()[1:]
			if jsonMode {
				argv = prefix()
			}
			var diagnostic bytes.Buffer
			output := &exactWriter{n: 0}
			if jsonMode {
				output.err = io.ErrClosedPipe
			}
			if exit := runner.Run(t.Context(), append(argv, "recipient", "tool", "inspect"), inspectionUnreadInput{}, output, &diagnostic); exit != 3 || calls != map[bool]int{false: 2, true: 1}[statusFailure] || output.calls != 1 || evidence.saved+evidence.loaded+evidence.resolved != 0 {
				t.Fatal("failed read/output retried or became mutation uncertainty")
			}
		}
	}
}
