package notices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/localswim/localswim/internal/board/noticeinterval"
)

func recoveryPlan(t *testing.T, s *Store, want RecoveryAction) *State {
	t.Helper()
	got, state, err := s.RecoveryPlan()
	requireErr(t, err, nil)
	if got != want {
		t.Fatalf("recovery plan = %d, want %d", got, want)
	}
	return state
}

func TestRecoveryPublicationMatrix(t *testing.T) {
	const (
		dirty = iota
		observe
		prepare
		fence
		accept
		finalize
	)
	matrix := []struct {
		name            string
		before, visible RecoveryAction
	}{
		{"dirty", RecoveryNoop, RecoveryClearDirty},
		{"observation", RecoveryClearDirty, RecoveryCapture},
		{"intent-and-gap", RecoveryCapture, RecoveryAttempt},
		{"fence", RecoveryAttempt, RecoveryUnknown},
		{"acceptance", RecoveryUnknown, RecoveryFinalize},
		{"finalization", RecoveryFinalize, RecoveryNoop},
	}
	for phase, test := range matrix {
		for _, lands := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/visible=%t", test.name, lands), func(t *testing.T) {
				f := &memoryFile{}
				s := f.open(t)
				target, err := s.Configure(baseline)
				requireErr(t, err, nil)
				upper := baseline
				upper.Gap++ // The exact coverage changes at the same event.
				evidence := noticeinterval.Evidence{Class: Gapped, Upper: &upper}
				var original *Intent
				invocations := 0
				permit := (func() error)(nil)
				for step := dirty; step <= phase; step++ {
					if step == prepare {
						// A recovery reservation is separate from a running worker.
						s = f.open(t)
					}
					f.failNext, f.land = step == phase, lands
					switch step {
					case dirty:
						err = s.BeginRun()
					case observe:
						err = s.Observe(sample)
					case prepare:
						original, err = s.PrepareRecovery(evidence)
					case fence:
						invoke, fenceErr := s.Fence(original, sample, true)
						err = fenceErr
						if fenceErr == nil {
							permit = func() error {
								invocations++
								_, err := invoke(sample)
								return err
							}
						} else if invoke != nil {
							t.Fatal("ambiguous fence granted an acceptance permit")
						}
					case accept:
						err = permit()
					case finalize:
						err = s.FinalizeRecovery(original)
					}
					if step == phase {
						wantErr := ErrStorage
						if step == accept {
							wantErr = ErrUnknown
						}
						requireErr(t, err, wantErr)
					} else {
						requireErr(t, err, nil)
					}
				}
				writes := f.writes
				if s.running {
					requireErr(t, s.EndRun(), ErrGuarded)
				}
				_, _, err = s.RecoveryPlan()
				requireErr(t, err, ErrUnknown)
				if f.writes != writes {
					t.Fatal("uncertain current owner attempted a publication")
				}
				reopened := f.open(t)
				want := test.before
				if lands {
					want = test.visible
				}
				state := recoveryPlan(t, reopened, want)
				if state.TargetID != target || (state.Checkpoint != baseline && !(phase == finalize && lands && state.Checkpoint == upper)) {
					t.Fatal("reopen changed target or prematurely advanced checkpoint")
				}
				if original != nil && state.Pending != nil && !state.Pending.exact(*original) {
					t.Fatal("reopen changed frozen intent")
				}
				observation := state.Observation
				intent := state.Pending
				action := want
				if action == RecoveryCapture {
					intent, err = reopened.PrepareRecovery(evidence)
					requireErr(t, err, nil)
					if observation == nil || intent.ObservationID != observation.ID || intent.Lower != observation.Lower {
						t.Fatal("capture replaced the original observation")
					}
					action = RecoveryAttempt
				}
				if action == RecoveryAttempt {
					invoke, err := reopened.Fence(intent, sample, true)
					requireErr(t, err, nil)
					invocations++
					pair, err := invoke(sample)
					requireErr(t, err, nil)
					if pair.DeliveryID != intent.DeliveryID || pair.AcceptedAt != sample {
						t.Fatal("recovery acceptance changed identity or time")
					}
					_, err = invoke(sample)
					requireErr(t, err, ErrGuarded)
					action = RecoveryFinalize
				}
				if action == RecoveryFinalize {
					requireErr(t, reopened.FinalizeRecovery(intent), nil)
					recoveryPlan(t, reopened, RecoveryNoop)
					if end := reopened.Snapshot(); end.Checkpoint != upper || end.Dirty || len(end.Gaps) != 1 ||
						end.Gaps[0].ObservationID != intent.ObservationID || !same(end.Gaps[0].Upper, intent.Upper) ||
						end.Last.DeliveryID != intent.DeliveryID || reopened.Inbox()[0].AcceptedAt != sample {
						t.Fatal("disposition did not preserve exact proof")
					}
					action = RecoveryNoop
				}
				if action == RecoveryClearDirty {
					requireErr(t, reopened.ClearDirty(), nil)
					action = RecoveryNoop
					if end := reopened.Snapshot(); end.Checkpoint != baseline || end.Last != nil || len(reopened.Inbox()) != 0 {
						t.Fatal("idle recovery invented evidence")
					}
				}
				writes = f.writes
				again := f.open(t)
				recoveryPlan(t, again, action)
				if invocations > 1 || len(again.Inbox()) > 1 || f.writes != writes {
					t.Fatal("repeat recovery changed or duplicated evidence")
				}
				if action == RecoveryUnknown {
					refused, err := again.Fence(intent, sample, true)
					requireErr(t, err, ErrGuarded)
					if refused != nil || f.writes != writes || !again.Snapshot().Pending.exact(*intent) {
						t.Fatal("unknown proof became eligible to resend")
					}
				}
			})
		}
	}
}

