package boardapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/writer"
)

const testBoard = "10000000-0000-0000-0000-000000000001"
const testInc = "20000000-0000-0000-0000-000000000001"
const testActor = "30000000-0000-0000-0000-000000000001"
const testCard = "40000000-0000-0000-0000-000000000001"
const base = "/api/v1/boards/" + testBoard

func TestExactRoutesExcludeOtherAdapters(t *testing.T) {
	for _, tc := range []struct {
		method, path, kind, command string
		status                      int
	}{
		{"GET", base, "board", "", 0}, {"GET", base + "/cards/" + testCard, "card", "", 0}, {"GET", base + "/cards/" + testCard + "/history", "history", "", 0},
		{"POST", base + "/cards", "mutation", "card.create", 0}, {"PATCH", base + "/cards/" + testCard, "mutation", "card.edit", 0},
		{"POST", base + "/cards/" + testCard + "/comments", "mutation", "comment.add", 0}, {"POST", base + "/cards/" + testCard + "/link-add", "mutation", "link.add", 0},
		{"POST", base + "/cards/" + testCard + "/link-remove", "mutation", "link.remove", 0}, {"POST", base + "/cards/" + testCard + "/review-handoff", "mutation", "review-handoff", 0}, {"POST", base + "/cards/" + testCard + "/approve", "mutation", "approve", 0},
		{"POST", base + "/recovery/preview", "preview", "", 0}, {"POST", base + "/recovery/confirm", "confirm", "", 0}, {"GET", base + "/operations/" + testCard + "?incarnation=" + testInc, "lookup", "", 0},
		{"GET", base + "/", "", "", 404}, {"GET", base + "?", "", "", 400}, {"GET", base + "?x=1", "", "", 400},
		{"DELETE", base + "/cards/" + testCard, "", "", 405}, {"GET", base + "/cards/" + testCard + "/history/", "", "", 404},
		{"POST", base + "/exports", "", "", 404}, {"POST", "/api/v1/grants", "", "", 404}, {"POST", base + "/cards/" + testCard + "/attachments", "", "", 404},
		{"GET", base + "/../" + testBoard, "", "", 404}, {"GET", strings.Replace(base, "boards", "%62oards", 1), "", "", 400},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			q, s := selectRoute(httptest.NewRequest(tc.method, tc.path, nil))
			if s != tc.status || s == 0 && (q.kind != tc.kind || string(q.command) != tc.command) {
				t.Fatal("route mismatch")
			}
		})
	}
}

func TestLookupExactOriginalEvidence(t *testing.T) {
	path := base + "/operations/" + testCard
	query := "incarnation=" + testInc + "&fingerprintVersion=1&fingerprint=" + strings.Repeat("a", 64)
	for _, suffix := range []string{"", "&fingerprintVersion=1", "&actorId=" + testActor, "&command=card.create", "&target=" + testCard, "&extra=1", ";bad"} {
		r := httptest.NewRequest("GET", path+"?"+query+suffix, nil)
		k, e := lookupIdentity(r, route{board: testBoard, operation: testCard}, testActor)
		if suffix == "" {
			if e != nil || k.ActorID != testActor || k.Incarnation != testInc {
				t.Fatal("original identity")
			}
		} else if e == nil {
			t.Fatal("lookup extra/duplicate accepted")
		}
	}
	for _, q := range []string{strings.Replace(query, "=1&", "=01&", 1), strings.Replace(query, "fingerprint=", "fingerprint=A", 1), "incarnation=" + testInc + "&fingerprintVersion=1"} {
		if _, e := lookupIdentity(httptest.NewRequest("GET", path+"?"+q, nil), route{board: testBoard, operation: testCard}, testActor); e == nil {
			t.Fatal("malformed lookup")
		}
	}
}

