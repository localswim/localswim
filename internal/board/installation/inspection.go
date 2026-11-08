// Package installation owns metadata inspection and the authority-bound identity engine.
package installation

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrBusy       = errors.New("installation inspection busy")
	ErrClosed     = errors.New("installation inspection closed")
	ErrStale      = errors.New("installation inspection stale")
	ErrIncomplete = errors.New("installation inspection close incomplete")
)

type Observation struct{ State, Code string }

func Unqualified(code string) Observation { return Observation{"unqualified", code} }
func (o Observation) Valid() bool {
	switch o.State {
	case "candidate":
		return o.Code == "CANDIDATE_PRESENT"
	case "missing":
		return o.Code == "TOOL_MISSING"
	case "unqualified":
		switch o.Code {
		case "PLATFORM_UNQUALIFIED", "SOURCE_UNQUALIFIED", "PATH_UNQUALIFIED", "PUBLISHER_UNQUALIFIED", "FILE_CHANGED", "INSPECTION_UNKNOWN", "INSPECTION_TIMED_OUT":
			return true
		}
	}
	return false
}

// Backend returns owned cleanup even on failure. Cleanup must retain failed handles.
// Its caller keeps the independent work lease until cleanup actually succeeds.
type Backend func(context.Context) (Observation, func() error)

// closeOnce retains ambiguous resources forever rather than replaying a Close
// against a handle value whose lifetime can no longer be established.
func closeOnce(steps ...func() error) func() error {
	next, poisoned := 0, false
	return func() error {
		if poisoned {
			return ErrIncomplete
		}
		for next < len(steps) {
			if steps[next] != nil {
				if err := steps[next](); err != nil {
					poisoned = true
					return ErrIncomplete
				}
			}
			next++
		}
		return nil
	}
}

type Check func(context.Context) error
type answer struct {
	observation Observation
	err         error
}
type active struct {
	cancel context.CancelFunc
	done   chan struct{}
	// Retains custody after ambiguous cleanup; never invoked again without proof.
	quarantined    func() error
	probe          *probeTuple
	phase          string
	refusal        error
	workerDone     bool
	observers      int
	nextGeneration string
}
type Inspector struct {
	mu              sync.Mutex
	generation      string
	backend         Backend
	current         *active
	closing         bool
	deadline, drain time.Duration
	checkInterval   time.Duration
	checkGate       chan struct{}
	changed         chan struct{}
	admissions      map[*controlAdmission]struct{}
	available       string
	tombstone       *probeTuple
	binding         candidateBinding
	fresh           func() (string, error)
	probeBackend    probeBackend
	shutdown        context.Context
	shutdownCancel  context.CancelFunc
}

func New(generation string) *Inspector { return newInspector(generation, inspectNative) }
func newInspector(generation string, backend Backend) *Inspector {
	return newInspectorWithEntropy(generation, backend, freshGeneration)
}
func newInspectorWithEntropy(generation string, backend Backend, fresh func() (string, error)) *Inspector {
	s := &Inspector{generation: generation, backend: backend, deadline: 10 * time.Second, drain: 2 * time.Second, checkInterval: 100 * time.Millisecond, checkGate: make(chan struct{}, 1), changed: make(chan struct{}), admissions: make(map[*controlAdmission]struct{}), fresh: fresh, probeBackend: probeNative}
	s.shutdown, s.shutdownCancel = context.WithCancel(context.Background())
	s.checkGate <- struct{}{}
	if value, err := s.fresh(); err == nil && isGeneration(value) {
		s.available = value
	}
	return s
}
func (s *Inspector) Inspect(ctx context.Context, generation string, check Check) (Observation, error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return Observation{}, ErrClosed
	}
	if generation != s.generation {
		s.mu.Unlock()
		return Observation{}, ErrStale
	}
	if s.current != nil {
		s.mu.Unlock()
		return Observation{}, ErrBusy
	}
	work, cancel := context.WithTimeout(ctx, s.deadline)
	a := &active{cancel: cancel, done: make(chan struct{})}
	s.current = a
	s.mu.Unlock()
	result := make(chan answer, 1)
	go func() {
		var out answer
		if check == nil {
			out.err = ErrClosed
		} else if out.err = s.checked(work, check); out.err == nil {
			var cleanup func() error
			out.observation, cleanup = s.backend(work)
			if !out.observation.Valid() {
				out.observation = Unqualified("INSPECTION_UNKNOWN")
			}
			if cleanup != nil {
				if cleanup() != nil {
					s.mu.Lock()
					a.quarantined = cleanup
					s.mu.Unlock()
					cancel()
					result <- answer{err: ErrIncomplete}
					return // Neither release nor close(done): physical custody is unresolved.
				}
			}
			if work.Err() != nil {
				out.observation = Unqualified("INSPECTION_TIMED_OUT")
			}
			// This is a fresh authority/physical check, independent of the metadata timeout.
			s.mu.Lock()
			closing := s.closing
			s.mu.Unlock()
			if closing {
				out.err = ErrClosed
			} else {
				out.err = s.checked(ctx, check)
			}
		}
		cancel()
		s.mu.Lock()
		if s.closing {
			out.err = ErrClosed
			out.observation = Observation{}
		} else if ctx.Err() != nil {
			out.err = ctx.Err()
			out.observation = Observation{}
		}
		s.current = nil
		close(a.done)
		s.notifyLocked()
		s.mu.Unlock()
		result <- out
	}()
	select {
	case out := <-result:
		return out.observation, out.err
	case <-work.Done():
	}
	timer := time.NewTimer(s.drain)
	defer timer.Stop()
	select {
	case out := <-result:
		return out.observation, out.err
	case <-timer.C:
		// No final authority check completed within the foreground bound. Expose no facts.
		return Observation{}, ErrIncomplete
	}
}

// Close fences new work immediately and never releases an active native owner.
func (s *Inspector) Close() error {
	s.mu.Lock()
	s.closing = true
	s.shutdownCancel()
	if s.current != nil {
		s.current.cancel()
	}
	for admission := range s.admissions {
		admission.cancel()
	}
	s.mu.Unlock()
	timer := time.NewTimer(s.drain)
	defer timer.Stop()
	for {
		s.mu.Lock()
		complete := s.current == nil && len(s.admissions) == 0
		changed := s.changed
		s.mu.Unlock()
		if complete {
			return nil
		}
		select {
		case <-changed:
		case <-timer.C:
			return ErrIncomplete
		}
	}
}
