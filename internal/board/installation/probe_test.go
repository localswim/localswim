package installation

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func controlledInspector(backend probeBackend) *Inspector {
	s := newInspector("runtime", func(context.Context) (Observation, func() error) {
		return Observation{"candidate", "CANDIDATE_PRESENT"}, nil
	})
	s.probeBackend = backend
	s.deadline = time.Second
	s.drain = 40 * time.Millisecond
	s.checkInterval = 5 * time.Millisecond
	return s
}
func joinedBackend(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
	v := "1.2.3"
	return ProbeObservation{"supported", "OK", &v, true}, nil, nil
}
func controlGeneration(t *testing.T, s *Inspector) string {
	t.Helper()
	v, e := s.Control(context.Background(), "runtime", "configuration", allow)
	if e != nil || v.Phase != "ready" || !v.Joined {
		t.Fatal(v, e)
	}
	return v.ProbeGeneration
}

func TestProbeNonceOneUseAndOnlyCurrentTombstone(t *testing.T) {
	var launches atomic.Int32
	s := controlledInspector(func(ctx context.Context, c Check, b *candidateBinding) (ProbeObservation, func() error, error) {
		launches.Add(1)
		return joinedBackend(ctx, c, b)
	})
	first := controlGeneration(t, s)
	if launches.Load() != 0 {
		t.Fatal("control launched")
	}
	out, e := s.Probe(context.Background(), "runtime", "configuration", first, allow)
	if e != nil || out.State != "supported" || !out.Joined {
		t.Fatal(out, e)
	}
	if _, e = s.Probe(context.Background(), "runtime", "configuration", first, allow); e != ErrStale {
		t.Fatal(e)
	}
	for range 2 {
		v, e := s.Stop(context.Background(), "runtime", "configuration", first, allow)
		if e != nil || !v.Joined || v.Phase != "joined" {
			t.Fatal(v, e)
		}
	}
	second := controlGeneration(t, s)
	if first == second {
		t.Fatal("generation reused")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.probeBackend = func(ctx context.Context, _ Check, _ *candidateBinding) (ProbeObservation, func() error, error) {
		close(entered)
		<-release
		return nativeFailure("CANCELLED", true), nil, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Probe(context.Background(), "runtime", "configuration", second, allow)
		done <- err
	}()
	<-entered
	if _, e = s.Probe(context.Background(), "runtime", "configuration", second, allow); e != ErrBusy {
		t.Fatal(e)
	}
	if _, e = s.Stop(context.Background(), "runtime", "configuration", first, allow); e != ErrStale {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if launches.Load() != 1 {
		t.Fatal("unexpected launches")
	}
}
func TestMetadataAndActualShareOneSlot(t *testing.T) {
	s := controlledInspector(joinedBackend)
	nonce := controlGeneration(t, s)
	entered, release := make(chan struct{}), make(chan struct{})
	s.backend = func(ctx context.Context) (Observation, func() error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			t.Error("metadata cancelled by foreign Stop")
		}
		return Observation{"candidate", "CANDIDATE_PRESENT"}, nil
	}
	done := make(chan error, 1)
	go func() { _, e := s.Inspect(context.Background(), "runtime", allow); done <- e }()
	<-entered
	if _, e := s.Control(context.Background(), "runtime", "configuration", allow); e != ErrBusy {
		t.Fatal(e)
	}
	if _, e := s.Probe(context.Background(), "runtime", "configuration", nonce, allow); e != ErrBusy {
		t.Fatal(e)
	}
	if _, e := s.Stop(context.Background(), "runtime", "configuration", nonce, allow); e != ErrStale {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if got := controlGeneration(t, s); got != nonce {
		t.Fatal("metadata consumed actual generation")
	}
}
func TestUnboundAndDeniedProbeDoNotConsumeGeneration(t *testing.T) {
	s := controlledInspector(joinedBackend)
	nonce := controlGeneration(t, s)
	denied := errors.New("denied")
	if _, e := s.Probe(context.Background(), "runtime", "configuration", nonce, func(context.Context) error { return denied }); e != denied {
		t.Fatal(e)
	}
	if got := controlGeneration(t, s); got != nonce {
		t.Fatal("denied check consumed generation")
	}
	if _, e := s.Probe(context.Background(), "runtime", "", nonce, allow); e != ErrUnavailable {
		t.Fatal(e)
	}
	if got := controlGeneration(t, s); got != nonce {
		t.Fatal("unbound config consumed generation")
	}
}
func TestEntropyFailureMakesFurtherAdmissionUnavailable(t *testing.T) {
	s := controlledInspector(joinedBackend)
	nonce := controlGeneration(t, s)
	s.fresh = func() (string, error) { return "", errors.New("no entropy") }
	if _, e := s.Probe(context.Background(), "runtime", "configuration", nonce, allow); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Control(context.Background(), "runtime", "configuration", allow); e != ErrUnavailable {
		t.Fatal(e)
	}
	if _, e := s.Probe(context.Background(), "runtime", "configuration", nonce, allow); e != ErrStale {
		t.Fatal(e)
	}
}
func TestActiveRevocationAndSerializedChecksWithoutIdleRenewal(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	var revoked atomic.Bool
	var checks, concurrent, maxConcurrent atomic.Int32
	denied := errors.New("revoked")
	check := func(ctx context.Context) error {
		n := concurrent.Add(1)
		defer concurrent.Add(-1)
		for old := maxConcurrent.Load(); n > old && !maxConcurrent.CompareAndSwap(old, n); old = maxConcurrent.Load() {
		}
		checks.Add(1)
		time.Sleep(time.Millisecond)
		if revoked.Load() {
			return denied
		}
		return ctx.Err()
	}
	s := controlledInspector(func(ctx context.Context, _ Check, _ *candidateBinding) (ProbeObservation, func() error, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return nativeFailure("CANCELLED", true), nil, nil
	})
	nonce := controlGeneration(t, s)
	done := make(chan error, 1)
	go func() { _, e := s.Probe(context.Background(), "runtime", "configuration", nonce, check); done <- e }()
	<-entered
	revoked.Store(true)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel")
	}
	if e := <-done; e != denied {
		t.Fatal(e)
	}
	if maxConcurrent.Load() != 1 {
		t.Fatal("overlapping authority checks")
	}
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		active := s.current != nil
		s.mu.Unlock()
		if !active {
			break
		}
		select {
		case <-deadline:
			t.Fatal("operation did not join")
		case <-time.After(time.Millisecond):
		}
	}
	count := checks.Load()
	time.Sleep(20 * time.Millisecond)
	if checks.Load() != count {
		t.Fatal("idle renewal")
	}
}
func TestBlockedControlAdmissionRetainsCloseAndIsRemoved(t *testing.T) {
	s := controlledInspector(joinedBackend)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := s.Control(context.Background(), "runtime", "configuration", func(context.Context) error { close(entered); <-release; return nil })
		done <- e
	}()
	<-entered
	if e := s.Close(); e != ErrIncomplete {
		t.Fatal("blocked check released runtime", e)
	}
	close(release)
	if e := <-done; e != ErrClosed && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.admissions) != 0 {
		t.Fatal("completed check retained")
	}
}
func TestExpiredStopCannotCancelAfterHeldAdmissionReturns(t *testing.T) {
	entered, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s := controlledInspector(func(ctx context.Context, _ Check, _ *candidateBinding) (ProbeObservation, func() error, error) {
		close(entered)
		select {
		case <-ctx.Done():
			close(stopped)
		case <-release:
		}
		return nativeFailure("CANCELLED", true), nil, nil
	})
	nonce := controlGeneration(t, s)
	probeDone := make(chan error, 1)
	go func() {
		_, e := s.Probe(context.Background(), "runtime", "configuration", nonce, allow)
		probeDone <- e
	}()
	<-entered
	checkEntered, checkRelease := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	stopDone := make(chan error, 1)
	go func() {
		_, e := s.Stop(ctx, "runtime", "configuration", nonce, func(context.Context) error { close(checkEntered); <-checkRelease; return nil })
		stopDone <- e
	}()
	<-checkEntered
	cancel()
	close(checkRelease)
	if e := <-stopDone; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	select {
	case <-stopped:
		t.Fatal("expired Stop cancelled active operation")
	default:
	}
	close(release)
	if e := <-probeDone; e != nil {
		t.Fatal(e)
	}
}
func TestProbeCleanupQuarantineRetainsSlotWithoutRetry(t *testing.T) {
	var closes atomic.Int32
	s := controlledInspector(func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
		return nativeFailure("EXIT_FAILED", true), func() error { closes.Add(1); return errors.New("uncertain close") }, nil
	})
	nonce := controlGeneration(t, s)
	out, e := s.Probe(context.Background(), "runtime", "configuration", nonce, allow)
	if e != nil || out.Code != "DRAIN_INCOMPLETE" || out.Joined {
		t.Fatal(out, e)
	}
	for range 2 {
		v, e := s.Stop(context.Background(), "runtime", "configuration", nonce, allow)
		if e != nil || v.Joined {
			t.Fatal(v, e)
		}

	}
	for range 2 {
		if e := s.Close(); e != ErrIncomplete {
			t.Fatal(e)
		}
	}
	if closes.Load() != 1 {
		t.Fatal("numeric close replayed")
	}
}
func TestIdentityChangePermanentlyInvalidatesBinding(t *testing.T) {
	s := controlledInspector(func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
		return ProbeObservation{}, nil, ErrIdentityChanged
	})
	nonce := controlGeneration(t, s)
	if _, e := s.Probe(context.Background(), "runtime", "configuration", nonce, allow); e != ErrIdentityChanged {
		t.Fatal(e)
	}
	if _, e := s.Control(context.Background(), "runtime", "configuration", allow); e != ErrIdentityChanged {
		t.Fatal(e)
	}
	if _, e := s.Probe(context.Background(), "runtime", "configuration", nonce, allow); e != ErrIdentityChanged {
		t.Fatal(e)
	}
}
func TestHeldWatcherPreventsJoinAndCloseUntilCheckReturns(t *testing.T) {
	entered, releaseNative, enteredCheck, releaseCheck := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	check := func(context.Context) error {
		if count.Add(1) == 3 {
			close(enteredCheck)
			<-releaseCheck
		}
		return nil
	}
	s := controlledInspector(func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
		close(entered)
		<-releaseNative
		return nativeFailure("CANCELLED", true), nil, nil
	})
	s.deadline = 30 * time.Millisecond
	nonce := controlGeneration(t, s)
	done := make(chan error, 1)
	go func() { _, e := s.Probe(context.Background(), "runtime", "configuration", nonce, check); done <- e }()
	<-entered
	<-enteredCheck
	close(releaseNative)
	if e := <-done; e != ErrUnavailable {
		t.Fatal("unjoined check exposed facts", e)
	}
	if e := s.Close(); e != ErrIncomplete {
		t.Fatal(e)
	}
	close(releaseCheck)
	deadline := time.After(time.Second)
	for s.Close() != nil {
		select {
		case <-deadline:
			t.Fatal("check did not eventually join")
		default:
		}
	}
}