func TestResultMappingDoesNotInventProof(t *testing.T) {
	for _, code := range []string{"INVALID_INPUT", "UNAUTHENTICATED", "FORBIDDEN", "NOT_FOUND", "OPERATION_MISMATCH", "REVISION_CONFLICT", "UNAVAILABLE"} {
		w := httptest.NewRecorder()
		result(w, writer.Result{Refused: &T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}}}, false, true)
		if w.Code != statusFor(code) {
			t.Fatal("refusal status")
		}
		if _, e := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); e != nil {
			t.Fatal("refusal shape")
		}
	}
	u := T.KnownIdentityUnknown{APIVersion: 1, Outcome: "unknown", BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), ActorID: A.UUID(testActor), OpID: A.UUID(testCard), FingerprintVersion: 1, Fingerprint: A.Digest(strings.Repeat("a", 64)), Error: T.Error{Code: "OUTCOME_UNKNOWN", Message: "Original outcome unavailable"}}
	for _, lookup := range []bool{false, true} {
		w := httptest.NewRecorder()
		result(w, writer.Result{Unknown: &u, LookupCompleted: lookup}, false, lookup)
		want := 503
		if lookup {
			want = 200
		}
		if w.Code != want {
			t.Fatal("unknown status")
		}
		decoded, e := T.Decode[T.KnownIdentityUnknown](w.Body.Bytes())
		if e != nil || decoded != u {
			t.Fatal("unknown identity changed")
		}
	}
	w := httptest.NewRecorder()
	result(w, writer.Result{Unknown: &u}, false, true)
	if w.Code != 503 {
		t.Fatal("failed lookup reported successful observation")
	}
	w = httptest.NewRecorder()
	result(w, writer.Result{}, false, false)
	if w.Code != 503 {
		t.Fatal("invalid sum")
	}
}

func TestBodyBoundAndFailClosedConstruction(t *testing.T) {
	if _, e := New(nil, nil, nil); e == nil {
		t.Fatal("missing providers")
	}
	var h *Handler
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", base, nil))
	if w.Code != 503 {
		t.Fatal("nil handler")
	}
	w = httptest.NewRecorder()
	if _, ok := body(w, httptest.NewRequest("POST", base, strings.NewReader(strings.Repeat("x", maxBody+1)))); ok || w.Code != 413 {
		t.Fatal("body cap")
	}
	r := httptest.NewRequest("GET", base, nil)
	r.Header.Add("Authorization", "Bearer "+strings.Repeat("A", 43))
	r.Header.Add("Authorization", "Bearer "+strings.Repeat("A", 43))
	if _, ok := bearer(r); ok {
		t.Fatal("duplicate bearer")
	}
}

func TestRecoveryCacheLifetimePreservesNewPreview(t *testing.T) {
	h := &Handler{store: &S.Store{}, recovery: map[[32]byte]recoveryEntry{}}
	token := T.SecretToken(strings.Repeat("A", 43))
	a := &grants.Service{}
	first := h.recoveryFor("cli", token, a, true)
	h.finishPreview("cli", token, nil)
	key := recoveryKey("cli", string(token))
	entry := h.recovery[key]
	entry.until = time.Now().Add(time.Second)
	h.recovery[key] = entry
	if h.recoveryFor("cli", token, a, true) != first {
		t.Fatal("unexpired instance replaced")
	}
	entry = h.recovery[key]
	entry.until = time.Now().Add(-time.Second)
	h.recovery[key] = entry
	if h.recoveryFor("cli", token, a, false) != first {
		t.Fatal("in-flight preview evicted")
	}
	until := time.Now().UTC().Add(301 * time.Second).Truncate(time.Second)
	h.finishPreview("cli", token, &T.Preview{ExpiresAt: A.Timestamp(until.Format(time.RFC3339))})
	if h.recovery[key].until.Before(until) {
		t.Fatal("new preview expired early")
	}
	before := h.recovery[key].until
	if h.recoveryFor("cli", token, a, false) != first || !h.recovery[key].until.Equal(before) {
		t.Fatal("confirm extends/changes cache")
	}
	entry = h.recovery[key]
	entry.until = time.Now().Add(-time.Second)
	h.recovery[key] = entry
	if h.recoveryFor("cli", token, a, false) == first || len(h.recovery) != 0 {
		t.Fatal("expired confirm retained instance")
	}
	if h.recoveryFor("browser", token, a, false) == nil || len(h.recovery) != 0 {
		t.Fatal("uncached confirm has no S03 service")
	}
}
