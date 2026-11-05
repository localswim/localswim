//go:build windows && amd64 && k02_native

package core

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/localswim/localswim/internal/board/policyedit"
	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

func TestNativeWorkflowLostResponsePreservesOriginalUntilLookup(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("isolated native port unavailable")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	f := newNativeAtPort(t, &port)
	connectionContext, err := f.provider.manager.ReadContext()
	if err != nil {
		t.Fatal("native connection context unavailable")
	}
	candidate := policyedit.DefaultDefinition("51000000-0000-4000-8000-000000000011", 2)
	candidate.Lanes[0].Label = "Workflow response lost"
	wire := f.wire(t, "", 1, 0, map[string]any{"expectedPolicyVersion": 1, "workflow": candidate})
	original, err := evidence.Freeze(evidence.Context{InstallationID: connectionContext.InstallationID, Origin: connectionContext.Origin}, contract.ResolvedRoute{Command: "workflow.edit", BoardID: testBoard}, contract.AuthenticatedActor{ActorID: testActor}, wire)
	if err != nil {
		t.Fatal("workflow original could not freeze")
	}
	f.lost.Store(true)
	unknown, err := T.Decode[T.KnownIdentityUnknown](f.call(t, 4, wire, "workflow", "edit", "--input", "-"))
	if err != nil || string(unknown.OpID) != original.Identity().OpID || string(unknown.Fingerprint) != original.Fingerprint() || string(unknown.BoardID) != testBoard || string(unknown.Incarnation) != testInc || string(unknown.ActorID) != testActor || unknown.FingerprintVersion != 1 {
		t.Fatal("lost workflow response changed UNKNOWN identity")
	}
	path := filepath.Join(f.root, "evidence", string(unknown.OpID)+".json")
	retained, err := f.runner.Evidence.Load(path)
	if err != nil || !bytes.Equal(retained.Bytes(), original.Bytes()) || !bytes.Equal(retained.Semantic(), original.Semantic()) || !bytes.Equal(retained.Wire(), original.Wire()) {
		t.Fatal("lost workflow response changed retained original")
	}
	if f.mutations.Load() != 1 {
		t.Fatal("workflow loss caused retry or missing dispatch")
	}
	for _, command := range [][]string{{"workflow", "show"}, {"board", "read"}} {
		current, err := T.Decode[T.BoardRead](f.call(t, 0, nil, command...))
		if err != nil || current.Result.Revision != 2 || current.Result.PolicyVersion != 2 || !reflect.DeepEqual(current.Result.Workflow, &candidate) {
			t.Fatal("committed workflow state unavailable after lost response")
		}
		if _, err := os.Stat(path + ".resolved"); !os.IsNotExist(err) {
			t.Fatal("current read resolved original evidence")
		}
		unchanged, err := f.runner.Evidence.Load(path)
		if err != nil || !bytes.Equal(unchanged.Bytes(), original.Bytes()) || f.mutations.Load() != 1 {
			t.Fatal("current read changed or replayed original")
		}
	}
	terminal := nativeTerminal(t, f.call(t, 0, nil, "operation", "read", string(unknown.OpID), "--evidence", path))
	if terminal.Outcome != "committed" || terminal.Command != "workflow.edit" || !original.MatchesTerminal(terminal) || f.mutations.Load() != 1 {
		t.Fatal("deliberate workflow lookup failed or replayed mutation")
	}
	retained, err = f.runner.Evidence.Load(path)
	if err != nil || !bytes.Equal(retained.Bytes(), original.Bytes()) {
		t.Fatal("lookup rewrote retained workflow original")
	}
}
