package main

import (
	"bytes"
	"encoding/json"
	"net"
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
	if exit := runCLIWithInput([]string{"board", "create", "--name", "Verification fixture"}, strings.NewReader(""), &stdout, &stderr, getenv); exit != 0 {
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
