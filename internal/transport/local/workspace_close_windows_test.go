//go:build windows && amd64

package local

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func closeFixture(t *testing.T) (*WorkspaceHost, *Runtime) {
	t.Helper()
	h, e := StartWorkspaceHost(context.Background(), WorkspaceHostOptions{WorkspaceID: uuid(), Port: freePort(t)})
	if e != nil {
		t.Fatal(e)
	}
	r, e := OpenEngine(context.Background(), EngineOptions{DataDir: filepath.Join(testRoot(t), "m"), WorkspaceID: h.id, Origin: h.Origin()})
	if e != nil {
		_ = h.Close()
		t.Fatal(e)
	}
	if e = h.Attach(r); e != nil {
		_ = r.Close()
		_ = h.Close()
		t.Fatal(e)
	}
	return h, r
}
func TestWorkspaceCloseReleasesCompletedMemberDespiteShutdownDiagnostic(t *testing.T) {
	for _, detach := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "detach"}[detach], func(t *testing.T) {
			h, r := closeFixture(t)
			if e := r.Close(); e != nil {
				t.Fatal(e)
			}
			// Labeled completed-shutdown diagnostic: native release already confirmed by
			// the engine Done channel; a cached transport error is not ownership.
			r.closeErr = errors.New("injected completed shutdown timeout")
			if detach {
				if e := h.Detach(r.id.BoardID); e == nil {
					t.Fatal("diagnostic lost")
				}
				h.mu.Lock()
				remaining := len(h.members)
				h.mu.Unlock()
				if remaining != 0 {
					t.Fatal("released member retained")
				}
				if e := h.Close(); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := h.Close(); e == nil {
					t.Fatal("diagnostic lost")
				}
				select {
				case <-h.Done():
				default:
					t.Fatal("released member prevented host completion")
				}
			}
		})
	}
}
func TestWorkspaceCloseRetainsUnresolvedMemberForRetry(t *testing.T) {
	h, r := closeFixture(t)
	r.storeCloseErr = errors.New("injected unresolved store cleanup")
	if e := h.Close(); e == nil {
		t.Fatal("unresolved close falsely succeeded")
	}
	select {
	case <-h.Done():
		t.Fatal("host completed with owned member")
	default:
	}
	h.mu.Lock()
	retained := h.members[r.id.BoardID] == r
	h.mu.Unlock()
	if !retained {
		t.Fatal("cleanup ownership lost")
	}
	r.storeCloseErr = nil
	if e := h.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("retry did not complete")
	}
}
