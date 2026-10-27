//go:build windows && amd64 && v02qualification

package main

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Interposition happens only after the real handler returns. Domain admission,
// commits and operation receipts are never replaced by a fixture result.
type delivery struct {
	mu        sync.Mutex
	next      http.Handler
	nextMode  string
	gate      chan struct{}
	reached   bool
	active    sync.WaitGroup
	mutations atomic.Uint64
	lookups   atomic.Uint64
}
type responseBuffer struct {
	header http.Header
	status int
	bytes.Buffer
}

func (b *responseBuffer) Header() http.Header { return b.header }
func (b *responseBuffer) WriteHeader(n int) {
	if b.status == 0 {
		b.status = n
	}
}
func (b *responseBuffer) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	return b.Buffer.Write(p)
}
func (d *delivery) release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.gate != nil {
		close(d.gate)
		d.gate = nil
	}
	d.reached = false
}
func (d *delivery) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.active.Add(1)
	defer d.active.Done()
	if strings.Contains(r.URL.Path, "/operations/") && r.Method == "GET" {
		d.lookups.Add(1)
	}
	mode := ""
	var gate chan struct{}
	d.mu.Lock()
	if r.Method != "GET" && strings.Contains(r.URL.Path, "/boards/") {
		d.mutations.Add(1)
		mode = d.nextMode
		d.nextMode = ""
		if mode == "delay" {
			gate = make(chan struct{})
			d.gate = gate
		}
	}
	d.mu.Unlock()
	if mode == "" {
		d.next.ServeHTTP(w, r)
		return
	}
	b := &responseBuffer{header: make(http.Header)}
	d.next.ServeHTTP(b, r)
	if mode == "drop" {
		if h, ok := w.(http.Hijacker); ok {
			c, _, e := h.Hijack()
			if e == nil {
				_ = c.Close()
			}
		}
		return
	}
	d.mu.Lock()
	d.reached = true
	d.mu.Unlock()
	select {
	case <-gate:
	case <-r.Context().Done():
	}
	for k, v := range b.header {
		w.Header()[k] = v
	}
	if b.status == 0 {
		b.status = 200
	}
	w.WriteHeader(b.status)
	_, _ = w.Write(b.Bytes())
}