func TestInspectionCannotSelectActualCandidateAndChangedPinCannotRebind(t *testing.T) {
	s := controlledInspector(func(_ context.Context, _ Check, b *candidateBinding) (ProbeObservation, func() error, error) {
		if b.key != "" {
			t.Error("metadata inspection populated candidate pin")
		}
		if err := b.choose("candidate-A"); err != nil {
			t.Error(err)
		}
		return nativeFailure("EXIT_FAILED", true), nil, nil
	})
	if _, err := s.Inspect(context.Background(), "runtime", allow); err != nil {
		t.Fatal(err)
	}
	nonce := controlGeneration(t, s)
	if _, err := s.Probe(context.Background(), "runtime", "configuration", nonce, allow); err != nil {
		t.Fatal(err)
	}
	s.probeBackend = func(_ context.Context, _ Check, b *candidateBinding) (ProbeObservation, func() error, error) {
		return ProbeObservation{}, nil, b.choose("candidate-B")
	}
	nonce = controlGeneration(t, s)
	if _, err := s.Probe(context.Background(), "runtime", "configuration", nonce, allow); err != ErrIdentityChanged {
		t.Fatal(err)
	}
	if s.binding.key != "candidate-A" {
		t.Fatal("changed candidate rebound")
	}
}
func TestBoundedControlRecordsAndContextChecks(t *testing.T) {
	s := controlledInspector(joinedBackend)
	for range 100 {
		controlGeneration(t, s)
	}
	s.mu.Lock()
	count := len(s.admissions)
	s.mu.Unlock()
	if count != 0 {
		t.Fatal("unbounded ended admission records", count)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Control(ctx, "runtime", "configuration", allow); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Control(context.Background(), "old", "configuration", allow); err != ErrStale {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestStopFinalAuthorityLossRedactsAndRetainsBlockedCheck(t *testing.T) {
	nativeEntered, nativeRelease := make(chan struct{}), make(chan struct{})
	s := controlledInspector(func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
		close(nativeEntered)
		<-nativeRelease
		return nativeFailure("CANCELLED", true), nil, nil
	})
	nonce := controlGeneration(t, s)
	probeDone := make(chan error, 1)
	go func() {
		_, err := s.Probe(context.Background(), "runtime", "configuration", nonce, allow)
		probeDone <- err
	}()
	<-nativeEntered
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	check := func(context.Context) error {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
		return nil
	}
	stopDone := make(chan error, 1)
	go func() {
		_, err := s.Stop(context.Background(), "runtime", "configuration", nonce, check)
		stopDone <- err
	}()
	<-entered
	if err := <-stopDone; err != ErrUnavailable {
		t.Fatal("blocked disclosure invented join", err)
	}
	close(nativeRelease)
	if err := s.Close(); err != ErrIncomplete {
		t.Fatal("blocked disclosure lost custody", err)
	}
	close(release)
	<-probeDone
	deadline := time.After(time.Second)
	for s.Close() != nil {
		select {
		case <-deadline:
			t.Fatal("late Stop check not joined")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestProbeFinalDeniedAndMalformedBackendCannotDisclosePositive(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var revoked atomic.Bool
	denied := errors.New("late denial")
	s := controlledInspector(func(ctx context.Context, c Check, b *candidateBinding) (ProbeObservation, func() error, error) {
		out, _, err := joinedBackend(ctx, c, b)
		return out, func() error { close(entered); <-release; return nil }, err
	})
	check := func(context.Context) error {
		if revoked.Load() {
			return denied
		}
		return nil
	}
	nonce := controlGeneration(t, s)
	done := make(chan probeAnswer, 1)
	go func() {
		v, e := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
		done <- probeAnswer{v, e}
	}()
	<-entered
	revoked.Store(true)
	close(release)
	out := <-done
	if out.err != denied || out.observation.ToolVersion != nil || out.observation.State != "" {
		t.Fatal(out)
	}
	s = controlledInspector(func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
		v := "private-invalid"
		return ProbeObservation{"supported", "OK", &v, true}, nil, nil
	})
	nonce = controlGeneration(t, s)
	v, e := s.Probe(context.Background(), "runtime", "configuration", nonce, allow)
	if e != nil || v.Code != "EXIT_FAILED" || v.ToolVersion != nil {
		t.Fatal(v, e)
	}
}

func TestInitialEntropyFailureNeverExposesFallbackNonce(t *testing.T) {
	for _, value := range []string{"", "00000000-0000-0000-0000-000000000000", "not-a-nonce"} {
		s := newInspectorWithEntropy("runtime", func(context.Context) (Observation, func() error) {
			t.Fatal("metadata run from control")
			return Observation{}, nil
		}, func() (string, error) { return value, errors.New("entropy unavailable") })
		s.probeBackend = func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
			t.Fatal("native run after entropy failure")
			return ProbeObservation{}, nil, nil
		}
		if _, err := s.Control(context.Background(), "runtime", "configuration", allow); err != ErrUnavailable {
			t.Fatal(value, err)
		}
		if _, err := s.Probe(context.Background(), "runtime", "configuration", value, allow); err != ErrUnavailable {
			t.Fatal(value, err)
		}
	}
}
func TestBackendUnjoinedCannotReleaseSlot(t *testing.T) {
	s := controlledInspector(func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
		return nativeFailure("DRAIN_INCOMPLETE", false), nil, nil
	})
	nonce := controlGeneration(t, s)
	out, err := s.Probe(context.Background(), "runtime", "configuration", nonce, allow)
	if err != nil || out.Joined || out.Code != "DRAIN_INCOMPLETE" {
		t.Fatal(out, err)
	}
	if _, err = s.Probe(context.Background(), "runtime", "configuration", nonce, allow); err != ErrBusy {
		t.Fatal("unjoined backend released slot", err)
	}
	if err = s.Close(); err != ErrIncomplete {
		t.Fatal(err)
	}
}

func TestDeadlineAndObserverDenialAcrossFinalDisclosure(t *testing.T) {
	for _, denyObserver := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "observer-denied"}[denyObserver], func(t *testing.T) {
			s := controlledInspector(joinedBackend)
			s.deadline = 30 * time.Millisecond
			s.drain = 100 * time.Millisecond
			s.checkInterval = time.Second
			var calls atomic.Int32
			entered, release := make(chan struct{}), make(chan struct{})
			denied := errors.New("disclosure denied")
			check := func(context.Context) error {
				switch calls.Add(1) {
				case 3:
					close(entered)
					<-release
				case 4:
					if denyObserver {
						return denied
					}
				}
				return nil
			}
			nonce := controlGeneration(t, s)
			done := make(chan probeAnswer, 1)
			go func() {
				v, e := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
				done <- probeAnswer{v, e}
			}()
			<-entered
			deadline := time.After(time.Second)
			for {
				s.mu.Lock()
				observers := s.current.observers
				s.mu.Unlock()
				if observers > 0 {
					break
				}
				select {
				case <-deadline:
					t.Fatal("foreground observer missing")
				case <-time.After(time.Millisecond):
				}
			}
			close(release)
			out := <-done
			if denyObserver {
				if out.err != denied || out.observation.ToolVersion != nil {
					t.Fatal("observer denial lost", out)
				}
			} else {
				if out.err != nil || out.observation.Code != "TIMED_OUT" || out.observation.ToolVersion != nil {
					t.Fatal("late final check disclosed success", out)
				}
			}
		})
	}
}

