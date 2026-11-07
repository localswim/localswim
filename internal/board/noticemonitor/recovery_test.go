package noticemonitor

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

func interruptedFixture(t *testing.T, stage string) (*fixture, *Monitor) {
	t.Helper()
	f := &fixture{}
	s := f.store(t)
	at := stamp(time.Now())
	lower := noticeinterval.Cut{At: at}
	_, err := s.Configure(lower)
	must(t, err)
	must(t, s.BeginRun())
	f.activity(1, notices.Human, "new")
	if stage != "idle" {
		must(t, s.Observe(at))
	}
	if stage != "idle" && stage != "observation" {
		upper := noticeinterval.Cut{Event: 1, Revision: 1, At: at}
		count := A.Number(1)
		intent, err := s.Prepare(noticeinterval.Evidence{Upper: &upper, Class: notices.Human, Count: &count})
		must(t, err)
		if stage != "prepared" {
			accept, err := s.Fence(intent, at, false)
			must(t, err)
			if stage != "attempted" {
				_, err = accept(at)
				must(t, err)
				if stage == "checkpoint" {
					must(t, s.Finalize(intent.TargetID, intent.DeliveryID))
				}
			}
		}
	}
	return f, f.monitor(t)
}

func TestRecoverRetainsProofAndCatchesUp(t *testing.T) {
	for _, stage := range []string{"idle", "observation", "prepared", "attempted", "accepted", "checkpoint"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				f, m := interruptedFixture(t, stage)
				defer m.Close(ctx)
				before := m.Snapshot()
				f.activity(2, notices.Human, "downtime")
				view, err := m.RecoverView(ctx, before.Generation, allow)
				must(t, err)
				if view.Generation == before.Generation || view.State.TargetID != before.State.TargetID {
					t.Fatal("recovery changed the target or kept the old generation")
				}
				if _, err = m.RecoverView(ctx, before.Generation, allow); !errors.Is(err, ErrStale) {
					t.Fatal("old generation was not refused", err)
				}
				if stage == "attempted" {
					if view.Phase != "guarded" || view.Reason != "unknown" ||
						view.State.Pending.DeliveryID != before.State.Pending.DeliveryID || len(m.Inbox()) != 0 {
						t.Fatal("attempt without exact receipt lost its guard")
					}
					if err := m.Start(ctx, view.Generation, allow); !errors.Is(err, notices.ErrGuarded) {
						t.Fatal("Start bypassed the original pending intent", err)
					}
					return
				}
				if view.Phase != "stopped" || view.State.Dirty || view.State.Pending != nil {
					t.Fatal("confirmed evidence did not settle to stopped")
				}
				wantCut := A.Number(1)
				switch stage {
				case "idle":
					wantCut = 0
				case "observation":
					wantCut = 2
					if view.State.Last.ObservationID != before.State.Observation.ID || len(f.captured()) != 1 {
						t.Fatal("retained observation was replaced or captured repeatedly")
					}
				}
				if view.State.Checkpoint.Event != wantCut || (stage != "observation" && len(f.captured()) != 0) {
					t.Fatal("recovery used the wrong cut or recaptured an original intent")
				}
				inbox := m.Inbox()
				if before.State.Pending != nil &&
					(len(inbox) != 1 || inbox[0].DeliveryID != before.State.Pending.DeliveryID) {
					t.Fatal("settled notice lost the original identity")
				}
				repeated, err := m.RecoverView(ctx, view.Generation, allow)
				must(t, err)
				if repeated.State.Checkpoint.Event != wantCut || len(m.Inbox()) != len(inbox) {
					t.Fatal("repeated recovery changed durable effects")
				}
				must(t, m.Start(ctx, repeated.Generation, allow))
				synctest.Wait()
				advance(3*time.Second + time.Millisecond)
				stop(t, m)
				wantPairs := 2
				if stage == "idle" || stage == "observation" {
					wantPairs = 1
				}
				if after := m.Snapshot().State; after.Checkpoint.Event != 2 || len(m.Inbox()) != wantPairs {
					t.Fatal("explicit Start did not catch up exactly once")
				}
			})
		})
	}
}

func TestRecoverRequiresCurrentLineageForWrites(t *testing.T) {
	ctx := context.Background()
	for _, stage := range []string{"idle", "prepared", "accepted"} {
		t.Run(stage, func(t *testing.T) {
			f, m := interruptedFixture(t, stage)
			defer m.Close(ctx)
			original := bytes.Clone(f.data)
			f.observeError = ErrUnavailable
			view, err := m.RecoverView(ctx, m.Snapshot().Generation, allow)
			must(t, err)
			if view.Phase != "guarded" || view.Reason != "unavailable" || !bytes.Equal(f.data, original) {
				t.Fatal("unproved coverage authorized a recovery mutation")
			}
			if stage == "accepted" {
				p := view.State.Pending
				pair, err := m.Lookup(ctx, p.TargetID, p.DeliveryID, allow)
				if pair != nil || !errors.Is(err, notices.ErrUnknown) || !bytes.Equal(f.data, original) {
					t.Fatal("quiescent lookup settled without lineage", err)
				}
			}
		})
	}
}

