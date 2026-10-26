package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

const testInstallation = "10000000-0000-4000-8000-000000000001"
const testBoard = "00000000-0000-4000-8000-000000000001"
const testInc = "00000000-0000-4000-8000-000000000002"
const testActor = "00000000-0000-4000-8000-00000000000a"
const testGrant = "00000000-0000-4000-8000-000000000050"

var selected = Selectors{testInstallation, testBoard, testInc, testGrant}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("../../../..", "fixtures/contracts/transport-envelopes", name))
	if e != nil {
		t.Fatal("fixture unavailable")
	}
	return b
}
func prefix() []string {
	return []string{"--json", "--installation", testInstallation, "--board", testBoard, "--incarnation", testInc, "--grant", testGrant}
}
func args(command ...string) []string { return append(prefix(), command...) }

type memoryEvidence struct {
	record                          evidence.Record
	failSave, failLoad, failResolve bool
	saved, loaded, resolved         int
}

func (s *memoryEvidence) Save(r evidence.Record) (string, error) {
	s.saved++
	if s.failSave {
		return "", errors.New("PRIVATE_PATH SECRET")
	}
	s.record = r
	return "synthetic-evidence", nil
}
func (s *memoryEvidence) Load(string) (evidence.Record, error) {
	s.loaded++
	if s.failLoad {
		return evidence.Record{}, errors.New("PRIVATE_PATH SECRET")
	}
	return s.record, nil
}
func (s *memoryEvidence) Resolve(r evidence.Record, v T.Terminal) error {
	s.resolved++
	if s.failResolve {
		return errors.New("PRIVATE_PATH SECRET")
	}
	if !r.MatchesTerminal(v) {
		return errors.New("mismatch")
	}
	return nil
}

type provider struct {
	conn           *Connection
	err            error
	opened, native int
}

