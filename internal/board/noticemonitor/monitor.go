// Package noticemonitor serializes one board's fixed-target notice lifecycle.
// The caller owns the native board lease until Close succeeds.
package noticemonitor

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/noticeinterval"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

var (
	ErrStale       = errors.New("notice control generation stale")
	ErrBusy        = errors.New("notice control busy")
	ErrAuthority   = errors.New("notice authority refused")
	ErrUnavailable = errors.New("notice source unavailable")
)

type Authority func(context.Context, writer.Read) error

type Source struct {
	// Check validates ordinary authority even when an interval cannot be read.
	Check   func(context.Context, Authority) error
	Observe func(context.Context, Authority) (noticeinterval.Cut, error)
	Capture func(context.Context, noticeinterval.Cut, Authority) (noticeinterval.Evidence, error)
	// Signal returns an opaque native DB/WAL signature. Changes are wake hints.
	Signal func() (string, error)
}

type Status struct {
	Generation A.UUID
	Phase      string
	Reason     string
	State      *notices.State
}

type timings struct {
	poll, quiet, maxAge, stop time.Duration
}

type run struct {
	stop       chan struct{}
	done       chan struct{}
	stopAt     time.Time
	completion A.UUID
	partial    Status
	terminal   Status
}

type Monitor struct {
	store  *notices.Store
	source Source
	timing timings
	fault  error

	// The gate only guards memory. Source and Store operations may block.
	gate            sync.Mutex
	generation      A.UUID
	phase           string
	reason          string
	closed          bool
	reserved        bool
	run             *run
	calls           int
	closeGeneration A.UUID
	closedDone      chan struct{}
}

func New(store *notices.Store, source Source) (*Monitor, error) {
	if store == nil {
		return nil, notices.ErrStorage
	}
	m, err := newMonitor(source)
	if err != nil {
		return nil, err
	}
	m.store = store
	m.phase, m.reason = m.durableStatus()
	return m, nil
}

// NewUnavailable retains an authorized status surface for a refused sidecar.
// It never exposes private facts or opens a worker for the refused store.
func NewUnavailable(source Source, cause error) (*Monitor, error) {
	m, err := newMonitor(source)
	if err != nil {
		return nil, err
	}
	if !errors.Is(cause, notices.ErrForeign) {
		cause = notices.ErrStorage
	}
	m.fault = cause
	m.phase, m.reason = faultStatus(cause)
	return m, nil
}

func newMonitor(source Source) (*Monitor, error) {
	if source.Check == nil || source.Observe == nil || source.Capture == nil || source.Signal == nil {
		return nil, ErrUnavailable
	}
	generation, err := nextGeneration()
	if err != nil {
		return nil, err
	}
	return &Monitor{
		source: source, generation: generation, closedDone: make(chan struct{}),
		timing: timings{time.Second, 2 * time.Second, 10 * time.Second, 2 * time.Second},
	}, nil
}

func nextGeneration() (A.UUID, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", notices.ErrStorage
	}
	return A.UUID(id.String()), nil
}

func (m *Monitor) durableStatus() (string, string) {
	if m.store.Uncertain() {
		return "failed", "storage"
	}
	state := m.store.Snapshot()
	if state == nil {
		return "unconfigured", ""
	}
	if state.Dirty {
		if state.Last != nil && state.Last.Upper == nil {
			return "guarded", "unavailable"
		}
		return "guarded", "unknown"
	}
	return "stopped", ""
}

func (m *Monitor) Snapshot() Status {
	m.gate.Lock()
	defer m.gate.Unlock()
	return m.snapshotLocked()
}

func (m *Monitor) snapshotLocked() Status {
	s := Status{Generation: m.generation, Phase: m.phase, Reason: m.reason}
	if m.reason != "foreign" {
		s.State = m.store.Snapshot()
	}
	return s
}

func (m *Monitor) Inbox() []notices.Pair {
	m.gate.Lock()
	defer m.gate.Unlock()
	if m.reason == "foreign" {
		return []notices.Pair{}
	}
	return m.store.Inbox()
}

// borrow keeps Close from releasing the owner while a pre-admission authority
// read or a deliberate lookup can still access the board or private store.
func (m *Monitor) borrow() bool {
	m.gate.Lock()
	defer m.gate.Unlock()
	if m.closed {
		return false
	}
	m.calls++
	return true
}

func (m *Monitor) release() {
	m.gate.Lock()
	defer m.gate.Unlock()
	m.calls--
	m.closeIfQuiescent()
}

