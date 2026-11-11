package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{"backup", "verify", "-"},
		{"backup", "verify", path, path},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	if !strings.Contains(cliHelp, "backup verify FILE") {
		t.Error("help omitted the local backup command")
	}
}
