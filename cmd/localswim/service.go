package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/board/credentials"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
)

// sourceSelection identifies only a preexisting board. It is not provisioning,
// restore activation, grant bootstrap, an alternate native root or a port override.
type sourceSelection struct {
	Directory      string
	Identity       schema.Identity
	InstallationID string
}

type service struct {
	store    *S.Store
	native   *credentials.Service
	sessions *session.Service
	server   *H.Server
	cancel   context.CancelFunc
	once     sync.Once
	err      error
	requests *requestLifetime
	ctx      context.Context
	done     chan struct{}
}

// requestLifetime joins handler completion even when transport shutdown times
// out. Closing a socket alone does not prove that its SQL/export lease ended.
type requestLifetime struct {
	mu      sync.Mutex
	closing bool
	active  sync.WaitGroup
	next    http.Handler
}

func (l *requestLifetime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		body, _ := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "UNAVAILABLE", Message: "Service unavailable"}})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(body)
		return
	}
	l.active.Add(1)
	l.mu.Unlock()
	defer l.active.Done()
	l.next.ServeHTTP(w, r)
}

func (l *requestLifetime) stop() { l.mu.Lock(); l.closing = true; l.mu.Unlock() }

// startService borrows an already verified embedded handler. The caller must
// validate assets before this function opens a source or acquires N01 authority.
func startService(ctx context.Context, selected sourceSelection, assets http.Handler) (*service, error) {
	if ctx == nil || ctx.Err() != nil || assets == nil {
		return nil, errComposition
	}
	store, e := S.Existing(ctx, selected.Directory, selected.Identity)
	if e != nil {
		return nil, errComposition
	}
	s := &service{store: store, ctx: ctx, done: make(chan struct{})}
	good := false
	defer func() {
		if !good {
			_ = s.close()
		}
	}()
	s.native, e = credentials.SourceRoot(store)
	if e != nil {
		return nil, errComposition
	}
	if selected.InstallationID != "" && s.native.Context().InstallationID != selected.InstallationID {
		return nil, errComposition
	}
	s.sessions, e = session.New(store, s.native)
	if e != nil {
		return nil, errComposition
	}
	handler, e := routes(store, s.native, s.sessions, assets)
	if e != nil {
		return nil, e
	}
	// Cancellation first drains transport. The provider's context watcher must
	// not release the N01/source lifetimes while requests still own SQL snapshots.
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.cancel = cancel
	s.requests = &requestLifetime{next: handler}
	s.server, e = H.Listen(lifetime, s.native, s.requests)
	if e != nil {
		return nil, errComposition
	}
	good = true
	go func() {
		select {
		case <-ctx.Done():
			_ = s.close()
		case <-s.server.Done():
			_ = s.closeWithCause(true)
		case <-lifetime.Done():
		}
	}()
	return s, nil
}

func (s *service) close() error {
	return s.closeWithCause(false)
}

func (s *service) closeWithCause(unexpected bool) error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		defer close(s.done)
		// Cancellation wins a simultaneous transport-completion observation.
		if unexpected && s.ctx.Err() == nil {
			s.store.Fence()
			s.err = errComposition
		}
		if s.requests != nil {
			s.requests.stop()
		}
		if s.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			s.err = errors.Join(s.err, s.server.Shutdown(ctx))
			cancel()
		}
		if s.cancel != nil {
			s.cancel()
		}
		if s.requests != nil {
			s.requests.active.Wait()
		}
		if s.sessions != nil {
			s.sessions.Close()
		}
		if s.native != nil {
			s.native.Close()
		}
		if s.store != nil {
			s.err = errors.Join(s.err, s.store.Close())
		}
	})
	return s.err
}
