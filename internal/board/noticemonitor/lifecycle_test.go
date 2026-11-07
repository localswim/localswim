package noticemonitor

import (
	"bytes"
	"context"
	"errors"
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

const futureTarget A.UUID = "20000000-0000-4000-8000-000000000099"

func control(t *testing.T, m *Monitor, action string, target A.UUID, auth Authority) Result {
	t.Helper()
	view := m.Snapshot()
	result, err := m.Control(context.Background(), action, view.Generation, m.revision(), target, "", auth)
	must(t, err)
	return result
}

func TestLiveReloadRetargetAndNoops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{signature: "idle"}
		m := f.monitor(t)
		defer m.Close(ctx)
		for _, action := range []string{"reload", "retarget"} {
			_, err := m.Control(ctx, action, m.Snapshot().Generation, 0, futureTarget, "", allow)
			if !errors.Is(err, ErrBusy) || len(f.data) != 0 {
				t.Fatal("unconfigured live control allocated evidence", action, err)
			}
		}
		start(t, m, allow)
		initial := m.Snapshot()
		m.gate.Lock()
		oldRun := m.run
		m.gate.Unlock()
		originalBytes := bytes.Clone(f.data)
		same := control(t, m, "retarget", initial.State.TargetID, allow)
		if same.Generation != initial.Generation || !bytes.Equal(originalBytes, f.data) {
			t.Fatal("same target changed running state")
		}
		m.gate.Lock()
		sameRun := m.run
		m.gate.Unlock()
		if sameRun != oldRun {
			t.Fatal("same target drained its worker")
		}
		f.activity(1, notices.Human, "human")
		advance(time.Second + time.Millisecond)
		observation := m.Snapshot().State.Observation
		reloaded := control(t, m, "reload", "", allow)
		synctest.Wait()
		m.gate.Lock()
		newRun := m.run
		m.gate.Unlock()
		select {
		case <-oldRun.done:
		default:
			t.Fatal("replacement preceded predecessor quiescence")
		}
		if newRun == oldRun || reloaded.Phase != "running" || reloaded.State.ConfigurationRevision != 1 ||
			reloaded.State.TargetID != initial.State.TargetID || len(m.Inbox()) != 1 || m.Snapshot().State.Last.ObservationID != observation.ID {
			t.Fatal("reload lost the original batch or configuration")
		}
		retargeted := control(t, m, "retarget", futureTarget, allow)
		if retargeted.Phase != "running" || retargeted.State.TargetID != futureTarget || retargeted.State.ConfigurationRevision != 2 ||
			retargeted.State.Last.TargetID != initial.State.TargetID {
			t.Fatal("running retarget lost old evidence")
		}
		f.activity(2, "all-automation", "bot")
		advance(3*time.Second + time.Millisecond)
		if s := m.Snapshot().State; s.Last.Class != notices.Suppressed || s.Last.TargetID != futureTarget || len(m.Inbox()) != 1 {
			t.Fatal("replacement catch-up/suppression changed")
		}
		stop(t, m)
		before := m.Snapshot()
		originalBytes = bytes.Clone(f.data)
		idle := control(t, m, "reload", "", allow)
		configured := control(t, m, "configure", "", allow)
		if idle.Generation != before.Generation || configured.Generation != before.Generation || !bytes.Equal(originalBytes, f.data) {
			t.Fatal("quiescent no-op changed evidence")
		}
		changed := control(t, m, "retarget", initial.State.TargetID, allow)
		if changed.Phase != "stopped" || changed.State.ConfigurationRevision != 3 {
			t.Fatal("stopped retarget implicitly started")
		}
	})
}

