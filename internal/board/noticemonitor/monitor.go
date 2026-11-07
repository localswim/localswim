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
	ErrStale          = errors.New("notice control generation stale")
	ErrBusy           = errors.New("notice control busy")
	ErrAuthority      = errors.New("notice authority refused")
	ErrUnavailable    = errors.New("notice source unavailable")
	ErrOutcomeUnknown = errors.New("notice request outcome unknown")
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
	Operation  string
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
	operation       *operation
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
	if m.operation != nil && m.reason != "foreign" {
		s.Operation = m.operation.action
	}
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
	if m.closed && m.calls == 0 && m.run == nil && !m.reserved {
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
func (m *Monitor) precondition(ctx context.Context, expected A.UUID, revision A.Number) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.closed {
		return ErrBusy
	}
	if expected != m.generation || revision != m.revision() {
		return ErrStale
	}
	if m.reserved {
		return ErrBusy
	}
	return nil
}

// Control is the coordinated selected-board control boundary. Only a successful
// reservation detaches from the HTTP lifetime; all later effects retain auth.
// The returned view belongs to this operation, even if another control wins.
func (m *Monitor) Control(ctx context.Context, action string, expected A.UUID, revision A.Number, target A.UUID, delivery string, auth Authority) (Result, error) {
	if !m.borrow() {
		return Result{}, ErrBusy
	}
	defer m.release()
	if err := m.authorize(ctx, auth); err != nil {
		return Result{}, err
	}
	switch action {
	case "configure", "start", "stop", "reload", "recover":
	case "retarget":
		parsed, err := uuid.Parse(string(target))
		if err != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 || parsed.String() != string(target) {
			return Result{}, notices.ErrInvalid
		}
	case "lookup":
		parsed, err := uuid.Parse(string(target))
		if err != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 || parsed.String() != string(target) || A.Validate(A.Digest(delivery)) != nil {
			return Result{}, notices.ErrInvalid
		}
	default:
		return Result{}, notices.ErrInvalid
	}
	// Refuse unconfigured live controls before allocating transition IDs.
	m.gate.Lock()
	firstErr := m.precondition(ctx, expected, revision)
	if firstErr == nil && (action == "reload" || action == "retarget") && m.store.Snapshot() == nil {
		firstErr = ErrBusy
	}
	m.gate.Unlock()
	if firstErr != nil {
		return Result{}, firstErr
	}
	admission, err := nextGeneration()
	if err != nil {
		return Result{}, err
	}
	completion, err := nextGeneration()
	if err != nil {
		return Result{}, err
	}
	workerCompletion, err := nextGeneration()
	if err != nil {
		return Result{}, err
	}
	m.gate.Lock()
	if err := m.precondition(ctx, expected, revision); err != nil {
		m.gate.Unlock()
		return Result{}, err
	}
	if m.fault != nil || m.reason == "foreign" {
		m.gate.Unlock()
		return Result{}, notices.ErrForeign
	}
	state := m.store.Snapshot()
	if (action == "reload" || action == "retarget") && state == nil {
		m.gate.Unlock()
		return Result{}, ErrBusy
	}
	if action == "start" && (m.run != nil || state == nil || state.Dirty || m.store.Uncertain()) {
		m.gate.Unlock()
		return Result{}, notices.ErrGuarded
	}
	if action == "recover" && m.run != nil {
		m.gate.Unlock()
		return Result{}, ErrBusy
	}
	if action == "retarget" && target != state.TargetID && state.ConfigurationRevision == notices.MaxConfigurationRevision {
		m.gate.Unlock()
		return Result{}, notices.ErrConfigurationExhausted
	}
	// These no-ops preserve both versions. Same-target Retarget still validates
	// fresh confirmed bytes under the reservation, outside the memory gate.
	noop := action == "configure" && state != nil || action == "stop" && m.run == nil ||
		action == "reload" && m.run == nil || action == "retarget" && target == state.TargetID ||
		action == "lookup" && (m.run != nil || state == nil || state.Pending == nil || state.Pending.TargetID != target || state.Pending.DeliveryID != delivery)
	op := &operation{action: action, done: make(chan struct{}), completion: completion, workerCompletion: workerCompletion, noop: noop}
	m.reserved, m.operation = true, op
	if !noop {
		m.generation = admission
	}
	predecessor := m.run
	if (action == "stop" || action == "reload" || action == "retarget" && !noop) && predecessor != nil {
		m.beginDrain(predecessor, m.generation)
	}
	// Operation ownership survives a canceled caller and the response deadline.
	m.calls++
	deadline := time.Now().Add(2 * time.Second)
	m.gate.Unlock()
	go m.perform(op, predecessor, revision, target, delivery, auth)
	return m.respond(op, deadline, auth)
}

