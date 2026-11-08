package installation

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func allow(context.Context) error { return nil }
func TestFreshObservationAndRefusal(t *testing.T) {
	var count atomic.Int32
	s := newInspector("g", func(context.Context) (Observation, func() error) {
		count.Add(1)
		return Observation{"candidate", "CANDIDATE_PRESENT"}, nil
	})
	if _, e := s.Inspect(context.Background(), "old", allow); !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
	denied := errors.New("denied")
	if _, e := s.Inspect(context.Background(), "g", func(context.Context) error { return denied }); e != denied {
		t.Fatal(e)
	}
	for range 2 {
		v, e := s.Inspect(context.Background(), "g", allow)
		if e != nil || v.State != "candidate" {
			t.Fatal(v, e)
		}
	}
	if count.Load() != 2 {
		t.Fatal("cached or unauthorized inspection")
	}
}
func TestBusyLateDeniedAndNoLockAcrossBackend(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := newInspector("g", func(context.Context) (Observation, func() error) {
		close(entered)
		<-release
		return Observation{"candidate", "CANDIDATE_PRESENT"}, nil
	})
	var revoked atomic.Bool
	check := func(context.Context) error {
		if revoked.Load() {
			return ErrStale
		}
		return nil
	}
	done := make(chan answer, 1)
	go func() { v, e := s.Inspect(context.Background(), "g", check); done <- answer{v, e} }()
	<-entered
	if _, e := s.Inspect(context.Background(), "g", allow); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	revoked.Store(true)
	close(release)
	out := <-done
	if out.err != ErrStale {
		t.Fatal("late facts disclosed", out)
	}
}
func TestIncompleteCleanupRetainsSlotAndClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := newInspector("g", func(context.Context) (Observation, func() error) {
		close(entered)
		return Observation{"candidate", "CANDIDATE_PRESENT"}, func() error { <-release; return nil }
	})
	s.deadline = 20 * time.Millisecond
	s.drain = 20 * time.Millisecond
	done := make(chan error, 1)
	go func() { _, e := s.Inspect(context.Background(), "g", allow); done <- e }()
	<-entered
	if e := <-done; !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
	if _, e := s.Inspect(context.Background(), "g", allow); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if e := s.Close(); !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
	close(release)
	deadline := time.After(time.Second)
	for {
		if s.Close() == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("cleanup not joined")
		default:
		}
	}
	if _, e := s.Inspect(context.Background(), "g", allow); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}
func TestInvalidBackendCannotExposeFacts(t *testing.T) {
	s := newInspector("g", func(context.Context) (Observation, func() error) {
		return Observation{"candidate", "private error"}, nil
	})
	v, e := s.Inspect(context.Background(), "g", allow)
	if e != nil || v != Unqualified("INSPECTION_UNKNOWN") {
		t.Fatal(v, e)
	}
}

func TestAmbiguousClosePoisonsWithoutReplay(t *testing.T) {
	var closes atomic.Int32
	cleanup := closeOnce(func() error { closes.Add(1); return errors.New("closed but response uncertain") })
	s := newInspector("g", func(context.Context) (Observation, func() error) {
		return Observation{"candidate", "CANDIDATE_PRESENT"}, cleanup
	})
	s.drain = time.Millisecond
	v, e := s.Inspect(context.Background(), "g", allow)
	if e != ErrIncomplete || v.State != "" {
		t.Fatal("ambiguous token close disclosed candidate", v, e)
	}
	for range 3 {
		if s.Close() != ErrIncomplete {
			t.Fatal("false joined")
		}
		if cleanup() != ErrIncomplete {
			t.Fatal("poison cleared")
		}
	}
	if closes.Load() != 1 {
		t.Fatal("closed/recycled resource was replayed", closes.Load())
	}
}

func TestCloseDuringFinalAuthorityCheckRedacts(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var checks atomic.Int32
	check := func(context.Context) error {
		if checks.Add(1) == 2 {
			close(entered)
			<-release
		}
		return nil
	}
	s := newInspector("g", func(context.Context) (Observation, func() error) {
		return Observation{"candidate", "CANDIDATE_PRESENT"}, nil
	})
	s.drain = time.Millisecond
	done := make(chan answer, 1)
	go func() { v, e := s.Inspect(context.Background(), "g", check); done <- answer{v, e} }()
	<-entered
	s.mu.Lock()
	active := s.current
	s.mu.Unlock()
	if active == nil {
		t.Fatal("final check lost active ownership")
	}
	if s.Close() != ErrIncomplete {
		t.Fatal("final check not retained")
	}
	s.mu.Lock()
	retained := s.current == active
	s.mu.Unlock()
	if !retained {
		t.Fatal("blocked final check released ownership")
	}
	select {
	case <-active.done:
		t.Fatal("blocked final check reported joined")
	default:
	}
	close(release)
	out := <-done
	if !errors.Is(out.err, ErrClosed) && !errors.Is(out.err, ErrIncomplete) || out.observation != (Observation{}) {
		t.Fatal("closed late result escaped", out)
	}
	select {
	case <-active.done:
	case <-time.After(time.Second):
		t.Fatal("final check did not join")
	}
	if s.Close() != nil {
		t.Fatal("joined close failed")
	}
}
