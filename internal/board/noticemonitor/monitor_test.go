package noticemonitor

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

type fixture struct {
	mu             sync.Mutex
	data           []byte
	writeHook      func() error
	writeAfterHook func() error
	captureHook    func()
	observeError   error
	foreign        bool
	cut            noticeinterval.Cut
	signature      string
	class          string
	captures       []noticeinterval.Cut
}

func (f *fixture) store(t *testing.T) *notices.Store {
	t.Helper()
	binding := notices.Binding{
		RootKey: "12345678:0000000000000001", DatabaseKey: "12345678:0000000000000002",
		InstallationID: "00000000-0000-4000-8000-000000000001",
		BoardID:        "00000000-0000-4000-8000-000000000002",
		Incarnation:    "00000000-0000-4000-8000-000000000003",
	}
	store, err := notices.Open(binding, func(int64) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.data == nil {
			return nil, fs.ErrNotExist
		}
		return slices.Clone(f.data), nil
	}, func(_ notices.Binding, data []byte) error {
		f.mu.Lock()
		hook := f.writeHook
		f.mu.Unlock()
		if hook != nil {
			if err := hook(); err != nil {
				return err
			}
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.data = slices.Clone(data)
		if f.writeAfterHook != nil {
			return f.writeAfterHook()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func (f *fixture) source() Source {
	return Source{
		Check: func(ctx context.Context, auth Authority) error {
			return auth(ctx, nil)
		},
		Observe: func(ctx context.Context, auth Authority) (noticeinterval.Cut, error) {
			if err := auth(ctx, nil); err != nil {
				return noticeinterval.Cut{}, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.foreign {
				return noticeinterval.Cut{}, notices.ErrForeign
			}
			cut := f.cut
			cut.At = stamp(time.Now())
			return cut, f.observeError
		},
		Capture: func(ctx context.Context, lower noticeinterval.Cut, auth Authority) (noticeinterval.Evidence, error) {
			if err := auth(ctx, nil); err != nil {
				return noticeinterval.Evidence{}, err
			}
			f.mu.Lock()
			cut, class, hook := f.cut, f.class, f.captureHook
			cut.At = stamp(time.Now())
			f.captures = append(f.captures, cut)
			f.mu.Unlock()
			if hook != nil {
				hook()
			}
			if class == notices.Unavailable {
				return noticeinterval.Evidence{Class: class}, nil
			}
			if class == notices.Gapped || class == notices.Expired {
				return noticeinterval.Evidence{Class: class, Upper: &cut}, nil
			}
			count := cut.Event - lower.Event
			if count == 0 {
				class = notices.ObservedEmpty
			}
			return noticeinterval.Evidence{Class: class, Upper: &cut, Count: &count}, nil
		},
		Signal: func() (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.signature, nil
		},
	}
}

func (f *fixture) monitor(t *testing.T) *Monitor {
	t.Helper()
	m, err := New(f.store(t), f.source())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fixture) activity(event A.Number, class, signal string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cut.Event, f.cut.Revision, f.class, f.signature = event, event, class, signal
}

func (f *fixture) captured() []noticeinterval.Cut {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.captures)
}

func allow(context.Context, writer.Read) error { return nil }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func start(t *testing.T, m *Monitor, auth Authority) {
	t.Helper()
	ctx := context.Background()
	must(t, m.Configure(ctx, m.Snapshot().Generation, auth))
	must(t, m.Start(ctx, m.Snapshot().Generation, auth))
	synctest.Wait()
}

func advance(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

func stop(t *testing.T, m *Monitor) {
	t.Helper()
	must(t, m.Stop(context.Background(), m.Snapshot().Generation, allow))
	if s := m.Snapshot(); s.Phase != "stopped" || s.State.Dirty {
		t.Fatalf("clean Stop got %s (%s)", s.Phase, s.Reason)
	}
}

func TestIdleDebounceMaxAgeAndCatchUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{signature: "initial"}
		m := f.monitor(t)
		defer m.Close(ctx)
		start(t, m, allow)
		original := m.Snapshot()
		advance(11 * time.Second)
		if state := m.Snapshot().State; state.Observation != nil || state.Checkpoint != original.State.Checkpoint || len(m.Inbox()) != 0 {
			t.Fatal("idle changed durable state")
		}

		f.activity(1, "all-automation", "first")
		advance(time.Second)
		first := m.Snapshot().State.Observation
		if first == nil {
			t.Fatal("new signal was not persisted")
		}
		f.activity(2, "all-automation", "second")
		advance(time.Second)
		latest := m.Snapshot().State.Observation
		if latest == nil || first.ID != latest.ID || first.FirstAt != latest.FirstAt || first.LatestAt == latest.LatestAt {
			t.Fatal("burst lost observation identity or timestamps")
		}
		advance(2*time.Second + time.Millisecond)
		if state := m.Snapshot().State; state.Last.Class != notices.Suppressed || state.Checkpoint.Event != 2 || len(m.Inbox()) != 0 {
			t.Fatal("complete automation burst was not suppressed exactly once")
		}

		// Polling observes a file-only change every second, so only the
		// maximum age can close this batch before the changes stop.
		for i := range 11 {
			f.activity(2, notices.ObservedEmpty, string(rune('a'+i)))
			advance(time.Second)
		}
		advance(time.Millisecond)
		pairs := m.Inbox()
		if len(pairs) != 1 || pairs[0].Class != notices.ObservedEmpty || pairs[0].LowerEvent != 2 || *pairs[0].UpperEvent != 2 {
			t.Fatalf("max-age batch = %+v", pairs)
		}
		state := m.Snapshot().State
		cuts := f.captured()
		if len(cuts) != 2 || cuts[1] != state.Checkpoint || *state.Last.Upper != state.Checkpoint {
			t.Fatal("equal-cut acceptance did not advance the full descriptor")
		}
		advance(11 * time.Second)
		if len(m.Inbox()) != 1 || m.Snapshot().State.Observation != nil {
			t.Fatal("unchanged coverage opened another batch")
		}

		stale := m.Snapshot().Generation
		stop(t, m)
		must(t, m.Close(ctx))
		f.activity(3, notices.Human, "downtime")
		reopened := f.monitor(t)
		defer reopened.Close(ctx)
		if reopened.Snapshot().Generation == stale {
			t.Fatal("engine generation reused")
		}
		start(t, reopened, allow)
		advance(2*time.Second + time.Millisecond)
		pairs = reopened.Inbox()
		if len(pairs) != 2 || pairs[0].Class != notices.Human || pairs[0].LowerEvent != 2 ||
			pairs[0].TargetID != original.State.TargetID {
			t.Fatalf("catch-up = %+v", pairs)
		}
		if err := reopened.Stop(ctx, stale, allow); !errors.Is(err, ErrStale) || reopened.Snapshot().Phase != "running" {
			t.Fatalf("stale Stop changed new run: %v", err)
		}
		stop(t, reopened)
		must(t, reopened.Close(ctx))
	})
}

func TestStopKeepsOwnerThroughStalledPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{}
		m := f.monitor(t)
		defer m.Close(ctx)
		start(t, m, allow)
		f.activity(1, notices.Human, "")
		advance(time.Second + time.Millisecond)
		entered, unblock := make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(unblock) })
		defer release()
		var once sync.Once
		f.mu.Lock()
		f.writeHook = func() error {
			once.Do(func() {
				close(entered)
				<-unblock
			})
			return nil
		}
		f.mu.Unlock()

		generation := m.Snapshot().Generation
		type stopResult struct {
			view Status
			err  error
		}
		result := make(chan stopResult, 2)
		startAt := time.Now()
		doStop := func(expected A.UUID) {
			view, err := m.StopView(ctx, expected, allow)
			result <- stopResult{view, err}
		}
		go doStop(generation)
		<-entered
		if s := m.Snapshot(); s.Phase != "draining" || s.Generation == generation || !s.State.Dirty {
			t.Fatalf("stalled drain reported %s", s.Phase)
		}
		if err := m.Start(ctx, generation, allow); !errors.Is(err, ErrStale) {
			t.Fatalf("stale Start returned %v", err)
		}
		if err := m.Start(ctx, m.Snapshot().Generation, allow); !errors.Is(err, ErrBusy) {
			t.Fatalf("draining Start returned %v", err)
		}
		if err := m.Stop(ctx, m.Snapshot().Generation, allow); !errors.Is(err, ErrBusy) {
			t.Fatalf("matching duplicate Stop bypassed reservation: %v", err)
		}
		got := <-result
		must(t, got.err)
		if got.view.Phase != "partial-drain" || !got.view.State.Dirty {
			t.Fatal("Stop lost partial operation result")
		}
		s := m.Snapshot()
		if time.Since(startAt) != time.Second || s.Phase != "partial-drain" || !s.State.Dirty {
			t.Fatalf("Stop returned after %v with %s", time.Since(startAt), s.Phase)
		}
		closeCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
		defer cancel()
		if err := m.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close released stalled ownership: %v", err)
		}
		release()
		must(t, m.Close(ctx))
		final := m.Snapshot()
		if final.Phase != "stopped" || final.Generation == s.Generation || final.State.Dirty || len(m.Inbox()) != 1 {
			t.Fatalf("drain completion reported %s", final.Phase)
		}
	})
}

