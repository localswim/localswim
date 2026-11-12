package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/v2"
)

func TestM96BackupVerifyDispatchesOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloaded backup.zip")
	if err := os.WriteFile(path, []byte("invalid ZIP"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	getenv := func(name string) string {
		t.Errorf("offline verification checked %s", name)
		return ""
	}
	code := runCLIWithInput([]string{"backup", "verify", path}, nil, &stdout, &stderr, getenv)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "integrity") {
		t.Fatalf("expected local integrity refusal, got exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	for _, args := range [][]string{
		{"backup", "verify"},
		{"backup", "verify", path, path},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	if !strings.Contains(cliHelp, "backup verify <FILE|->") {
		t.Error("help omitted the local backup command")
	}
}

func TestBackupVerifyStdinAndCleanup(t *testing.T) {
	human := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("h"), 32))
	bot := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("b"), 32))
	app, err := v2.Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": human}[key]
	}
	var out, stderr bytes.Buffer
	if code := runCLIWithInput([]string{"board", "create", "--name", "Stdin verification"}, nil, &out, &stderr, getenv); code != 0 {
		t.Fatal(&stderr)
	}
	var board struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runCLIWithInput([]string{"backup", "create", board.ID}, nil, &out, &stderr, getenv); code != 0 {
		t.Fatal(&stderr)
	}
	var backup struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out.Bytes(), &backup); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.zip")
	out.Reset()
	if code := runCLIWithInput([]string{"backup", "download", board.ID, backup.ID, path}, nil, &out, &stderr, getenv); code != 0 {
		t.Fatal(&stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, spool)
	}
	offline := func(string) string { t.Fatal("offline verification consulted live configuration"); return "" }
	var expected []byte
	for _, tc := range []struct {
		name, path string
		input      io.Reader
		code       int
		errorText  string
	}{
		{"file", path, nil, 0, ""},
		{"stdin", "-", bytes.NewReader(data), 0, ""},
		{"malformed", "-", strings.NewReader("invalid ZIP"), 1, "integrity"},
		{"input failure", "-", &attachmentErrorReader{}, 1, "standard input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out.Reset()
			stderr.Reset()
			code := runCLIWithInput([]string{"backup", "verify", tc.path}, tc.input, &out, &stderr, offline)
			if code != tc.code || (tc.errorText != "" && !strings.Contains(stderr.String(), tc.errorText)) {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, &out, &stderr)
			}
			if code == 0 {
				if expected == nil {
					expected = bytes.Clone(out.Bytes())
				}
				if !bytes.Equal(expected, out.Bytes()) {
					t.Fatal("stdin and file results differ")
				}
			} else if out.Len() != 0 {
				t.Fatal("failed verification wrote a success result")
			}
			files, err := os.ReadDir(spool)
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary input remains: %v, %v", files, err)
			}
		})
	}
	if err := verifyBackupFile("-", bytes.NewReader(data), exportFailWriter{}); err == nil {
		t.Fatal("stdout failure was ignored")
	}
	files, err := os.ReadDir(spool)
	if err != nil || len(files) != 0 {
		t.Fatalf("stdout failure leaked temporary input: %v, %v", files, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("verification changed source archive")
	}
}
