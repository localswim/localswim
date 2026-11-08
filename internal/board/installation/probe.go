package installation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrIdentityChanged = errors.New("installation identity changed")
	ErrUnavailable     = errors.New("installation control unavailable")
)

type ControlView struct {
	ProbeGeneration, Phase string
	Joined                 bool
}
type ProbeObservation struct {
	State, Code string
	ToolVersion *string
	Joined      bool
}
type StopObservation struct {
	ProbeGeneration, Phase string
	Joined                 bool
}

type probeBackend func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error)
type candidateBinding struct {
	key            string
	invalid        bool
	runtime, probe string
}

// choose is called only by the exclusive actual backend. Inspection never calls
// it, and the invalid flag is changed separately under the Inspector state gate.
func (b *candidateBinding) choose(key string) error {
	if key == "" {
		return ErrUnavailable
	}
	if b.key != "" && b.key != key {
		return ErrIdentityChanged
	}
	if b.key == "" {
		b.key = key
	}
	return nil
}

type probeTuple struct{ configuration, generation string }
type probeAnswer struct {
	observation ProbeObservation
	err         error
}
type controlAdmission struct{ cancel context.CancelFunc }

func freshGeneration() (string, error) {
	v, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return v.String(), nil
}
func isGeneration(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.Version() == 4 && id.Variant() == uuid.RFC4122 && id.String() == value
}

// checked serializes only authority callbacks. Neither the state mutex nor any
// file-handle cursor is held while a potentially slow SQL/native check executes.
func (s *Inspector) checked(ctx context.Context, check Check) error {
	work, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.shutdown, cancel)
	defer func() { stop(); cancel() }()
	ctx = work
	if check == nil {
		return ErrUnavailable
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.checkGate:
	}
	defer func() { s.checkGate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := check(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

// beginControl retains each started admission independently of its HTTP caller.
// A blocked check therefore prevents Runtime.Close from releasing physical custody.
func (s *Inspector) beginControl(ctx context.Context, generation string) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, nil, ErrClosed
	}
	if generation != s.generation {
		return nil, nil, ErrStale
	}
	work, cancel := context.WithCancel(ctx)
	admission := &controlAdmission{cancel: cancel}
	s.admissions[admission] = struct{}{}
	var once sync.Once
	return work, func() {
		once.Do(func() { cancel(); s.mu.Lock(); delete(s.admissions, admission); s.notifyLocked(); s.mu.Unlock() })
	}, nil
}
func (s *Inspector) notifyLocked() { close(s.changed); s.changed = make(chan struct{}) }

func (s *Inspector) Control(ctx context.Context, runtimeGeneration, configurationGeneration string, check Check) (ControlView, error) {
	work, end, err := s.beginControl(ctx, runtimeGeneration)
	if err != nil {
		return ControlView{}, err
	}
	defer end()
	if err = s.checked(work, check); err != nil {
		return ControlView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return ControlView{}, ErrClosed
	}
	if err = work.Err(); err != nil {
		return ControlView{}, err
	}
	if configurationGeneration == "" {
		return ControlView{}, ErrUnavailable
	}
	if a := s.current; a != nil {
		if a.probe == nil {
			return ControlView{}, ErrBusy
		}
		if a.probe.configuration != configurationGeneration {
			return ControlView{}, ErrStale
		}
		return ControlView{a.probe.generation, a.phase, false}, nil
	}
	if s.binding.invalid {
		return ControlView{}, ErrIdentityChanged
	}
	if s.available == "" {
		return ControlView{}, ErrUnavailable
	}
	return ControlView{s.available, "ready", true}, nil
}

