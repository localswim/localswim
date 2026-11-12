package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestServePositionalArguments(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data directory")
	for _, test := range []struct {
		name       string
		args       []string
		port       int
		background bool
	}{
		{"default port", []string{root}, 0, false},
		{"available port", []string{root, "0"}, 0, false},
		{"fixed port", []string{root, "53148"}, 53148, false},
		{"minimum fixed port", []string{root, "1"}, 1, false},
		{"maximum port", []string{root, "65535"}, 65535, false},
		{"background", []string{"--background", root, "53148"}, 53148, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotRoot, port, background, err := parseServeArguments(test.args)
			if err != nil || gotRoot != root || port != test.port || background != test.background {
				t.Fatalf("parse = (%q, %d, %t, %v)", gotRoot, port, background, err)
			}
		})
	}
}

func TestServeRejectsInvalidArguments(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unused")
	for _, test := range []struct {
		name string
		args []string
	}{
		{"missing directory", nil},
		{"empty directory", []string{""}},
		{"relative directory", []string{"relative"}},
		{"too many arguments", []string{root, "12", "13"}},
		{"empty port", []string{root, ""}},
		{"negative port", []string{root, "-1"}},
		{"positive sign", []string{root, "+1"}},
		{"fraction", []string{root, "1.5"}},
		{"exponent", []string{root, "1e3"}},
		{"leading space", []string{root, " 1"}},
		{"trailing space", []string{root, "1 "}},
		{"nondecimal", []string{root, "0x80"}},
		{"too large", []string{root, "65536"}},
		{"overflow", []string{root, "99999999999999999999999"}},
		{"old data flag", []string{"--data", root}},
		{"old port flag", []string{root, "--port", "12"}},
		{"old flags", []string{"--data", root, "--port", "12"}},
		{"background missing port", []string{"--background", root}},
		{"background zero port", []string{"--background", root, "0"}},
		{"background alias", []string{"-background", root, "12"}},
		{"background assignment", []string{"--background=true", root, "12"}},
		{"repeated background", []string{"--background", "--background", root, "12"}},
		{"trailing background", []string{root, "12", "--background"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := serve(test.args, &stdout, &stderr); err == nil {
				t.Fatal("invalid startup arguments accepted")
			}
			if stdout.Len() != 0 {
				t.Fatalf("invalid startup announced a listener: %q", stdout.String())
			}
		})
	}
}

func TestServeHelp(t *testing.T) {
	for _, option := range []string{"-h", "--help"} {
		var stdout, stderr bytes.Buffer
		if err := serve([]string{option}, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		help := stderr.String()
		if !strings.Contains(help, "serve [--background] DATA_DIRECTORY [PORT]") ||
			!strings.Contains(help, "defaults to 0") || !strings.Contains(help, "explicit nonzero PORT") {
			t.Fatalf("incomplete startup help: %q", help)
		}
		if strings.Contains(help, "--data") || strings.Contains(help, "--port") {
			t.Fatalf("removed startup flags remain in help: %q", help)
		}
	}
}
