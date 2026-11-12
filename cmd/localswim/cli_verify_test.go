package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/v2"
)

func TestCLIBoardVerifyReportsAndExitCodes(t *testing.T) {
	dataDir := t.TempDir()
	humanToken := strings.Repeat("A", 43)
	botToken := strings.Repeat("B", 42) + "A"
	service, err := v2.Open(dataDir, humanToken, botToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = service.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)
	env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": humanToken}
	getenv := func(key string) string { return env[key] }

	var stdout, stderr bytes.Buffer
	if exit := runCLIWithInput([]string{"board", "create", "name", "Verification fixture"}, strings.NewReader(""), &stdout, &stderr, getenv); exit != 0 {
		t.Fatalf("create fixture exited %d: %s", exit, &stderr)
	}
	var board struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &board); err != nil || board.ID == "" {
		t.Fatalf("read fixture ID: %v", err)
	}
	snapshot := filepath.Join(dataDir, "boards", board.ID+".json")
	validSnapshot, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	for _, fixture := range []struct {
		name      string
		snapshot  []byte
		valid     bool
		exit      int
		wantIssue string
	}{
		{"valid", validSnapshot, true, 0, ""},
		{"corrupt", []byte("{"), false, 1, "integrity_failure"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if err := os.WriteFile(snapshot, fixture.snapshot, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []struct {
				name string
				args []string
				env  func(string) string
			}{
				{"live", []string{"board", "verify", board.ID}, getenv},
				{"offline", []string{"board", "verify", "--data", dataDir, board.ID}, func(string) string { return "" }},
			} {
				t.Run(mode.name, func(t *testing.T) {
					var stdout, stderr bytes.Buffer
					exit := runCLIWithInput(mode.args, strings.NewReader(""), &stdout, &stderr, mode.env)
					if exit != fixture.exit || (stderr.Len() != 0) == fixture.valid {
						t.Fatalf("verification exited %d: %s", exit, &stderr)
					}
					var report struct {
						BoardID  string `json:"board_id"`
						At       string `json:"at"`
						Valid    *bool  `json:"valid"`
						Revision *int   `json:"revision"`
						Issues   []struct {
							Field string `json:"field"`
							Code  string `json:"code"`
						} `json:"issues"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &report); err != nil ||
						report.BoardID != board.ID || report.At == "" ||
						report.Valid == nil || *report.Valid != fixture.valid || report.Issues == nil {
						t.Fatalf("incomplete verification report: %s", &stdout)
					}
					if fixture.valid {
						if report.Revision == nil || *report.Revision != 1 || len(report.Issues) != 0 {
							t.Fatalf("invalid success report: %s", &stdout)
						}
					} else if report.Revision != nil || len(report.Issues) != 1 ||
						report.Issues[0].Field != "snapshot" || report.Issues[0].Code != fixture.wantIssue {
						t.Fatalf("invalid failure report: %s", &stdout)
					}
				})
			}
			current, err := os.ReadFile(snapshot)
			if err != nil || !bytes.Equal(current, fixture.snapshot) {
				t.Fatalf("verification modified snapshot: %v", err)
			}
		})
	}
}

func TestCLIBoardVerifyRejectsUnusableLiveReports(t *testing.T) {
	const report = `{"board_id":"` + t03Board + `","at":"2026-11-10 14:44:56+00:00","valid":true,"revision":1,"issues":[]}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"verdict only", `{"valid":true}`},
		{"wrong board", strings.Replace(report, t03Board, "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", 1)},
		{"null verdict", strings.Replace(report, `"valid":true`, `"valid":null`, 1)},
		{"invalid timestamp", strings.Replace(report, "2026-11-10", "2026-02-30", 1)},
		{"missing revision", strings.Replace(report, `"revision":1,`, "", 1)},
		{"string revision", strings.Replace(report, `"revision":1`, `"revision":"1"`, 1)},
		{"unsafe revision", strings.Replace(report, `"revision":1`, `"revision":9007199254740992`, 1)},
		{"null issues", strings.Replace(report, `"issues":[]`, `"issues":null`, 1)},
		{"incomplete issue", strings.Replace(report, `"issues":[]`, `"issues":[{"field":"snapshot"}]`, 1)},
		{"duplicate verdict", strings.Replace(report, `"valid":true`, `"valid":false,"valid":true`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/"+v2.Version+"/verifications/"+t03Board {
					t.Error("verification used the wrong method or route")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput([]string{"board", "verify", t03Board}, strings.NewReader(""), &stdout, &stderr,
				func(key string) string { return env[key] })
			if exit != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "complete verification report for the requested board") {
				t.Fatalf("unusable report was not refused: exit=%d, stdout=%q, stderr=%q", exit, &stdout, &stderr)
			}
		})
	}
}

func TestCLIBoardVerifyChecksLiveHTTPResponse(t *testing.T) {
	const report = `{"board_id":"` + t03Board + `","at":"2026-11-10 14:44:56+00:00","valid":true,"revision":1,"issues":[]}`
	for _, tc := range []struct {
		name      string
		status    int
		mediaType string
		wantError string
	}{
		{"unexpected success status", http.StatusAccepted, "application/json", "unexpected HTTP 202"},
		{"wrong media type", http.StatusOK, "text/plain", "did not return an application/json response"},
		{"JSON with parameters", http.StatusOK, "application/json; charset=utf-8", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.mediaType)
				w.WriteHeader(tc.status)
				w.Write([]byte(report))
			}))
			t.Cleanup(server.Close)
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput([]string{"board", "verify", t03Board}, strings.NewReader(""), &stdout, &stderr,
				func(key string) string { return env[key] })
			if tc.wantError != "" {
				if exit != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.wantError) {
					t.Fatalf("unexpected response was not refused: exit=%d, stdout=%q, stderr=%q", exit, &stdout, &stderr)
				}
				return
			}
			if exit != 0 || stderr.Len() != 0 || !equalCLIJSON(t, stdout.Bytes(), []byte(report)) {
				t.Fatalf("expected response was not preserved: exit=%d, stdout=%q, stderr=%q", exit, &stdout, &stderr)
			}
		})
	}
}

func TestM95CLIVerificationRecoveryPoint(t *testing.T) {
	const report = `{"board_id":"` + t03Board + `","at":"2026-11-10 14:44:56+00:00","valid":false,"revision":null,"issues":[{"field":"snapshot","code":"integrity_failure"}],"recovery":`
	for _, tc := range []struct {
		name, recovery string
		accepted       bool
	}{
		{"same ID", `{"mode":"same_id","revision_floor":2}`, true},
		{"fresh only", `{"mode":"fresh_only","revision_floor":null}`, true},
		{"missing floor", `{"mode":"same_id"}`, false},
		{"exhausted revision", `{"mode":"same_id","revision_floor":9007199254740991}`, false},
		{"contradictory floor", `{"mode":"fresh_only","revision_floor":2}`, false},
		{"private data", `{"mode":"same_id","revision_floor":2,"restore_actors":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := report + tc.recovery + "}"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput([]string{"board", "verify", t03Board}, strings.NewReader(""), &stdout, &stderr,
				func(key string) string { return env[key] })
			if tc.accepted {
				if exit != 1 || !strings.Contains(stderr.String(), "board verification failed") ||
					!equalCLIJSON(t, stdout.Bytes(), []byte(body)) {
					t.Fatal("CLI did not preserve the valid recovery report and failed snapshot verdict")
				}
			} else if exit != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "complete verification report") {
				t.Fatal("CLI accepted a malformed recovery report")
			}
		})
	}
}
