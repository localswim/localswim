package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

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
