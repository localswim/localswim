package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLIBoardRenameUsesRevisionCheckedPatch(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const tag = `"board-revision-1"`
	const name = "  Client \"North\" \\ 東京  "
	for _, tc := range []struct {
		name         string
		args         []string
		status       int
		wantExit     int
		wantRequests int32
	}{
		{"automatic revision", []string{"board", "rename", id, name}, 200, 0, 2},
		{"explicit revision", []string{"board", "rename", "--if-match", tag, id, name}, 200, 0, 1},
		{"stale revision", []string{"board", "rename", id, name}, 412, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/api/2026-11-10/boards/"+id || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("wrong rename route or authorization")
				}
				w.Header().Set("ETag", tag)
				if r.Method == http.MethodGet {
					w.Write([]byte(`{"name":"Before"}`))
					return
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["name"] != name ||
					r.Method != http.MethodPatch || r.Header.Get("If-Match") != tag || r.Header.Get("Content-Type") != "application/json" {
					t.Error("rename did not preserve the name or revision")
				}
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					json.NewEncoder(w).Encode(body)
				}
			}))
			defer server.Close()
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(tc.args, strings.NewReader("not JSON"), &stdout, &stderr, func(key string) string { return env[key] })
			if exit != tc.wantExit || requests.Load() != tc.wantRequests {
				t.Fatalf("rename exit %d, requests %d, stderr %q", exit, requests.Load(), &stderr)
			}
			if tc.wantExit == 0 {
				var body map[string]string
				if json.Unmarshal(stdout.Bytes(), &body) != nil || body["name"] != name || stderr.Len() != 0 {
					t.Fatal("rename did not return the updated board")
				}
			} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), "412") {
				t.Fatal("stale rename did not return a clear refusal")
			}
		})
	}
}

func TestCLIBoardRenameRequiresIDAndOneNonemptyName(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, args := range [][]string{
		{"board", "rename", id},
		{"board", "rename", id, ""},
		{"board", "rename", id, string([]byte{0xff})},
		{"board", "rename", id, "too", "many"},
		{"board", "rename", "invalid", "Name"},
	} {
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr,
			func(string) string { t.Fatal("invalid rename reached execution"); return "" })
		if exit != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("invalid rename accepted: %v", args)
		}
	}
}

func TestCLIExportUsesSnapshotEndpoint(t *testing.T) {
	const boardID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const snapshot = `{"format":"localswim-snapshot","format_version":2}`
	for _, args := range [][]string{
		{"board", "export", boardID},
		{"board", "export", "--type", "json", boardID},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/2026-11-10/snapshots/"+boardID ||
					r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("wrong export route, method or authorization")
				}
				w.Write([]byte(snapshot))
			}))
			defer server.Close()
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, func(key string) string { return env[key] })
			if exit != 0 || stdout.String() != snapshot+"\n" || stderr.Len() != 0 || requests.Load() != 1 {
				t.Fatalf("export failed: exit %d, requests %d, stderr %q", exit, requests.Load(), &stderr)
			}
		})
	}
}

func TestCLIExportRejectsUnsupportedTypeBeforeDispatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLIWithInput(
		[]string{"board", "export", "--type", "csv", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		strings.NewReader(""), &stdout, &stderr,
		func(string) string { t.Fatal("invalid export type reached execution"); return "" },
	)
	if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `unsupported export type "csv"`) {
		t.Fatalf("wrong type refusal: exit %d, stderr %q", exit, &stderr)
	}
}

func TestCLIWorkspacePinsUsesOneRevisionBoundMutation(t *testing.T) {
	const pinBody = `{"pinned_board_ids":[]}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/2026-11-10/workspace" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("wrong API route or authorization")
		}
		switch requests.Add(1) {
		case 1:
			if r.Method != http.MethodGet {
				t.Error("expected initial GET")
			}
			w.Header().Set("ETag", `"workspace-revision-2"`)
			w.Write([]byte(pinBody))
		case 2:
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != pinBody || r.Method != http.MethodPatch ||
				r.Header.Get("If-Match") != `"workspace-revision-2"` || r.Header.Get("Content-Type") != "application/json" {
				t.Error("expected PATCH bound to the initial ETag")
			}
			w.WriteHeader(http.StatusPreconditionFailed)
		default:
			t.Error("unexpected additional request")
		}
	}))
	defer server.Close()

	env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
	getenv := func(key string) string { return env[key] }
	var stdout, stderr bytes.Buffer
	exit := runCLIWithInput([]string{"workspace", "pins"}, strings.NewReader(pinBody), &stdout, &stderr, getenv)
	if exit != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "412") || requests.Load() != 2 {
		t.Fatalf("wrong stale mutation result: exit %d, requests %d, stderr %q", exit, requests.Load(), &stderr)
	}
}

func TestCLIUnknownCommandIsRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLIWithInput(
		[]string{"board", "unknown", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		strings.NewReader(""), &stdout, &stderr,
		func(string) string { t.Fatal("unknown command reached execution"); return "" },
	)
	if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("unknown command accepted: exit %d, stderr %q", exit, &stderr)
	}
}
