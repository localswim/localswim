package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestM074NativeCLIDispatchAvoidsLiveConfiguration(t *testing.T) {
	dataDir := t.TempDir()
	for _, args := range [][]string{
		{"board", "get", "--data", dataDir, t03Board},
		{"board", "export", "--data", dataDir, t03Board},
		{"card", "inspect", "--data", dataDir, "--prose", t03Board, "1"},
		{"card", "next", "--data", dataDir, t03Board},
		{"card", "search", "--data", dataDir, t03Board},
		{"comment", "list", "--data", dataDir, t03Board},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput(args, strings.NewReader(""), &out, &stderr, func(name string) string {
			t.Fatalf("%v attempted to read live configuration %s", args[:2], name)
			return ""
		})
		if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "could not read saved board") {
			t.Fatalf("%v did not report missing native data: code=%d, error=%s", args[:2], code, &stderr)
		}
	}
}

func TestM074NativeCLIDispatchRefusesLanesView(t *testing.T) {
	_, err := parseCLI([]string{"board", "get", "--view", "lanes", "--data", t.TempDir(), t03Board})
	if err == nil || !strings.Contains(err.Error(), "--view lanes requires a live board") {
		t.Fatalf("expected a live-only lanes refusal, got %v", err)
	}
}

func TestCLIBoardDataOptionRejectsExplicitlyEmptyPath(t *testing.T) {
	for _, action := range []string{"get", "export", "verify"} {
		for _, option := range [][]string{{"--data", ""}, {"--data="}} {
			args := append([]string{"board", action}, option...)
			args = append(args, t03Board)
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, func(key string) string {
				t.Fatalf("board %s with empty --data tried to read live configuration %s", action, key)
				return ""
			})
			if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "command options require nonempty values") {
				t.Fatalf("board %s %q returned exit=%d, stdout=%q, stderr=%q", action, option, exit, &stdout, &stderr)
			}
		}
	}
}