func TestLiveRetargetGuardedOriginalWork(t *testing.T) {
	for _, stage := range []string{"observation", "prepared", "attempted", "accepted"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, m := interruptedFixture(t, stage)
				defer m.Close(context.Background())
				before := m.Snapshot()
				after := control(t, m, "retarget", futureTarget, allow)
				if after.Phase != "guarded" || after.State.ConfigurationRevision != 2 || after.State.Checkpoint != before.State.Checkpoint ||
					after.State.Observation.TargetID != before.State.TargetID || len(f.captured()) != 0 {
					t.Fatal("retarget changed original work")
				}
				if before.State.Pending != nil && after.State.Pending.DeliveryID != before.State.Pending.DeliveryID {
					t.Fatal("retarget replaced immutable pending")
				}
				settled := control(t, m, "recover", "", allow)
				if stage == "attempted" {
					if settled.Phase != "guarded" || len(m.Inbox()) != 0 {
						t.Fatal("retarget invented attempt permission")
					}
				} else if settled.Phase != "stopped" || len(m.Inbox()) != 1 || m.Inbox()[0].TargetID != before.State.TargetID {
					t.Fatal("recovery moved original target")
				}
			})
		})
	}
}

func TestLiveControlsReserveAcrossEveryWorkerPublication(t *testing.T) {
	for _, action := range []string{"stop", "reload", "retarget"} {
		for _, phase := range []string{"source", "observation", "capture", "intent", "fence", "acceptance", "disposition"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx := context.Background()
					f := &fixture{}
					m := f.monitor(t)
					defer m.Close(ctx)
					start(t, m, allow)
					entered, release := make(chan struct{}), make(chan struct{})
					unblock := sync.OnceFunc(func() { close(release) })
					defer unblock()
					pause := sync.OnceFunc(func() { close(entered); <-release })
					if phase == "source" {
						observe := m.source.Observe
						m.source.Observe = func(ctx context.Context, auth Authority) (noticeinterval.Cut, error) {
							pause()
							return observe(ctx, auth)
						}
					} else if phase == "observation" {
						f.mu.Lock()
						f.writeHook = func() error { pause(); return nil }
						f.mu.Unlock()
					} else {
						f.activity(1, notices.Human, "wake")
						advance(time.Second + time.Millisecond)
						if phase == "capture" {
							f.mu.Lock()
							f.captureHook = pause
							f.mu.Unlock()
						} else {
							want := map[string]int{"intent": 1, "fence": 2, "acceptance": 3, "disposition": 4}[phase]
							var writes atomic.Int32
							f.mu.Lock()
							f.writeHook = func() error {
								if writes.Add(1) == int32(want) {
									pause()
								}
								return nil
							}
							f.mu.Unlock()
						}
					}
					if phase == "source" || phase == "observation" {
						f.activity(1, notices.Human, "wake")
						advance(time.Second)
					}
					m.gate.Lock()
					originalRun := m.run
					m.gate.Unlock()
					old := m.Snapshot()
					results := make(chan Result, 1)
					go func() {
						result, err := m.Control(ctx, action, old.Generation, old.State.ConfigurationRevision, futureTarget, "", allow)
						if err != nil {
							t.Error(err)
						}
						results <- result
					}()
					<-entered
					synctest.Wait()
					reserved := m.Snapshot()
					if reserved.Operation != action {
						t.Fatal("missing control reservation")
					}
					for _, competitor := range []string{"configure", "start", "stop", "reload", "retarget", "recover", "lookup"} {
						target, delivery := futureTarget, ""
						if competitor == "lookup" {
							delivery = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
						}
						if _, err := m.Control(ctx, competitor, old.Generation, 1, target, delivery, allow); !errors.Is(err, ErrStale) {
							t.Fatal("old versions were not stale", competitor, err)
						}
						if _, err := m.Control(ctx, competitor, reserved.Generation, 1, target, delivery, allow); !errors.Is(err, ErrBusy) {
							t.Fatal("matching competitor crossed reservation", competitor, err)
						}
					}
					partial := <-results
					if partial.Phase != "partial-drain" || partial.Operation != action || partial.State.ConfigurationRevision != 1 {
						t.Fatal("partial response misreported operation")
					}
					m.gate.Lock()
					active := m.run
					m.gate.Unlock()
					if active != originalRun {
						t.Fatal("replacement ran during old effect")
					}
					closeCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
					if err := m.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("Close released admitted work")
					}
					cancel()
					unblock()
					must(t, m.Close(ctx))
					if m.Snapshot().Operation != "" || m.run != nil || len(m.Inbox()) > 1 || partial.Phase != "partial-drain" || partial.State.ConfigurationRevision != 1 {
						t.Fatal("completion changed captured response or duplicated effects")
					}
					if action == "retarget" && m.Snapshot().State.ConfigurationRevision != 2 {
						t.Fatal("Close rolled back admitted target publication")
					}
				})
			})
		}
	}
}

