package credentials

import (
	"context"
	"errors"
	"testing"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
)

// Ordinary tests never resolve an N01 root or construct a real native provider.
func TestSessionAccessUnavailableLifecycle(t *testing.T) {
	for _, s := range []*Service{nil, {}, {closed: true}} {
		if s.Active() {
			t.Fatal("unavailable lifecycle reported active")
		}
		if _, err := s.VerifyCLI(context.Background(), nil, "", time.Now()); !errors.Is(err, ErrDenied) {
			t.Fatal("unavailable lifecycle verified CLI")
		}
		called := false
		err := s.WithBrowserGrant(context.Background(), "", T.CodeRequest{}, func(BrowserGrantScope) error { called = true; return nil })
		if err == nil || called {
			t.Fatal("unavailable lifecycle published browser scope")
		}
	}
}

func TestSessionAccessDoesNotWaitForLifecycleWriter(t *testing.T) {
	s := &Service{}
	s.mu.Lock()
	defer s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if s.Active() {
			t.Error("contended lifecycle reported active")
		}
		if _, err := s.VerifyCLI(context.Background(), nil, "", time.Now()); err == nil {
			t.Error("contended lifecycle verified CLI")
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("board-held callback waited for lifecycle writer")
	}
}