func TestHeldEntropyCannotDiscloseAfterRevocationOrDeadline(t *testing.T) {
	for _, cause := range []string{"revoked", "deadline"} {
		t.Run(cause, func(t *testing.T) {
			s := controlledInspector(joinedBackend)
			s.drain = 100 * time.Millisecond
			var revoked atomic.Bool
			denied := errors.New("revoked during entropy")
			check := func(context.Context) error {
				if revoked.Load() {
					return denied
				}
				return nil
			}
			if cause == "deadline" {
				s.deadline = 50 * time.Millisecond
				s.checkInterval = time.Second
			}
			entered, release := make(chan struct{}), make(chan struct{})
			s.fresh = func() (string, error) { close(entered); <-release; return freshGeneration() }
			nonce := controlGeneration(t, s)
			done := make(chan probeAnswer, 1)
			go func() {
				v, e := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
				done <- probeAnswer{v, e}
			}()
			<-entered
			if cause == "revoked" {
				revoked.Store(true)
			} else {
				time.Sleep(60 * time.Millisecond)
			}
			s.mu.Lock()
			retained := s.current != nil
			s.mu.Unlock()
			if !retained {
				t.Fatal("entropy work lost reservation")
			}
			close(release)
			out := <-done
			if cause == "revoked" {
				if out.err != denied || out.observation.ToolVersion != nil || out.observation.State != "" {
					t.Fatal("entropy gap disclosed after denial", out)
				}
			} else {
				if out.err != nil || out.observation.Code != "TIMED_OUT" || out.observation.ToolVersion != nil {
					t.Fatal("entropy gap disclosed after deadline", out)
				}
			}
			limit := time.After(time.Second)
			for {
				s.mu.Lock()
				active := s.current != nil
				s.mu.Unlock()
				if !active {
					break
				}
				select {
				case <-limit:
					t.Fatal("entropy worker failed to join")
				case <-time.After(time.Millisecond):
				}
			}
		})
	}
}