func (p *provider) Open(context.Context, Selectors) (*Connection, error) {
	p.opened++
	return p.conn, p.err
}
func (p *provider) NativeOwner(context.Context, evidence.Record) (*Connection, error) {
	p.native++
	return p.conn, p.err
}
func connection(t *testing.T, call func(*http.Request) (int, []byte, error)) *Connection {
	t.Helper()
	return &Connection{context: evidence.Context{InstallationID: testInstallation, Origin: "https://synthetic.localhost:47443"}, metadata: CredentialMetadata{Selected: selected, ActorID: testActor, ExpiresAt: time.Now().Add(time.Hour)}, authorization: "Bearer SYNTHETIC_SECRET", do: func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/session" {
			return response(200, fixture(t, "session-read.json")), nil
		}
		status, b, e := call(r)
		if e != nil {
			return nil, e
		}
		return response(status, b), nil
	}}
}
func response(status int, b []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b))}
}
func fixtureRecord(t *testing.T, name string) (evidence.Record, []byte) {
	t.Helper()
	semantic := fixture(t, "intent-"+name+".canonical.bin")
	var m map[string]json.RawMessage
	_ = json.Unmarshal(semantic, &m)
	w := map[string]json.RawMessage{}
	for _, key := range []string{"opId", "incarnation", "target", "expectedRevision", "expectedCardRevision"} {
		w[key] = m[key]
	}
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(m["payload"], &payload)
	for k, v := range payload {
		if string(v) == "null" {
			delete(payload, k)
		}
	}
	w["input"], _ = json.Marshal(payload)
	var cmd string
	_ = json.Unmarshal(m["command"], &cmd)
	var target contract.Target
	_ = json.Unmarshal(m["target"], &target)
	route := contract.ResolvedRoute{Command: contract.Command(cmd), BoardID: testBoard, TargetID: target.CardID}
	if target.GrantID != "" {
		route.TargetID = target.GrantID
	}
	if strings.HasPrefix(cmd, "grant.") {
		w["boardId"] = m["boardId"]
	}
	b, _ := json.Marshal(w)
	r, e := evidence.Freeze(evidence.Context{InstallationID: testInstallation, Origin: "https://synthetic.localhost:47443"}, route, contract.AuthenticatedActor{ActorID: testActor}, b)
	if e != nil {
		t.Fatal("fixture freeze")
	}
	return r, b
}
func run(t *testing.T, r Runner, a []string, input []byte, want int) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := r.Run(context.Background(), a, bytes.NewReader(input), &stdout, &stderr)
	if exit != want {
		t.Fatalf("exit %d want %d diagnostic %s", exit, want, stderr.String())
	}
	b := stdout.Bytes()
	if !bytes.HasSuffix(b, []byte("\n")) || bytes.Contains(b, []byte("SYNTHETIC_SECRET")) || bytes.Contains(b, []byte("PRIVATE_PATH")) || bytes.Contains(stderr.Bytes(), []byte("PRIVATE_PATH")) {
		t.Fatal("unsafe output")
	}
	if _, e := contract.DecodeJSON(b); e != nil {
		t.Fatal("exactly one JSON envelope required")
	}
	return b
}
func TestAllCommandsAndExactTerminalOutput(t *testing.T) {
	cases := []struct {
		name    string
		command []string
	}{
		{"card-create", []string{"card", "create"}}, {"card-edit", []string{"card", "edit"}}, {"comment-add", []string{"comment", "add"}}, {"link-add", []string{"card", "link-add"}}, {"link-remove", []string{"card", "link-remove"}}, {"review-handoff", []string{"card", "review-handoff"}}, {"approve", []string{"card", "approve"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original, wire := fixtureRecord(t, tc.name)
			store := &memoryEvidence{}
			calls := 0
			c := connection(t, func(req *http.Request) (int, []byte, error) {
				calls++
				if store.saved != 1 || store.loaded != 1 || !bytes.Equal(store.record.Semantic(), original.Semantic()) {
					t.Fatal("dispatch before immutable readback")
				}
				b, _ := io.ReadAll(req.Body)
				if _, e := T.FreezeMutation(b, original.Route(), contract.AuthenticatedActor{ActorID: testActor}); e != nil {
					t.Fatal("wire mismatch")
				}
				return 200, fixture(t, "command-"+tc.name+".json"), nil
			})
			command := append([]string{}, tc.command...)
			if tc.name != "card-create" {
				command = append(command, original.Route().TargetID)
			}
			command = append(command, "--input", "-")
			out := run(t, Runner{&provider{conn: c}, store}, args(command...), wire, 0)
			if !bytes.Equal(out, fixture(t, "command-"+tc.name+".json")) || calls != 1 || store.resolved != 1 {
				t.Fatal("terminal output/replay/status")
			}
		})
	}
}
func TestEvidenceFailureNeverDispatches(t *testing.T) {
	_, wire := fixtureRecord(t, "card-create")
	for _, mode := range []string{"absent", "exclusive", "flush", "readback"} {
		t.Run(mode, func(t *testing.T) {
			store := &memoryEvidence{failSave: mode == "exclusive" || mode == "flush", failLoad: mode == "readback"}
			c := connection(t, func(*http.Request) (int, []byte, error) { t.Fatal("dispatch on evidence failure"); return 0, nil, nil })
			r := Runner{&provider{conn: c}, store}
			if mode == "absent" {
				r.Evidence = nil
			}
			run(t, r, args("card", "create", "--input", "-"), wire, 1)
		})
	}
}
func TestPossibleTransmissionNeverUsesHTTPNoncommit(t *testing.T) {
	_, wire := fixtureRecord(t, "card-create")
	for _, name := range []string{"lost-response", "unauthenticated.json", "conflict-no-proof.json", "unavailable-read.json", "unknown.json", "malformed"} {
		t.Run(name, func(t *testing.T) {
			store := &memoryEvidence{}
			calls := 0
			c := connection(t, func(*http.Request) (int, []byte, error) {
				calls++
				switch name {
				case "lost-response":
					return 0, nil, errors.New("SECRET")
				case "malformed":
					return 200, []byte(`{"token":"SECRET"}`), nil
				}
				return 403, fixture(t, name), nil
			})
			b := run(t, Runner{&provider{conn: c}, store}, args("card", "create", "--input", "-"), wire, 4)
			if _, e := T.Decode[T.KnownIdentityUnknown](b); e != nil || calls != 1 || store.resolved != 0 {
				t.Fatal("unknown must retain original")
			}
		})
	}
}
func TestLookupNeverExecutesOrRebinds(t *testing.T) {
	original, _ := fixtureRecord(t, "card-create")
	lookup := args("operation", "read", original.Identity().OpID, "--evidence", "synthetic-evidence")
	for _, name := range []string{"confirmed", "denied", "missing", "wrong-actor", "wrong-route", "missing-file", "selector", "source-authority"} {
		t.Run(name, func(t *testing.T) {
			store := &memoryEvidence{record: original, failLoad: name == "missing-file"}
			calls := 0
			c := connection(t, func(req *http.Request) (int, []byte, error) {
				calls++
				if req.Method != "GET" || req.Body != nil || req.URL.Query().Get("fingerprint") != original.Fingerprint() {
					t.Fatal("lookup reconstructed/replayed")
				}
				switch name {
				case "confirmed":
					return 200, fixture(t, "command-card-create.json"), nil
				case "denied":
					return 403, fixture(t, "lookup-denied.json"), nil
				case "source-authority":
					return 200, fixture(t, "source-authority.json"), nil
				}
				return 200, fixture(t, "lookup-missing.json"), nil
			})
			a := append([]string{}, lookup...)
			want := 4
			if name == "confirmed" {
				want = 0
			}
			if name == "wrong-actor" {
				c.metadata.ActorID = "00000000-0000-4000-8000-000000000099"
			}
			if name == "wrong-route" {
				a[len(a)-3] = "00000000-0000-4000-8000-000000000099"
			}
			if name == "selector" {
				a = []string{"--json", "--board", testBoard, "operation", "read", original.Identity().OpID, "--evidence", "synthetic-evidence"}
			}
			run(t, Runner{&provider{conn: c}, store}, a, nil, want)
			if store.saved != 0 || calls > 1 {
				t.Fatal("lookup side effect/retry")
			}
		})
	}
}
func TestReadOutputAndIdentityAndExitClasses(t *testing.T) {
	for _, tc := range []struct {
		cmd, file string
		extra     []string
	}{{"board", "board-read.json", nil}, {"card", "card-read.json", []string{"00000000-0000-4000-8000-00000000000b"}}} {
		c := connection(t, func(*http.Request) (int, []byte, error) { return 200, fixture(t, tc.file), nil })
		a := append([]string{tc.cmd, "read"}, tc.extra...)
		b := run(t, Runner{&provider{conn: c}, nil}, args(a...), nil, 0)
		if !bytes.Equal(b, fixture(t, tc.file)) {
			t.Fatal("exact read output")
		}
	}
	for _, tc := range []struct {
		status int
		file   string
		exit   int
	}{{403, "forbidden.json", 2}, {503, "unavailable-read.json", 3}, {200, "unknown.json", 3}} {
		c := connection(t, func(*http.Request) (int, []byte, error) { return tc.status, fixture(t, tc.file), nil })
		run(t, Runner{&provider{conn: c}, nil}, args("board", "read"), nil, tc.exit)
	}
	run(t, Runner{}, []string{"--json", "board", "read"}, nil, 2)
	run(t, Runner{}, []string{"--json", "--board", testBoard, "--board", testBoard, "board", "read"}, nil, 1)
	run(t, Runner{}, args("board", "read"), nil, 3)
	for _, cmd := range [][]string{{"setup", "status"}, {"export", "create"}, {"card", "create", "--input", "-", "--actor", testActor}, {"board", "read", "--grant", testGrant}} {
		run(t, Runner{}, args(cmd...), nil, 1)
	}
}
func TestInputFilesStrictBindingAndNoLateReplay(t *testing.T) {
	original, wire := fixtureRecord(t, "card-create")
	store := &memoryEvidence{}
	c := connection(t, func(*http.Request) (int, []byte, error) { return 200, fixture(t, "command-card-create.json"), nil })
	r := Runner{&provider{conn: c}, store}
	file := filepath.Join(t.TempDir(), "input.json")
	if os.WriteFile(file, wire, 0600) != nil {
		t.Fatal("input fixture")
	}
	run(t, r, args("card", "create", "--input", file), nil, 0)
	bad := bytes.Replace(wire, []byte(testInc), []byte("00000000-0000-4000-8000-000000000099"), 1)
	run(t, r, args("card", "create", "--input", "-"), bad, 2)
	run(t, r, args("card", "create", "--input", "-"), []byte(`{"duplicate":1,"duplicate":2}`), 2)
	ctx, cancel := context.WithCancel(context.Background())
	c.do = func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v1/session" {
			return response(200, fixture(t, "session-read.json")), nil
		}
		cancel()
		return response(200, fixture(t, "command-card-create.json")), nil
	}
	var out bytes.Buffer
	if r.Run(ctx, args("card", "create", "--input", "-"), bytes.NewReader(wire), &out, io.Discard) != 4 {
		t.Fatal("late/canceled response changed uncertainty")
	}
	if !bytes.Equal(store.record.Semantic(), original.Semantic()) {
		t.Fatal("original changed")
	}
}