type Result struct {
	Status
	Lookup *notices.Pair
}

type operation struct {
	action                       string
	done                         chan struct{}
	completion, workerCompletion A.UUID
	noop                         bool
	result                       Result
}

// respond gives a fresh response authority read its own bounded opportunity.
// The goroutine owns a borrow even if its native read ignores cancellation.
// It never writes a response; only this coordinator can disclose a result.
func (m *Monitor) respond(op *operation, deadline time.Time, auth Authority) (Result, error) {
	wait := time.NewTimer(min(time.Second, time.Until(deadline)))
	select {
	case <-op.done:
	case <-wait.C:
	}
	wait.Stop()
	responseCtx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	authorized := make(chan error, 1)
	m.gate.Lock()
	m.calls++
	m.gate.Unlock()
	go func() {
		defer m.release()
		authorized <- m.authorize(responseCtx, auth)
	}()
	select {
	case err := <-authorized:
		if err != nil || !time.Now().Before(deadline) {
			return Result{}, ErrOutcomeUnknown
		}
	case <-responseCtx.Done():
		return Result{}, ErrOutcomeUnknown
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	select {
	case <-op.done:
		return op.result, nil
	default:
		// Snapshot the admitted operation while it still owns the reservation.
		// No native read is permitted after final response authorization.
		if m.operation == op {
			m.phase = "partial-drain"
		}
		result := Result{Status: m.snapshotLocked()}
		result.Phase, result.Operation = "partial-drain", op.action
		return result, nil
	}
}

func (m *Monitor) perform(op *operation, predecessor *run, revision A.Number, target A.UUID, delivery string, auth Authority) {
	defer m.release()
	ctx := context.Background()
	var err error
	var pair *notices.Pair
	switch op.action {
	case "configure":
		if !op.noop {
			var baseline noticeinterval.Cut
			baseline, err = m.source.Observe(ctx, auth)
			if err == nil {
				err = m.authorize(ctx, auth)
			}
			if err == nil {
				_, err = m.store.Configure(baseline)
			}
		}
	case "start":
		err = m.startWorker(op, auth)
	case "stop", "reload", "retarget":
		if predecessor != nil && !op.noop {
			<-predecessor.done
		}
		if op.action == "retarget" {
			if m.store.Uncertain() {
				err = notices.ErrStorage
			} else {
				state := m.store.Snapshot()
				bound := state.Checkpoint
				if state.Pending != nil {
					bound = pendingBound(state.Pending)
				}
				_, err = m.recoveryCut(ctx, auth, bound)
				if err == nil {
					err = m.authorize(ctx, auth)
				}
				if err == nil {
					err = m.store.Retarget(target, revision)
				}
			}
		}
		if err == nil && predecessor != nil && !op.noop && op.action != "stop" {
			state := m.store.Snapshot()
			if !m.store.Uncertain() && state != nil && !state.Dirty && state.Pending == nil && state.Observation == nil {
				err = m.startWorker(op, auth)
			}
		}
	case "recover":
		err = m.recover(ctx, auth)
	case "lookup":
		state := m.store.Snapshot()
		reconcile := predecessor == nil && state != nil && state.Pending != nil && state.Pending.TargetID == target && state.Pending.DeliveryID == delivery
		if reconcile {
			_, err = m.recoveryCut(ctx, auth, pendingBound(state.Pending))
		}
		if err == nil {
			err = m.authorize(ctx, auth)
		}
		if err == nil {
			pair, _, err = m.store.Lookup(target, delivery, reconcile)
		}
		if errors.Is(err, notices.ErrUnknown) {
			err = nil
		}
	}
	m.gate.Lock()
	m.reserved, m.operation = false, nil
	if !op.noop {
		m.generation = op.completion
	}
	if m.run == nil {
		// A quiescent no-op preserves an existing guarded/failed posture.
		if !op.noop {
			m.phase, m.reason = m.durableStatus()
		}
		if predecessor != nil && predecessor.terminal.Reason != "" && op.action != "lookup" && op.action != "recover" {
			m.phase, m.reason = predecessor.terminal.Phase, predecessor.terminal.Reason
		}
	} else if m.run.stopAt.IsZero() {
		m.phase, m.reason = "running", ""
	}
	if err != nil {
		m.phase, m.reason = faultStatus(err)
	}
	op.result = Result{Status: m.snapshotLocked(), Lookup: pair}
	close(op.done)
	m.closeIfQuiescent()
	m.gate.Unlock()
}

// startWorker is shared by Start and clean replacement. It cannot lend this
// caller's authority to the predecessor and never runs a recovery action.
func (m *Monitor) startWorker(op *operation, auth Authority) error {
	ctx := context.Background()
	state := m.store.Snapshot()
	if state == nil || state.Dirty || m.store.Uncertain() {
		return notices.ErrGuarded
	}
	cut, err := m.recoveryCut(ctx, auth, state.Checkpoint)
	_ = cut
	if err != nil {
		return err
	}
	initial := observation{cut: state.Checkpoint}
	signature, signalErr := m.source.Signal()
	if errors.Is(signalErr, notices.ErrForeign) {
		return signalErr
	}
	if signalErr == nil {
		initial.signature, initial.sampled = signature, true
	}
	if err = m.authorize(ctx, auth); err != nil {
		return err
	}
	m.gate.Lock()
	closed := m.closed
	m.gate.Unlock()
	if closed {
		return ErrBusy
	}
	if err = m.store.BeginRun(); err != nil {
		return err
	}
	// A dirty publication and worker admission are separate phase admissions.
	if err = m.authorize(ctx, auth); err != nil {
		m.store.AbandonRun()
		return err
	}
	r := &run{stop: make(chan struct{}), done: make(chan struct{}), completion: op.workerCompletion}
	m.gate.Lock()
	if m.closed {
		m.gate.Unlock()
		m.store.AbandonRun()
		return ErrBusy
	}
	m.run, m.phase, m.reason = r, "running", ""
	m.gate.Unlock()
	go m.work(r, auth, initial)
	return nil
}

// beginDrain is the short-gate observation admission fence. It never waits.
func (m *Monitor) beginDrain(r *run, generation A.UUID) {
	if !r.stopAt.IsZero() {
		return
	}
	r.stopAt = time.Now()
	m.generation, m.phase, m.reason = generation, "draining", ""
	close(r.stop)
}

func (m *Monitor) recordPartialDrain(r *run) {
	m.phase = "partial-drain"
	r.partial = m.snapshotLocked()
}

// Close is terminal. A timeout retains the native owner until every operation,
// borrowed authority read and worker has joined.
func (m *Monitor) Close(ctx context.Context) error {
	generation, err := nextGeneration()
	if err != nil {
		return err
	}
	m.gate.Lock()
	if !m.closed {
		m.closed, m.closeGeneration = true, generation
	}
	if r := m.run; r != nil {
		m.beginDrain(r, m.closeGeneration)
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

// These package wrappers keep existing in-process callers on the same control
// implementation. Network callers must supply both exact versions to Control.
func (m *Monitor) revision() A.Number {
	if s := m.store.Snapshot(); s != nil {
		return s.ConfigurationRevision
	}
	return 0
}
func (m *Monitor) Configure(ctx context.Context, expected A.UUID, auth Authority) error {
	_, err := m.Control(ctx, "configure", expected, m.revision(), "", "", auth)
	return err
}
func (m *Monitor) Start(ctx context.Context, expected A.UUID, auth Authority) error {
	_, err := m.Control(ctx, "start", expected, m.revision(), "", "", auth)
	return err
}
func (m *Monitor) Stop(ctx context.Context, expected A.UUID, auth Authority) error {
	_, err := m.StopView(ctx, expected, auth)
	return err
}
func (m *Monitor) StopView(ctx context.Context, expected A.UUID, auth Authority) (Status, error) {
	r, err := m.Control(ctx, "stop", expected, m.revision(), "", "", auth)
	return r.Status, err
}
func (m *Monitor) RecoverView(ctx context.Context, expected A.UUID, auth Authority) (Status, error) {
	r, err := m.Control(ctx, "recover", expected, m.revision(), "", "", auth)
	return r.Status, err
}
func (m *Monitor) Lookup(ctx context.Context, target A.UUID, id string, auth Authority) (*notices.Pair, error) {
	r, err := m.Control(ctx, "lookup", m.Snapshot().Generation, m.revision(), target, id, auth)
	if err == nil && r.Lookup == nil {
		err = notices.ErrUnknown
	}
	return r.Lookup, err
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