func TestOrderlyWatcherStopDoesNotInventAuthorityRefusal(t *testing.T) {
	for _, translated := range []error{ErrUnavailable, errors.New("translated SQL cancellation")} {
		t.Run(translated.Error(), func(t *testing.T) {
			entered := make(chan struct{})
			var calls atomic.Int32
			check := func(ctx context.Context) error {
				if calls.Add(1) == 3 {
					close(entered)
					<-ctx.Done()
					return translated
				}
				return nil
			}
			s := controlledInspector(func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
				<-entered
				return nativeFailure("EXIT_FAILED", true), nil, nil
			})
			nonce := controlGeneration(t, s)
			out, err := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
			if err != nil || out.Code != "EXIT_FAILED" || !out.Joined || calls.Load() < 4 {
				t.Fatal("orderly watcher cancellation invented refusal", out, err, calls.Load())
			}
		})
	}
}
func TestObservedWatcherDenialBeforeOrderlyStopStaysRedacted(t *testing.T) {
	denied := errors.New("genuine authority revocation")
	var calls atomic.Int32
	check := func(context.Context) error {
		if calls.Add(1) == 3 {
			return denied
		}
		return nil
	}
	s := controlledInspector(func(ctx context.Context, _ Check, _ *candidateBinding) (ProbeObservation, func() error, error) {
		<-ctx.Done()
		return nativeFailure("CANCELLED", true), nil, nil
	})
	nonce := controlGeneration(t, s)
	out, err := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
	if err != denied || out.State != "" || out.ToolVersion != nil || calls.Load() < 4 {
		t.Fatal("observed denial was lost after cancellation", out, err, calls.Load())
	}
}

