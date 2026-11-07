package core

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	P "github.com/localswim/localswim/internal/contract/noticetext"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestNoticeTextExactReadAndRefusal(t *testing.T) {
	for _, mode := range []string{"complete", "fallback", "legacy", "missing", "other-target", "wrong-board", "wrong-incarnation", "lookup", "malformed", "denied", "lost", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			target, delivery := T.UUIDv4(testGrant), A.Digest(strings.Repeat("a", 64))
			one, zero := A.Number(1), A.Number(0)
			payload := P.Build(&P.NoticeTextFacts{Label: " Café 泳 ", From: "2026-11-07T00:00:00Z", Through: "2026-11-07T00:00:01Z", Tickets: []A.Positive{1}})
			if mode == "fallback" {
				payload = P.Build(nil)
			}
			pair := T.NoticePair{TargetID: target, DeliveryID: delivery, AcceptedAt: "2026-11-07T00:00:02Z", Class: "human", UpperEvent: &one, EventCount: &one, Payload: &payload}
			v := T.NoticeView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, ControlGeneration: testActor, State: "stopped", ConfigurationRevision: 1, TargetID: &target, Checkpoint: &zero, Entries: []T.NoticePair{pair}}
			switch mode {
			case "legacy":
				v.Entries[0].Payload = nil
			case "missing":
				v.Entries = []T.NoticePair{}
			case "other-target":
				v.Entries[0].TargetID = T.UUIDv4(testActor)
			case "wrong-board":
				v.Identity.BoardID = testInc
			case "wrong-incarnation":
				v.Identity.Incarnation = testBoard
			case "lookup":
				v.Lookup = &T.NoticeLookup{State: "accepted", Receipt: &pair}
			}
			wire, err := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: v})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "malformed" {
				wire = bytes.Replace(wire, []byte(`"version":1`), []byte(`"version":2`), 1)
			}
			if mode == "oversized" {
				wire = append(wire, bytes.Repeat([]byte(" "), T.NoticeResponseLimit)...)
			}
			calls := 0
			provider := &provider{conn: connection(t, func(r *http.Request) (int, []byte, error) {
				calls++
				if r.Method != "GET" || r.URL.RequestURI() != noticePath(Selectors{BoardID: testBoard, Incarnation: testInc}, "inbox", "GET") || r.Body != nil {
					t.Fatal("raw text used a mutation or another route")
				}
				if mode == "lost" {
					return 0, nil, errors.New("PRIVATE")
				}
				if mode == "denied" {
					b, _ := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "FORBIDDEN", Message: "PRIVATE"}})
					return 403, b, nil
				}
				return 200, wire, nil
			})}
			var out, stderr bytes.Buffer
			exit := (Runner{Provider: provider}).Run(t.Context(), append(prefix()[1:], "notices", "text", "--target", string(target), "--delivery", string(delivery)), nil, &out, &stderr)
			if mode == "complete" || mode == "fallback" {
				if exit != 0 || !bytes.Equal(out.Bytes(), []byte(payload.Text)) || stderr.Len() != 0 {
					t.Fatal("original bytes changed")
				}
			} else if exit == 0 || out.Len() != 0 || stderr.Len() == 0 || stderr.Len() > 64 || strings.Contains(stderr.String(), "PRIVATE") {
				t.Fatal("refusal leaked output or succeeded")
			}
			if calls != 1 {
				t.Fatal("read replayed")
			}
		})
	}
}

type exactWriter struct {
	n     int
	err   error
	calls int
}

func (w *exactWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.n > len(p) {
		return w.n, w.err
	}
	return w.n, w.err
}
func TestExactTextWriter(t *testing.T) {
	for _, w := range []*exactWriter{{n: 0}, {n: 2}, {n: 3, err: errors.New("failure")}, {n: 4}} {
		if WriteExactText(w, "abc") == nil || w.calls != 1 {
			t.Fatal("short/error write succeeded or retried")
		}
	}
	if WriteExactText(nil, "abc") == nil || WriteExactText(io.Discard, strings.Repeat("x", 513)) == nil || WriteExactText(io.Discard, string([]byte{255})) == nil || WriteExactText(io.Discard, "abc") != nil {
		t.Fatal("writer bounds invalid")
	}
}
func TestNoticeTextInvocationRefusalHasEmptyStdout(t *testing.T) {
	for _, flags := range [][]string{nil, {"--target", testActor}, {"--target", testActor, "--delivery", "bad"}, {"--target", testActor, "--delivery", strings.Repeat("a", 64), "--target", testActor}} {
		var out, stderr bytes.Buffer
		p := &provider{}
		if exit := (Runner{Provider: p}).Run(t.Context(), append(append(prefix()[1:], "notices", "text"), flags...), nil, &out, &stderr); exit != 1 || out.Len() != 0 || p.opened != 0 || stderr.String() != "INVOCATION_OR_IO\n" {
			t.Fatal("bad raw invocation leaked")
		}
	}
	var out, stderr bytes.Buffer
	if exit := (Runner{}).Run(t.Context(), args("notices", "text", "--target", testActor, "--delivery", strings.Repeat("a", 64)), nil, &out, &stderr); exit != 1 || out.Len() != 0 || stderr.String() != "INVOCATION_OR_IO\n" {
		t.Fatal("json raw text accepted")
	}
}

func TestNoticeTextRunnerPartialOutputFails(t *testing.T) {
	target, zero, one := T.UUIDv4(testGrant), A.Number(0), A.Number(1)
	payload := P.Build(nil)
	view := T.NoticeView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, ControlGeneration: testActor, State: "stopped", ConfigurationRevision: 1, TargetID: &target, Checkpoint: &zero, Entries: []T.NoticePair{{TargetID: target, DeliveryID: A.Digest(strings.Repeat("a", 64)), AcceptedAt: "2026-11-07T00:00:02Z", Class: "human", UpperEvent: &one, EventCount: &one, Payload: &payload}}}
	wire, err := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: view})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := &provider{conn: connection(t, func(r *http.Request) (int, []byte, error) { calls++; return 200, wire, nil })}
	var stderr bytes.Buffer
	w := &exactWriter{n: 5}
	exit := (Runner{Provider: p}).Run(t.Context(), append(prefix()[1:], "notices", "text", "--target", string(target), "--delivery", strings.Repeat("a", 64)), nil, w, &stderr)
	if exit != 1 || stderr.String() != "INVOCATION_OR_IO\n" || w.calls != 1 || calls != 1 {
		t.Fatal("partial output succeeded or replayed")
	}
}
