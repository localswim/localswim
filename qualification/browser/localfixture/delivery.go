//go:build windows && amd64 && v02qualification

package main

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type delivery struct {
	mu      sync.Mutex
	mode    string
	gate    chan struct{}
	reached bool
	count   atomic.Uint64
}
type captured struct {
	header http.Header
	status int
	bytes.Buffer
}

func (c *captured) Header() http.Header { return c.header }
func (c *captured) WriteHeader(n int) {
	if c.status == 0 {
		c.status = n
	}
}
func (c *captured) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	return c.Buffer.Write(p)
}
func (d *delivery) arm(mode string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.mode != "" || d.gate != nil {
		return false
	}
	d.mode = mode
	return true
}
func (d *delivery) delayReached() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.reached }
func (d *delivery) release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mode = ""
	if d.gate != nil {
		close(d.gate)
		d.gate = nil
	}
	d.reached = false
}
func (d *delivery) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := ""
		var gate chan struct{}
		d.mu.Lock()
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && strings.HasPrefix(r.URL.Path, "/api/v1/boards/") {
			d.count.Add(1)
			mode = d.mode
			d.mode = ""
			if mode == "delayNext" {
				gate = make(chan struct{})
				d.gate = gate
			}
		}
		d.mu.Unlock()
		if mode == "" {
			next.ServeHTTP(w, r)
			return
		}
		b := &captured{header: make(http.Header)}
		next.ServeHTTP(b, r)
		if mode == "dropNext" {
			// Deliver the actual response framing before truncating its body.
			// A close before headers lets browsers transparently retry a POST.
			// The length describes the captured handler body; no body is invented.
			for k, values := range b.header {
				w.Header()[k] = append([]string(nil), values...)
			}
			w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
			status := b.status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			} else {
				panic(http.ErrAbortHandler)
			}
			if h, ok := w.(http.Hijacker); ok {
				if c, _, e := h.Hijack(); e == nil {
					_ = c.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		}
		d.mu.Lock()
		if d.gate == gate {
			d.reached = true
		}
		d.mu.Unlock()
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		d.mu.Lock()
		if d.gate == gate {
			d.gate = nil
			d.reached = false
		}
		d.mu.Unlock()
		for k, values := range b.header {
			w.Header()[k] = append([]string(nil), values...)
		}
		status := b.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = w.Write(b.Bytes())
	})
}