func (s *Inspector) Probe(ctx context.Context, runtimeGeneration, configurationGeneration, probeGeneration string, check Check) (ProbeObservation, error) {
	admission, end, err := s.beginControl(ctx, runtimeGeneration)
	if err != nil {
		return ProbeObservation{}, err
	}
	defer end()
	if err = s.checked(admission, check); err != nil {
		return ProbeObservation{}, err
	}
	s.mu.Lock()
	if err = admission.Err(); err != nil {
		s.mu.Unlock()
		return ProbeObservation{}, err
	}
	if s.closing {
		s.mu.Unlock()
		return ProbeObservation{}, ErrClosed
	}
	if runtimeGeneration != s.generation {
		s.mu.Unlock()
		return ProbeObservation{}, ErrStale
	}
	if s.current != nil {
		s.mu.Unlock()
		return ProbeObservation{}, ErrBusy
	}
	if s.binding.invalid {
		s.mu.Unlock()
		return ProbeObservation{}, ErrIdentityChanged
	}
	if s.tombstone != nil && s.tombstone.generation == probeGeneration {
		s.mu.Unlock()
		return ProbeObservation{}, ErrStale
	}
	if configurationGeneration == "" || s.available == "" {
		s.mu.Unlock()
		return ProbeObservation{}, ErrUnavailable
	}
	if probeGeneration != s.available {
		s.mu.Unlock()
		return ProbeObservation{}, ErrStale
	}
	work, cancel := context.WithTimeout(ctx, s.deadline)
	a := &active{cancel: cancel, done: make(chan struct{}), probe: &probeTuple{configurationGeneration, probeGeneration}, phase: "running"}
	s.current, s.available, s.tombstone = a, "", nil
	s.binding.runtime = runtimeGeneration
	s.binding.probe = probeGeneration
	s.mu.Unlock()
	result := make(chan probeAnswer, 1)
	go s.runProbe(ctx, work, a, check, result)
	select {
	case out := <-result:
		return out.observation, out.err
	case <-work.Done():
	}
	s.mu.Lock()
	if s.current == a {
		a.phase = "draining"
	}
	s.mu.Unlock()
	// A foreground incomplete view needs its own fresh current authority check.
	// The reference keeps the native slot until this check actually returns.
	disclosure := make(chan error, 1)
	s.mu.Lock()
	if s.current != a {
		s.mu.Unlock()
		out := <-result
		return out.observation, out.err
	}
	a.observers++
	s.mu.Unlock()
	go func() {
		err := s.checked(ctx, check)
		s.mu.Lock()
		if err != nil && a.refusal == nil {
			a.refusal = err
		}
		if errors.Is(err, ErrIdentityChanged) {
			s.binding.invalid = true
		}
		a.observers--
		s.finishProbeLocked(a)
		s.mu.Unlock()
		disclosure <- err
	}()
	timer := time.NewTimer(s.drain)
	defer timer.Stop()
	for {
		select {
		case out := <-result:
			return out.observation, out.err
		case err := <-disclosure:
			if err != nil {
				return ProbeObservation{}, err
			}
			disclosure = nil
			s.mu.Lock()
			refusal, closing := a.refusal, s.closing
			s.mu.Unlock()
			if closing {
				return ProbeObservation{}, ErrClosed
			}
			if refusal != nil {
				return ProbeObservation{}, refusal
			}
			// Wait out the cleanup allowance; a completed worker takes precedence.
		case <-timer.C:
			if disclosure != nil {
				return ProbeObservation{}, ErrUnavailable
			}
			s.mu.Lock()
			refusal, closing := a.refusal, s.closing
			s.mu.Unlock()
			if closing {
				return ProbeObservation{}, ErrClosed
			}
			if refusal != nil {
				return ProbeObservation{}, refusal
			}
			return ProbeObservation{"incomplete", "DRAIN_INCOMPLETE", nil, false}, nil
		}
	}
}

func (s *Inspector) runProbe(caller, work context.Context, a *active, check Check, result chan<- probeAnswer) {
	out := probeAnswer{}
	nextPrepared := false
	if out.err = s.checked(work, check); out.err == nil {
		watchDone := make(chan struct{})
		watchCtx, stopWatch := context.WithCancel(work)
		go func() {
			defer close(watchDone)
			ticker := time.NewTicker(s.checkInterval)
			defer ticker.Stop()
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-ticker.C:
					if err := s.checked(watchCtx, check); err != nil {
						if watchCtx.Err() == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
							s.mu.Lock()
							if a.refusal == nil {
								a.refusal = err
							}
							s.mu.Unlock()
							a.cancel()
						}
						return
					}
				}
			}
		}()
		var cleanup func() error
		out.observation, cleanup, out.err = s.probeBackend(work, func(ctx context.Context) error { return s.checked(ctx, check) }, &s.binding)
		if out.err == nil && (!out.observation.Joined || !out.observation.Valid()) {
			if out.observation.Joined {
				out.observation = nativeFailure("EXIT_FAILED", true)
			} else {
				out.observation = nativeFailure("DRAIN_INCOMPLETE", false)
				cleanup = closeOnce(cleanup, func() error { return ErrIncomplete })
			}
		}
		var cleanupErr error
		if cleanup != nil {
			cleanupErr = cleanup()
		}
		if cleanupErr == nil {
			s.prepareNext(a)
			nextPrepared = true
		}
		stopWatch()
		<-watchDone
		if cleanupErr != nil {
			s.mu.Lock()
			a.quarantined = cleanup
			a.phase = "draining"
			s.mu.Unlock()
			a.cancel()
			finalErr := s.checked(caller, check)
			s.mu.Lock()
			if finalErr != nil {
				out.err = finalErr
			}
			if a.refusal != nil {
				out.err = a.refusal
			}
			if errors.Is(out.err, ErrIdentityChanged) {
				s.binding.invalid = true
			}
			if s.closing {
				out.err = ErrClosed
			}
			s.mu.Unlock()
			out.observation = nativeFailure("DRAIN_INCOMPLETE", false)
			if out.err != nil {
				out.observation = ProbeObservation{}
			}
			result <- out
			return // Quarantined custody has no done event and is never replayed.
		}
		if errors.Is(work.Err(), context.DeadlineExceeded) && out.observation.Code == "OK" {
			out.observation = ProbeObservation{"unknown", "TIMED_OUT", nil, true}
		}
		if work.Err() != nil && out.observation.Code == "OK" {
			out.observation = ProbeObservation{"unknown", "CANCELLED", nil, true}
		}
	}
	// Entropy and watcher join precede the last authority check. Either can block;
	// a prior read must never authorize disclosure after that blocking work.
	if !nextPrepared {
		s.prepareNext(a)
	}
	if out.observation.Code == "OK" && work.Err() != nil {
		out.observation = cancellationObservation(work)
	}
	if out.err == nil {
		out.err = s.checked(caller, check)
	}
	if out.observation.Code == "OK" && work.Err() != nil {
		out.observation = cancellationObservation(work)
	}
	s.mu.Lock()
	if a.refusal != nil {
		out.err = a.refusal
	}
	if errors.Is(out.err, ErrIdentityChanged) {
		s.binding.invalid = true
	}
	if s.closing {
		out.err = ErrClosed
	}
	if caller.Err() != nil {
		out.err = caller.Err()
	}
	if out.err != nil {
		out.observation = ProbeObservation{}
	}
	a.workerDone = true
	s.finishProbeLocked(a)
	s.mu.Unlock()
	// Observers are already serialized through the final check, but can still
	// be returning. Do not publish an actual-join result until their release.
	<-a.done
	a.cancel()
	s.mu.Lock()
	if a.refusal != nil {
		out.err = a.refusal
	}
	if s.closing {
		out.err = ErrClosed
	}
	if caller.Err() != nil {
		out.err = caller.Err()
	}
	if out.err != nil {
		out.observation = ProbeObservation{}
	}
	s.mu.Unlock()
	result <- out
}