func TestCaptureAndFinalAdmissionRevocation(t *testing.T) {
	for _, atFinalCheck := range []bool{false, true} {
		t.Run(map[bool]string{false: "capture", true: "final"}[atFinalCheck], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				f := &fixture{}
				m := f.monitor(t)
				defer m.Close(ctx)
				revoked := make(chan struct{})
				auth := func(context.Context, writer.Read) error {
					select {
					case <-revoked:
						return ErrAuthority
					default:
						return nil
					}
				}
				start(t, m, auth)
				f.activity(1, notices.Human, "")
				advance(time.Second + time.Millisecond)
				if atFinalCheck {
					f.mu.Lock()
					f.captureHook = func() { close(revoked) }
					f.mu.Unlock()
				} else {
					close(revoked)
				}
				must(t, m.Stop(ctx, m.Snapshot().Generation, allow))
				s := m.Snapshot()
				if s.Phase != "guarded" || s.Reason != "authority" || !s.State.Dirty ||
					s.State.Observation == nil || s.State.Pending != nil || len(m.Inbox()) != 0 {
					t.Fatalf("revocation reported %s (%s)", s.Phase, s.Reason)
				}
				must(t, m.Close(ctx))
				reopened := f.monitor(t)
				if reopened.Snapshot().Phase != "guarded" {
					t.Fatal("interrupted observation reopened clean")
				}
				if err := reopened.Start(ctx, reopened.Snapshot().Generation, allow); !errors.Is(err, notices.ErrGuarded) {
					t.Fatalf("interrupted restart returned %v", err)
				}
				must(t, reopened.Close(ctx))
			})
		})
	}
}