func TestLegacyRecoveryEncodingAndIdentity(t *testing.T) {
	legacyIntent := Intent{
		Version: 1, Binding: selected, ObservationID: "87654321-1111-4111-8111-012345678904",
		Lower: baseline, TargetID: "87654321-1111-4111-8111-012345678903", Class: Unavailable,
	}
	if legacyIntent.identity() != "94153e1b5e39fadc76ccf2459632f1ab64fb8a21e664531616b8fa2f3ff5727b" {
		t.Fatal("v1 delivery identity, including the explicit null upper, changed")
	}
	t.Run("clean", func(t *testing.T) {
		const v1 = `{"version":1,"binding":{"rootKey":"89abcdef:0000000000000010","databaseKey":"89abcdef:0000000000000011","installationId":"87654321-1111-4111-8111-012345678900","boardId":"87654321-1111-4111-8111-012345678901","incarnation":"87654321-1111-4111-8111-012345678902"},"targetId":"87654321-1111-4111-8111-012345678903","checkpoint":{"event":23,"revision":12,"at":"2026-11-06T10:00:00Z","floor":"","gap":0},"dirty":false,"observation":null,"gaps":[],"pending":null,"last":null,"inbox":[]}`
		f := &memoryFile{data: []byte(v1)}
		s := f.open(t)
		before := recoveryPlan(t, s, RecoveryNoop)
		if f.writes != 0 || string(f.data) != v1 {
			t.Fatal("legacy no-op rewrote original evidence")
		}
		requireErr(t, s.BeginRun(), nil)
		want := bytes.Replace([]byte(v1), []byte(`"version":1`), []byte(`"version":2`), 1)
		want = bytes.Replace(want, []byte(`"dirty":false`), []byte(`"dirty":true`), 1)
		want = append(want[:len(want)-1], []byte(`,"pendingAttempt":null}`)...)
		if s.Snapshot().TargetID != before.TargetID || !bytes.Equal(f.data, want) {
			t.Fatal("authorized upgrade changed legacy evidence")
		}
	})
	for _, hasReceipt := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending/receipt=%t", hasReceipt), func(t *testing.T) {
			s, f, _ := start(t)
			i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
			if hasReceipt {
				accept(t, s, i, sample)
			}
			d, err := decode(f.data, selected)
			requireErr(t, err, nil)
			d.Version, d.PendingAttempt = legacyVersion, nil
			f.data, err = d.encode()
			requireErr(t, err, nil)
			writes := f.writes
			reopened := f.open(t)
			if !hasReceipt {
				recoveryPlan(t, reopened, RecoveryUnknown)
				permit, err := reopened.Fence(i, sample, true)
				requireErr(t, err, ErrGuarded)
				if permit != nil || f.writes != writes || reopened.current.Load().Version != legacyVersion {
					t.Fatal("receipt-free legacy pending was upgraded or attempted")
				}
				return
			}
			recoveryPlan(t, reopened, RecoveryFinalize)
			requireErr(t, reopened.FinalizeRecovery(i), nil)
			pair := reopened.Inbox()[0]
			if pair.DeliveryID != i.DeliveryID || pair.AcceptedAt != sample || reopened.current.Load().Version != stateVersion {
				t.Fatal("positive legacy disposition changed the original receipt")
			}
		})
	}
}

