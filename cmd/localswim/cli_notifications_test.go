package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLINotificationsUseConditionalBoardSettings(t *testing.T) {
	const board = "01234567-89ab-4def-8123-0123456789ab"
	const currentTag = `"notification-revision-4"`
	const updatedTag = `"notification-revision-5"`
	const disabled = `{"enabled":false,"target_thread_id":null}`
	const configured = `{"enabled":false,"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ab"}`
	const enabled = `{"enabled":true,"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ab"}`
	const retargeted = `{"enabled":true,"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ac"}`
	const target = "018E7F0A-BCDE-7ABC-8DEF-0123456789AB"
	const nextTarget = "018E7F0A-BCDE-7ABC-8DEF-0123456789AC"
	tests := []struct {
		name, action, revision, target     string
		current, patch, updated, wantError string
		status                             int
		missingBoardTag, unknown           bool
	}{
		{name: "default status", action: "status", current: disabled},
		{name: "target preserves disabled", action: "target", target: target, revision: "4", current: disabled,
			patch: `{"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ab"}`, updated: configured, status: 200},
		{name: "enable retains target", action: "enable", current: configured,
			patch: `{"enabled":true}`, updated: enabled, status: 200},
		{name: "retarget preserves enabled", action: "target", target: nextTarget, current: enabled,
			patch: `{"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ac"}`, updated: retargeted, status: 200},
		{name: "disable retains target", action: "disable", current: enabled,
			patch: `{"enabled":false}`, updated: configured, status: 200},
		{name: "missing target", action: "enable", current: disabled, wantError: "set a notification target before enabling"},
		{name: "stale input revision", action: "enable", revision: "3", current: configured, wantError: "revision mismatch"},
		{name: "stale write validator", action: "disable", current: enabled,
			patch: `{"enabled":false}`, status: 412, wantError: "HTTP 412"},
		{name: "service error", action: "disable", current: enabled,
			patch: `{"enabled":false}`, status: 500, wantError: "HTTP 500", unknown: true},
		{name: "unconfirmed target", action: "target", target: nextTarget, current: enabled,
			patch:   `{"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ac"}`,
			updated: enabled, status: 200, wantError: "did not confirm", unknown: true},
		{name: "unconfirmed validator", action: "enable", current: configured,
			patch: `{"enabled":true}`, updated: enabled, status: 200, missingBoardTag: true,
			wantError: "did not confirm", unknown: true},
		{name: "malformed current settings", action: "status",
			current: `{"enabled":true,"target_thread_id":null}`, wantError: "did not return valid notification settings"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/2026-11-10/boards/"+board || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("command did not address the authenticated board")
				}
				switch requests.Add(1) {
				case 1:
					if r.Method != http.MethodGet || r.Header.Get("If-Match") != "" {
						t.Error("first request was not an unconditional board read")
					}
					w.Header().Set("ETag", currentTag)
					fmt.Fprintf(w, `{"id":%q,"revision":4,"notifications":%s,"name":"fixture board"}`,
						board, test.current)
				case 2:
					if test.patch == "" {
						t.Error("command sent an unexpected write")
						w.WriteHeader(500)
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil || r.Method != http.MethodPatch || r.Header.Get("If-Match") != currentTag ||
						string(body) != `{"notifications":`+test.patch+`}` {
						t.Errorf("invalid notification patch: method %s, body %s, err %v", r.Method, body, err)
					}
					w.Header().Set("ETag", updatedTag)
					if !test.missingBoardTag {
						w.Header().Set("X-Board-ETag", updatedTag)
					}
					w.WriteHeader(test.status)
					if test.updated != "" {
						fmt.Fprintf(w, `{"id":%q,"revision":5,"notifications":%s,"name":"fixture board"}`,
							board, test.updated)
					}
				default:
					t.Error("command made an extra request")
					w.WriteHeader(500)
				}
			}))
			defer server.Close()

			args := []string{"board", "notifications", test.action}
			if test.revision != "" {
				args = append(args, "--revision", test.revision)
			}
			args = append(args, strings.ToUpper(board))
			if test.target != "" {
				args = append(args, test.target)
			}
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, func(key string) string { return env[key] })
			wantRequests := int32(1)
			if test.patch != "" {
				wantRequests++
			}
			if requests.Load() != wantRequests {
				t.Fatalf("requests = %d, want %d", requests.Load(), wantRequests)
			}
			if test.wantError != "" {
				if exit != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.wantError) ||
					strings.Contains(stderr.String(), "Mutation outcome unknown") != test.unknown ||
					strings.Contains(stderr.String(), "board notifications status "+board) != test.unknown {
					t.Fatalf("exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
				}
				return
			}
			revision, settings := 4, test.current
			if test.patch != "" {
				revision, settings = 5, test.updated
			}
			want := fmt.Sprintf(`{"board_id":%q,"revision":%d,%s`, board, revision, settings[1:])
			if exit != 0 || stderr.Len() != 0 || strings.TrimSpace(stdout.String()) != want {
				t.Fatalf("exit %d, stdout %q, stderr %q, want %s", exit, &stdout, &stderr, want)
			}
		})
	}
}

func TestCLINotificationsRejectInvalidArgumentsBeforeTransport(t *testing.T) {
	const board = "01234567-89ab-4def-8123-0123456789ab"
	for _, args := range [][]string{
		{},
		{"retarget", board},
		{"target", board},
		{"target", board, "018e7f0a-bcde-9abc-8def-0123456789ab"},
		{"enable", "--revision", "0", board},
		{"disable", "invalid-board"},
		{"status", "--revision", "1", board},
	} {
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput(slices.Concat([]string{"board", "notifications"}, args), strings.NewReader(""), &stdout, &stderr,
			func(string) string {
				t.Fatal("invalid invocation initialized the live client")
				return ""
			})
		if exit != 2 || stdout.Len() != 0 {
			t.Errorf("arguments %v: exit %d, stdout %q, stderr %q", args, exit, &stdout, &stderr)
		}
	}
}