func TestEqualCutLossCapacityAndUnavailable(t *testing.T) {
	for _, class := range []string{notices.Gapped, notices.Unavailable} {
		t.Run(class, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				f := &fixture{class: class}
				m := f.monitor(t)
				defer m.Close(ctx)
				start(t, m, allow)
				checkpoint := m.Snapshot().State.Checkpoint
				if class == notices.Unavailable {
					f.mu.Lock()
					// No file or cut change: a source failure after current
					// admission is sufficient to open the observation.
					f.observeError = ErrUnavailable
					f.mu.Unlock()
					advance(4 * time.Second)
					s := m.Snapshot()
					if s.Phase != "guarded" || s.Reason != "unavailable" ||
						s.State.Checkpoint != checkpoint || len(s.State.Gaps) != 1 ||
						s.State.Gaps[0].Upper != nil || len(m.Inbox()) != 1 {
						t.Fatal("unknown upper failed to retain its gap and checkpoint")
					}
					advance(11 * time.Second)
					if len(m.Inbox()) != 1 {
						t.Fatal("guard replayed unavailable observation")
					}
					return
				}
				for i := 1; i <= notices.MaxGaps+1; i++ {
					f.mu.Lock()
					f.cut.Gap = A.Number(i)
					f.mu.Unlock()
					advance(3*time.Second + time.Millisecond)
					s := m.Snapshot()
					if i <= notices.MaxGaps && (s.Phase != "running" || s.State.Checkpoint.Event != checkpoint.Event ||
						s.State.Checkpoint.Gap != A.Number(i) || len(s.State.Gaps) != i || len(m.Inbox()) != i) {
						t.Fatalf("equal-cut loss %d failed to preserve its coverage checkpoint", i)
					}
					if i == 1 {
						advance(11 * time.Second)
						if len(m.Inbox()) != 1 || m.Snapshot().State.Observation != nil {
							t.Fatal("unchanged gap watermark opened another batch")
						}
					}
				}
				s := m.Snapshot()
				if s.Phase != "failed" || s.Reason != "capacity" || s.State.Observation == nil ||
					s.State.Pending != nil || len(s.State.Gaps) != notices.MaxGaps ||
					s.State.Checkpoint.Gap != notices.MaxGaps || len(m.Inbox()) != notices.MaxGaps {
					t.Fatal("gap capacity did not preserve unresolved evidence")
				}
			})
		})
	}
}