func TestStrictAttemptDecodingAndFreshProof(t *testing.T) {
	s, f, target := start(t)
	i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
	canonical := bytes.Clone(f.data)
	bad := [][]byte{
		bytes.Replace(canonical, []byte(`"version":2`), []byte(`"version":3`), 1),
		bytes.Replace(canonical, []byte(`"version":2`), []byte(`"version":1`), 1),
		bytes.Replace(canonical, []byte(`,"pendingAttempt":"prepared"`), nil, 1),
		bytes.Replace(canonical, []byte(`"pendingAttempt":"prepared"`), []byte(`"pendingAttempt":null`), 1),
		bytes.Replace(canonical, []byte(`"pendingAttempt":"prepared"`), []byte(`"pendingAttempt":"new"`), 1),
		bytes.Replace(canonical, []byte(`"pendingAttempt":"prepared"`), []byte(`"pendingAttempt":"prepared","pendingAttempt":"prepared"`), 1),
		bytes.Repeat([]byte{' '}, MaxStateBytes+1),
	}
	accepted := accept(t, s, i, sample)
	published := bytes.Clone(f.data)
	bad = append(bad,
		bytes.Replace(published, []byte(`"pendingAttempt":"attempted"`), []byte(`"pendingAttempt":"prepared"`), 1),
		bytes.Replace(published, []byte(`"class":"observed-empty"`), []byte(`"class":"unavailable"`), 1))
	for n, raw := range bad {
		f.data = raw
		if _, err := Open(selected, f.read, f.write); !errors.Is(err, ErrInvalid) {
			t.Fatalf("malformed fixture %d: %v", n, err)
		}
	}
	f.data = published
	reopened := f.open(t)
	writes := f.writes
	altered, err := decode(published, selected)
	requireErr(t, err, nil)
	altered.Inbox[0].AcceptedAt = "2026-11-07T10:00:00Z"
	f.data, err = altered.encode()
	requireErr(t, err, nil)
	_, _, err = reopened.RecoveryPlan()
	requireErr(t, err, ErrUnknown)
	requireErr(t, reopened.FinalizeRecovery(i), ErrUnknown)
	_, _, err = reopened.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, ErrUnknown)
	f.data = nil
	_, _, err = reopened.RecoveryPlan()
	requireErr(t, err, ErrUnknown)
	f.readErr = ErrForeign
	_, _, err = reopened.RecoveryPlan()
	requireErr(t, err, ErrForeign)
	f.readErr = errors.New("unavailable")
	_, _, err = reopened.RecoveryPlan()
	requireErr(t, err, ErrStorage)
	if f.writes != writes {
		t.Fatal("failed evidence check wrote state")
	}
	projection, err := json.Marshal(accepted)
	requireErr(t, err, nil)
	if bytes.Contains(projection, []byte("pendingAttempt")) {
		t.Fatal("private marker leaked in receipt projection")
	}
}

