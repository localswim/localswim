package local

import (
	"net/http/httptest"
	"testing"
)

func TestStrictRawCookieParsing(t *testing.T) {
	for _, tc := range []struct {
		name           string
		fields         []string
		valid, present bool
		value          string
	}{
		{"absent", nil, true, false, ""},
		{"ordinary", []string{"other=ok; session=token"}, true, true, "token"},
		{"quoted", []string{`session="token"`}, true, true, "token"},
		{"well-formed-stale", []string{"session=stale"}, true, true, "stale"},
		{"duplicate", []string{"session=token; session=other"}, false, false, ""},
		{"multiple-fields", []string{"session=token", "session=other"}, false, false, ""},
		{"unterminated-duplicate", []string{`session=token; session="unterminated`}, false, false, ""},
		{"backslash-duplicate", []string{`session=token; session=bad\value`}, false, false, ""},
		{"unrelated-malformed", []string{`session=token; other=bad\value`}, false, false, ""},
		{"malformed-name", []string{"bad name=value"}, false, false, ""},
		{"missing-equals", []string{"session"}, false, false, ""},
		{"empty-field", []string{""}, false, false, ""},
		{"invalid-quoted-tail", []string{`session="token"tail`}, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := httptest.NewRequest("GET", "/", nil)
			for _, field := range tc.fields {
				q.Header.Add("Cookie", field)
			}
			value, present, valid := sessionCookie(q, "session")
			if valid != tc.valid || present != tc.present || value != tc.value {
				t.Fatalf("cookie parse valid=%v present=%v", valid, present)
			}
		})
	}
}
