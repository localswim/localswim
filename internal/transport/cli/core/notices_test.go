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
	for _, action := range []string{"configure", "start", "stop", "recover", "reload", "retarget"} {
		t.Run(action, func(t *testing.T) { testNoticeControlNeverReplays(t, action) })
	}
}

func testNoticeControlNeverReplays(t *testing.T, action string) {
	const private = "SYNTHETIC_PRIVATE_PROSE"
	for _, mode := range []string{"stale", "lost-response", "invalid-response", "unavailable-response", "wrong-board", "oversized-status", "denied-status", "denied-post", "unknown-response", "malformed-unknown", "busy", "overflow"} {
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
				var q T.NoticeControlRequest
				var decodeErr error
				if action == "retarget" {
					x, e := T.Decode[T.NoticeRetargetRequest](body)
					decodeErr = e
					if x.TargetID != testActor {
						t.Fatal("retarget lost canonical target")
					}
					q = T.NoticeControlRequest{Incarnation: x.Incarnation, ExpectedControlGeneration: x.ExpectedControlGeneration, ExpectedConfigurationRevision: x.ExpectedConfigurationRevision}
				} else {
					q, decodeErr = T.Decode[T.NoticeControlRequest](body)
				}
				if err != nil || decodeErr != nil || q.Incarnation != testInc || q.ExpectedControlGeneration != testGrant || r.GetBody != nil {
					t.Fatal("control request lost exact generation or permitted replay")
				}
				switch mode {
				case "lost-response":
					return 0, nil, errors.New(private)
				case "invalid-response":
					return 200, []byte(private), nil
				case "unknown-response":
					b, _ := T.Readable(T.NoticeOutcomeUnknown{APIVersion: 1, Outcome: "unknown", Error: T.NoticeUnknownError{Code: "OUTCOME_UNKNOWN", Message: T.NoticeUnknownMessage}})
					return 503, b, nil
				case "malformed-unknown":
					return 503, []byte(`{"apiVersion":1,"outcome":"unknown","error":{"code":"OUTCOME_UNKNOWN","message":"private"}}`), nil
				case "busy":
					return 409, refusal("CONTROL_BUSY"), nil
				case "overflow":
					return 409, refusal("CONFIGURATION_EXHAUSTED"), nil
				case "unavailable-response":
					return 503, refusal("UNAVAILABLE"), nil
				case "denied-post":
					return 403, refusal("FORBIDDEN"), nil
				}
				return 409, refusal("CONTROL_STALE"), nil
			})}
			var out, diagnostic bytes.Buffer
			command := []string{"notices", action}
			if action == "retarget" {
				command = append(command, "--target-id", testActor)
			}
			exit := (Runner{Provider: p}).Run(t.Context(), args(command...), nil, &out, &diagnostic)
			code, expectedExit := "UNAVAILABLE", 3
			want := []string{"GET /api/v1/boards/" + testBoard + "/notices/status?incarnation=" + testInc}
			switch mode {
			case "stale", "lost-response", "invalid-response", "unavailable-response", "denied-post", "unknown-response", "malformed-unknown", "busy", "overflow":
				want = append(want, "POST /api/v1/boards/"+testBoard+"/notices/"+action)
			}
			switch mode {
			case "stale":
				code, expectedExit = "CONTROL_STALE", 2
			case "busy":
				code, expectedExit = "CONTROL_BUSY", 2
			case "overflow":
				code, expectedExit = "CONFIGURATION_EXHAUSTED", 2
			case "unavailable-response":
				code, expectedExit = "UNAVAILABLE", 2
			case "denied-status", "denied-post":
				code, expectedExit = "FORBIDDEN", 2
			case "lost-response", "invalid-response", "unknown-response", "malformed-unknown":
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
				ConfigurationRevision: 1, TargetID: &target, Checkpoint: &zero, Entries: []T.NoticePair{}, Lookup: lookup,
			}})
			if err != nil {
				t.Fatal("invalid lookup fixture")
			}
			calls := 0
			p := &provider{conn: connection(t, func(r *http.Request) (int, []byte, error) {
				calls++
				if r.Method == http.MethodGet {
					status := T.NoticeView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, ControlGeneration: testActor, ConfigurationRevision: 1, State: "guarded", TargetID: &target, Checkpoint: &zero, Entries: []T.NoticePair{}}
					b, e := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: status})
					if e != nil {
						t.Fatal(e)
					}
					return 200, b, nil
				}
				if r.Method != http.MethodPost || r.URL.RequestURI() != "/api/v1/boards/"+testBoard+"/notices/lookup" {
					t.Fatal("lookup did not use its exact POST route")
				}
				body, err := io.ReadAll(r.Body)
				q, decodeErr := T.Decode[T.NoticeLookupRequest](body)
				if err != nil || decodeErr != nil || q.Incarnation != testInc || q.TargetID != target || q.DeliveryID != digest || q.ExpectedControlGeneration != testActor || q.ExpectedConfigurationRevision != 1 {
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
			if exit != want || calls != 2 || (want == 0 && !bytes.Equal(out.Bytes(), wire)) ||
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
		State: "partial-drain", ConfigurationRevision: 1, TargetID: &target, Checkpoint: &zero, Entries: []T.NoticePair{},
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

func TestNoticeRetargetRejectsNoncanonicalInvocation(t *testing.T) {
	for _, flags := range [][]string{
		nil, {"--target-id", ""}, {"--target-id", strings.ToUpper(testActor)},
		{"--target-id", " " + testActor}, {"--target-id", testActor + " "},
		{"--target-id", "00000000-0000-0000-0000-000000000000"},
		{"--target-id", "https://example.invalid/target"}, {"--target-id", strings.Repeat("a", 4096)},
		{"--target-id", testActor, "--target-id", testActor},
		{"--target-id", testActor, "--delivery-id", strings.Repeat("a", 64)},
	} {
		p := &provider{}
		var out bytes.Buffer
		command := append([]string{"notices", "retarget"}, flags...)
		if exit := (Runner{Provider: p}).Run(t.Context(), args(command...), nil, &out, io.Discard); exit != 1 || p.opened != 0 {
			t.Fatal("invalid target invocation reached credentials or service")
		}
	}
}

func TestNoticeRetargetPreservesHistoricalOutputAndExactVersions(t *testing.T) {
	future, original := T.UUIDv4(testActor), T.UUIDv4(testGrant)
	zero, one := A.Number(0), A.Number(1)
	delivery := A.Digest(strings.Repeat("a", 64))
	op := T.NoticeOperation("retarget")
	cut := &T.NoticeCut{TargetID: original, Class: "human", LowerEvent: zero, UpperEvent: &one, EventCount: &one, DeliveryID: &delivery}
	view := T.NoticeView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, ControlGeneration: testGrant,
		ConfigurationRevision: 9007199254740991, State: "partial-drain", ControlOperation: &op, TargetID: &future, Checkpoint: &zero,
		Last: cut, Pending: cut, Entries: []T.NoticePair{}}
	wire, err := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: view})
	if err != nil {
		t.Fatal(err)
	}
	for _, jsonMode := range []bool{false, true} {
		calls := 0
		p := &provider{conn: connection(t, func(r *http.Request) (int, []byte, error) {
			calls++
			if calls == 1 && r.Method != http.MethodGet || calls == 2 && r.Method != http.MethodPost {
				t.Fatal("control did not use one status then one POST")
			}
			if r.Method == http.MethodPost {
				b, e := io.ReadAll(r.Body)
				q, de := T.Decode[T.NoticeRetargetRequest](b)
				if e != nil || de != nil || q.ExpectedConfigurationRevision != view.ConfigurationRevision || q.ExpectedControlGeneration != view.ControlGeneration || q.TargetID != future {
					t.Fatal("exact versions/target lost")
				}
			}
			return 200, wire, nil
		})}
		command := append(prefix()[1:], "notices", "retarget", "--target-id", string(future))
		if jsonMode {
			command = args("notices", "retarget", "--target-id", string(future))
		}
		var out bytes.Buffer
		if exit := (Runner{Provider: p}).Run(t.Context(), command, nil, &out, io.Discard); exit != 0 || calls != 2 {
			t.Fatal("confirmed partial control did not retain single submission")
		}
		if jsonMode {
			if !bytes.Equal(out.Bytes(), wire) {
				t.Fatal("versioned JSON changed original evidence")
			}
		} else {
			for _, want := range []string{"Future target: " + string(future), "Configuration revision: 9007199254740991", "Pending original target: " + string(original), "Last original target: " + string(original), "Pending delivery: " + string(delivery), "events (0, 1], count 1", "Incomplete control: retarget"} {
				if !strings.Contains(out.String(), want) {
					t.Fatal("readable output concealed original versus future evidence")
				}
			}
		}
	}
}
