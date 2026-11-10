package client

import (
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

func TestNewRejectsNonlocalOrAmbiguousOrigins(t *testing.T) {
	for _, origin := range []string{
		"http://localhost:8080", "https://127.0.0.1:8080", "http://127.0.0.1",
		"http://user@127.0.0.1:8080", "http://127.0.0.1:8080/",
		"http://127.0.0.1:8080?", "http://127.0.0.1:8080#",
		"http://127.0.0.1:0", "http://127.0.0.1:65536",
	} {
		if _, err := New(origin, "test-token"); err == nil {
			t.Errorf("accepted invalid origin %q", origin)
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
		_, err = api.Do(http.MethodPatch, "/workspace", []byte(`{"pinnedBoardIds":[]}`), `"workspace-2"`)
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
