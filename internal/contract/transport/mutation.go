package transport

import (
	"fmt"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

// FreezeMutation applies D03's complete required precondition spelling, then
// reuses C01's command catalog, strict inputs, independent route/actor binding,
// omission normalization and immutable semantic hash. It authenticates nobody,
// performs no admission/dispatch and makes no terminal-proof claim.
func FreezeMutation(wire []byte, route contract.ResolvedRoute, actor contract.AuthenticatedActor) (contract.Intent, error) {
	v, e := contract.DecodeJSON(wire)
	if e != nil {
		return contract.Intent{}, fmt.Errorf("invalid mutation JSON")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return contract.Intent{}, fmt.Errorf("invalid mutation object")
	}
	revision, ok := m["expectedCardRevision"]
	if !ok {
		return contract.Intent{}, fmt.Errorf("expectedCardRevision required")
	}
	known := false
	for _, d := range contract.Commands() {
		if d.Command != route.Command {
			continue
		}
		known = true
		if d.Target == "card" {
			raw, e := contract.Canonical(revision)
			if e != nil {
				return contract.Intent{}, fmt.Errorf("invalid card precondition")
			}
			if _, e = A.Decode[A.Positive](raw); e != nil {
				return contract.Intent{}, fmt.Errorf("positive card precondition required")
			}
		} else if revision != nil {
			return contract.Intent{}, fmt.Errorf("null card precondition required")
		}
	}
	if !known {
		return contract.Intent{}, fmt.Errorf("unsupported mutation command")
	}
	intent, e := contract.Freeze(wire, route, actor)
	if e != nil {
		return contract.Intent{}, fmt.Errorf("invalid mutation request or resolved context")
	}
	return intent, nil
}