func TestCancelledWatcherCheckDoesNotInventRefusal(t *testing.T) {
	for _, cause := range []string{"owned-stop", "deadline", "current-denied"} {
		t.Run(cause, func(t *testing.T) {
			entered := make(chan struct{})
			var calls atomic.Int32
			var deniedNow atomic.Bool
			denied := errors.New("current authority denied")
			check := func(ctx context.Context) error {
				if calls.Add(1) == 3 {
					close(entered)
					<-ctx.Done()
					return ErrUnavailable
				}
				if deniedNow.Load() {
					return denied
				}
				return nil
			}
			s := controlledInspector(func(ctx context.Context, _ Check, _ *candidateBinding) (ProbeObservation, func() error, error) {
				<-ctx.Done()
				return cancellationObservation(ctx), nil, nil
			})
			s.drain = 100 * time.Millisecond
			if cause == "deadline" {
				s.deadline = 40 * time.Millisecond
			}
			nonce := controlGeneration(t, s)
			done := make(chan probeAnswer, 1)
			go func() {
				v, e := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
				done <- probeAnswer{v, e}
			}()
			<-entered
			if cause != "deadline" {
				if cause == "current-denied" {
					deniedNow.Store(true)
				}
				s.mu.Lock()
				operation := s.current
				s.mu.Unlock()
				operation.cancel()
			}
			out := <-done
			if cause == "current-denied" {
				if out.err != denied || out.observation.State != "" {
					t.Fatal("final current denial lost", out)
				}
			} else {
				want := "CANCELLED"
				if cause == "deadline" {
					want = "TIMED_OUT"
				}
				if out.err != nil || out.observation.Code != want || !out.observation.Joined {
					t.Fatal("cancelled watcher invented authority loss", out)
				}
			}
		})
	}
}

