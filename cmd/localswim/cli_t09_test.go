package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLIBoardAvailability(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const oldTag = `"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa-7"`
	const newTag = `"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa-8"`
	const problemType = "application/problem+json"
	const unavailable = `{"type":"about:blank","title":"Service Unavailable","status":503,"code":"unavailable","detail":"Unable to flush the board.","request_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","outcome":"not_applied","errors":[]}`
	board := func(revision int, availability string) string {
		return fmt.Sprintf(`{"id":%q,"revision":%d,"availability":%q,"name":"  東京  "}`,
			id, revision, availability)
	}
	for _, tc := range []struct {
		name, action, revision, previous, readTag, reply, errorText string
		responseBoardTag, contentType                               string
		status                                                      int
		unknown                                                     bool
	}{
		{name: "close", action: "close", revision: "7", previous: "open", readTag: oldTag, status: 200, reply: board(8, "closed")},
		{name: "reopen", action: "reopen", previous: "closed", readTag: oldTag, status: 200, reply: board(8, "open")},
		{name: "mismatched revision", action: "close", revision: "6", previous: "open", readTag: oldTag, errorText: "revision mismatch"},
		{name: "mismatched validator", action: "close", previous: "open", readTag: newTag, errorText: "revision did not match"},
		{name: "invalid availability", action: "close", previous: "unknown", readTag: oldTag, errorText: "did not return valid board availability"},
		{name: "stale validator", action: "close", previous: "open", readTag: oldTag, status: 412, errorText: "HTTP 412"},
		{name: "unusable reply", action: "close", previous: "open", readTag: oldTag, status: 200, reply: "[]", errorText: "invalid JSON object", unknown: true},
		{name: "unconfirmed close", action: "close", previous: "open", readTag: oldTag, status: 200, reply: board(8, "open"), errorText: "did not confirm", unknown: true},
		{name: "missing board validator", action: "close", previous: "open", readTag: oldTag, status: 200, reply: board(8, "closed"), responseBoardTag: "missing", errorText: "matching ETag and X-Board-ETag", unknown: true},
		{name: "mismatched board validator", action: "close", previous: "open", readTag: oldTag, status: 200, reply: board(8, "closed"), responseBoardTag: oldTag, errorText: "matching ETag and X-Board-ETag", unknown: true},
		{name: "known 503 refusal", action: "close", previous: "open", readTag: oldTag, status: 503, reply: unavailable, contentType: problemType, errorText: "unavailable, not applied"},
		{name: "503 without Problem media type", action: "close", previous: "open", readTag: oldTag, status: 503, reply: unavailable, errorText: "HTTP 503", unknown: true},
		{name: "malformed 503 Problem", action: "close", previous: "open", readTag: oldTag, status: 503, reply: unavailable[:len(unavailable)-1], contentType: problemType, errorText: "HTTP 503", unknown: true},
		{name: "incomplete 503 Problem", action: "close", previous: "open", readTag: oldTag, status: 503, reply: `{"status":503,"code":"unavailable","outcome":"not_applied"}`, contentType: problemType, errorText: "HTTP 503", unknown: true},
		{name: "unknown 503 outcome", action: "close", previous: "open", readTag: oldTag, status: 503, reply: strings.Replace(unavailable, "not_applied", "unknown", 1), contentType: problemType, errorText: "HTTP 503", unknown: true},
		{name: "ambiguous 503 outcome", action: "close", previous: "open", readTag: oldTag, status: 503, reply: strings.Replace(unavailable, `"outcome":`, `"outcome":"unknown","outcome":`, 1), contentType: problemType, errorText: "HTTP 503", unknown: true},
		{name: "other 5xx remains unknown", action: "close", previous: "open", readTag: oldTag, status: 500, reply: unavailable, contentType: problemType, errorText: "HTTP 500", unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := "closed"
			if tc.action == "reopen" {
				target = "open"
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/2026-11-10/boards/"+id || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("wrong board route or authorization")
				}
				switch requests.Add(1) {
				case 1:
					if r.Method != http.MethodGet || r.Header.Get("If-Match") != "" {
						t.Error("expected unconditioned board read")
					}
					w.Header().Set("ETag", tc.readTag)
					fmt.Fprint(w, board(7, tc.previous))
				case 2:
					if tc.status == 0 {
						t.Error("write reached after refused preflight")
						return
					}
					body, err := io.ReadAll(r.Body)
					if r.Method != http.MethodPatch || r.Header.Get("If-Match") != oldTag ||
						r.Header.Get("Content-Type") != "application/json" ||
						err != nil || string(body) != fmt.Sprintf(`{"availability":%q}`, target) {
						t.Error("expected availability PATCH with the original validator")
					}
					w.Header().Set("ETag", newTag)
					switch tc.responseBoardTag {
					case "":
						w.Header().Set("X-Board-ETag", newTag)
					case "missing":
					default:
						w.Header().Set("X-Board-ETag", tc.responseBoardTag)
					}
					if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
					}
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.reply)
				default:
					t.Error("unexpected additional request")
				}
			}))
			defer server.Close()
			args := []string{"board", tc.action}
			if tc.revision != "" {
				args = append(args, "--revision", tc.revision)
			}
			args = append(args, id)
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(args, strings.NewReader("unused"), &stdout, &stderr, func(key string) string { return env[key] })
			wantRequests := int32(2)
			if tc.status == 0 {
				wantRequests = 1
			}
			if requests.Load() != wantRequests {
				t.Fatalf("sent %d requests, wanted %d", requests.Load(), wantRequests)
			}
			if tc.errorText == "" {
				if exit != 0 || stderr.Len() != 0 || !equalCLIJSON(t, stdout.Bytes(), []byte(tc.reply)) ||
					!strings.Contains(stdout.String(), "\n  \"name\": \"  東京  \"\n") {
					t.Fatalf("expected full pretty board JSON, exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
				}
			} else if exit != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.errorText) ||
				strings.Contains(stderr.String(), "Mutation outcome unknown") != tc.unknown ||
				strings.Contains(stderr.String(), "localswim board get "+id) != tc.unknown {
				t.Fatalf("unexpected refusal, exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
			}
		})
	}
}
