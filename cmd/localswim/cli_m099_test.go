package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestM099HelpDiscovery(t *testing.T) {
	cases := []struct {
		args   []string
		want   string
		absent string
	}{
		{nil, "localswim board create", ""},
		{[]string{"--help"}, "localswim attachment upload", ""},
		{[]string{"board"}, "localswim board create", "localswim card create"},
		{[]string{"board", "--help"}, "localswim board create", "localswim attachment upload"},
		{[]string{"help", "board", "create"}, "json <JSON_FILE|->", "localswim board patch"},
		{[]string{"card", "inspect", "-h"}, "--prose", "localswim card create"},
		{[]string{"card", "set-parent", "--help"}, "child card revision", "localswim card clear-parent"},
		{[]string{"card", "clear-parent", "--help"}, "child card revision", "localswim card set-parent"},
		{[]string{"attachment", "upload", "--help"}, "10 MiB", "localswim attachment detach"},
		{[]string{"board", "notifications", "--help"}, "localswim board notifications target", "localswim board create"},
		{[]string{"board", "notifications", "target", "--help"}, "notification routing revision", "localswim board notifications enable"},
		{[]string{"workspace", "publishing", "-h"}, "localswim workspace publishing enable", "localswim board publishing"},
		{[]string{"workspace", "pin", "set", "--help"}, "workspace revision", "localswim workspace pin add"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput(tc.args, nil, &stdout, &stderr, func(string) string {
				t.Fatal("help reached execution")
				return ""
			})
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), tc.want) || (tc.absent != "" && strings.Contains(stdout.String(), tc.absent)) {
				t.Fatalf("unexpected help: code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
		})
	}
}

func TestM099EveryCommandHasScopedUsage(t *testing.T) {
	for _, line := range commandUsageLines() {
		name := usageCommand(line)
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(strings.Fields(name), "--help")
			code := runCLIWithInput(args, nil, &stdout, &stderr, func(string) string {
				t.Fatal("help reached execution")
				return ""
			})
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), line) || !strings.Contains(stdout.String(), "Examples:") {
				t.Fatalf("missing command help: code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
		})
	}
}

func TestM099InvalidInvocationIsScopedAndDoesNotExecute(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"}, {"unknown", "--help"}, {"help", "unknown"},
		{""}, {"", "--help"}, {"help", ""},
		{"board create", "--help"}, {"help", "board create"},
		{"board", "notifications target", "--help"},
		{"board", "unknown", "--help"}, {"board", "create"},
		{"board", "notifications"}, {"workspace", "pin"},
		{"card", "inspect", "--bad"}, {"attachment", "upload"},
		{"board", "notifications", "unknown", "--help"},
		{"workspace", "pin", "unknown", "--help"},
		{"board", "create", "--bad", "--help"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput(args, nil, &stdout, &stderr, func(string) string {
				t.Fatal("invalid invocation reached execution")
				return ""
			})
			if code != 2 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "ERROR ") || !strings.Contains(stderr.String(), "Usage:") {
				t.Fatalf("unexpected usage error: code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
			if args[0] == "board" && strings.Contains(stderr.String(), "localswim card create") {
				t.Fatal("board usage error included unrelated card help")
			}
		})
	}
}

func TestM099HelpTokensRemainValidCommandInputs(t *testing.T) {
	nameArgs := []string{"board", "create", "name", "--help"}
	fileArgs := []string{"board", "create", "json", "--help"}
	for _, args := range [][]string{nameArgs, fileArgs} {
		if _, ok := requestedCLIHelp(args); ok {
			t.Fatalf("help intercepted command input: %q", args)
		}
	}
	named, err := parseCLI(nameArgs)
	if err != nil || !bytes.Contains(named.body, []byte(`"name":"--help"`)) {
		t.Fatalf("help token was not preserved as the name: %s, %v", named.body, err)
	}
	fromFile, err := parseCLI(fileArgs)
	if err != nil || fromFile.inputPath != "--help" {
		t.Fatalf("help token was not preserved as the file path: %q, %v", fromFile.inputPath, err)
	}
}

type failingHelpWriter struct{}

func (failingHelpWriter) Write([]byte) (int, error) {
	return 0, errors.New("unwritable help output")
}

func TestM099HelpWriteFailure(t *testing.T) {
	if code := runCLIWithInput([]string{"card", "--help"}, nil, failingHelpWriter{}, &bytes.Buffer{}, nil); code != 1 {
		t.Fatalf("help output failure exit = %d", code)
	}
}
