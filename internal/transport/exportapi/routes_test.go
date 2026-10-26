package exportapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	T "github.com/localswim/localswim/internal/contract/transport"
)

const testBoard = "10000000-0000-0000-0000-000000000001"
const testInc = "20000000-0000-0000-0000-000000000001"
const base = "/api/v1/boards/" + testBoard + "/exports"
const validBody = `{"incarnation":"` + testInc + `","expectedRevision":2,"input":{}}`

func TestExactExportRoute(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", base, 0}, {"GET", base, 405}, {"DELETE", base, 405},
		{"POST", base + "/", 404}, {"POST", base + "?", 400}, {"POST", base + "?x=1", 400},
		{"POST", strings.Replace(base, "boards", "%62oards", 1), 400},
		{"POST", strings.Replace(base, testBoard, strings.ToUpper("abcdefab-0000-0000-0000-000000000001"), 1), 404},
		{"POST", "/api/v1/boards/../exports", 404}, {"POST", base + "/more", 404},
		{"POST", strings.TrimSuffix(base, "/exports"), 404}, {"POST", "/api/v1/grants", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			board, status := selectRoute(httptest.NewRequest(tc.method, tc.path, nil))
			if status != tc.status || status == 0 && board != testBoard {
				t.Fatal("route mismatch")
			}
		})
	}
}

func TestStrictExportEnvelope(t *testing.T) {
	for _, raw := range []string{validBody,
		`{}`, strings.Replace(validBody, `"input":{}`, `"input":{},"input":{}`, 1),
		strings.Replace(validBody, `"input":{}`, `"input":{"path":"save.zip"}`, 1),
		strings.Replace(validBody, `"input":{}`, `"input":null`, 1),
		strings.Replace(validBody, `"input":{}`, `"opId":"30000000-0000-0000-0000-000000000001","input":{}`, 1),
		strings.Replace(validBody, `"expectedRevision":2`, `"expectedRevision":2e0`, 1),
		strings.Replace(validBody, `"expectedRevision":2`, `"expectedRevision":9007199254740992`, 1),
		strings.Replace(validBody, `"input":{}`, `"input":{"bad":"\ud800"}`, 1),
		string([]byte{0xff}) + validBody, validBody + `{}`, "\ufeff" + validBody,
	} {
		w := httptest.NewRecorder()
		q, ok := request(w, httptest.NewRequest("POST", base, strings.NewReader(raw)))
		if raw == validBody {
			if !ok || q.ExpectedRevision != 2 {
				t.Fatal("valid request")
			}
			continue
		}
		if ok || w.Code != 400 {
			t.Fatal("malformed request accepted")
		}
		if _, e := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); e != nil {
			t.Fatal("refusal shape")
		}
	}
	w := httptest.NewRecorder()
	if _, ok := request(w, httptest.NewRequest("POST", base, strings.NewReader(strings.Repeat(" ", maxBody+1)))); ok || w.Code != 413 {
		t.Fatal("body cap")
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", base, nil)
	r.Body = io.NopCloser(errorReader{})
	if _, ok := request(w, r); ok || w.Code != 400 {
		t.Fatal("body read error")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }

func TestConstructionAndUnauthenticatedDispatchFailClosed(t *testing.T) {
	if _, e := New(nil, nil, nil); e == nil {
		t.Fatal("missing providers")
	}
	var h *Handler
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", base, bytes.NewBufferString(validBody)))
	if w.Code != 503 {
		t.Fatal("nil handler")
	}
	h = &Handler{}
	w = httptest.NewRecorder()
	h.dispatch(w, httptest.NewRequest("POST", base, bytes.NewBufferString(validBody)))
	if w.Code != 401 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatal("missing boundary credential")
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{base + "?bad", 400}, {base + "/", 404}} {
		w = httptest.NewRecorder()
		h.dispatch(w, httptest.NewRequest(http.MethodPost, tc.path, nil))
		if w.Code != tc.status {
			t.Fatal("route refusal")
		}
	}
}