func TestRecoverySuppressionAndUnknownUpper(t *testing.T) {
	s, f, target := start(t)
	upper := baseline
	upper.Event++
	requireErr(t, s.Observe(sample), nil)
	observed := s.Snapshot().Observation
	s = f.open(t)
	recoveryPlan(t, s, RecoveryCapture)
	requireErr(t, s.SuppressRecovery(upper), nil)
	state := recoveryPlan(t, s, RecoveryNoop)
	if state.Checkpoint != upper || state.TargetID != target || state.Observation != nil || state.Dirty ||
		state.Last.ObservationID != observed.ID || state.Last.Class != Suppressed || len(s.Inbox()) != 0 {
		t.Fatal("recovery suppression failed to settle only the observed interval")
	}

	requireErr(t, s.BeginRun(), nil)
	requireErr(t, s.Observe(sample), nil)
	s = f.open(t)
	recoveryPlan(t, s, RecoveryCapture)
	i, err := s.PrepareRecovery(noticeinterval.Evidence{Class: Unavailable})
	requireErr(t, err, nil)
	permit, err := s.Fence(i, sample, true)
	requireErr(t, err, nil)
	_, err = permit(sample)
	requireErr(t, err, nil)
	s = f.open(t)
	recoveryPlan(t, s, RecoveryFinalize)
	requireErr(t, s.FinalizeRecovery(i), nil)
	writes := f.writes
	s = f.open(t)
	state = recoveryPlan(t, s, RecoveryUnavailable)
	requireErr(t, s.ClearDirty(), ErrGuarded)
	requireErr(t, s.BeginRun(), ErrGuarded)
	pair, changed, err := s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, nil)
	if changed || pair.UpperEvent != nil || pair.AcceptedAt != sample || state.Checkpoint != upper ||
		!state.Dirty || len(state.Gaps) != 1 || state.Gaps[0].Upper != nil || state.Pending != nil || f.writes != writes {
		t.Fatal("null upper recovery invented coverage or changed its settled proof")
	}
}

func TestRecoveryCapacityAndAbandonedPermit(t *testing.T) {
	s, f, target := start(t)
	for range MaxPairs {
		i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
		accept(t, s, i, sample)
		requireErr(t, s.Finalize(target, i.DeliveryID), nil)
	}
	i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
	s = f.open(t)
	writes := f.writes
	recoveryPlan(t, s, RecoveryAttempt)
	permit, err := s.Fence(i, sample, true)
	requireErr(t, err, ErrCapacity)
	if permit != nil || f.writes != writes || len(s.Inbox()) != MaxPairs {
		t.Fatal("pre-fence refusal changed prepared state or reclaimed pairs")
	}
	recoveryPlan(t, s, RecoveryAttempt)
	permit, err = s.Fence(i, "2026-11-13T10:00:00Z", true)
	requireErr(t, err, nil)
	if permit == nil || f.writes != writes+1 || len(s.Inbox()) != MaxPairs {
		t.Fatal("fence did not preserve expired pairs until acceptance")
	}
	// A backwards clock adjustment can make the same pairs ineligible again.
	pair, err := permit(sample)
	requireErr(t, err, ErrCapacity)
	if pair != nil || f.writes != writes+1 || len(s.Inbox()) != MaxPairs {
		t.Fatal("acceptance used obsolete capacity or reclaimed a recent pair")
	}
	recoveryPlan(t, s, RecoveryUnknown)
	requireErr(t, s.FinalizeRecovery(i), ErrUnknown)
	reopened := f.open(t)
	state := recoveryPlan(t, reopened, RecoveryUnknown)
	another, err := reopened.Fence(i, "2026-11-13T10:00:00Z", true)
	requireErr(t, err, ErrGuarded)
	if another != nil || f.writes != writes+1 || state.Checkpoint != baseline ||
		!state.Pending.exact(*i) || len(reopened.Inbox()) != MaxPairs {
		t.Fatal("later authority reconstructed abandoned permission")
	}
}

