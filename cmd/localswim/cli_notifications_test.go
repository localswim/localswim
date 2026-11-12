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

func TestCLINotificationsUseConditionalRouteSettings(t *testing.T) {
	const board = "01234567-89ab-4def-8123-0123456789ab"
	const currentTag = `"notifications-` + board + `-4"`
	const updatedTag = `"notifications-` + board + `-5"`
	const wrongBoardTag = `"01234567-89ab-4def-8123-0123456789ac-4"`
	const disabled = `{"enabled":false,"target_thread_id":null}`
	const configured = `{"enabled":false,"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ab"}`
	const enabled = `{"enabled":true,"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ab"}`
	const retargeted = `{"enabled":true,"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ac"}`
	const target = "018E7F0A-BCDE-7ABC-8DEF-0123456789AB"
	const nextTarget = "018E7F0A-BCDE-7ABC-8DEF-0123456789AC"
	const unavailable = `{"type":"about:blank","title":"Service Unavailable","status":503,"code":"unavailable","detail":"This board is closed or draining.","request_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","outcome":"not_applied","errors":[]}`
	const uncertain = `{"type":"about:blank","title":"Service Unavailable","status":503,"code":"outcome_unknown","detail":"Inspect the saved state before attempting another write.","request_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","outcome":"unknown","errors":[]}`
	tests := []struct {
		name, action, revision, target         string
		current, patch, updated, wantError     string
		readTag, readExtras, replyTag, problem string
		status, replyRevision                  int
		missingRouteTag, unknown               bool
	}{
		{name: "default status", action: "status", current: disabled},
		{name: "status wrong board validator", action: "status", current: disabled, readTag: wrongBoardTag,
			wantError: "revision did not match its HTTP validator"},
		{name: "explicit revision with a different read validator", action: "enable", revision: "4", current: configured,
			readTag: updatedTag, wantError: "revision did not match its HTTP validator"},
		{name: "explicit revision cannot match an unrelated member", action: "enable", revision: "5", current: configured,
			readExtras: `,"Revision":5`, wantError: "did not return the addressed notification route"},
		{name: "target preserves disabled", action: "target", target: target, revision: "4", current: disabled,
			patch: `{"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ab"}`, updated: configured, status: 200},
		{name: "retargeting to the same target is a no-op", action: "target", target: target, current: configured,
			patch: `{"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ab"}`, updated: configured, status: 200, replyRevision: 4},
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
		{name: "validated unavailable refusal", action: "disable", current: enabled,
			patch: `{"enabled":false}`, status: 503, problem: unavailable, wantError: "HTTP 503 (unavailable, not applied)"},
		{name: "unvalidated unavailable response", action: "disable", current: enabled,
			patch: `{"enabled":false}`, status: 503, problem: `{"code":"unavailable","outcome":"not_applied"}`,
			wantError: "HTTP 503", unknown: true},
		{name: "uncertain durable write", action: "disable", current: enabled,
			patch: `{"enabled":false}`, status: 503, problem: uncertain, wantError: "HTTP 503", unknown: true},
		{name: "unconfirmed target", action: "target", target: nextTarget, current: enabled,
			patch:   `{"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ac"}`,
			updated: enabled, status: 200, wantError: "did not confirm", unknown: true},
		{name: "unconfirmed validator", action: "enable", current: configured,
			patch: `{"enabled":true}`, updated: enabled, status: 200, missingRouteTag: true,
			wantError: "one canonical strong ETag", unknown: true},
		{name: "revision and reply validators disagree", action: "enable", current: configured,
			patch: `{"enabled":true}`, updated: enabled, status: 200, replyTag: currentTag,
			wantError: "did not confirm", unknown: true},
		{name: "change without a revision advance", action: "enable", current: configured,
			patch: `{"enabled":true}`, updated: enabled, status: 200, replyRevision: 4,
			wantError: "did not confirm", unknown: true},
		{name: "change skips a revision", action: "enable", current: configured,
			patch: `{"enabled":true}`, updated: enabled, status: 200, replyRevision: 6,
			wantError: "did not confirm", unknown: true},
		{name: "no-op with a revision advance", action: "enable", current: enabled,
			patch: `{"enabled":true}`, updated: enabled, status: 200,
			wantError: "did not confirm", unknown: true},
		{name: "malformed current settings", action: "status",
			current: `{"enabled":true,"target_thread_id":null}`, wantError: "did not return valid notification settings"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readTag := test.readTag
			if readTag == "" {
				readTag = currentTag
			}
			replyRevision := test.replyRevision
			if replyRevision == 0 {
				replyRevision = 5
			}
			replyTag := test.replyTag
			if replyTag == "" {
				replyTag = fmt.Sprintf(`"notifications-%s-%d"`, board, replyRevision)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/2026-11-10/notifications/"+board || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("command did not address the authenticated board")
				}
				switch requests.Add(1) {
				case 1:
					if r.Method != http.MethodGet || r.Header.Get("If-Match") != "" {
						t.Error("first request was not an unconditional route read")
					}
					w.Header().Set("ETag", readTag)
					fmt.Fprintf(w, `{"board_id":%q,"revision":4,%s%s}`,
						board, test.current[1:len(test.current)-1], test.readExtras)
				case 2:
					if test.patch == "" {
						t.Error("command sent an unexpected write")
						w.WriteHeader(500)
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil || r.Method != http.MethodPatch || r.Header.Get("If-Match") != currentTag ||
						string(body) != test.patch {
						t.Errorf("invalid notification patch: method %s, body %s, err %v", r.Method, body, err)
					}
					if test.problem != "" {
						w.Header().Set("Content-Type", "application/problem+json")
						w.WriteHeader(test.status)
						fmt.Fprint(w, test.problem)
						return
					}
					if !test.missingRouteTag {
						w.Header().Set("ETag", replyTag)
					}
					w.WriteHeader(test.status)
					if test.updated != "" {
						fmt.Fprintf(w, `{"board_id":%q,"revision":%d,%s`,
							board, replyRevision, test.updated[1:])
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
				revision, settings = replyRevision, test.updated
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
