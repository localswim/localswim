package client

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestM86StreamedRestorePreservesOutcomeAndNoReplay(t *testing.T) {
	for _, status := range []int{200, 307, 412, 500} {
		for _, malformed := range []bool{false, true} {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				data, err := io.ReadAll(r.Body)
				if err != nil || string(data) != "ZIP body" || r.ContentLength != 8 ||
					r.Header.Get("Content-Type") != "application/zip" || r.Header.Get("If-Match") != `"original-validator"` {
					t.Errorf("incorrect streamed body or headers: %v", err)
				}
				w.Header().Set("X-Board-ETag", `"committed-validator"`)
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(status)
				if malformed {
					_, _ = io.WriteString(w, "broken")
				} else {
					_, _ = io.WriteString(w, `{"revision":13}`)
				}
			}))
			api, err := New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			_, err = api.DoWithReader(http.MethodPut, "/snapshots/00000000-0000-4000-8000-000000000001",
				strings.NewReader("ZIP body"), 8, `"original-validator"`, "application/zip")
			server.Close()
			wantUnknown := status == 307 || status == 500 || status == 200 && malformed
			if errors.Is(err, ErrMutationOutcomeUnknown) != wantUnknown || requests != 1 ||
				(status == 200 && !malformed) != (err == nil) {
				t.Fatalf("status=%d malformed=%t requests=%d error=%v", status, malformed, requests, err)
			}
		}
	}
}

func TestM86OnlyZIPRestoreExtendsClientDeadline(t *testing.T) {
	const ordinary = 100 * time.Millisecond
	for _, media := range []string{"application/zip", "application/json"} {
		t.Run(media, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				_, _ = io.Copy(io.Discard, r.Body)
				time.Sleep(3 * ordinary)
				w.Header().Set("X-Board-ETag", `"committed-validator"`)
				_, _ = io.WriteString(w, `{"revision":13}`)
			}))
			api, err := New(server.URL, "test-token")
			if err != nil {
				server.Close()
				t.Fatal(err)
			}
			api.http.Timeout = ordinary
			_, err = api.DoWithReader(http.MethodPut, "/snapshots/00000000-0000-4000-8000-000000000001",
				strings.NewReader("data"), 4, `"original-validator"`, media)
			server.Close()
			if requests != 1 || api.http.Timeout != ordinary {
				t.Fatal("stream changed the shared deadline or replayed the write")
			}
			if media == "application/zip" && err != nil || media == "application/json" && !errors.Is(err, ErrMutationOutcomeUnknown) {
				t.Fatalf("unexpected deadline behavior: %v", err)
			}
		})
	}
}
