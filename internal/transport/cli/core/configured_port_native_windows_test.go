//go:build windows && amd64 && k02_native

package core

import (
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

func TestNativeConfiguredPortPinnedEvidenceAndLookup(t *testing.T) {
	// Explicit synthetic fixture choice, never a product port search/fallback.
	port := 48443
	f := newNativeAtPort(t, &port)
	stored, err := f.provider.manager.ReadContext()
	if err != nil || stored.Port != port || !strings.HasSuffix(stored.Authority, ":48443") || stored.Origin != "https://"+stored.Authority {
		t.Fatal("stored explicit port not preserved")
	}
	if _, err := T.Decode[T.BoardRead](f.call(t, 0, nil, "board", "read")); err != nil {
		t.Fatal("configured pinned current read unavailable")
	}
	f.lost.Store(true)
	wire := f.wire(t, "", 1, 0, map[string]any{"title": "Configured origin", "description": ""})
	unknown, err := T.Decode[T.KnownIdentityUnknown](f.call(t, 4, wire, "card", "create", "--input", "-"))
	if err != nil {
		t.Fatal("configured origin lost identity")
	}
	path := filepath.Join(f.root, "evidence", string(unknown.OpID)+".json")
	original, err := f.runner.Evidence.Load(path)
	if err != nil || original.Context().Origin != stored.Origin || original.Context().InstallationID != stored.InstallationID {
		t.Fatal("private original origin binding unavailable")
	}
	before := f.mutations.Load()
	terminal := nativeTerminal(t, f.call(t, 0, nil, "operation", "read", string(unknown.OpID), "--evidence", path))
	if !original.MatchesTerminal(terminal) || f.mutations.Load() != before {
		t.Fatal("configured original lookup replayed or rebound")
	}
	// Same semantic identity at a different origin is no authority to query the
	// actual current connection. The wrong original remains unknown, untouched.
	other, err := evidence.Freeze(evidence.Context{InstallationID: stored.InstallationID, Origin: strings.Replace(stored.Origin, ":48443", ":47443", 1)}, original.Route(), contract.AuthenticatedActor{ActorID: original.Identity().ActorID}, original.Wire())
	if err != nil || other.Fingerprint() != original.Fingerprint() {
		t.Fatal("context-only mismatch fixture unavailable")
	}
	c, err := f.provider.Open(nativeContext(t), f.provider.metadata.Selected)
	if err != nil {
		t.Fatal("configured selected connection unavailable")
	}
	prior := c.do
	lookups := 0
	c.do = func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/operations/") {
			lookups++
		}
		return prior(r)
	}
	r := Runner{Provider: &provider{conn: c}, Evidence: &memoryEvidence{record: other}}
	var stdout bytes.Buffer
	if r.Run(nativeContext(t), f.args("operation", "read", string(unknown.OpID), "--evidence", path), nil, &stdout, io.Discard) != 4 || lookups != 0 || f.mutations.Load() != before {
		t.Fatal("different origin dispatched original lookup")
	}
}
