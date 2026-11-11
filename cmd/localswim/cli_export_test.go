package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/v2"
)

func TestExportRequiresExplicitDestination(t *testing.T) {
	for _, args := range [][]string{
		{"board", "export", t03Board},
		{"board", "export", t03Board, ""},
		{"board", "export", t03Board, "-", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		code := runCLIWithInput(args, nil, &stdout, &stderr, func(string) string {
			t.Fatal("invalid export reached execution")
			return ""
		})
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "FILE or -") {
			t.Fatalf("invalid destination accepted: %v, %d, %s", args, code, &stderr)
		}
	}
}

func TestExportFileAndStdoutLiveAndOffline(t *testing.T) {
	root := t.TempDir()
	human := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("h"), 32))
	bot := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("b"), 32))
	app, err := v2.Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	defer server.Close()
	defer app.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/2026-11-10/boards", strings.NewReader(`{"name":"Export café"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+human)
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("create: %s, %v", raw, err)
	}
	var board struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &board); err != nil {
		t.Fatal(err)
	}
	env := func(key string) string {
		return map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": bot}[key]
	}
	var expected []byte
	for _, offline := range []bool{false, true} {
		if offline {
			server.Close()
			if err := app.Close(); err != nil {
				t.Fatal(err)
			}
		}
		args := []string{"board", "export"}
		getenv := env
		if offline {
			args = append(args, "--data", root)
			getenv = func(string) string { t.Fatal("offline export read live config"); return "" }
		}
		args = append(args, board.ID)
		var stdout, stderr bytes.Buffer
		if code := runCLIWithInput(append(args, "-"), nil, &stdout, &stderr, getenv); code != 0 || stderr.Len() != 0 {
			t.Fatalf("stdout export: %d, %s", code, &stderr)
		}
		if !json.Valid(stdout.Bytes()) || !strings.Contains(stdout.String(), "Export café") || !bytes.HasSuffix(stdout.Bytes(), []byte("\n")) {
			t.Fatalf("invalid UTF-8 JSON export: %s", &stdout)
		}
		if expected == nil {
			expected = bytes.Clone(stdout.Bytes())
		}
		if !bytes.Equal(expected, stdout.Bytes()) {
			t.Fatal("offline output differs")
		}
		path := filepath.Join(t.TempDir(), "board café.json")
		stdout.Reset()
		if code := runCLIWithInput(append(args, path), nil, &stdout, &stderr, getenv); code != 0 || stdout.Len() != 0 {
			t.Fatalf("file export: %d, %s", code, &stderr)
		}
		saved, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(saved, expected) {
			t.Fatalf("file output differs: %v", err)
		}
		if code := runCLIWithInput(append(args, path), nil, &stdout, &stderr, getenv); code != 1 {
			t.Fatal("existing export was overwritten")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, expected) {
			t.Fatal("existing file changed")
		}
	}
}

type exportFailWriter struct{}

func (exportFailWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestExportFailuresDoNotCreateDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	env := func(key string) string {
		return map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}[key]
	}
	path := filepath.Join(t.TempDir(), "failed.json")
	var stdout, stderr bytes.Buffer
	code := runCLIWithInput([]string{"board", "export", t03Board, path}, nil, &stdout, &stderr, env)
	if code != 1 {
		t.Fatal("failed read succeeded")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed read created destination")
	}
	success := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true}`)) }))
	defer success.Close()
	env = func(key string) string {
		return map[string]string{"LOCALSWIM_URL": success.URL, "LOCALSWIM_TOKEN": "test-token"}[key]
	}
	stderr.Reset()
	code = runCLIWithInput([]string{"board", "export", t03Board, "-"}, nil, exportFailWriter{}, &stderr, env)
	if code != 1 || !strings.Contains(stderr.String(), "standard output") {
		t.Fatalf("output failure: %d, %s", code, &stderr)
	}
}
