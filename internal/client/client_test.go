package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIsJSONObjectRequiresValidOriginalBytes(t *testing.T) {
	if !IsJSONObject([]byte(" \t\r\n{}\r\n")) {
		t.Fatal("rejected valid JSON whitespace")
	}
	for _, input := range []string{"\u00a0{}", "{}\u00a0", "{\"text\":\"\xff\"}", "[]"} {
		if IsJSONObject([]byte(input)) {
			t.Errorf("accepted invalid object bytes %q", input)
		}
	}
}

func TestM85OrdinaryMutationsRequireExactFenceRefusal(t *testing.T) {
	const problemType = "application/problem+json"
	const problem = `{"type":"about:blank","title":"Service Unavailable","status":503,"code":"unavailable","detail":"Writes are paused after an uncertain commit. Inspect the saved state.","request_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","outcome":"not_applied","errors":[]}`
	const tag = `"canonical-revision-1"`
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		for _, tc := range []struct {
			name, contentType, body string
			unknown                 bool
		}{
			{"known fence", problemType, problem, false},
			{"unrelated refusal", problemType, strings.Replace(problem, "Writes are paused", "Another operation is paused", 1), true},
			{"unknown outcome", problemType, strings.Replace(problem, "not_applied", "unknown", 1), true},
			{"duplicate outcome", problemType, strings.Replace(problem, `"outcome":`, `"outcome":"unknown","outcome":`, 1), true},
			{"wrong media type", "application/json", problem, true},
			{"invalid JSON", problemType, problem[:len(problem)-1], true},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != method {
						t.Error("unexpected request method")
					}
					w.Header().Set("Content-Type", tc.contentType)
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte(tc.body))
				}))
				defer server.Close()
				api, err := New(server.URL, "test-token")
				if err != nil {
					t.Fatal(err)
				}
				if method == http.MethodDelete {
					err = api.DetachAttachment("/attachments/example", tag, tag)
				} else {
					_, err = api.Do(method, "/cards/example", []byte(`{"title":"Changed"}`), tag)
				}
				if err == nil || errors.Is(err, ErrMutationOutcomeUnknown) != tc.unknown ||
					(!tc.unknown && !strings.Contains(err.Error(), "unavailable, not applied")) {
					t.Fatalf("unexpected outcome classification: %v", err)
				}
				if calls.Load() != 1 {
					t.Fatal("mutation request was not sent exactly once")
				}
			})
		}
	}
}

func TestNewRejectsNonlocalOrAmbiguousOrigins(t *testing.T) {
	for _, origin := range []string{
		"http://localhost.example:8080", "http://localhost.:8080", "https://127.0.0.1:8080", "http://127.0.0.1",
		"http://localhost", "http://user@localhost:8080", "http://localhost:8080/",
		"http://user@127.0.0.1:8080", "http://127.0.0.1:8080/",
		"http://127.0.0.1:8080?", "http://127.0.0.1:8080#",
		"http://127.0.0.1:0", "http://127.0.0.1:65536",
	} {
		if _, err := New(origin, "test-token"); err == nil {
			t.Errorf("accepted invalid origin %q", origin)
		}
	}
}

func TestNewAcceptsLoopbackHostAliases(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "LOCALHOST"} {
		api, err := New("http://"+host+":8080", "test-token")
		if err != nil {
			t.Fatalf("host %s: %v", host, err)
		}
		if api.origin != "http://127.0.0.1:8080" {
			t.Fatalf("host %s did not use numeric loopback transport", host)
		}
	}
}

func TestMutationDoesNotRedirectOrLeakResponse(t *testing.T) {
	var redirectHits atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectHits.Add(1)
	}))
	defer redirect.Close()

	for _, status := range []int{http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPreconditionFailed, http.StatusOK} {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if r.Method != http.MethodPatch || r.URL.Path != apiPath+"/workspace" ||
				r.Header.Get("Authorization") != "Bearer test-token" ||
				r.Header.Get("If-Match") != `"workspace-2"` {
				t.Error("mutation request did not match the contract")
			}
			w.Header().Set("Location", redirect.URL)
			w.Header().Set("ETag", `"workspace-revision-2"`)
			w.WriteHeader(status)
			w.Write([]byte("response content must stay private"))
		}))
		api, err := New(server.URL, "test-token")
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		_, err = api.Do(http.MethodPatch, "/workspace", []byte(`{"pinned_board_ids":[]}`), `"workspace-2"`)
		server.Close()
		if err == nil || strings.Contains(err.Error(), "must stay private") {
			t.Errorf("unexpected error for HTTP %d: %v", status, err)
		}
		uncertain := status != http.StatusPreconditionFailed
		if err != nil && strings.Contains(err.Error(), "outcome unknown") != uncertain {
			t.Errorf("wrong outcome certainty for HTTP %d: %v", status, err)
		}
		if hits.Load() != 1 || redirectHits.Load() != 0 {
			t.Errorf("mutation dispatched more than once for HTTP %d", status)
		}
	}
}

func TestMutationRequiresCanonicalSuccessAcknowledgment(t *testing.T) {
	const tag = `"workspace-revision-2"`
	cases := []struct {
		name, method string
		status       int
		tags         []string
		success      bool
	}{
		{"created", http.MethodPost, http.StatusCreated, []string{tag}, true},
		{"updated", http.MethodPatch, http.StatusOK, []string{tag}, true},
		{"unexpected create status", http.MethodPost, http.StatusOK, []string{tag}, false},
		{"unexpected patch status", http.MethodPatch, http.StatusAccepted, []string{tag}, false},
		{"missing tag", http.MethodPatch, http.StatusOK, nil, false},
		{"weak tag", http.MethodPatch, http.StatusOK, []string{"W/" + tag}, false},
		{"malformed tag", http.MethodPatch, http.StatusOK, []string{`"short"`}, false},
		{"multiple tags", http.MethodPatch, http.StatusOK, []string{tag, tag}, false},
		{"joined tags", http.MethodPatch, http.StatusOK, []string{tag + ", " + tag}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				for _, value := range tc.tags {
					w.Header().Add("ETag", value)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte("{}"))
			}))
			defer server.Close()
			api, err := New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			reply, err := api.Do(tc.method, "/boards", []byte("{}"), "")
			if tc.success {
				if err != nil || reply.ETag != tag {
					t.Fatalf("valid acknowledgment refused: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "outcome unknown") {
				t.Fatalf("unusable acknowledgment was not reported as uncertain: %v", err)
			}
			if hits.Load() != 1 {
				t.Fatalf("expected exactly one mutation request, got %d", hits.Load())
			}
		})
	}
}