// This injected adapter models the three direct native admission boundaries.
// Kernel custody remains covered separately by the owned native helper suite.
func directPhaseBackend(ctx context.Context, check Check, _ *candidateBinding) (ProbeObservation, func() error, error) {
	for range 3 {
		if err := check(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return cancellationObservation(ctx), nil, nil
			}
			return ProbeObservation{}, nil, err
		}
	}
	return joinedBackend(ctx, check, nil)
}
func TestDirectPhaseCancelledCheckDeadlineTranslation(t *testing.T) {
	for _, phase := range []struct {
		name string
		call int32
	}{{"before-create", 4}, {"before-resume", 5}} {
		t.Run(phase.name, func(t *testing.T) {
			var calls atomic.Int32
			check := func(ctx context.Context) error {
				if calls.Add(1) == phase.call {
					<-ctx.Done()
					return ErrUnavailable
				}
				return nil
			}
			s := controlledInspector(directPhaseBackend)
			s.deadline = 40 * time.Millisecond
			s.drain = 200 * time.Millisecond
			s.checkInterval = time.Hour
			nonce := controlGeneration(t, s)
			out, err := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
			if err != nil || out.Code != "TIMED_OUT" || !out.Joined || out.ToolVersion != nil {
				t.Fatal("direct deadline cancellation invented refusal", out, err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestDirectPhaseCancelledCheckAfterAuthorizedStop(t *testing.T) {
	for _, phase := range []struct {
		name string
		call int32
	}{{"before-create", 5}, {"before-resume", 6}} {
		t.Run(phase.name, func(t *testing.T) {
			backendEntered, releaseBackend := make(chan struct{}), make(chan struct{})
			stopEntered, releaseStop, directEntered := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			check := func(ctx context.Context) error {
				switch calls.Add(1) {
				case 3:
					close(stopEntered)
					<-releaseStop
				case phase.call:
					close(directEntered)
					<-ctx.Done()
					return ErrUnavailable
				}
				return nil
			}
			s := controlledInspector(func(ctx context.Context, c Check, b *candidateBinding) (ProbeObservation, func() error, error) {
				close(backendEntered)
				<-releaseBackend
				return directPhaseBackend(ctx, c, b)
			})
			s.deadline = 2 * time.Second
			s.drain = 200 * time.Millisecond
			s.checkInterval = time.Hour
			nonce := controlGeneration(t, s)
			probeDone := make(chan probeAnswer, 1)
			go func() {
				out, err := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
				probeDone <- probeAnswer{out, err}
			}()
			<-backendEntered
			stopDone := make(chan struct {
				out StopObservation
				err error
			}, 1)
			go func() {
				out, err := s.Stop(context.Background(), "runtime", "configuration", nonce, check)
				stopDone <- struct {
					out StopObservation
					err error
				}{out, err}
			}()
			<-stopEntered
			// Stop has fresh admission. Hold only the short state gate so the next direct
			// phase can enter after that Check releases its separate serialization gate.
			s.mu.Lock()
			close(releaseStop)
			close(releaseBackend)
			select {
			case <-directEntered:
			case <-time.After(time.Second):
				s.mu.Unlock()
				t.Fatal("direct phase did not enter before admitted Stop")
			}
			s.mu.Unlock()
			stopped := <-stopDone
			if stopped.err != nil || !stopped.out.Joined || stopped.out.Phase != "joined" {
				t.Fatal("authorized Stop did not observe actual join", stopped)
			}
			out := <-probeDone
			if out.err != nil || out.observation.Code != "CANCELLED" || !out.observation.Joined || out.observation.ToolVersion != nil {
				t.Fatal("direct Stop cancellation invented refusal", out)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestDirectPhaseLiveDenialAndFinalFreshCheckRemainAuthoritative(t *testing.T) {
	for _, phase := range []struct {
		name string
		call int32
	}{{"before-create", 4}, {"before-resume", 5}, {"final-disclosure", 6}} {
		t.Run(phase.name, func(t *testing.T) {
			denied := errors.New("live authority denied")
			var calls atomic.Int32
			check := func(context.Context) error {
				if calls.Add(1) == phase.call {
					return denied
				}
				return nil
			}
			s := controlledInspector(directPhaseBackend)
			s.checkInterval = time.Hour
			nonce := controlGeneration(t, s)
			out, err := s.Probe(context.Background(), "runtime", "configuration", nonce, check)
			if err != denied || out.State != "" || out.ToolVersion != nil {
				t.Fatal("live direct/final denial lost", out, err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCompletedLiveCheckFailureSurvivesLaterCancellation(t *testing.T) {
	s := controlledInspector(joinedBackend)
	ctx, cancel := context.WithCancel(context.Background())
	observed := errors.New("already observed live denial")
	result := s.checked(ctx, func(context.Context) error { return observed })
	if result != observed {
		t.Fatal("live check error changed", result)
	}
	cancel()
	if !observedCheckFailure(ctx, result) {
		t.Fatal("later watcher stop erased completed live failure")
	}
	live := context.Background()
	if result = s.checked(live, func(context.Context) error { return context.DeadlineExceeded }); result != context.DeadlineExceeded || !observedCheckFailure(live, result) {
		t.Fatal("live outer check lost nested deadline failure", result)
	}
	cancelled, cancelCheck := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.checked(cancelled, func(ctx context.Context) error { close(entered); <-ctx.Done(); return ErrUnavailable })
	}()
	<-entered
	cancelCheck()
	result = <-done
	if !errors.Is(result, context.Canceled) || observedCheckFailure(cancelled, result) {
		t.Fatal("cancelled callback created an authority observation", result)
	}
}

func TestWorkspacePreparationUsesReturnedCauseAndCleanupPrecedence(t *testing.T) {
	realFailure := errors.New("real workspace failure")
	for _, row := range []struct {
		name string
		err  error
		code string
	}{{"cancelled", context.Canceled, "CANCELLED"}, {"deadline", context.DeadlineExceeded, "TIMED_OUT"}, {"wrapped-cancelled", errors.Join(errors.New("preparation"), context.Canceled), "CANCELLED"}, {"real-failure", realFailure, "EXIT_FAILED"}} {
		t.Run(row.name, func(t *testing.T) {
			out := preparationCauseObservation(row.err)
			if out.Code != row.code || !out.Joined || out.ToolVersion != nil {
				t.Fatal("wrong preparation cause", out)
			}
		})
	}
	later, cancel := context.WithCancel(context.Background())
	cancel()
	if later.Err() == nil || preparationCauseObservation(realFailure).Code != "EXIT_FAILED" {
		t.Fatal("later cancellation replaced the returned real failure")
	}
	s := controlledInspector(func(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
		return preparationCauseObservation(context.DeadlineExceeded), func() error { return ErrIncomplete }, nil
	})
	nonce := controlGeneration(t, s)
	out, err := s.Probe(context.Background(), "runtime", "configuration", nonce, allow)
	if err != nil || out.Code != "DRAIN_INCOMPLETE" || out.Joined || out.ToolVersion != nil {
		t.Fatal("cleanup uncertainty lost precedence", out, err)
	}
	if err = s.Close(); err != ErrIncomplete {
		t.Fatal("unresolved preparation cleanup released custody", err)
	}
}

func TestMetadataReservationDiscardsJoinedProbeTombstoneOnly(t *testing.T) {
	s := controlledInspector(joinedBackend)
	first := controlGeneration(t, s)
	out, err := s.Probe(context.Background(), "runtime", "configuration", first, allow)
	if err != nil || !out.Joined {
		t.Fatal(out, err)
	}
	ready, err := s.Control(context.Background(), "runtime", "configuration", allow)
	if err != nil || ready.Phase != "ready" || ready.ProbeGeneration == first {
		t.Fatal(ready, err)
	}
	inspected, err := s.Inspect(context.Background(), "runtime", allow)
	if err != nil || inspected.State != "candidate" {
		t.Fatal(inspected, err)
	}
	after, err := s.Control(context.Background(), "runtime", "configuration", allow)
	if err != nil || after != ready {
		t.Fatal("metadata consumed/rotated the available probe generation", ready, after, err)
	}
	if _, err = s.Stop(context.Background(), "runtime", "configuration", first, allow); err != ErrStale {
		t.Fatal("metadata reservation retained obsolete probe tombstone", err)
	}
	if _, err = s.Probe(context.Background(), "runtime", "configuration", first, allow); err != ErrStale {
		t.Fatal("obsolete probe generation replayed", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}
