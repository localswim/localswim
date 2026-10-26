package https

import (
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictBearerHeader(t *testing.T) {
	for _, value := range []string{"", "bearer " + strings.Repeat("A", 43), "Bearer " + strings.Repeat("A", 42), "Bearer " + strings.Repeat("A", 43) + ",x", "Bearer " + strings.Repeat("A", 42) + " "} {
		r := httptest.NewRequest("GET", "https://fixture.localhost/", nil)
		r.Header.Set("Authorization", value)
		if _, ok := bearer(r); ok {
			t.Fatal("invalid authorization header")
		}
	}
	r := httptest.NewRequest("GET", "https://fixture.localhost/", nil)
	r.Header.Add("Authorization", "Bearer "+strings.Repeat("A", 43))
	r.Header.Add("Authorization", "Bearer "+strings.Repeat("A", 43))
	if _, ok := bearer(r); ok {
		t.Fatal("duplicate authorization")
	}
}
func TestJSONBoundAndRefusalRedaction(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", 4097), "{}"} {
		r := httptest.NewRequest("POST", "https://fixture.localhost/", strings.NewReader(body))
		w := httptest.NewRecorder()
		r.Header.Set("Content-Type", "text/plain")
		if _, e := jsonBody(w, r); e == nil {
			t.Fatal("non JSON accepted")
		}
		r.Body = io.NopCloser(strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		_, e := jsonBody(w, r)
		if (len(body) > 4096) != (e != nil) {
			t.Fatal("body cap")
		}
	}
	w := httptest.NewRecorder()
	headers(w)
	refuse(w, 401, "UNAUTHENTICATED")
	if _, e := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); e != nil {
		t.Fatal("refusal shape")
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("secret/cache/cross-origin boundary")
	}
}