func TestQuiescentLookupByDifferentAuthority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{}
		m := f.monitor(t)
		defer m.Close(ctx)
		revoked := make(chan struct{})
		start(t, m, func(context.Context, writer.Read) error {
			select {
			case <-revoked:
				return ErrAuthority
			default:
				return nil
			}
		})
		f.activity(1, notices.Human, "")
		advance(time.Second + time.Millisecond)
		writes := 0
		f.mu.Lock()
		f.writeHook = func() error {
			writes++
			if writes == 3 { // prepare, fence, accept, then final disposition is denied
				close(revoked)
			}
			return nil
		}
		f.mu.Unlock()
		must(t, m.Stop(ctx, m.Snapshot().Generation, allow))
		before := m.Snapshot()
		pending := before.State.Pending
		if before.Phase != "guarded" || before.Reason != "authority" || pending == nil ||
			len(m.Inbox()) != 1 || before.State.Checkpoint.Event != 0 {
			t.Fatal("accepted unresolved receipt lost its guard")
		}
		receipt, err := m.Lookup(ctx, pending.TargetID, pending.DeliveryID, allow)
		must(t, err)
		after := m.Snapshot()
		if receipt == nil || receipt.DeliveryID != pending.DeliveryID || after.Phase != "stopped" ||
			after.Generation == before.Generation || after.State.Checkpoint.Event != 1 || after.State.Dirty {
			t.Fatal("current authority could not settle exact durable receipt")
		}
		again, err := m.Lookup(ctx, pending.TargetID, pending.DeliveryID, allow)
		must(t, err)
		if again == nil || again.AcceptedAt != receipt.AcceptedAt || len(m.Inbox()) != 1 ||
			m.Snapshot().Generation != after.Generation {
			t.Fatal("repeat lookup changed lifecycle or acceptance")
		}
	})
}

func TestCompetingConfigureReservationsAndCloseBorrow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{}
		m := f.monitor(t)
		defer m.Close(ctx)
		entered, unblock := make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(unblock) })
		defer release()
		f.mu.Lock()
		f.writeHook = func() error {
			close(entered)
			<-unblock
			return nil
		}
		f.mu.Unlock()
		generation := m.Snapshot().Generation
		result := make(chan error, 1)
		go func() { result <- m.Configure(ctx, generation, allow) }()
		<-entered
		claimed := m.Snapshot()
		if claimed.Generation == generation || claimed.State != nil {
			t.Fatal("Configure failed to reserve generation before publication")
		}
		if err := m.Configure(ctx, generation, allow); !errors.Is(err, ErrStale) {
			t.Fatalf("competing Configure returned %v", err)
		}
		if err := m.Stop(ctx, claimed.Generation, allow); !errors.Is(err, ErrBusy) {
			t.Fatalf("Stop crossed Configure reservation: %v", err)
		}
		closeCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
		defer cancel()
		if err := m.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close released an admitted Configure write: %v", err)
		}
		release()
		must(t, <-result)
		must(t, m.Close(ctx))
		if m.Snapshot().Phase != "stopped" {
			t.Fatal("completed configuration was not retained")
		}
	})
}

func TestNoticeClassesAndForeignScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{}
		m := f.monitor(t)
		defer m.Close(ctx)
		start(t, m, allow)
		for i, class := range []string{notices.Human, notices.Mixed, notices.UnknownActor, notices.OtherActor, notices.Expired} {
			f.activity(A.Number(2*(i+1)), class, "")
			advance(3*time.Second + time.Millisecond)
			pairs := m.Inbox()
			if len(pairs) != i+1 || pairs[0].Class != class || pairs[0].LowerEvent != A.Number(2*i) {
				t.Fatalf("class %s produced the wrong notice", class)
			}
		}
		f.mu.Lock()
		f.foreign = true
		before := slices.Clone(f.data)
		f.mu.Unlock()
		advance(time.Second)
		s := m.Snapshot()
		if s.Phase != "guarded" || s.Reason != "foreign" || s.State != nil || len(m.Inbox()) != 0 {
			t.Fatal("foreign scope exposed private facts")
		}
		f.mu.Lock()
		equal := slices.Equal(before, f.data)
		f.mu.Unlock()
		if !equal {
			t.Fatal("foreign scope changed durable state")
		}
	})
}

