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

func TestCLIPublishingUsesAddressedResource(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, scope := range []string{"board", "workspace"} {
		for _, action := range []string{"enable", "disable", "status"} {
			t.Run(scope+"/"+action, func(t *testing.T) {
				args := []string{scope, "publishing", action}
				if action == "disable" {
					args = append(args, "--revision", "7")
				}
				updatedTag := `"published-revision-8"`
				path, fields, tag := "/workspace", `"pinned_board_ids":["`+id+`"]`, `"workspace-revision-7"`
				if scope == "board" {
					args = append(args, strings.ToUpper(id))
					path, fields, tag = "/boards/"+id, `"id":"`+id+`","name":"  東京 <A>  "`, fmt.Sprintf("\"%s-7\"", id)
					updatedTag = fmt.Sprintf("\"%s-8\"", id)
				}
				status := func(revision int, enabled bool) string {
					documentFields := fields
					return fmt.Sprintf(`{%s,"revision":%d,"publishing":{"enabled":%t,"repository":"  dépôt  ","last_success_at":"2026-11-10 12:34:56+00:00","pending":true,"error_code":"push_failed"}}`, documentFields, revision, enabled)
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/api/2026-11-10"+path || r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("wrong publishing route or authorization")
					}
					switch requests.Add(1) {
					case 1:
						if r.Method != http.MethodGet || r.Header.Get("If-Match") != "" {
							t.Error("expected one unconditioned read")
						}
						w.Header().Set("ETag", tag)
						fmt.Fprint(w, status(7, action != "enable"))
					case 2:
						expected := fmt.Sprintf(`{"publishing":{"enabled":%t}}`, action == "enable")
						body, err := io.ReadAll(r.Body)
						if action == "status" || r.Method != http.MethodPatch || r.Header.Get("If-Match") != tag ||
							r.Header.Get("Content-Type") != "application/json" || err != nil || string(body) != expected {
							t.Error("expected one PATCH changing only enabled with the original resource validator")
						}
						w.Header().Set("ETag", updatedTag)
						fmt.Fprint(w, status(8, action == "enable"))
					default:
						t.Error("unexpected additional request")
					}
				}))
				defer server.Close()
				env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
				var stdout, stderr bytes.Buffer
				exit := runCLIWithInput(args, strings.NewReader("unused"), &stdout, &stderr, func(key string) string { return env[key] })
				wantRequests, wantResponse := int32(2), status(8, action == "enable")
				if action == "status" {
					wantRequests, wantResponse = 1, status(7, true)
				}
				if exit != 0 || requests.Load() != wantRequests || stderr.Len() != 0 {
					t.Fatalf("exit %d, requests %d, stderr %q", exit, requests.Load(), &stderr)
				}
				if !equalCLIJSON(t, stdout.Bytes(), []byte(wantResponse)) || !strings.Contains(stdout.String(), "\n  \"publishing\": {\n") ||
					!strings.Contains(stdout.String(), "  dépôt  ") {
					t.Fatalf("expected full pretty UTF-8 JSON, got %q", &stdout)
				}
			})
		}
	}
}

func TestCLIBoardPublishingRequiresMatchingRevision(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const readTag = `"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa-4"`
	const updatedTag = `"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa-5"`
	const wrongTag = `"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa-1"`
	status := func(revision int, enabled bool) string {
		return fmt.Sprintf(`{"id":%q,"revision":%d,"publishing":{"enabled":%t,"repository":null,"last_success_at":null,"pending":false,"error_code":null}}`, id, revision, enabled)
	}
	for _, tc := range []struct {
		name, readBodyTag, updatedBodyTag string
		wantWrites                        int32
	}{
		{"missing preflight tag", "", updatedTag, 0},
		{"mismatched preflight tag", wrongTag, updatedTag, 0},
		{"missing success tag", readTag, "", 1},
		{"mismatched success tag", readTag, wrongTag, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					reads.Add(1)
					w.Header().Set("ETag", tc.readBodyTag)
					fmt.Fprint(w, status(4, false))
				case http.MethodPatch:
					writes.Add(1)
					w.Header().Set("ETag", tc.updatedBodyTag)
					fmt.Fprint(w, status(5, true))
				default:
					t.Error("unexpected publishing request")
				}
			}))
			defer server.Close()
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			args := []string{"board", "publishing", "enable", "--revision", "4", id}
			exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, func(key string) string { return env[key] })
			wantError := "validator"
			if tc.readBodyTag == "" {
				wantError = "strong resource ETag"
			}
			if tc.wantWrites == 1 {
				wantError = "Inspect publishing status before retrying"
				if tc.updatedBodyTag == "" {
					wantError = "canonical strong ETag"
				}
			}
			if exit != 1 || stdout.Len() != 0 || reads.Load() != 1 || writes.Load() != tc.wantWrites ||
				!strings.Contains(stderr.String(), wantError) ||
				strings.Contains(stderr.String(), "Mutation outcome unknown") != (tc.wantWrites == 1) {
				t.Fatalf("exit %d, reads %d, writes %d, stdout %q, stderr %q", exit, reads.Load(), writes.Load(), &stdout, &stderr)
			}
		})
	}
}

func TestCLIPublishingRefusalAndUnknownOutcome(t *testing.T) {
	const tag = `"workspace-revision-4"`
	const current = `{"pinned_board_ids":[],"revision":4,"publishing":{"enabled":false,"repository":null,"last_success_at":null,"pending":false,"error_code":null}}`
	for _, tc := range []struct {
		name, revision, readTag, responseTag, response, errorText string
		status                                                    int
		requests                                                  int32
		unknown                                                   bool
	}{
		{"revision mismatch", "5", tag, "", "", "revision mismatch", 0, 1, false},
		{"weak read tag", "4", "W/" + tag, "", "", "strong resource ETag", 0, 1, false},
		{"stale write", "4", tag, "", "", "HTTP 412", 412, 2, false},
		{"failed write", "4", tag, "", "", "HTTP 500", 500, 2, true},
		{"unusable success body", "4", tag, tag, "[]", "invalid JSON object", 200, 2, true},
		{"unconfirmed enable", "4", tag, tag, current, "publishing status before retrying", 200, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch requests.Add(1) {
				case 1:
					w.Header().Set("ETag", tc.readTag)
					fmt.Fprint(w, current)
				case 2:
					if tc.status == 0 {
						t.Error("write reached after refused preflight")
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.Header().Set("ETag", tc.responseTag)
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.response)
				default:
					t.Error("publishing mutation was replayed")
				}
			}))
			defer server.Close()
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			args := []string{"workspace", "publishing", "enable", "--revision", tc.revision}
			exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, func(key string) string { return env[key] })
			if exit != 1 || stdout.Len() != 0 || requests.Load() != tc.requests || !strings.Contains(stderr.String(), tc.errorText) ||
				strings.Contains(stderr.String(), "Mutation outcome unknown") != tc.unknown {
				t.Fatalf("exit %d, requests %d, stdout %q, stderr %q", exit, requests.Load(), &stdout, &stderr)
			}
		})
	}
}