func TestRecoveryAcceptanceTimeFollowsFence(t *testing.T) {
	s, f, _ := start(t)
	intent := prepare(t, s, complete(ObservedEmpty, baseline, 0))
	s = f.open(t)
	permit, err := s.Fence(intent, sample, true)
	requireErr(t, err, nil)
	const acceptedAt = "2026-11-06T10:01:00Z"
	pair, err := permit(acceptedAt)
	requireErr(t, err, nil)
	if pair == nil || pair.AcceptedAt != acceptedAt {
		t.Fatal("receipt records the time of the fence instead of acceptance")
	}
	requireErr(t, s.FinalizeRecovery(intent), nil)
	if after := f.open(t).Inbox(); len(after) != 1 || after[0].AcceptedAt != acceptedAt {
		t.Fatal("reopen changed the recorded acceptance time")
	}
}

func TestQuiescentLookupRefusesUnexpectedAcceptanceState(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprintf("acceptance-uncertain=%t", ambiguous), func(t *testing.T) {
			s, f, target := start(t)
			first := prepare(t, s, complete(ObservedEmpty, baseline, 0))
			accept(t, s, first, sample)
			requireErr(t, s.Finalize(target, first.DeliveryID), nil)
			i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
			permit, err := s.Fence(i, sample, false)
			requireErr(t, err, nil)
			f.failNext, f.land = ambiguous, true
			_, err = permit(sample)
			if ambiguous {
				requireErr(t, err, ErrUnknown)
			} else {
				requireErr(t, err, nil)
			}
			requireErr(t, s.EndRun(), ErrGuarded)
			writes := f.writes
			actual := bytes.Clone(f.data)
			altered, err := decode(actual, selected)
			requireErr(t, err, nil)
			altered.Inbox[0].AcceptedAt = "2026-11-05T10:00:00Z"
			f.data, err = altered.encode()
			requireErr(t, err, nil)
			pair, changed, err := s.Lookup(target, i.DeliveryID, true)
			requireErr(t, err, ErrUnknown)
			if pair != nil || changed || f.writes != writes {
				t.Fatal("lookup adopted unrelated receipt history")
			}
			if ambiguous {
				altered, err = decode(actual, selected)
				requireErr(t, err, nil)
				altered.Inbox[1].AcceptedAt = "2026-11-07T10:00:00Z"
				f.data, err = altered.encode()
				requireErr(t, err, nil)
				pair, changed, err = s.Lookup(target, i.DeliveryID, true)
				requireErr(t, err, ErrUnknown)
				if pair != nil || changed || f.writes != writes {
					t.Fatal("lookup adopted a changed acceptance time")
				}
			}
			f.data = actual
			pair, changed, err = s.Lookup(target, i.DeliveryID, true)
			requireErr(t, err, nil)
			if pair == nil || !changed || pair.AcceptedAt != sample || f.writes != writes+1 {
				t.Fatal("exact matching acceptance failed to reconcile")
			}
		})
	}
	t.Run("failed-fence-cannot-prove-acceptance", func(t *testing.T) {
		s, f, target := start(t)
		i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
		other := &memoryFile{data: bytes.Clone(f.data)}
		sibling := other.open(t)
		invoke, err := sibling.Fence(i, sample, true)
		requireErr(t, err, nil)
		_, err = invoke(sample)
		requireErr(t, err, nil)
		f.failNext = true
		invoke, err = s.Fence(i, sample, false)
		requireErr(t, err, ErrStorage)
		if invoke != nil {
			t.Fatal("failed fence granted permission")
		}
		requireErr(t, s.EndRun(), ErrGuarded)
		f.data = other.data
		writes := f.writes
		pair, changed, err := s.Lookup(target, i.DeliveryID, true)
		requireErr(t, err, ErrUnknown)
		if pair != nil || changed || f.writes != writes {
			t.Fatal("lookup inferred acceptance from failed fence")
		}
	})
}
