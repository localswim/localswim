package https

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/board/credentials"
)

var ErrUnavailable = errors.New("HTTPS service unavailable")

// Server borrows G02's already-owned N01 lifetime; it does not acquire a second
// setup lock. The composition owner shuts transport down before closing G02.
type Server struct {
	server    *http.Server
	listeners []net.Listener
	done      chan struct{}
	once      sync.Once
}

// Listen binds both exclusive loopback families at the accepted stored port or
// refuses atomically. There is no alternate port, wildcard, proxy or HTTP path.
func Listen(ctx context.Context, native *credentials.Service, handler http.Handler) (*Server, error) {
	if ctx == nil || native == nil || handler == nil {
		return nil, ErrUnavailable
	}
	c := native.Context()
	cfg := native.TLSConfig()
	if c.Authority == "" || c.Port < 1024 || c.Port > 65535 || cfg == nil || cfg.InsecureSkipVerify || cfg.MinVersion < tls.VersionTLS12 {
		return nil, ErrUnavailable
	}
	listeners, e := listenBoth(ctx, strconv.Itoa(c.Port))
	if e != nil {
		return nil, ErrUnavailable
	}
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers(w)
		if !native.Active() {
			refuse(w, 503, "UNAVAILABLE")
			return
		}
		if !connectionBoundary(w, r, c) {
			return
		}
		if r.Method != http.MethodGet && !writeMedia(r) {
			refuse(w, 400, "INVALID_REQUEST")
			return
		}
		handler.ServeHTTP(w, r)
	})
	s := &Server{server: &http.Server{Handler: guarded, TLSConfig: cfg, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024, ErrorLog: log.New(io.Discard, "", 0)}, listeners: listeners, done: make(chan struct{})}
	var workers sync.WaitGroup
	for _, l := range listeners {
		workers.Add(1)
		go func(l net.Listener) { defer workers.Done(); _ = s.server.Serve(tls.NewListener(l, cfg)); s.Close() }(l)
	}
	go func() { workers.Wait(); close(s.done) }()
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}
func (s *Server) Close() {
	if s != nil {
		s.once.Do(func() {
			_ = s.server.Close()
			for _, l := range s.listeners {
				_ = l.Close()
			}
		})
	}
}
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	e := s.server.Shutdown(ctx)
	s.Close()
	if e != nil {
		return ErrUnavailable
	}
	return nil
}
