package grantapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

const testBoard = "10000000-0000-0000-0000-000000000001"
const testInc = "20000000-0000-0000-0000-000000000001"
const testActor = "30000000-0000-0000-0000-000000000001"
const testGrant = "40000000-0000-0000-0000-000000000001"

func wire(command string) []byte {
	target := any(nil)
	input := map[string]any{"granteeActorId": testActor, "kind": "automation", "rights": []string{"read"}}
	if command == "grant.revoke" {
		target = map[string]any{"grantId": testGrant}
		input = map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"boardId": testBoard, "incarnation": testInc, "opId": "50000000-0000-0000-0000-000000000001", "target": target, "expectedRevision": 0, "expectedCardRevision": nil, "input": input})
	return b
}

func TestOnlyExactGrantRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path, command, target string
		status                        int
	}{
		{"POST", "/api/v1/grants", "grant.create", "", 0},
		{"DELETE", "/api/v1/grants/" + testGrant, "grant.revoke", testGrant, 0},
		{"GET", "/api/v1/grants", "", "", 405},
		{"POST", "/api/v1/grants/" + testGrant, "", "", 405},
		{"POST", "/api/v1/grants/", "", "", 404},
		{"DELETE", "/api/v1/grants/" + testGrant + "/", "", "", 404},
		{"POST", "/api/v1//grants", "", "", 404},
		{"POST", "/api/v1/grants?x=1", "", "", 400},
		{"POST", "/api/v1/grants?", "", "", 400},
		{"POST", "/api/v1/%67rants", "", "", 400},
		{"POST", "/api/v1/grants/codes", "", "", 404},
		{"GET", "/api/v1/boards/" + testBoard + "/operations/" + testGrant, "", "", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			c, g, s := selectRoute(r)
			if string(c) != tc.command || g != tc.target || s != tc.status {
				t.Fatal("route selection")
			}
		})
	}
}

func TestStrictBodyAndIndependentBinding(t *testing.T) {
	good := wire("grant.create")
	for _, b := range [][]byte{append([]byte{0xff}, good...), []byte(`{"boardId":"` + testBoard + `","boardId":"` + testBoard + `"}`), []byte(`{"boardId":"\ud800"}`), []byte(`[]`), []byte(strings.Repeat("x", maxBody+1))} {
		r := httptest.NewRequest("POST", "/api/v1/grants", strings.NewReader(string(b)))
		_, _, status := bodyScope(httptest.NewRecorder(), r)
		if status == 0 {
			t.Fatal("invalid syntax accepted")
		}
	}
	for _, tc := range []struct {
		command, target string
		body            []byte
	}{
		{"grant.create", "", good},
		{"grant.revoke", testGrant, wire("grant.revoke")},
	} {
		r := httptest.NewRequest("POST", "/api/v1/grants", strings.NewReader(string(tc.body)))
		b, board, status := bodyScope(httptest.NewRecorder(), r)
		if status != 0 || board != testBoard {
			t.Fatal("valid selector")
		}
		i, err := T.FreezeMutation(b, contract.ResolvedRoute{Command: contract.Command(tc.command), BoardID: board, TargetID: tc.target}, contract.AuthenticatedActor{ActorID: testActor})
		if err != nil {
			t.Fatal("freeze")
		}
		_, _, actor, _, pre, err := i.Context()
		if err != nil || actor != testActor || pre.ExpectedCardRevision != nil {
			t.Fatal("authenticated binding")
		}
		if _, err = T.FreezeMutation(b, contract.ResolvedRoute{Command: contract.Command(tc.command), BoardID: testActor, TargetID: tc.target}, contract.AuthenticatedActor{ActorID: testActor}); err == nil {
			t.Fatal("wrong board accepted")
		}
	}
	for _, replace := range []struct{ from, to string }{
		{`"expectedCardRevision":null`, `"expectedCardRevision":1`},
		{`"expectedRevision":0`, `"expectedRevision":0.0`},
		{`"rights":["read"]`, `"rights":["read","read"]`},
		{`"rights":["read"]`, `"rights":["read","create"]`},
		{`"kind":"automation"`, `"kind":"other"`},
		{`"input":{`, `"actorId":"` + testActor + `","input":{`},
	} {
		b := strings.Replace(string(good), replace.from, replace.to, 1)
		if _, err := T.FreezeMutation([]byte(b), contract.ResolvedRoute{Command: "grant.create", BoardID: testBoard}, contract.AuthenticatedActor{ActorID: testActor}); err == nil {
			t.Fatal("invalid complete request accepted")
		}
	}
	if _, err := T.FreezeMutation(wire("grant.revoke"), contract.ResolvedRoute{Command: "grant.revoke", BoardID: testBoard, TargetID: testActor}, contract.AuthenticatedActor{ActorID: testActor}); err == nil {
		t.Fatal("wrong target accepted")
	}
}

func TestRefusalMappingAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"INVALID_INPUT", 400}, {"UNAUTHENTICATED", 401}, {"FORBIDDEN", 403}, {"NOT_FOUND", 404}, {"OPERATION_MISMATCH", 409}, {"UNAVAILABLE", 503}} {
		w := httptest.NewRecorder()
		result(w, writer.Result{Refused: &T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: tc.code, Message: "Request refused"}}}, false)
		if w.Code != tc.status {
			t.Fatal("status mapping")
		}
		if _, err := T.Decode[T.NonterminalRefusal](w.Body.Bytes()); err != nil {
			t.Fatal("refusal contract")
		}
		if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "status") {
			t.Fatal("response boundary")
		}
	}
	w := httptest.NewRecorder()
	result(w, writer.Result{}, false)
	if w.Code != 503 {
		t.Fatal("invalid result did not fail closed")
	}
	r := httptest.NewRequest("POST", "/api/v1/grants", nil)
	r.Header.Add("Authorization", "Bearer "+strings.Repeat("A", 43))
	r.Header.Add("Authorization", "Bearer "+strings.Repeat("A", 43))
	if _, ok := bearer(r); ok {
		t.Fatal("duplicate auth")
	}
}

func TestCreatedStatusRequiresThisGeneratedGrant(t *testing.T) {
	r := writer.Result{Terminal: &T.Terminal{Command: "grant.create", Outcome: "committed", ResultRefs: []A.FactRef{{Kind: "grant", ID: A.UUID(testGrant)}}}}
	if !freshCreate(r, testGrant) || freshCreate(r, testActor) || freshCreate(r, "") {
		t.Fatal("fresh status identity")
	}
	r.Terminal.Outcome = "not_committed"
	if freshCreate(r, testGrant) {
		t.Fatal("refused application marked created")
	}
	if freshCreate(writer.Result{}, testGrant) {
		t.Fatal("uncertain application marked created")
	}
}