func (m *Monitor) closeIfQuiescent() {
	if m.closed && m.calls == 0 && m.run == nil {
		select {
		case <-m.closedDone:
		default:
			close(m.closedDone)
		}
	}
}

func (m *Monitor) authorize(ctx context.Context, auth Authority) error {
	if auth == nil {
		return ErrAuthority
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.source.Check(ctx, auth); err != nil {
		return err
	}
	return ctx.Err()
}

// Check authorizes a read of the monitor's cached, sanitized projection.
func (m *Monitor) Check(ctx context.Context, auth Authority) error {
	if !m.borrow() {
		return ErrBusy
	}
	defer m.release()
	return m.authorize(ctx, auth)
}

// precondition runs under the gate, after current authority was checked.
func (m *Monitor) precondition(ctx context.Context, expected A.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.closed {
		return ErrBusy
	}
	if expected != m.generation {
		return ErrStale
	}
	if m.reserved {
		return ErrBusy
	}
	return nil
}

func (m *Monitor) Configure(ctx context.Context, expected A.UUID, auth Authority) error {
	if !m.borrow() {
		return ErrBusy
	}
	defer m.release()
	if err := m.authorize(ctx, auth); err != nil {
		return err
	}
	generation, err := nextGeneration()
	if err != nil {
		return err
	}
	m.gate.Lock()
	if err := m.precondition(ctx, expected); err != nil {
		m.gate.Unlock()
		return err
	}
	if m.fault != nil || m.reason == "foreign" {
		err := m.fault
		if err == nil {
			err = notices.ErrForeign
		}
		m.gate.Unlock()
		return err
	}
	if m.store.Snapshot() != nil {
		m.gate.Unlock()
		return nil
	}
	m.reserved, m.generation = true, generation
	m.gate.Unlock()

	baseline, err := m.source.Observe(ctx, auth)
	if err == nil {
		_, err = m.store.Configure(baseline)
	}
	m.finishControl(err)
	return err
}

func (m *Monitor) Start(ctx context.Context, expected A.UUID, auth Authority) error {
	if !m.borrow() {
		return ErrBusy
	}
	defer m.release()
	if err := m.authorize(ctx, auth); err != nil {
		return err
	}
	generation, err := nextGeneration()
	if err != nil {
		return err
	}
	completion, err := nextGeneration()
	if err != nil {
		return err
	}
	m.gate.Lock()
	if err := m.precondition(ctx, expected); err != nil {
		m.gate.Unlock()
		return err
	}
	if m.fault != nil || m.reason == "foreign" {
		err := m.fault
		if err == nil {
			err = notices.ErrForeign
		}
		m.gate.Unlock()
		return err
	}
	if m.run != nil {
		m.gate.Unlock()
		return ErrBusy
	}
	state := m.store.Snapshot()
	if state == nil || state.Dirty || m.store.Uncertain() {
		m.gate.Unlock()
		return notices.ErrGuarded
	}
	m.reserved, m.generation = true, generation
	m.gate.Unlock()

	cut, err := m.source.Observe(ctx, auth)
	if err == nil && !cut.Follows(state.Checkpoint) {
		err = notices.ErrForeign
	}
	initial := observation{cut: state.Checkpoint}
	if err == nil {
		// Establish the wake baseline before Start can return. A subsequent
		// eventless write is eligible even if it beats the first worker poll.
		signature, signalErr := m.source.Signal()
		switch {
		case errors.Is(signalErr, notices.ErrForeign):
			err = signalErr
		case signalErr == nil:
			initial.signature, initial.sampled = signature, true
		}
	}
	if err == nil {
		err = m.store.BeginRun()
	}
	if err != nil {
		m.finishControl(err)
		return err
	}

	r := &run{stop: make(chan struct{}), done: make(chan struct{}), completion: completion}
	m.gate.Lock()
	m.reserved, m.run = false, r
	m.phase, m.reason = "running", ""
	draining := m.closed
	if draining {
		m.beginDrain(r, m.closeGeneration)
	}
	m.gate.Unlock()
	if draining {
		go m.awaitDrain(r)
	}
	go m.work(r, auth, initial)
	return nil
}

func (m *Monitor) finishControl(err error) {
	m.gate.Lock()
	defer m.gate.Unlock()
	m.reserved = false
	m.phase, m.reason = m.durableStatus()
	if err != nil {
		m.phase, m.reason = faultStatus(err)
	}
}

func (m *Monitor) Stop(ctx context.Context, expected A.UUID, auth Authority) error {
	_, err := m.StopView(ctx, expected, auth)
	return err
}

// StopView returns the outcome of the admitted Stop, even when a later Start
// changes the current status before the request can publish its response.
func (m *Monitor) StopView(ctx context.Context, expected A.UUID, auth Authority) (Status, error) {
	if !m.borrow() {
		return Status{}, ErrBusy
	}
	defer m.release()
	if err := m.authorize(ctx, auth); err != nil {
		return Status{}, err
	}
	generation, err := nextGeneration()
	if err != nil {
		return Status{}, err
	}
	m.gate.Lock()
	if err := m.precondition(ctx, expected); err != nil {
		m.gate.Unlock()
		return Status{}, err
	}
	r := m.run
	if r == nil {
		s := m.snapshotLocked()
		m.gate.Unlock()
		return s, nil
	}
	if r.stopAt.IsZero() {
		m.beginDrain(r, generation)
	}
	m.gate.Unlock()
	return m.awaitDrain(r), nil
}

// beginDrain runs under the gate and never waits for the worker.
func (m *Monitor) beginDrain(r *run, generation A.UUID) {
	r.stopAt = time.Now()
	m.generation = generation
	m.phase, m.reason = "draining", ""
	close(r.stop)
}

func (m *Monitor) awaitDrain(r *run) Status {
	timer := time.NewTimer(time.Until(r.stopAt.Add(m.timing.stop)))
	defer timer.Stop()
	select {
	case <-r.done:
	case <-timer.C:
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	if m.run == r && r.partial.Phase == "" {
		m.recordPartialDrain(r)
	}
	if r.partial.Phase != "" {
		return r.partial
	}
	return r.terminal
}

func (m *Monitor) recordPartialDrain(r *run) {
	m.phase = "partial-drain"
	r.partial = m.snapshotLocked()
}

// Close fences new access and drains an active worker. A deadline does not
// transfer ownership: the caller may release the native lease only on success.
func (m *Monitor) Close(ctx context.Context) error {
	generation, err := nextGeneration()
	if err != nil {
		return err
	}
	m.gate.Lock()
	if !m.closed {
		m.closed, m.closeGeneration = true, generation
	}
	if r := m.run; r != nil && r.stopAt.IsZero() {
		m.beginDrain(r, m.closeGeneration)
		go m.awaitDrain(r)
	}
	m.closeIfQuiescent()
	m.gate.Unlock()
	select {
	case <-m.closedDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Monitor) Lookup(ctx context.Context, target A.UUID, id string, auth Authority) (*notices.Pair, error) {
	if !m.borrow() {
		return nil, ErrBusy
	}
	defer m.release()
	if err := m.authorize(ctx, auth); err != nil {
		return nil, err
	}
	generation, err := nextGeneration()
	if err != nil {
		return nil, err
	}
	m.gate.Lock()
	if err := ctx.Err(); err != nil {
		m.gate.Unlock()
		return nil, err
	}
	if m.closed || m.reserved {
		m.gate.Unlock()
		return nil, ErrBusy
	}
	if m.fault != nil || m.reason == "foreign" {
		m.gate.Unlock()
		return nil, notices.ErrUnknown
	}
	quiescent := m.run == nil
	if quiescent {
		m.reserved = true
	}
	m.gate.Unlock()

	pair, changed, err := m.store.Lookup(target, id, quiescent)
	if quiescent {
		m.gate.Lock()
		m.reserved = false
		if changed {
			m.generation = generation
			m.phase, m.reason = m.durableStatus()
		}
		m.gate.Unlock()
	}
	return pair, err
}

func isAuthority(err error) bool {
	return errors.Is(err, ErrAuthority) || errors.Is(err, grants.ErrDenied) || errors.Is(err, grants.ErrUnauthenticated)
}

func faultStatus(err error) (string, string) {
	switch {
	case isAuthority(err):
		return "guarded", "authority"
	case errors.Is(err, notices.ErrForeign):
		return "guarded", "foreign"
	case errors.Is(err, notices.ErrCapacity):
		return "failed", "capacity"
	case errors.Is(err, notices.ErrUnknown), errors.Is(err, notices.ErrGuarded):
		return "guarded", "unknown"
	case errors.Is(err, notices.ErrStorage), errors.Is(err, notices.ErrInvalid):
		return "failed", "storage"
	default:
		return "guarded", "unavailable"
	}
}
