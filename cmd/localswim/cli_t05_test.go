package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const t05From = "2026-11-10 18:00:00+00:00"
const t05To = "2026-11-10 18:00:02+00:00"

func TestT05CLIActivityBoundsAndViews(t *testing.T) {
	for _, view := range []string{"full", "monitor"} {
		t.Run(view, func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				query := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.Path != t03Root+"/activities" ||
					query.Get("board_id") != t03Board || query.Get("view") != view ||
					query.Get("from") != t05From || query.Get("to") != t05To ||
					query.Get("ticket") != "7" || query.Get("limit") != "12" || query.Get("cursor") != "a+b=" {
					t.Errorf("unexpected activity request %s %s", r.Method, r.URL)
				}
				_, _ = io.WriteString(w, `{"activities":[],"next_cursor":null}`)
			}))
			defer server.Close()
			args := []string{"activity", "list", "--from", t05From, "--to", t05To,
				"--ticket", "0007", "--limit", "12", "--cursor", "a+b="}
			if view != "full" {
				args = append(args, "--view", "MONITOR")
			}
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput(append(args, t03Board), strings.NewReader(""), &stdout, &stderr, t03Env(server.URL))
			if code != 0 || reads.Load() != 1 || !equalCLIJSON(t, stdout.Bytes(), []byte(`{"activities":[],"next_cursor":null}`)) {
				t.Fatalf("activity result %d, reads %d, stdout %q, stderr %q", code, reads.Load(), &stdout, &stderr)
			}
		})
	}
	for _, options := range [][]string{
		{"--from", t05From},
		{"--from", t05To, "--to", t05From},
		{"--from", "2026-11-10T18:00:00Z", "--to", t05To},
		{"--from", "2026-11-10 18:00:00.1+00:00", "--to", t05To},
		{"--from", t05From, "--to", t05To, "--ticket", "0"},
		{"--from", t05From, "--to", t05To, "--limit", "201"},
		{"--from", t05From, "--to", t05To, "--cursor", ""},
		{"--from", t05From, "--to", t05To, "--view", "other"},
	} {
		args := append(append([]string{"activity", "list"}, options...), t03Board)
		if _, err := parseCLI(args); err == nil {
			t.Errorf("accepted invalid activity options %v", options)
		}
	}
}

