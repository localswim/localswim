package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func failureExit(t *testing.T, err error, want int) {
	t.Helper()
	var f *Failure
	if !errors.As(err, &f) || f.Exit != want || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("fixed error class wanted %d", want)
	}
}

func TestRevokeCredentialOrderedOneShotAndSpent(t *testing.T) {
	for _, mode := range []string{"confirmed", "lost", "malformed", "refused", "wrong-scope", "truncated", "cancel-during-send", "marker-unknown", "marker-failed", "cancel-after-marker"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gets, deletes, markers := 0, 0, 0
			c := connection(t, nil)
			c.do = func(r *http.Request) (*http.Response, error) {
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/session":
					gets++
					if markers != 0 || deletes != 0 {
						t.Fatal("late session authentication")
					}
					return response(200, fixture(t, "session-read.json")), nil
				case "DELETE /api/v1/session":
					deletes++
					body, readErr := io.ReadAll(r.Body)
					if gets != 1 || markers != 1 || deletes != 1 || r.Header.Get("Authorization") != "Bearer SYNTHETIC_SECRET" || readErr != nil || len(body) != 0 || r.ContentLength != 0 || r.Header.Get("Content-Type") != "application/json" || len(r.Header.Values("Content-Type")) != 1 || r.GetBody != nil {
						t.Fatal("revocation order/authority/replay")
					}
					if mode == "lost" {
						return nil, errors.New("PRIVATE_SECRET PRIVATE_PATH")
					}
					b := []byte(`{"apiVersion":1,"scope":"session","outcome":"revoked"}`)
					status := 200
					if mode == "malformed" {
						b = []byte(`{"token":"PRIVATE_SECRET"}`)
					}
					if mode == "wrong-scope" {
						b = []byte(`{"apiVersion":1,"scope":"credential","outcome":"revoked"}`)
					}
					if mode == "refused" {
						status = 401
						b = fixture(t, "unauthenticated.json")
					}
					if mode == "cancel-during-send" {
						cancel()
					}
					resp := response(status, b)
					if mode == "truncated" {
						resp.ContentLength = int64(len(b) + 1)
					}
					return resp, nil
				default:
					t.Fatal("unexpected method or route")
					return nil, nil
				}
			}
			cl, e := Open(ctx, &provider{conn: c}, selected)
			if e != nil {
				t.Fatal(e)
			}
			defer cl.Close()
			err := cl.RevokeCredential(ctx, func() error {
				markers++
				if gets != 1 || deletes != 0 {
					t.Fatal("marker precedes authentication")
				}
				switch mode {
				case "marker-unknown":
					return errors.New("PRIVATE_SECRET PRIVATE_PATH")
				case "marker-failed":
					return &Failure{"INVOCATION_OR_IO", 1}
				case "cancel-after-marker":
					cancel()
				}
				return nil
			})
			if mode == "confirmed" {
				if err != nil {
					t.Fatal(err)
				}
			} else if mode == "marker-failed" {
				failureExit(t, err, 1)
			} else if mode == "cancel-after-marker" {
				failureExit(t, err, 3)
			} else if mode == "refused" {
				failureExit(t, err, 2)
			} else {
				failureExit(t, err, 4)
			}
			wantDeletes := 1
			if strings.HasPrefix(mode, "marker-") || mode == "cancel-after-marker" {
				wantDeletes = 0
			}
			if gets != 1 || deletes != wantDeletes || markers != 1 {
				t.Fatal("dispatch count")
			}
			if err := cl.RevokeCredential(context.Background(), func() error { t.Fatal("second marker"); return nil }); err == nil {
				t.Fatal("revocation replay")
			}
			if _, _, _, err := cl.Request(context.Background(), "GET", "/api/v1/boards/"+testBoard, nil, "", "application/json", 100); err == nil {
				t.Fatal("spent ordinary use")
			}
			_, wire := fixtureRecord(t, "card-create")
			store := &memoryEvidence{}
			if out := cl.Dispatch(context.Background(), store, contract.ResolvedRoute{Command: "card.create", BoardID: testBoard}, wire, nil); out.Exit() != 3 || store.saved != 0 {
				t.Fatal("spent evidence or mutation dispatch")
			}
			if cl.Metadata().Selected != selected || gets != 1 || deletes != wantDeletes {
				t.Fatal("metadata/no-late-auth")
			}
		})
	}
}