func TestControlResponseAuthorityDeadlineAndOutcome(t *testing.T) {
	for _, action := range []string{"configure", "start", "stop", "recover", "reload", "retarget", "lookup"} {
		for _, outcome := range []string{"deny", "fail", "timeout"} {
			t.Run(action+"/"+outcome, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx := context.Background()
					f := &fixture{}
					m := f.monitor(t)
					defer m.Close(ctx)
					if action != "configure" {
						control(t, m, "configure", "", allow)
					}
					if action == "stop" || action == "reload" {
						control(t, m, "start", "", allow)
						synctest.Wait()
					}
					before := m.Snapshot()
					release := make(chan struct{})
					unblock := sync.OnceFunc(func() { close(release) })
					defer unblock()
					check := m.source.Check
					m.source.Check = func(ctx context.Context, auth Authority) error {
						if _, final := ctx.Deadline(); final {
							switch outcome {
							case "deny":
								return ErrAuthority
							case "fail":
								return ErrUnavailable
							case "timeout":
								<-release
							}
						}
						return check(ctx, auth)
					}
					started := time.Now()
					target, delivery := futureTarget, ""
					if action == "lookup" {
						delivery = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
					}
					result, err := m.Control(ctx, action, before.Generation, m.revision(), target, delivery, allow)
					if !errors.Is(err, ErrOutcomeUnknown) || result.State != nil {
						t.Fatal("post-admission authority disclosed a private result", err)
					}
					if time.Since(started) > 2*time.Second {
						t.Fatal("final authority exceeded absolute decision deadline")
					}
					if action == "retarget" && m.Snapshot().State.ConfigurationRevision != 2 {
						t.Fatal("response denial rolled back target publication")
					}
					if action == "reload" && m.Snapshot().Phase != "running" {
						t.Fatal("response denial rolled back replacement admission")
					}
					if outcome == "timeout" {
						if time.Since(started) != 2*time.Second {
							t.Fatal("uncancelable response read did not reach deadline")
						}
						closeCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
						if err := m.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal("uncancelable authority borrow lost owner")
						}
						cancel()
					}
					unblock()
					must(t, m.Close(ctx))
				})
			})
		}
	}
}

func TestRetargetPublicationFaultNeverAdoptsVisibleBytes(t *testing.T) {
	for _, visible := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "replacement-visible"}[visible], func(t *testing.T) {
			f := &fixture{}
			m := f.monitor(t)
			control(t, m, "configure", "", allow)
			old := m.Snapshot()
			fault := func() error { return notices.ErrStorage }
			if visible {
				f.writeAfterHook = fault
			} else {
				f.writeHook = fault
			}
			result := control(t, m, "retarget", futureTarget, allow)
			if result.Phase != "failed" || result.Reason != "storage" || result.State.ConfigurationRevision != 1 || !m.store.Uncertain() {
				t.Fatal("uncertain publication adopted visible bytes")
			}
			if _, err := m.Control(context.Background(), "retarget", old.Generation, 1, futureTarget, "", allow); !errors.Is(err, ErrStale) {
				t.Fatal("old request revived")
			}
			must(t, m.Close(context.Background()))
			f.writeHook, f.writeAfterHook = nil, nil
			reopened := f.monitor(t)
			defer reopened.Close(context.Background())
			want := A.Number(1)
			if visible {
				want = 2
			}
			if reopened.Snapshot().State.ConfigurationRevision != want || reopened.Snapshot().Generation == result.Generation || reopened.run != nil {
				t.Fatal("reopen did not independently validate exact publication")
			}
		})
	}
}

