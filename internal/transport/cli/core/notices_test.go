package core

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestNoticeControlNeverReplays(t *testing.T) {
	for _, action := range []string{"stop", "recover"} {
		t.Run(action, func(t *testing.T) { testNoticeControlNeverReplays(t, action) })
	}
}

func testNoticeControlNeverReplays(t *testing.T, action string) {
	const private = "SYNTHETIC_PRIVATE_PROSE"
	for _, mode := range []string{"stale", "lost-response", "invalid-response", "unavailable-response", "wrong-board", "oversized-status", "denied-status", "denied-post"} {
		t.Run(mode, func(t *testing.T) {
			view := T.NoticeView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, ControlGeneration: testGrant,
				State: "unconfigured", Entries: []T.NoticePair{}}
			if mode == "wrong-board" {
				view.Identity.BoardID = testInc
			}
			wire, err := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: view})
			if err != nil {
				t.Fatal("invalid fixture")
			}
			refusal := func(code string) []byte {
				t.Helper()
				b, err := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: private}})
				if err != nil {
					t.Fatal("invalid refusal fixture")
				}
				return b
			}
			var paths []string
			p := &provider{conn: connection(t, func(r *http.Request) (int, []byte, error) {
				paths = append(paths, r.Method+" "+r.URL.RequestURI())
				if r.Header.Get("Authorization") != "Bearer SYNTHETIC_SECRET" {
					t.Fatal("current credential missing")
				}
				if r.Method == http.MethodGet {
					if r.Body != nil {
						t.Fatal("read carries a body")
					}
					switch mode {
					case "denied-status":
						return 403, refusal("FORBIDDEN"), nil
					case "oversized-status":
						return 200, append(wire, bytes.Repeat([]byte(" "), T.NoticeResponseLimit)...), nil
					}
					return 200, wire, nil
				}
				body, err := io.ReadAll(r.Body)
				q, decodeErr := T.Decode[T.NoticeControlRequest](body)
				if err != nil || decodeErr != nil || q.Incarnation != testInc || q.ExpectedControlGeneration != testGrant || r.GetBody != nil {
					t.Fatal("control request lost exact generation or permitted replay")
				}
				switch mode {
				case "lost-response":
					return 0, nil, errors.New(private)
				case "invalid-response":
					return 200, []byte(private), nil
				case "unavailable-response":
					return 503, refusal("UNAVAILABLE"), nil
				case "denied-post":
					return 403, refusal("FORBIDDEN"), nil
				}
				return 409, refusal("CONTROL_STALE"), nil
			})}
			var out, diagnostic bytes.Buffer
			exit := (Runner{Provider: p}).Run(t.Context(), args("notices", action), nil, &out, &diagnostic)
			code, expectedExit := "UNAVAILABLE", 3
			want := []string{"GET /api/v1/boards/" + testBoard + "/notices/status?incarnation=" + testInc}
			switch mode {
			case "stale", "lost-response", "invalid-response", "unavailable-response", "denied-post":
				want = append(want, "POST /api/v1/boards/"+testBoard+"/notices/"+action)
			}
			switch mode {
			case "stale":
				code, expectedExit = "CONTROL_STALE", 2
			case "denied-status", "denied-post":
				code, expectedExit = "FORBIDDEN", 2
			case "lost-response", "invalid-response", "unavailable-response":
				code, expectedExit = "OUTCOME_UNKNOWN", 4
				v, err := A.Decode[noticeOutcomeUnknown](out.Bytes())
				if err != nil || v.Outcome != "unknown" || v.Error.Code != code {
					t.Fatal("submitted action uncertainty was not preserved")
				}
			}
			if exit != expectedExit || diagnostic.String() != code+"\n" || strings.Join(paths, ",") != strings.Join(want, ",") ||
				strings.Contains(out.String()+diagnostic.String(), private) {
				t.Fatal("unexpected notice dispatch, replay or disclosure")
			}
		})
	}
}