func TestRevokeCredentialPreflightAndNoNativeFallback(t *testing.T) {
	for _, mode := range []string{"canceled", "missing-marker", "native"} {
		t.Run(mode, func(t *testing.T) {
			c := connection(t, func(*http.Request) (int, []byte, error) { t.Fatal("preflight dispatch"); return 0, nil, nil })
			cl, e := Open(context.Background(), &provider{conn: c}, selected)
			if e != nil {
				t.Fatal(e)
			}
			defer cl.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			marker := func() error { t.Fatal("preflight marker"); return nil }
			want := 3
			switch mode {
			case "canceled":
				cancel()
			case "missing-marker":
				marker = nil
				want = 1
			case "native":
				c.native = true
				want = 2
			}
			failureExit(t, cl.RevokeCredential(ctx, marker), want)
		})
	}
}

func TestEphemeralRefusalDomainUncertaintyMatrix(t *testing.T) {
	original, _ := fixtureRecord(t, "card-create")
	req := T.CodeRequest{InstallationID: T.UUIDv4(testInstallation), BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), GrantID: A.UUID(testGrant)}
	for _, tc := range []struct {
		status int
		code   string
		want   int
	}{{400, "INVALID_INPUT", 2}, {401, "UNAUTHENTICATED", 2}, {403, "FORBIDDEN", 2}, {404, "NOT_FOUND", 2}, {409, "INCARNATION_CONFLICT", 2}, {413, "INPUT_TOO_LARGE", 2}, {503, "UNAVAILABLE", 4}, {500, "UNAVAILABLE", 4}, {200, "FORBIDDEN", 4}, {401, "FORBIDDEN", 4}, {403, "PRIVATE_SECRET", 4}} {
		body, e := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: tc.code, Message: "PRIVATE_SECRET PRIVATE_PATH"}})
		if e != nil {
			t.Fatal(e)
		}
		for _, code := range []bool{false, true} {
			c := connection(t, nil)
			c.native = true
			calls := 0
			c.do = func(*http.Request) (*http.Response, error) { calls++; return response(tc.status, body), nil }
			cl, e := OpenCurrent(context.Background(), &currentProvider{c: c})
			if e != nil {
				t.Fatal(e)
			}
			var err error
			if code {
				err = cl.IssueCode(context.Background(), req, func(T.CodeSecret) error { t.Fatal("refused secret callback"); return nil })
			} else {
				err = cl.IssueCredential(context.Background(), req, func(T.CredentialSecret) error { t.Fatal("refused secret callback"); return nil })
			}
			failureExit(t, err, tc.want)
			if calls != 1 {
				t.Fatal("ephemeral replay")
			}
			cl.Close()
		}
		// Self-revocation has the same strictly bound credential-action refusal
		// class, while preserving the marker and preventing any replay.
		c := connection(t, func(*http.Request) (int, []byte, error) { return tc.status, body, nil })
		cl, e := Open(context.Background(), &provider{conn: c}, selected)
		if e != nil {
			t.Fatal(e)
		}
		c.do = func(*http.Request) (*http.Response, error) { return response(tc.status, body), nil }
		markers := 0
		failureExit(t, cl.RevokeCredential(context.Background(), func() error { markers++; return nil }), tc.want)
		if markers != 1 {
			t.Fatal("credential refusal lost marker")
		}
		cl.Close()
		for _, lookup := range []bool{false, true} {
			out := classifyMutation(tc.status, body, original, lookup)
			if out.exit != 4 {
				t.Fatal("ephemeral refusal leaked into domain noncommit")
			}
			b, _ := T.Readable(out.value)
			if bytes.Contains(b, []byte("PRIVATE")) {
				t.Fatal("unsafe domain refusal rendering")
			}
		}
	}
	for _, code := range []string{"CONFIRMATION_REQUIRED", "CONFIRMATION_REFUSED"} {
		out := Refused(code, 2)
		b, e := T.Readable(out.Value())
		if e != nil || !bytes.Contains(b, []byte(code)) || diagnostic(out.Value()) != code || out.Exit() != 2 {
			t.Fatal("confirmation diagnostic fidelity")
		}
	}
}
