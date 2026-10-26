package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestDefinitiveNoncommitAndStatusPersistenceFailure(t *testing.T) {
	original, wire := fixtureRecord(t, "card-create")
	terminal, e := T.Decode[T.Terminal](fixture(t, "command-card-create.json"))
	if e != nil {
		t.Fatal("fixture")
	}
	terminal.Outcome = "not_committed"
	terminal.Code = "REVISION_CONFLICT"
	terminal.Revision = nil
	terminal.CardRevision = nil
	terminal.ResultRefs = []A.FactRef{}
	terminal.ByteFacts = []A.ByteFact{}
	raw := json.RawMessage(`{"error":{"code":"REVISION_CONFLICT","message":"Request refused","field":null},"conflict":null}`)
	terminal.Result = &raw
	b, e := T.Readable(terminal)
	if e != nil {
		t.Fatal("noncommit fixture")
	}
	if !original.MatchesTerminal(terminal) {
		t.Fatal("terminal identity")
	}
	store := &memoryEvidence{}
	c := connection(t, func(*http.Request) (int, []byte, error) { return 200, b, nil })
	out := run(t, Runner{&provider{conn: c}, store}, args("card", "create", "--input", "-"), wire, 2)
	if !bytes.Equal(out, b) || store.resolved != 1 {
		t.Fatal("definitive noncommit")
	}
	store.failResolve = true
	c = connection(t, func(*http.Request) (int, []byte, error) { return 200, fixture(t, "command-card-create.json"), nil })
	var stdout, stderr bytes.Buffer
	if (Runner{&provider{conn: c}, store}).Run(context.Background(), args("card", "create", "--input", "-"), bytes.NewReader(wire), &stdout, &stderr) != 0 || !bytes.Contains(stderr.Bytes(), []byte("EVIDENCE_STATUS_UNAVAILABLE")) {
		t.Fatal("local status failure erased confirmed board save")
	}
}
func TestMismatchingTerminalCannotCloseOriginal(t *testing.T) {
	original, _ := fixtureRecord(t, "card-edit")
	terminal, e := T.Decode[T.Terminal](fixture(t, "command-card-edit.json"))
	if e != nil {
		t.Fatal("fixture")
	}
	for _, change := range []func(*T.Terminal){func(v *T.Terminal) { v.ActorID = "00000000-0000-4000-8000-000000000099" }, func(v *T.Terminal) { v.ExpectedRevision++ }, func(v *T.Terminal) { n := A.Positive(*v.ExpectedCardRevision + 1); v.ExpectedCardRevision = &n }, func(v *T.Terminal) {
		id := A.UUID("00000000-0000-4000-8000-000000000099")
		v.Target = &A.Target{CardID: &id}
	}, func(v *T.Terminal) {
		v.Fingerprint = A.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	}} {
		v := terminal
		change(&v)
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal("fixture encoding")
		}
		out := classifyMutation(200, b, original, false)
		if out.exit != 4 {
			t.Fatal("original semantic binding changed")
		}
	}
	b, _ := T.Readable(terminal)
	if classifyMutation(201, b, original, false).exit != 4 {
		t.Fatal("unexpected creation status accepted")
	}
}
func TestCurrentCredentialFailureAndSessionMismatch(t *testing.T) {
	original, _ := fixtureRecord(t, "card-create")
	for _, failure := range []*Failure{{"CREDENTIAL_EXPIRED", 2}, {"CREDENTIAL_DISABLED", 2}, {"UNSAFE_LOCAL_MATERIAL", 1}, {"CREDENTIAL_UNAVAILABLE", 3}, {"PRIVATE_PATH SECRET", 99}} {
		r := Runner{&provider{err: failure}, &memoryEvidence{record: original}}
		want := failure.Exit
		if want == 99 {
			want = 3
		}
		run(t, r, args("board", "read"), nil, want)
		run(t, r, args("operation", "read", original.Identity().OpID, "--evidence", "synthetic-evidence"), nil, 4)
	}
	c := connection(t, func(*http.Request) (int, []byte, error) {
		t.Fatal("board dispatched after session mismatch")
		return 0, nil, nil
	})
	c.metadata.ActorID = "00000000-0000-4000-8000-000000000099"
	run(t, Runner{&provider{conn: c}, nil}, args("board", "read"), nil, 2)
	for _, a := range [][]string{{"--json", "--installation", testInstallation, "--board", testBoard, "operation", "read", original.Identity().OpID, "--evidence", "synthetic-evidence", "--native-owner"}, {"--json", "--board", testBoard, "operation", "read", original.Identity().OpID, "--evidence", "synthetic-evidence", "--native-owner", "--native-owner"}} {
		run(t, Runner{}, a, nil, 4)
	}
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestOutputFailureAfterConfirmedMutationRetainsUncertainty(t *testing.T) {
	_, wire := fixtureRecord(t, "card-create")
	c := connection(t, func(*http.Request) (int, []byte, error) { return 200, fixture(t, "command-card-create.json"), nil })
	if (Runner{&provider{conn: c}, &memoryEvidence{}}).Run(context.Background(), args("card", "create", "--input", "-"), bytes.NewReader(wire), brokenOutput{}, io.Discard) != 4 {
		t.Fatal("post-save output failure downgraded uncertainty")
	}
}