func TestReplacementCallerCannotLendAuthorityToOldWork(t *testing.T) {
	for _, action := range []string{"reload", "retarget"} {
		phases := []int{2, 3, 4}
		if action == "retarget" {
			phases = []int{2, 3, 4, 5, 6}
		}
		for _, denyAt := range phases {
			t.Run(action+"/phase-"+string(rune('0'+denyAt)), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx := context.Background()
					f := &fixture{}
					m := f.monitor(t)
					defer m.Close(ctx)
					start(t, m, allow)
					f.activity(1, notices.Human, "old batch")
					advance(time.Second + time.Millisecond)
					original := m.Snapshot().State.Observation
					var checks atomic.Int32
					auth := func(context.Context, writer.Read) error {
						if checks.Add(1) >= int32(denyAt) {
							return ErrAuthority
						}
						return nil
					}
					before := m.Snapshot()
					_, err := m.Control(ctx, action, before.Generation, 1, futureTarget, "", auth)
					if !errors.Is(err, ErrOutcomeUnknown) {
						t.Fatal("post-admission denial claimed no effect", err)
					}
					state := m.Snapshot()
					if m.run != nil || len(m.Inbox()) != 1 || state.State.Last.ObservationID != original.ID || state.State.Last.TargetID != before.State.TargetID ||
						state.Phase != "guarded" || state.Reason != "authority" {
						t.Fatal("replacement caller changed predecessor authority or resurrected a worker")
					}
					want := A.Number(1)
					if action == "retarget" && denyAt >= 4 {
						want = 2
					}
					if state.State.ConfigurationRevision != want {
						t.Fatal("denied phase changed configuration boundary")
					}
					// A newly authorized read cannot resume the spent replacement plan.
					must(t, m.Check(ctx, allow))
					synctest.Wait()
					if m.run != nil {
						t.Fatal("reauthorization silently restarted replacement")
					}
				})
			})
		}
	}
}

func TestControlRevisionAndCancellationBeforeReservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{}
		m := f.monitor(t)
		defer m.Close(ctx)
		control(t, m, "configure", "", allow)
		before := m.Snapshot()
		original := bytes.Clone(f.data)
		for _, action := range []string{"configure", "start", "stop", "reload", "retarget", "recover", "lookup"} {
			delivery := ""
			if action == "lookup" {
				delivery = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			if _, err := m.Control(ctx, action, before.Generation, 0, futureTarget, delivery, allow); !errors.Is(err, ErrStale) {
				t.Fatal("revision mismatch admitted", action, err)
			}
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := m.Control(canceled, "retarget", before.Generation, 1, futureTarget, "", allow); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled request admitted", err)
		}
		if !bytes.Equal(original, f.data) || m.Snapshot().Generation != before.Generation {
			t.Fatal("pre-admission refusal changed state")
		}
	})
}

func TestResponseBelongsToAdmittedOperation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := &fixture{}
		m := f.monitor(t)
		defer m.Close(ctx)
		control(t, m, "configure", "", allow)
		original := m.Snapshot()
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		check := m.source.Check
		var finalReads atomic.Int32
		m.source.Check = func(ctx context.Context, auth Authority) error {
			if _, final := ctx.Deadline(); final && finalReads.Add(1) == 1 {
				close(entered)
				<-release
			}
			return check(ctx, auth)
		}
		first := make(chan Result, 1)
		go func() {
			result, err := m.Control(ctx, "retarget", original.Generation, 1, futureTarget, "", allow)
			if err != nil {
				t.Error(err)
			}
			first <- result
		}()
		<-entered
		second := control(t, m, "retarget", original.State.TargetID, allow)
		if second.State.ConfigurationRevision != 3 {
			t.Fatal("second operation did not finish while prior response authority waited")
		}
		unblock()
		result := <-first
		if result.State.ConfigurationRevision != 2 || result.State.TargetID != futureTarget || result.Generation == second.Generation || result.Operation != "" {
			t.Fatal("response borrowed a later operation's snapshot")
		}
	})
}
