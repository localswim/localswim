package core

import (
	"bytes"
	"context"
	"errors"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"net/http"
	"strings"
	"testing"
)

var recipientTestInput = T.RecipientInput{Version: 1, Recipient: T.RecipientReference{Kind: "codex-thread", ThreadID: "00000000-0000-7000-8000-000000000016"}, Policy: "conservative-metadata-v1"}

func recipientTestView() T.RecipientView {
	return T.RecipientView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, RuntimeGeneration: testGrant, State: "unconfigured"}
}
func recipientTestConnection(t *testing.T, call func(*http.Request) (int, []byte, error)) *Connection {
	c := connection(t, call)
	previous := c.do
	c.do = func(q *http.Request) (*http.Response, error) {
		r, e := previous(q)
		if r != nil {
			r.Header.Set("Cache-Control", "no-store")
		}
		return r, e
	}
	return c
}
func TestRecipientInvocationGrammar(t *testing.T) {
	for _, v := range [][]string{{"recipient", "status"}, {"recipient", "configure", "--input", `C:\private\input.json`}} {
		q, code, exit := parse(args(v...))
		if code != "" || exit != 0 || !strings.Contains(q.path, "/recipient/") {
			t.Fatal("valid grammar")
		}
	}
	for _, v := range [][]string{{"recipient", "configure"}, {"recipient", "configure", "--input", "-"}, {"recipient", "configure", "--input", "x", "--input", "x"}, {"recipient", "status", "--input", "x"}, {"recipient", "start"}, {"recipient", "configure", "--input", "x", "--target", "x"}} {
		_, code, _ := parse(args(v...))
		if code == "" {
			t.Fatal("invalid grammar accepted")
		}
	}
}
func TestRecipientCLIOriginalMatrix(t *testing.T) {
	for _, mode := range []string{"confirmed", "unconfigured200", "unknown200", "unavailable200", "malformed200", "wrong-board", "wrong-incarnation", "wrong-runtime", "wrong-request", "wrong-input", "wrong-revision", "missing-generation", "oversized", "lost", "stale", "busy", "fixed", "denied", "unauthenticated", "invalid", "wrong-status-refusal", "server-error", "malformed-refusal", "arbitrary-message"} {
		t.Run(mode, func(t *testing.T) {
			q := invocation{selected: selected, kind: "recipient configure", method: "POST"}
			original := T.RecipientConfigureRequest{Incarnation: testInc, ExpectedRuntimeGeneration: testGrant, RequestID: testActor, Input: recipientTestInput}
			calls := 0
			c := recipientTestConnection(t, func(r *http.Request) (int, []byte, error) {
				calls++
				b, e := io.ReadAll(r.Body)
				submitted, de := T.Decode[T.RecipientConfigureRequest](b)
				if e != nil || de != nil || submitted != original || r.GetBody != nil {
					t.Fatal("original not frozen or replayable")
				}
				if mode == "lost" {
					return 0, nil, errors.New("PRIVATE")
				}
				v := recipientTestView()
				v.State = "configured"
				v.ConfigurationRevision = 1
				gen, id := T.UUIDv4(testBoard), original.RequestID
				in := original.Input
				v.ConfigurationGeneration = &gen
				v.RequestID = &id
				v.Input = &in
				switch mode {
				case "unconfigured200", "unknown200", "unavailable200":
					v.State = T.RecipientState(strings.TrimSuffix(mode, "200"))
					v.ConfigurationRevision = 0
					v.ConfigurationGeneration = nil
					v.RequestID = nil
					v.Input = nil
					if v.State != "unconfigured" {
						reason := T.RecipientReason("unknown")
						v.Reason = &reason
					}
				case "wrong-board":
					v.Identity.BoardID = testInc
				case "wrong-incarnation":
					v.Identity.Incarnation = testBoard
				case "wrong-runtime":
					v.RuntimeGeneration = testActor
				case "wrong-request":
					*v.RequestID = testGrant
				case "wrong-input":
					v.Input.Recipient.ThreadID = T.RecipientThreadID(testActor)
				case "wrong-revision":
					v.ConfigurationRevision = 0
				case "missing-generation":
					v.ConfigurationGeneration = nil
				}
				if mode == "malformed200" {
					return 200, []byte("PRIVATE"), nil
				}
				if mode == "wrong-revision" || mode == "missing-generation" {
					return 200, []byte(`{"apiVersion":1,"scope":"recipient","result":{}}`), nil
				}
				code, status := "", 409
				switch mode {
				case "stale":
					code = "CONTROL_STALE"
				case "busy":
					code = "CONTROL_BUSY"
				case "fixed":
					code = "CONFIGURATION_FIXED"
				case "denied":
					code, status = "FORBIDDEN", 403
				case "unauthenticated":
					code, status = "UNAUTHENTICATED", 401
				case "invalid":
					code, status = "INVALID_INPUT", 400
				case "wrong-status-refusal":
					code, status = "CONTROL_BUSY", 400
				case "server-error":
					code, status = "UNAVAILABLE", 503
				case "arbitrary-message":
					code = "CONTROL_BUSY"
				case "malformed-refusal":
					return 409, []byte(`{}`), nil
				}
				if code != "" {
					message := "Request refused"
					if mode == "arbitrary-message" {
						message = "PRIVATE"
					}
					wire, _ := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: message}})
					return status, wire, nil
				}
				wire, e := T.Readable(T.RecipientResponse{APIVersion: 1, Scope: "recipient", Result: v})
				if e != nil {
					t.Fatal("fixture")
				}
				if mode == "oversized" {
					wire = append(wire, bytes.Repeat([]byte(" "), 4096)...)
				}
				return 200, wire, nil
			})
			out := requestRecipient(context.Background(), c, q, &original)
			want := 4
			if mode == "confirmed" {
				want = 0
			}
			switch mode {
			case "stale", "busy", "fixed", "denied", "unauthenticated", "invalid":
				want = 2
			}
			if out.exit != want || calls != 1 {
				t.Fatalf("exit=%d want=%d calls=%d", out.exit, want, calls)
			}
			if out.exit == 4 {
				wire, e := T.Readable(out.value)
				if e != nil || bytes.Contains(wire, []byte(recipientTestInput.Recipient.ThreadID)) || diagnostic(out.value) != "OUTCOME_UNKNOWN" {
					t.Fatal("unknown disclosed input")
				}
			}
		})
	}
}
func TestRecipientCLIPreflightNoReplay(t *testing.T) {
	for _, state := range []T.RecipientState{"unconfigured", "configured", "unknown", "unavailable"} {
		t.Run(string(state), func(t *testing.T) {
			q := invocation{selected: selected, kind: "recipient configure", method: "POST"}
			gets, posts := 0, 0
			c := recipientTestConnection(t, func(r *http.Request) (int, []byte, error) {
				v := recipientTestView()
				v.State = state
				if r.Method == "GET" {
					gets++
				} else {
					posts++
					return 503, []byte(`{}`), nil
				}
				if state == "configured" {
					gen, id := T.UUIDv4(testBoard), T.UUIDv4(testActor)
					in := recipientTestInput
					v.ConfigurationRevision = 1
					v.ConfigurationGeneration = &gen
					v.RequestID = &id
					v.Input = &in
				} else if state != "unconfigured" {
					reason := T.RecipientReason("unknown")
					v.Reason = &reason
				}
				b, _ := T.Readable(T.RecipientResponse{APIVersion: 1, Scope: "recipient", Result: v})
				return 200, b, nil
			})
			out := runRecipient(context.Background(), c, q, &recipientTestInput)
			if gets != 1 || state == "unconfigured" && (posts != 1 || out.exit != 4) || state != "unconfigured" && posts != 0 {
				t.Fatal("preflight/replay")
			}
		})
	}
}
