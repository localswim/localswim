package client

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestT07RestoreRequiresCommittedBoardValidator(t *testing.T) {
	for _, resource := range []string{"/snapshots/00000000-0000-4000-8000-000000000001", "/restorations"} {
		for _, tag := range []string{"", `W/"1234567890abcdef"`, `"1234567890abcdef"`} {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if tag != "" {
					w.Header().Set("X-Board-ETag", tag)
				}
				if resource == "/restorations" {
					w.WriteHeader(http.StatusCreated)
				}
				io.WriteString(w, `{"revision":13}`)
			}))
			api, err := New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			method := http.MethodPut
			if resource == "/restorations" {
				method = http.MethodPost
			}
			_, err = api.Do(method, resource, []byte(`{}`), "")
			server.Close()
			if tag == `"1234567890abcdef"` && err != nil {
				t.Fatal(err)
			}
			if tag != `"1234567890abcdef"` && !errors.Is(err, ErrMutationOutcomeUnknown) {
				t.Fatalf("missing outcome uncertainty for invalid board tag: %v", err)
			}
			if requests != 1 {
				t.Fatal("restore replayed")
			}
		}
	}
}