func TestStartBaselinesSignatureBeforeFirstWorkerPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{signature: "baseline"}
		source := f.source()
		sample := source.Signal
		initialEntered, initialDone, changeDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
		releaseInitial := sync.OnceFunc(func() { close(initialDone) })
		releaseWorker := sync.OnceFunc(func() { close(changeDone) })
		var calls atomic.Int32
		source.Signal = func() (string, error) {
			switch calls.Add(1) {
			case 1:
				signature, err := sample()
				close(initialEntered)
				<-initialDone
				return signature, err
			case 2:
				<-changeDone
			}
			return sample()
		}
		m, err := New(f.store(t), source)
		must(t, err)
		defer m.Close(ctx)
		defer releaseInitial()
		defer releaseWorker()
		must(t, m.Configure(ctx, m.Snapshot().Generation, allow))
		result := make(chan error, 1)
		generation := m.Snapshot().Generation
		go func() { result <- m.Start(ctx, generation, allow) }()
		<-initialEntered
		synctest.Wait()
		select {
		case <-result:
			t.Fatal("Start returned before sampling its initial signature")
		default:
		}
		releaseInitial()
		must(t, <-result)
		f.activity(0, notices.ObservedEmpty, "immediate eventless write")
		releaseWorker()
		advance(2*time.Second + time.Millisecond)
		if pairs := m.Inbox(); len(pairs) != 1 || pairs[0].Class != notices.ObservedEmpty {
			t.Fatal("the first worker poll lost a post-Start eventless write")
		}
		stop(t, m)
	})
}

func TestStopFencesUndiscoveredWakeAndBindsResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{}
		source := f.source()
		readCut := source.Observe
		entered, unblock := make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(unblock) })
		var reads atomic.Int32
		source.Observe = func(ctx context.Context, auth Authority) (noticeinterval.Cut, error) {
			if reads.Add(1) == 4 {
				close(entered)
				<-unblock
			}
			return readCut(ctx, auth)
		}
		m, err := New(f.store(t), source)
		must(t, err)
		defer m.Close(ctx)
		defer release()
		start(t, m, allow)
		<-entered

		result := make(chan Status, 1)
		errs := make(chan error, 1)
		expected := m.Snapshot().Generation
		go func() {
			view, err := m.StopView(ctx, expected, allow)
			result <- view
			errs <- err
		}()
		synctest.Wait()
		if m.Snapshot().Phase != "draining" {
			t.Fatal("Stop did not fence the blocked idle poll")
		}
		f.activity(1, notices.Human, "committed after Stop admission")
		release()
		view := <-result
		must(t, <-errs)
		if view.Phase != "stopped" || view.State.Observation != nil || view.State.Checkpoint.Event != 0 || len(m.Inbox()) != 0 {
			t.Fatal("a wake first seen after Stop opened a new batch")
		}

		must(t, m.Start(ctx, view.Generation, allow))
		if old := view; old.Phase != "stopped" || old.Generation != view.Generation ||
			old.Generation == m.Snapshot().Generation {
			t.Fatal("completed Stop response changed to a later run")
		}
		advance(2*time.Second + time.Millisecond)
		if pairs := m.Inbox(); len(pairs) != 1 || pairs[0].Class != notices.Human || *pairs[0].UpperEvent != 1 {
			t.Fatal("the next run missed the commit left by Stop")
		}
		stop(t, m)
	})
}

func TestActivePollYieldsToFinalCapture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{}
		source := f.source()
		readCut := source.Observe
		var reads atomic.Int32
		entered := make(chan struct{})
		source.Observe = func(ctx context.Context, auth Authority) (noticeinterval.Cut, error) {
			if reads.Add(1) == 5 {
				close(entered)
				<-ctx.Done()
				return noticeinterval.Cut{}, auth(ctx, nil)
			}
			return readCut(ctx, auth)
		}
		m, err := New(f.store(t), source)
		must(t, err)
		defer m.Close(ctx)
		start(t, m, func(ctx context.Context, _ writer.Read) error {
			if ctx.Err() != nil {
				return ErrAuthority
			}
			return nil
		})
		observed := time.Now().Add(time.Second)
		f.activity(1, notices.Human, "")
		<-entered
		advance(time.Second + time.Millisecond)
		if pairs := m.Inbox(); len(pairs) != 1 || pairs[0].Class != notices.Human ||
			pairs[0].AcceptedAt != stamp(observed.Add(2*time.Second)) {
			t.Fatal("the active poll did not yield to the quiet deadline and authorized capture")
		}
		stop(t, m)
	})
}