func TestRecoverCaptureRefusalDoesNotBecomeNotice(t *testing.T) {
	ctx := context.Background()
	f, m := interruptedFixture(t, "observation")
	defer m.Close(ctx)
	before := m.Snapshot()
	original := bytes.Clone(f.data)
	m.source.Capture = func(context.Context, noticeinterval.Cut, Authority) (noticeinterval.Evidence, error) {
		return noticeinterval.Evidence{}, ErrUnavailable
	}
	view, err := m.RecoverView(ctx, before.Generation, allow)
	must(t, err)
	if view.Phase != "guarded" || view.Reason != "unavailable" || view.State.Pending != nil ||
		view.State.Observation.ID != before.State.Observation.ID || len(m.Inbox()) != 0 ||
		!bytes.Equal(f.data, original) {
		t.Fatal("a refused capture became an admitted unavailable notice")
	}
}

func TestRecoverRechecksLineageAfterFence(t *testing.T) {
	ctx := context.Background()
	f, m := interruptedFixture(t, "prepared")
	defer m.Close(ctx)
	before := m.Snapshot()
	writes := 0
	f.writeHook = func() error {
		writes++
		f.observeError = ErrUnavailable
		return nil
	}
	view, err := m.RecoverView(ctx, before.Generation, allow)
	must(t, err)
	if writes != 1 || view.Phase != "guarded" || view.Reason != "unavailable" ||
		view.State.Pending.DeliveryID != before.State.Pending.DeliveryID ||
		view.State.Checkpoint != before.State.Checkpoint || len(m.Inbox()) != 0 {
		t.Fatal("unproved post-fence lineage allowed acceptance or disposition")
	}
	f.observeError = nil
	repeated, err := m.RecoverView(ctx, view.Generation, allow)
	must(t, err)
	if writes != 1 || repeated.Phase != "guarded" || repeated.Reason != "unknown" {
		t.Fatal("later lineage proof reopened a fenced attempt without a receipt")
	}
}

func TestRecoverRechecksAuthorityBetweenEffects(t *testing.T) {
	for _, stage := range []string{"after-fence", "after-acceptance"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			f, m := interruptedFixture(t, "prepared")
			defer m.Close(ctx)
			before := m.Snapshot()
			revokeAfter, writes := 1, 0
			if stage == "after-acceptance" {
				revokeAfter = 2
			}
			f.writeHook = func() error { writes++; return nil }
			auth := func(context.Context, writer.Read) error {
				if writes >= revokeAfter {
					return ErrAuthority
				}
				return nil
			}
			_, err := m.RecoverView(ctx, before.Generation, auth)
			if !errors.Is(err, ErrAuthority) || writes != revokeAfter ||
				m.Snapshot().State.Checkpoint.Event != before.State.Checkpoint.Event {
				t.Fatal("authority loss crossed a later effect boundary", err)
			}
			inbox := m.Inbox()
			result, err := m.RecoverView(ctx, m.Snapshot().Generation, allow)
			must(t, err)
			if stage == "after-fence" {
				if result.Phase != "guarded" || len(m.Inbox()) != 0 {
					t.Fatal("durable fence alone authorized another attempt")
				}
			} else if result.Phase != "stopped" || len(inbox) != 1 || len(m.Inbox()) != 1 ||
				m.Inbox()[0].DeliveryID != inbox[0].DeliveryID || m.Inbox()[0].AcceptedAt != inbox[0].AcceptedAt {
				t.Fatal("current authority failed to settle the original receipt")
			}
		})
	}
}

func TestRecoverKeepsReservationWhileClosing(t *testing.T) {
	ctx := context.Background()
	f, m := interruptedFixture(t, "prepared")
	entered, release := make(chan struct{}), make(chan struct{})
	var once bool
	f.writeHook = func() error {
		if !once {
			once = true
			close(entered)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := m.RecoverView(ctx, m.Snapshot().Generation, allow)
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		close(release)
		must(t, m.Close(ctx))
		t.Fatal("recovery did not enter the durable write", err)
	}
	t.Cleanup(func() {
		close(release)
		must(t, <-done)
		must(t, m.Close(ctx))
	})
	reserved := m.Snapshot()
	if err := m.Start(ctx, reserved.Generation, allow); !errors.Is(err, ErrBusy) {
		t.Fatal("Start crossed the recovery reservation", err)
	}
	if err := m.Stop(ctx, reserved.Generation, allow); !errors.Is(err, ErrBusy) {
		t.Fatal("Stop crossed the recovery reservation", err)
	}
	if _, err := m.RecoverView(ctx, reserved.Generation, allow); !errors.Is(err, ErrBusy) {
		t.Fatal("competing Recover crossed the reservation", err)
	}
	if _, err := m.Lookup(ctx, reserved.State.TargetID, reserved.State.Pending.DeliveryID, allow); !errors.Is(err, ErrBusy) {
		t.Fatal("Lookup crossed the recovery reservation", err)
	}
	closing, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.Close(closing); !errors.Is(err, context.Canceled) {
		t.Fatal("Close released a still-admitted recovery", err)
	}
}
