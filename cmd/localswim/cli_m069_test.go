package main

import (
	"net/http"
	"net/url"
	"testing"
)

func TestM069CLICommentArchiveSelection(t *testing.T) {
	for _, tc := range []struct {
		name, archived string
		args           []string
	}{
		{"board defaults to active", "exclude", []string{"--limit", "1", t03Board}},
		{"include archive", "include", []string{"--archived", "include", "--limit", "1", t03Board}},
		{"only archive", "only", []string{"--archived", "ONLY", "--limit", "1", t03Board}},
		{"focused card keeps archive readable", "", []string{"--limit", "1", t03Board, t03Card}},
		{"explicit selection on focused card", "exclude", []string{"--archived", "exclude", "--limit", "1", t03Board, t03Card}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := parseCLI(append([]string{"comment", "list"}, tc.args...))
			if err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(cmd.resource)
			if err != nil {
				t.Fatal(err)
			}
			query := target.Query()
			if cmd.method != http.MethodGet || target.Path != "/comments" ||
				query.Get("archived") != tc.archived || query.Get("board_id") != t03Board ||
				query.Get("limit") != "1" {
				t.Fatalf("unexpected request: %s %s", cmd.method, cmd.resource)
			}
		})
	}

	for _, args := range [][]string{
		{"--archived", "bad", t03Board},
		{"--archived=", t03Board, t03Card},
		{"--limit", "201", t03Board},
	} {
		if _, err := parseCLI(append([]string{"comment", "list"}, args...)); err == nil {
			t.Fatalf("unexpected acceptance of %v", args)
		}
	}
}