func TestT05CLILaneEditsAndPriority(t *testing.T) {
	for _, tc := range []struct {
		name, resource, revision, field, value, input string
		arguments                                     []string
		board, stale                                  bool
	}{
		{"retire", "/lanes/" + t03Lane, "12", "retired", "true", "", []string{"lane", "retire"}, true, false},
		{"enable", "/lanes/" + t03Lane, "12", "approval_enabled", "true", "", []string{"lane", "set-approval-button", "enabled"}, true, false},
		{"disable", "/lanes/" + t03Lane, "12", "approval_enabled", "false", "", []string{"lane", "set-approval-button", "disabled"}, true, false},
		{"stale lane", "/lanes/" + t03Lane, "11", "retired", "true", "", []string{"lane", "retire"}, true, true},
		{"priority", "/cards/" + t03Card, "12", "priority_id", fmt.Sprintf("%q", t03Lane), "", []string{"card", "set-priority", t03Lane}, false, false},
		{"priority document", "/cards/" + t03Card, "12", "priority_id", fmt.Sprintf("%q", t03Lane), fmt.Sprintf(`{"priority_id":%q}`, t03Lane), []string{"card", "patch", "-"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes atomic.Int32
			var boards atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == t03Root+tc.resource:
					w.Header().Set("ETag", t03ResourceTag)
					_, _ = fmt.Fprintf(w, `{"board_id":%q,"revision":12}`, t03Board)
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/boards/"+t03Board:
					boards.Add(1)
					w.Header().Set("ETag", t03BoardTag)
					_, _ = io.WriteString(w, `{"revision":99}`)
				case r.Method == http.MethodPatch && r.URL.Path == t03Root+tc.resource:
					writes.Add(1)
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || string(body[tc.field]) != tc.value {
						t.Errorf("unexpected patch %v (%v)", body, err)
					}
					if r.Header.Get("If-Match") != t03ResourceTag {
						t.Error("PATCH is missing the resource validator")
					}
					if tc.board && (len(body) != 2 || string(body["board_etag"]) != fmt.Sprintf("%q", t03BoardTag)) {
						t.Error("lane PATCH must include the exact board validator")
					}
					w.Header().Set("ETag", t03ResourceTag)
					w.Header().Set("X-Board-ETag", t03BoardTag)
					_, _ = io.WriteString(w, "{}")
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			id := t03Card
			if tc.board {
				id = t03Lane
			}
			args := []string{tc.arguments[0], tc.arguments[1], "--revision", tc.revision, id}
			args = append(args, tc.arguments[2:]...)
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput(args, strings.NewReader(tc.input), &stdout, &stderr, t03Env(server.URL))
			if tc.stale {
				if code != 1 || writes.Load() != 0 || boards.Load() != 0 || !strings.Contains(stderr.String(), "revision mismatch") {
					t.Fatalf("stale lane preflight result %d, writes %d, board reads %d: %s", code, writes.Load(), boards.Load(), &stderr)
				}
				return
			}
			if code != 0 || writes.Load() != 1 || (boards.Load() == 1) != tc.board {
				t.Fatalf("mutation result %d, writes %d, board reads %d: %s", code, writes.Load(), boards.Load(), &stderr)
			}
		})
	}
	for _, args := range [][]string{
		{"lane", "set-approval-button", t03Lane, "approve"},
		{"lane", "retire", t03Lane, "false"},
		{"lane", "approve", t03Lane},
		{"card", "set-priority", t03Card, "P1"},
		{"card", "patch", t03Card},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Errorf("accepted invalid edit %v", args)
		}
	}
	if cmd, err := parseCLI([]string{"lane", "get", t03Lane}); err != nil || cmd.method != http.MethodGet || cmd.resource != "/lanes/"+t03Lane {
		t.Fatalf("unexpected focused lane read: %s %s (%v)", cmd.method, cmd.resource, err)
	}
}

func TestT05CLIDetachPreconditionsAndNoReplay(t *testing.T) {
	for _, tc := range []struct {
		name, revision, attachmentTag, responseTag string
		status, code, writes                       int
		unknown                                    bool
	}{
		{"success", "23", t03ResourceTag, t03BoardTag, http.StatusNoContent, 0, 1, false},
		{"stale attachment", "22", t03ResourceTag, t03BoardTag, http.StatusNoContent, 1, 0, false},
		{"missing attachment validator", "23", "", t03BoardTag, http.StatusNoContent, 1, 0, false},
		{"server refusal", "23", t03ResourceTag, "", http.StatusPreconditionFailed, 1, 1, false},
		{"unverified acknowledgment", "23", t03ResourceTag, "", http.StatusNoContent, 1, 1, true},
		{"uncertain failure", "23", t03ResourceTag, "", http.StatusServiceUnavailable, 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/attachments/"+t03Card:
					if tc.attachmentTag != "" {
						w.Header().Set("ETag", tc.attachmentTag)
					}
					_, _ = w.Write(attachmentFixture(t03Card, nil))
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/boards/"+t03Board:
					w.Header().Set("ETag", t03BoardTag)
					_, _ = io.WriteString(w, `{"revision":84}`)
				case r.Method == http.MethodDelete && r.URL.Path == t03Root+"/attachments/"+t03Card:
					writes.Add(1)
					if r.Header.Get("If-Match") != t03ResourceTag || r.Header.Get("X-Expected-Board-ETag") != t03BoardTag || r.ContentLength != 0 {
						t.Error("DELETE must bind both exact validators with an empty body")
					}
					if tc.responseTag != "" {
						w.Header().Set("X-Board-ETag", tc.responseTag)
					}
					w.WriteHeader(tc.status)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			args := []string{"attachment", "detach", "--revision", tc.revision, t03Card}
			code := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, t03Env(server.URL))
			if code != tc.code || writes.Load() != int32(tc.writes) || stdout.Len() != 0 ||
				strings.Contains(stderr.String(), "Mutation outcome unknown") != tc.unknown {
				t.Fatalf("detach result %d, writes %d, stdout %q, stderr %q", code, writes.Load(), &stdout, &stderr)
			}
		})
	}
}