// prepareNext performs cryptographic I/O outside the state gate and before
// final disclosure. The current reservation owns its lifetime until it returns.
func (s *Inspector) prepareNext(a *active) {
	if next, err := s.fresh(); err == nil && isGeneration(next) && next != a.probe.generation {
		a.nextGeneration = next
	}
}

func (s *Inspector) finishProbeLocked(a *active) {
	if !a.workerDone || a.observers != 0 || a.quarantined != nil || s.current != a {
		return
	}
	s.current = nil
	copyTuple := *a.probe
	s.tombstone = &copyTuple
	if !s.closing && !s.binding.invalid {
		s.available = a.nextGeneration
	}
	close(a.done)
	s.notifyLocked()
}

func (s *Inspector) Stop(ctx context.Context, runtimeGeneration, configurationGeneration, probeGeneration string, check Check) (StopObservation, error) {
	work, end, err := s.beginControl(ctx, runtimeGeneration)
	if err != nil {
		return StopObservation{}, err
	}
	defer end()
	if err = s.checked(work, check); err != nil {
		return StopObservation{}, err
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return StopObservation{}, ErrClosed
	}
	if err = work.Err(); err != nil {
		s.mu.Unlock()
		return StopObservation{}, err
	}
	tuple := probeTuple{configurationGeneration, probeGeneration}
	a := s.current
	if a == nil {
		if s.tombstone != nil && *s.tombstone == tuple {
			s.mu.Unlock()
			return StopObservation{probeGeneration, "joined", true}, nil
		}
		s.mu.Unlock()
		return StopObservation{}, ErrStale
	}
	if a.probe == nil || *a.probe != tuple {
		s.mu.Unlock()
		return StopObservation{}, ErrStale
	}
	a.phase = "draining"
	a.cancel()
	s.mu.Unlock()
	// Reserve a small part of the same foreground allowance for fresh disclosure
	// authority. A blocked callback is independently retained after this returns.
	allowance := time.NewTimer(s.drain)
	defer allowance.Stop()
	observation := time.NewTimer(s.drain - min(s.checkInterval, s.drain/2))
	defer observation.Stop()
	select {
	case <-a.done:
	case <-observation.C:
	case <-work.Done():
		return StopObservation{}, work.Err()
	}
	finalCtx, finalEnd, err := s.beginControl(work, runtimeGeneration)
	if err != nil {
		return StopObservation{}, err
	}
	finalResult := make(chan error, 1)
	go func() { defer finalEnd(); finalResult <- s.checked(finalCtx, check) }()
	select {
	case err = <-finalResult:
		if err != nil {
			return StopObservation{}, err
		}
		s.mu.Lock()
		closing, refusal := s.closing, a.refusal
		s.mu.Unlock()
		if closing {
			return StopObservation{}, ErrClosed
		}
		if refusal != nil {
			return StopObservation{}, refusal
		}
		select {
		case <-a.done:
			return StopObservation{probeGeneration, "joined", true}, nil
		default:
			return StopObservation{probeGeneration, "incomplete", false}, nil
		}
	case <-work.Done():
		return StopObservation{}, work.Err()
	case <-allowance.C:
		return StopObservation{}, ErrUnavailable
	}
}
