//go:build windows && amd64 && l02_native && m11_cli_native && m12_cli_native

package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	P "github.com/localswim/localswim/internal/contract/noticetext"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/local"
)

func TestNoticeTextActualBuiltCLIBoundaries(t *testing.T) {
	base, binary := os.Getenv("M12_CLI_ROOT"), os.Getenv("M12_CLI_BINARY")
	if !safeAbsolute(base) || !safeAbsolute(binary) {
		t.Fatal("explicit native root/binary required")
	}
	root, err := os.MkdirTemp(base, "text-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if os.RemoveAll(root) != nil {
			t.Error("cleanup failed")
		}
	}()
	p := activityCLIProof{t, binary, root}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	data := filepath.Join(root, "data")
	workspaceID := bridgeUUID(t)
	engine, err := local.OpenEngine(t.Context(), local.EngineOptions{DataDir: data, Origin: "http://" + listener.Addr().String(), WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal("native engine unavailable")
	}
	defer engine.Close()
	info := activityReadFixture(t, data)
	modeWire, _ := T.Readable(T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: T.UUIDv4(workspaceID)})
	target, other := T.UUIDv4(bridgeUUID(t)), T.UUIDv4(bridgeUUID(t))
	delivery := A.Digest(strings.Repeat("a", 64))
	one, zero := A.Number(1), A.Number(0)
	original := P.Build(&P.NoticeTextFacts{Label: "Original Café 泳", From: "2026-11-07T01:00:00Z", Through: "2026-11-07T01:00:01Z", Tickets: []A.Positive{1}})
	pair := T.NoticePair{TargetID: target, DeliveryID: delivery, AcceptedAt: "2026-11-07T01:00:02Z", Class: "human", UpperEvent: &one, EventCount: &one, Payload: &original}
	var mode atomic.Int32
	var reads, posts atomic.Int32
	path := "/api/v1/boards/" + info.BoardID + "/notices/"
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/runtime-mode" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(modeWire)
			return
		}
		if strings.HasPrefix(r.URL.Path, path) {
			if r.Method != "GET" || r.URL.Path != path+"inbox" {
				posts.Add(1)
				w.WriteHeader(500)
				return
			}
			reads.Add(1)
			state := mode.Load()
			if state == 6 {
				conn, _, e := http.NewResponseController(w).Hijack()
				if e == nil {
					_ = conn.Close()
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if state == 5 {
				b, _ := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "FORBIDDEN", Message: "PRIVATE"}})
				w.WriteHeader(403)
				_, _ = w.Write(b)
				return
			}
			selected := pair
			view := T.NoticeView{Identity: A.Identity{BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation)}, ControlGeneration: T.UUIDv4(info.GrantID), State: "stopped", ConfigurationRevision: 2, TargetID: &other, Checkpoint: &zero, Entries: []T.NoticePair{selected}}
			switch state {
			case 1:
				fallback := P.Build(nil)
				view.Entries[0].Payload = &fallback
			case 2:
				view.Entries[0].Payload = nil
			case 3:
				view.Entries = []T.NoticePair{}
			case 4:
				view.Identity.BoardID = A.UUID(bridgeUUID(t))
			case 8:
				view.Identity.Incarnation = A.UUID(bridgeUUID(t))
			}
			// A mixed-target inbox retains both original identities.
			second := pair
			second.TargetID = other
			second.DeliveryID = A.Digest(strings.Repeat("b", 64))
			fallback := P.Build(nil)
			second.Payload = &fallback
			view.Entries = append(view.Entries, second)
			b, e := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: view})
			if e != nil {
				w.WriteHeader(500)
				return
			}
			if state == 7 {
				b = bytes.Replace(b, []byte(`"version":1`), []byte(`"version":2`), 1)
			}
			_, _ = w.Write(b)
			return
		}
		engine.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		if <-done != http.ErrServerClosed {
			t.Error("host cleanup failed")
		}
	}()
	args := []string{"--data", data}
	for i, name := range []string{"original-mixed-target", "fallback", "legacy", "stale-missing", "foreign-board", "denied", "uncertain-response", "malformed-payload", "foreign-incarnation"} {
		t.Run(name, func(t *testing.T) {
			p.t = t
			mode.Store(int32(i))
			before := reads.Load()
			want, code := 3, "UNAVAILABLE"
			if i < 2 {
				want, code = 0, ""
			}
			if i == 5 {
				want, code = 2, "FORBIDDEN"
			}
			got := p.call(args, nil, want, code, "notices", "text", "--target", string(target), "--delivery", string(delivery))
			if i == 0 && !bytes.Equal(got, []byte(original.Text)) || i == 1 && !bytes.Equal(got, []byte(P.FallbackText)) || i >= 2 && len(got) != 0 {
				t.Fatal("raw bytes/refusal mismatch")
			}
			if reads.Load() != before+1 || posts.Load() != 0 {
				t.Fatal("raw command replayed or mutated")
			}
		})
	}
	p.t = t
	mode.Store(0)
	if got := p.call(args, nil, 3, "UNAVAILABLE", "notices", "text", "--target", string(other), "--delivery", string(delivery)); len(got) != 0 {
		t.Fatal("target and delivery were not matched together")
	}
	for _, prefix := range [][]string{nil, {"--data", filepath.Join(root, "missing")}, {"--workspace", filepath.Join(root, "missing"), "--project", "p", "--board", "b"}} {
		want, code := 1, "INVOCATION_OR_IO"
		if len(prefix) == 2 {
			want, code = 3, "UNAVAILABLE"
		}
		got := p.call(prefix, nil, want, code, "notices", "text")
		if len(got) != 0 {
			t.Fatal("early refusal contaminated stdout")
		}
	}
	help := p.call(nil, nil, 0, "", "--help")
	if !bytes.Contains(help, []byte("notices text --target UUIDv4 --delivery DIGEST")) {
		t.Fatal("raw text help missing")
	}
}
