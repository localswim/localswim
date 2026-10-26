package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

// Ordinary tests never construct setup.Manager or access any native root.
func TestGrantAccessUnavailable(t *testing.T) {
	for _, s := range []*Service{nil, {}, {closed: true}} {
		called := false
		if err := s.WithNativeSnapshot(context.Background(), "", func(string, writer.Read) error { called = true; return nil }); !errors.Is(err, ErrUnavailable) || called {
			t.Fatal("unavailable service exposed snapshot")
		}
		if result, fresh := s.SubmitNativeObserved(context.Background(), "", contract.Intent{}, grants.Route{}); fresh || result.Result != nil {
			t.Fatal("unavailable service produced receipt")
		}
	}
}

func TestGrantAccessSelectOnly(t *testing.T) {
	for _, q := range []string{"DELETE FROM actor", "PRAGMA query_only=OFF", "SELECT 1; DELETE FROM actor", "SELECT 1\x00", ""} {
		if nativeSelect(q) {
			t.Fatal("non-single SELECT accepted")
		}
	}
	if !nativeSelect(" select 1") {
		t.Fatal("single SELECT refused")
	}
}

func TestGrantAccessFreshRequiresThisCommittedEffect(t *testing.T) {
	const id = "30000000-0000-0000-0000-000000000001"
	raw := json.RawMessage(`{"grantId":"` + id + `"}`)
	effect := writer.Effect{Result: raw, References: []A.FactRef{{Kind: "grant", ID: id}}}
	terminal := T.Terminal{Command: "grant.create", Outcome: "committed", Retention: "full", Result: &raw, ResultRefs: effect.References}
	if !nativeFreshCreate(writer.Result{Terminal: &terminal}, effect, id) {
		t.Fatal("matching Apply lost freshness")
	}
	for _, mode := range []string{"retained", "different-result", "different-reference", "compact", "revoke", "refused", "unknown", "not-committed"} {
		t.Run(mode, func(t *testing.T) {
			tm := terminal
			r := writer.Result{Terminal: &tm}
			capturedID := id
			switch mode {
			case "retained":
				capturedID = ""
			case "different-result":
				other := json.RawMessage(`{"grantId":"30000000-0000-0000-0000-000000000002"}`)
				tm.Result = &other
			case "different-reference":
				tm.ResultRefs = []A.FactRef{{Kind: "grant", ID: "30000000-0000-0000-0000-000000000002"}}
			case "compact":
				tm.Retention = "compact"
				tm.Result = nil
			case "revoke":
				tm.Command = "grant.revoke"
			case "refused":
				r.Refused = &T.NonterminalRefusal{}
			case "unknown":
				r.Unknown = &T.KnownIdentityUnknown{}
			case "not-committed":
				tm.Outcome = "not_committed"
			}
			if nativeFreshCreate(r, effect, capturedID) {
				t.Fatal("receipt not produced by this successful Apply reported fresh")
			}
		})
	}
}
