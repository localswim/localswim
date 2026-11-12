package client

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