func TestNoticeLookupExactReceipt(t *testing.T) {
	target, digest := T.UUIDv4(testGrant), A.Digest(strings.Repeat("a", 64))
	zero, one := A.Number(0), A.Number(1)
	for _, mode := range []string{"accepted", "unknown", "different-delivery", "missing-lookup"} {
		t.Run(mode, func(t *testing.T) {
			pair := T.NoticePair{TargetID: target, DeliveryID: digest, AcceptedAt: "2026-11-06T12:00:00Z", Class: "human", UpperEvent: &one, EventCount: &one}
			if mode == "different-delivery" {
				pair.DeliveryID = A.Digest(strings.Repeat("b", 64))
			}
			lookup := &T.NoticeLookup{State: "accepted", Receipt: &pair}
			switch mode {
			case "unknown":
				lookup = &T.NoticeLookup{State: "unknown"}
			case "missing-lookup":
				lookup = nil
			}
			wire, err := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: T.NoticeView{
				Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, ControlGeneration: testActor, State: "guarded",
				TargetID: &target, Checkpoint: &zero, Entries: []T.NoticePair{}, Lookup: lookup,
			}})
			if err != nil {
				t.Fatal("invalid lookup fixture")
			}
			calls := 0
			p := &provider{conn: connection(t, func(r *http.Request) (int, []byte, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.RequestURI() != "/api/v1/boards/"+testBoard+"/notices/lookup" {
					t.Fatal("lookup did not use its exact POST route")
				}
				body, err := io.ReadAll(r.Body)
				q, decodeErr := T.Decode[T.NoticeLookupRequest](body)
				if err != nil || decodeErr != nil || q.Incarnation != testInc || q.TargetID != target || q.DeliveryID != digest {
					t.Fatal("lookup lost exact scope or receipt ID")
				}
				return 200, wire, nil
			})}
			var out, diagnostic bytes.Buffer
			exit := (Runner{Provider: p}).Run(t.Context(), args("notices", "lookup", "--target-id", string(target), "--delivery-id", string(digest)), nil, &out, &diagnostic)
			want := 0
			if mode == "different-delivery" || mode == "missing-lookup" {
				want = 4
			}
			if exit != want || calls != 1 || (want == 0 && !bytes.Equal(out.Bytes(), wire)) ||
				(want != 0 && bytes.Contains(out.Bytes(), []byte(pair.DeliveryID))) {
				t.Fatal("lookup accepted wrong evidence or redacted a valid result")
			}
		})
	}
}

func TestNoticeStopConveysPartialDrainAndOutputUncertainty(t *testing.T) {
	target, zero, reason := T.UUIDv4(testGrant), A.Number(0), T.NoticeReason("capacity")
	v := T.NoticeView{
		Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, ControlGeneration: testActor,
		State: "partial-drain", TargetID: &target, Checkpoint: &zero, Entries: []T.NoticePair{},
	}
	wire := func(view T.NoticeView) []byte {
		t.Helper()
		b, err := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: view})
		if err != nil {
			t.Fatal("invalid notice state fixture")
		}
		return b
	}
	partial := wire(v)
	v.State, v.Reason = "guarded", &reason
	guarded := wire(v)
	calls := 0
	p := &provider{conn: connection(t, func(req *http.Request) (int, []byte, error) {
		if req.Method == http.MethodPost {
			calls++
			return 200, partial, nil
		}
		return 200, guarded, nil
	})}
	runner := Runner{Provider: p}
	var out bytes.Buffer
	readable := prefix()[1:]
	if exit := runner.Run(t.Context(), append(readable, "notices", "status"), nil, &out, io.Discard); exit != 0 ||
		!strings.Contains(out.String(), "State: guarded\n") || !strings.Contains(out.String(), "Reason: capacity\n") {
		t.Fatal("guarded capacity was obscured")
	}
	out.Reset()
	if exit := runner.Run(t.Context(), append(readable, "notices", "stop"), nil, &out, io.Discard); exit != 0 ||
		!strings.Contains(out.String(), "State: partial-drain\n") {
		t.Fatal("partial drain was obscured")
	}
	if exit := runner.Run(t.Context(), args("notices", "stop"), nil, nil, io.Discard); exit != 4 || calls != 2 {
		t.Fatal("lost output downgraded a submitted notice control or replayed it")
	}
}
