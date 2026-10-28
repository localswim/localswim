package workspace

import (
	"sync"

	T "github.com/localswim/localswim/internal/contract/transport"
	W "github.com/localswim/localswim/internal/workspace"
)

// Location locates private original evidence before attempting live authority.
// A nonnil Location on error owns cleanup and may locate immutable originals;
// it cannot authorize board dispatch or bypass Select's live/native checks.
type Location struct {
	mu      sync.Mutex
	root    *W.SelectionRoot
	cleanup closer
	done    chan struct{}
	closed  bool
}

func (*Location) String() string               { return "workspace private location [redacted]" }
func (l *Location) GoString() string           { return l.String() }
func (*Location) MarshalJSON() ([]byte, error) { return nil, fail("UNAVAILABLE", 3) }
func (l *Location) DataDir() string {
	if l == nil || l.root == nil {
		return ""
	}
	return l.root.DataDir()
}
func (l *Location) Done() <-chan struct{} { return l.done }
func (l *Location) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	if l.cleanup != nil {
		if l.cleanup.Close() != nil {
			return fail("UNAVAILABLE", 3)
		}
		l.cleanup = nil
	}
	if l.root != nil {
		if l.root.Close() != nil {
			return fail("UNAVAILABLE", 3)
		}
	}
	l.closed = true
	close(l.done)
	return nil
}
func Locate(root string, p, b T.WorkspaceKey) (*Location, error) {
	if !validKey(p) || !validKey(b) {
		return nil, fail("INVALID_INPUT", 2)
	}
	r, e := W.ReadSelection(root, p, b)
	l := &Location{root: r, done: make(chan struct{})}
	if e != nil {
		if x, ok := cleanupFrom(e); ok {
			l.cleanup = x
		}
		if r == nil && l.cleanup == nil {
			return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
		}
		return l, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	return l, nil
}
