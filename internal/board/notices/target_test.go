package notices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

const futureTarget A.UUID = "87654321-1111-4111-8111-0123456789ab"

func TestConfigurationRevisionTransitions(t *testing.T) {
	f := &memoryFile{}
	s := f.open(t)
	requireErr(t, s.Retarget(futureTarget, 1), ErrGuarded)
	target, err := s.Configure(baseline)
	requireErr(t, err, nil)
	if s.Snapshot().ConfigurationRevision != 1 {
		t.Fatal("initial revision is not 1")
	}
	original := bytes.Clone(f.data)
	writes := f.writes
	requireErr(t, s.Retarget(target, 1), nil)
	_, err = s.Configure(baseline)
	requireErr(t, err, nil)
	if f.writes != writes || !bytes.Equal(original, f.data) {
		t.Fatal("no-op wrote state")
	}
	for _, bad := range []A.UUID{"", A.UUID(strings.ToUpper(string(futureTarget))), "87654321-1111-5111-8111-012345678909", "87654321-1111-4111-7111-012345678909", A.UUID(" " + string(futureTarget)), "file:///private"} {
		requireErr(t, s.Retarget(bad, 1), ErrInvalid)
	}
	requireErr(t, s.Retarget(futureTarget, 0), ErrInvalid)
	requireErr(t, s.Retarget(futureTarget, MaxConfigurationRevision+1), ErrInvalid)
	requireErr(t, s.Retarget(futureTarget, 2), ErrStale)
	if f.writes != writes {
		t.Fatal("invalid/stale request wrote state")
	}
	requireErr(t, s.Retarget(futureTarget, 1), nil)
	if got := s.Snapshot(); got.TargetID != futureTarget || got.ConfigurationRevision != 2 || got.Checkpoint != baseline || got.Dirty {
		t.Fatal("retarget changed unrelated state")
	}
	requireErr(t, s.Retarget(target, 1), ErrStale)
	requireErr(t, s.Retarget(target, 2), nil)
	s = f.open(t)
	if got := s.Snapshot(); got.TargetID != target || got.ConfigurationRevision != 3 {
		t.Fatal("A-B-A reused a revision")
	}
	d := *s.current.Load()
	d.ConfigurationRevision = MaxConfigurationRevision
	f.data, err = d.encode()
	requireErr(t, err, nil)
	s = f.open(t)
	original = bytes.Clone(f.data)
	writes = f.writes
	requireErr(t, s.Retarget(futureTarget, MaxConfigurationRevision), ErrConfigurationExhausted)
	requireErr(t, s.Retarget(target, MaxConfigurationRevision), nil)
	if f.writes != writes || !bytes.Equal(original, f.data) {
		t.Fatal("overflow or max no-op changed state")
	}
}

func settleRecovered(t *testing.T, s *Store, i *Intent, at A.Timestamp) {
	t.Helper()
	invoke, err := s.Fence(i, at, true)
	requireErr(t, err, nil)
	p, err := invoke(at)
	requireErr(t, err, nil)
	if p.TargetID != i.TargetID || p.DeliveryID != i.DeliveryID || p.AcceptedAt != at {
		t.Fatal("acceptance changed original evidence")
	}
	requireErr(t, s.FinalizeRecovery(i), nil)
}

func TestPendingOriginalTargetAndMixedInbox(t *testing.T) {
	s, f, target := start(t)
	upper := baseline
	upper.Event++
	i := prepare(t, s, complete(Human, upper, 1))
	writes := f.writes
	requireErr(t, s.Retarget(target, 1), nil)
	if f.writes != writes || !s.running {
		t.Fatal("same-target retarget drained or rewrote a running owner")
	}
	requireErr(t, s.Retarget(futureTarget, 1), ErrGuarded)
	s = f.open(t) // interruption makes the original prepared observation quiescent
	before := s.Snapshot()
	requireErr(t, s.Retarget(futureTarget, 1), nil)
	after := s.Snapshot()
	if !after.Pending.exact(*i) || *after.Observation != *before.Observation || after.Checkpoint != before.Checkpoint || len(after.Gaps) != len(before.Gaps) {
		t.Fatal("retarget rewrote original evidence")
	}
	recoveryPlan(t, s, RecoveryAttempt)
	settleRecovered(t, s, i, sample)
	if s.Snapshot().Last.TargetID != target {
		t.Fatal("last was relabeled as future target")
	}
	s = f.open(t)
	requireErr(t, s.Retarget(futureTarget, 2), nil)
	if got := s.Snapshot(); got.Last.TargetID != target || got.TargetID != futureTarget {
		t.Fatal("retained last/future distinction lost")
	}
	requireErr(t, s.BeginRun(), nil)
	upper.Event++
	j := prepare(t, s, complete(Human, upper, 1))
	if j.TargetID != futureTarget {
		t.Fatal("new observation did not use future configuration")
	}
	at := A.Timestamp("2026-11-06T10:00:01Z")
	accept(t, s, j, at)
	requireErr(t, s.Finalize(j.TargetID, j.DeliveryID), nil)
	requireErr(t, s.EndRun(), nil)
	s = f.open(t)
	pairs := s.Inbox()
	if len(pairs) != 2 || pairs[0].TargetID != futureTarget || pairs[1].TargetID != target || pairs[1].AcceptedAt != sample {
		t.Fatal("mixed inbox changed identities/order/time")
	}
	writes = f.writes
	for _, intent := range []*Intent{i, j} {
		receipt, reconciled, err := s.Lookup(intent.TargetID, intent.DeliveryID, true)
		requireErr(t, err, nil)
		if reconciled || receipt.DeliveryID != intent.DeliveryID || receipt.TargetID != intent.TargetID {
			t.Fatal("historical exact lookup failed")
		}
	}
	_, _, err := s.Lookup(futureTarget, i.DeliveryID, true)
	requireErr(t, err, ErrUnknown)
	if f.writes != writes {
		t.Fatal("historical lookup mutated state")
	}
	requireErr(t, s.Retarget(target, 2), nil)
	if s.Snapshot().ConfigurationRevision != 3 || s.Snapshot().Last.TargetID != futureTarget {
		t.Fatal("A-B-A changed last or reused revision")
	}
}

func TestOpenObservationCapturesTargetAcrossRetarget(t *testing.T) {
	for _, suppressed := range []bool{false, true} {
		t.Run(fmt.Sprint(suppressed), func(t *testing.T) {
			s, f, target := start(t)
			requireErr(t, s.Observe(sample), nil)
			original := *s.Snapshot().Observation
			requireErr(t, s.Observe("2026-11-06T10:00:01Z"), nil)
			captured := s.Snapshot().Observation
			if captured.TargetID != target || captured.ConfigurationRevision != 1 || captured.ID != original.ID {
				t.Fatal("extension changed captured configuration")
			}
			s = f.open(t)
			requireErr(t, s.Retarget(futureTarget, 1), nil)
			s = f.open(t)
			recoveryPlan(t, s, RecoveryCapture)
			upper := baseline
			upper.Event++
			if suppressed {
				requireErr(t, s.SuppressRecovery(upper), nil)
				if got := f.open(t).Snapshot(); got.Last.TargetID != target || got.Last.DeliveryID != "" || len(s.Inbox()) != 0 {
					t.Fatal("suppression lost original target")
				}
			} else {
				intent, err := s.PrepareRecovery(complete(Human, upper, 1))
				requireErr(t, err, nil)
				if intent.TargetID != target || intent.ObservationID != original.ID {
					t.Fatal("recovery moved original observation")
				}
				settleRecovered(t, s, intent, sample)
			}
		})
	}
}

func TestLegacyVersionsMigrateLosslesslyOnRetarget(t *testing.T) {
	for _, version := range []int{legacyVersion, attemptVersion} {
		for _, phase := range []string{"clean", "idle", "observation", "prepared", "attempted", "receipt", "finalized"} {
			t.Run(fmt.Sprintf("v%d/%s", version, phase), func(t *testing.T) {
				s, f, target := start(t)
				if phase == "clean" {
					requireErr(t, s.EndRun(), nil)
				}
				var i *Intent
				if phase != "clean" && phase != "idle" {
					requireErr(t, s.Observe(sample), nil)
					if phase != "observation" {
						var err error
						i, err = s.Prepare(complete(ObservedEmpty, baseline, 0))
						requireErr(t, err, nil)
						if phase == "attempted" {
							_, err = s.Fence(i, sample, false)
							requireErr(t, err, nil)
						}
						if phase == "receipt" || phase == "finalized" {
							accept(t, s, i, sample)
						}
						if phase == "finalized" {
							requireErr(t, s.Finalize(target, i.DeliveryID), nil)
							requireErr(t, s.EndRun(), nil)
						}
					}
				}
				d := *s.current.Load()
				d.Version = version
				if version == legacyVersion {
					d.PendingAttempt = nil
				}
				var err error
				f.data, err = d.encode()
				requireErr(t, err, nil)
				old := bytes.Clone(f.data)
				writes := f.writes
				for range 3 {
					s = f.open(t)
				}
				if f.writes != writes || !bytes.Equal(f.data, old) || s.Snapshot().ConfigurationRevision != 1 {
					t.Fatal("legacy read rewrote bytes")
				}
				before := s.Snapshot()
				requireErr(t, s.Retarget(futureTarget, 1), nil)
				s = f.open(t)
				got := s.Snapshot()
				if s.current.Load().Version != stateVersion || got.ConfigurationRevision != 2 || got.Checkpoint != before.Checkpoint || got.Dirty != before.Dirty || got.TargetID != futureTarget {
					t.Fatal("migration changed coverage/posture")
				}
				if got.Observation != nil && (got.Observation.TargetID != target || got.Observation.ConfigurationRevision != 1) {
					t.Fatal("migration lost observation capture")
				}
				if i != nil && got.Pending != nil && !got.Pending.exact(*i) {
					t.Fatal("migration changed immutable intent")
				}
				if got.Last != nil && got.Last.TargetID != target {
					t.Fatal("migration lost last target")
				}
				if got.Pending != nil && version == legacyVersion {
					if *s.current.Load().PendingAttempt != legacyUnknown {
						t.Fatal("v1 pending gained permission")
					}
					if phase == "receipt" {
						recoveryPlan(t, s, RecoveryFinalize)
						p, changed, err := s.Lookup(target, i.DeliveryID, true)
						requireErr(t, err, nil)
						if !changed || p.AcceptedAt != sample {
							t.Fatal("legacy positive proof was not preserved")
						}
					} else {
						recoveryPlan(t, s, RecoveryUnknown)
						permit, err := s.Fence(i, sample, true)
						requireErr(t, err, ErrGuarded)
						if permit != nil {
							t.Fatal("legacy-unknown produced attempt permission")
						}
					}
				}
			})
		}
	}
}

func TestConfigurationPublicationFaultsAndReopen(t *testing.T) {
	for _, version := range []int{legacyVersion, attemptVersion, stateVersion} {
		for _, land := range []bool{false, true} {
			t.Run(fmt.Sprintf("v%d/visible=%t", version, land), func(t *testing.T) {
				s, f, target := start(t)
				i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
				d := *s.current.Load()
				d.Version = version
				if version == legacyVersion {
					d.PendingAttempt = nil
				}
				var err error
				f.data, err = d.encode()
				requireErr(t, err, nil)
				s = f.open(t)
				f.failNext, f.land = true, land
				requireErr(t, s.Retarget(futureTarget, 1), ErrStorage)
				if !s.Uncertain() || s.Snapshot().TargetID != target || s.Snapshot().ConfigurationRevision != 1 {
					t.Fatal("live owner adopted uncertain configuration")
				}
				writes := f.writes
				requireErr(t, s.Retarget(futureTarget, 1), ErrGuarded)
				requireErr(t, s.BeginRun(), ErrGuarded)
				_, _, err = s.RecoveryPlan()
				requireErr(t, err, ErrUnknown)
				if f.writes != writes {
					t.Fatal("uncertain owner published another effect")
				}
				for range 3 {
					s = f.open(t)
				}
				got := s.Snapshot()
				wantTarget, wantRevision := target, A.Number(1)
				if land {
					wantTarget, wantRevision = futureTarget, 2
				}
				if got.TargetID != wantTarget || got.ConfigurationRevision != wantRevision || !got.Pending.exact(*i) {
					t.Fatal("reopen did not reveal exact durable result")
				}
				if land {
					requireErr(t, s.Retarget(target, 1), ErrStale)
				} else {
					requireErr(t, s.Retarget(futureTarget, 1), nil)
				}
				if version == legacyVersion {
					recoveryPlan(t, s, RecoveryUnknown)
				} else {
					recoveryPlan(t, s, RecoveryAttempt)
				}
			})
		}
	}
}

func TestVersionedTargetsRejectMalformedAndForeignState(t *testing.T) {
	s, f, target := start(t)
	requireErr(t, s.Observe(sample), nil)
	canonical := bytes.Clone(f.data)
	f.data = []byte(`{"version":3}`)
	writesBefore := f.writes
	requireErr(t, s.Retarget(target, 1), ErrInvalid)
	if f.writes != writesBefore || !s.running {
		t.Fatal("corrupt no-op wrote state or drained the worker")
	}
	f.data = canonical
	s = f.open(t)
	replacements := [][2]string{
		{`"configurationRevision":1`, `"configurationRevision":0`},
		{`"configurationRevision":1`, `"configurationRevision":9007199254740992`},
		{`"configurationRevision":1`, `"configurationRevision":1.0`},
		{`"configurationRevision":1`, `"configurationRevision":1,"configurationRevision":1`},
		{`"version":3`, `"version":2`},
		{`"version":3`, `"version":99`},
	}
	for _, pair := range replacements {
		raw := bytes.Replace(canonical, []byte(pair[0]), []byte(pair[1]), 1)
		if bytes.Equal(raw, canonical) {
			t.Fatal("fixture not changed")
		}
		if _, err := decode(raw, selected); err != ErrInvalid {
			t.Fatalf("accepted malformed configuration: %v", err)
		}
	}
	d := *s.current.Load()
	d.Observation = copyValue(d.Observation)
	d.Observation.TargetID = futureTarget // equal revision must have the same target
	if _, err := d.encode(); err != ErrInvalid {
		t.Fatal("accepted inconsistent capture")
	}
	d = *s.current.Load()
	d.Binding.Incarnation = "87654321-1111-4111-8111-012345678908"
	var err error
	f.data, err = d.encode()
	requireErr(t, err, nil)
	writes := f.writes
	s.running = false
	requireErr(t, s.Retarget(futureTarget, 1), ErrForeign)
	if f.writes != writes {
		t.Fatal("foreign state overwritten")
	}
	if _, err := decode(append(canonical, bytes.Repeat([]byte{' '}, MaxStateBytes)...), selected); err != ErrInvalid {
		t.Fatal("size cap not enforced")
	}
	// Exact v1/v2 schemas reject new fields rather than ignoring them.
	for _, version := range []int{legacyVersion, attemptVersion} {
		d = *s.current.Load()
		d.Version = version
		if version == legacyVersion {
			d.PendingAttempt = nil
		}
		raw, err := d.encode()
		requireErr(t, err, nil)
		raw = bytes.Replace(raw, []byte(`"checkpoint":`), []byte(`"configurationRevision":1,"checkpoint":`), 1)
		if _, err := decode(raw, selected); err != ErrInvalid {
			t.Fatal("legacy input accepted a v3 field")
		}
	}
	// Record field names are required even when the JSON value would default.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "configurationRevision")
	raw, err := json.Marshal(fields)
	requireErr(t, err, nil)
	if _, err := decode(raw, selected); err != ErrInvalid {
		t.Fatal("missing revision accepted")
	}
}

func TestMixedTargetGlobalCapAndNormalReclamation(t *testing.T) {
	s, f, target := start(t)
	for n := range MaxPairs {
		if n == MaxPairs/2 {
			requireErr(t, s.EndRun(), nil)
			requireErr(t, s.Retarget(futureTarget, 1), nil)
			requireErr(t, s.BeginRun(), nil)
		}
		i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
		accept(t, s, i, sample)
		requireErr(t, s.Finalize(i.TargetID, i.DeliveryID), nil)
	}
	requireErr(t, s.EndRun(), nil)
	last := s.Snapshot().Last
	writes := f.writes
	requireErr(t, s.Retarget(target, 2), nil)
	if len(s.Inbox()) != MaxPairs || f.writes != writes+1 || s.Snapshot().Last.TargetID != futureTarget {
		t.Fatal("retarget reclaimed evidence")
	}
	requireErr(t, s.BeginRun(), nil)
	i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
	s = f.open(t)
	writes = f.writes
	for _, at := range []A.Timestamp{"2026-11-05T10:00:00Z", "2026-11-13T09:59:59Z"} {
		permit, err := s.Fence(i, at, true)
		requireErr(t, err, ErrCapacity)
		if permit != nil || f.writes != writes || len(s.Inbox()) != MaxPairs {
			t.Fatal("global cap or conservative retention violated")
		}
	}
	permit, err := s.Fence(i, "2026-11-13T10:00:00Z", true)
	requireErr(t, err, nil)
	if len(s.Inbox()) != MaxPairs {
		t.Fatal("fence reclaimed pairs before acceptance")
	}
	pair, err := permit("2026-11-13T10:00:00Z")
	requireErr(t, err, nil)
	if len(s.Inbox()) != 1 || s.Snapshot().Last.TargetID != last.TargetID || s.Snapshot().Last.DeliveryID != last.DeliveryID {
		t.Fatal("ordinary reclaim changed last evidence")
	}
	s = f.open(t)
	if got := s.Snapshot(); got.Last.TargetID != futureTarget || got.Pending.TargetID != target {
		t.Fatal("reopen lost reclaimed historical last target")
	}
	requireErr(t, s.Retarget(futureTarget, 3), nil)
	retained, err := retainedPairs(s.current.Load(), "2027-11-13T10:00:00Z")
	requireErr(t, err, nil)
	if len(retained) != 1 || retained[0].AcceptedAt != pair.AcceptedAt {
		t.Fatal("unresolved proof was not pinned")
	}
	proof, changed, err := s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, nil)
	if !changed || proof.AcceptedAt != pair.AcceptedAt || s.Snapshot().Last.TargetID != target || s.Snapshot().ConfigurationRevision != 4 {
		t.Fatal("pinned original proof did not settle after another target change")
	}
}

func TestOldTargetAttemptAcceptanceDispositionFaults(t *testing.T) {
	for _, phase := range []string{"fence", "acceptance", "disposition"} {
		for _, land := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/visible=%t", phase, land), func(t *testing.T) {
				s, f, target := start(t)
				upper := baseline
				upper.Event++
				i := prepare(t, s, complete(Human, upper, 1))
				s = f.open(t)
				requireErr(t, s.Retarget(futureTarget, 1), nil)
				if phase == "fence" {
					f.failNext, f.land = true, land
				}
				invoke, err := s.Fence(i, sample, true)
				if phase == "fence" {
					requireErr(t, err, ErrStorage)
					if invoke != nil {
						t.Fatal("uncertain fence gave permission")
					}
				} else {
					requireErr(t, err, nil)
					if phase == "acceptance" {
						f.failNext, f.land = true, land
					}
					_, err = invoke(sample)
					if phase == "acceptance" {
						requireErr(t, err, ErrUnknown)
					} else {
						requireErr(t, err, nil)
						f.failNext, f.land = true, land
						requireErr(t, s.FinalizeRecovery(i), ErrStorage)
					}
				}
				if !s.Uncertain() || s.Snapshot().TargetID != futureTarget || !s.Snapshot().Pending.exact(*i) {
					t.Fatal("fault adopted unconfirmed evidence")
				}
				if phase != "fence" && (phase == "disposition" || land) {
					proof, changed, err := s.Lookup(target, i.DeliveryID, true)
					requireErr(t, err, nil)
					if !changed || proof.TargetID != target || proof.AcceptedAt != sample || s.Snapshot().Last.TargetID != target || s.Snapshot().Checkpoint != upper {
						t.Fatal("positive original-target reconciliation failed")
					}
				}
				s = f.open(t)
				want := RecoveryNoop
				if phase == "fence" {
					want = RecoveryAttempt
					if land {
						want = RecoveryUnknown
					}
				}
				if phase == "acceptance" && !land {
					want = RecoveryUnknown
				}
				recoveryPlan(t, s, want)
				if got := s.Snapshot(); got.TargetID != futureTarget || got.ConfigurationRevision != 2 {
					t.Fatal("delivery fault changed future configuration")
				}
			})
		}
	}
}

func TestRetargetFailedConfirmationFencesSubsequentEffects(t *testing.T) {
	for _, posture := range []string{"running-idle", "running-open", "running-fenced", "stopped", "prepared"} {
		for _, failure := range []string{"invalid", "foreign", "mismatched", "missing", "read-failed"} {
			t.Run(posture+"/"+failure, func(t *testing.T) {
				s, f, target := start(t)
				var intent *Intent
				var invoke func(A.Timestamp) (*Pair, error)
				switch posture {
				case "running-open":
					requireErr(t, s.Observe(sample), nil)
				case "running-fenced", "prepared":
					intent = prepare(t, s, complete(ObservedEmpty, baseline, 0))
					if posture == "running-fenced" {
						var err error
						invoke, err = s.Fence(intent, sample, false)
						requireErr(t, err, nil)
					} else {
						s = f.open(t)
					}
				case "stopped":
					requireErr(t, s.EndRun(), nil)
				}
				prior := s.current.Load()
				proved := bytes.Clone(f.data)
				wantError := ErrUnknown
				switch failure {
				case "invalid":
					f.data = []byte(`{"version":3}`)
					wantError = ErrInvalid
				case "foreign":
					foreign := selected
					foreign.Incarnation = "87654321-1111-4111-8111-012345678908"
					d := &durable{Version: stateVersion, State: State{Binding: foreign, TargetID: target, ConfigurationRevision: 1, Checkpoint: baseline, Gaps: []Gap{}}, Inbox: []entry{}}
					var err error
					f.data, err = d.encode()
					requireErr(t, err, nil)
					wantError = ErrForeign
				case "mismatched":
					d := *prior
					d.TargetID, d.ConfigurationRevision = futureTarget, 2
					var err error
					f.data, err = d.encode()
					requireErr(t, err, nil)
				case "missing":
					f.data = nil
				case "read-failed":
					f.readErr = errors.New("synthetic bounded read failure")
					wantError = ErrStorage
				}
				unproved := bytes.Clone(f.data)
				writes := f.writes
				requested := futureTarget
				if strings.HasPrefix(posture, "running") {
					requested = target
				} // running no-op must still fence failed proof
				requireErr(t, s.Retarget(requested, 1), wantError)
				if !s.Uncertain() || !statesExact(prior, s.current.Load()) {
					t.Fatal("failed confirmation did not retain and fence confirmed facts")
				}
				f.readErr = nil // transient availability cannot silently restore effect permission
				for _, effect := range []func() error{
					s.BeginRun, func() error { return s.Observe(sample) }, func() error { _, err := s.Prepare(complete(ObservedEmpty, baseline, 0)); return err },
					func() error { return s.Retarget(futureTarget, 1) }, func() error { _, err := s.Configure(baseline); return err },
					s.ClearDirty, func() error { _, err := s.PrepareRecovery(complete(ObservedEmpty, baseline, 0)); return err },
				} {
					if err := effect(); err == nil {
						t.Fatal("unproved cached state authorized another effect")
					}
				}
				if intent != nil {
					permit, err := s.Fence(intent, sample, !s.running)
					if err == nil || permit != nil {
						t.Fatal("failed confirmation reconstructed an attempt permit")
					}
					if err := s.FinalizeRecovery(intent); err == nil {
						t.Fatal("failed confirmation authorized disposition without proof")
					}
				}
				if invoke != nil {
					pair, err := invoke(sample)
					if err == nil || pair != nil {
						t.Fatal("outstanding permit survived failed confirmation")
					}
				}
				_, _, err := s.Lookup(target, strings.Repeat("f", 64), true)
				requireErr(t, err, ErrUnknown)
				requireErr(t, s.EndRun(), ErrGuarded)
				if f.writes != writes || !bytes.Equal(f.data, unproved) || !statesExact(prior, s.current.Load()) || !s.Uncertain() {
					t.Fatal("later effects overwrote unproved bytes or cleared the guard")
				}
				// Reopen reads the actual durable result; it never repairs or
				// overwrites malformed/foreign evidence to reuse the cached state.
				actual, openErr := Open(selected, f.read, f.write)
				switch failure {
				case "invalid":
					requireErr(t, openErr, ErrInvalid)
				case "foreign":
					requireErr(t, openErr, ErrForeign)
				default:
					requireErr(t, openErr, nil)
					if failure == "missing" {
						if actual.Snapshot() != nil {
							t.Fatal("missing state reopened from cached facts")
						}
					} else if failure == "mismatched" {
						if got := actual.Snapshot(); got.TargetID != futureTarget || got.ConfigurationRevision != 2 {
							t.Fatal("reopen did not expose actual changed configuration")
						}
					} else if !statesExact(prior, actual.current.Load()) {
						t.Fatal("reopen lost actual readable proof")
					}
				}
				// Restoring readable proved bytes does not un-fence the live owner.
				f.data = proved
				requireErr(t, s.BeginRun(), ErrGuarded)
				requireErr(t, s.Retarget(futureTarget, 1), ErrGuarded)
				if f.writes != writes {
					t.Fatal("readability alone resumed cached effects")
				}
				s = f.open(t) // the host must close/drain under its native lease before this safe reopen
				if s.Uncertain() || !statesExact(prior, s.current.Load()) {
					t.Fatal("safe reopen did not load exact proved bytes")
				}
				want := RecoveryClearDirty
				switch posture {
				case "running-open":
					want = RecoveryCapture
				case "running-fenced":
					want = RecoveryUnknown
				case "stopped":
					want = RecoveryNoop
				case "prepared":
					want = RecoveryAttempt
				}
				recoveryPlan(t, s, want)
				if posture == "stopped" {
					requireErr(t, s.BeginRun(), nil)
				}
			})
		}
	}
}

func TestFailedConfirmationStillAllowsExactPositiveOnlySettlement(t *testing.T) {
	s, f, target := start(t)
	i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
	accepted := accept(t, s, i, sample)
	requireErr(t, s.EndRun(), ErrGuarded)
	proved := bytes.Clone(f.data)
	f.readErr = errors.New("synthetic bounded read failure")
	requireErr(t, s.Retarget(futureTarget, 1), ErrStorage)
	writes := f.writes
	f.readErr = nil
	_, changed, err := s.Lookup(futureTarget, i.DeliveryID, true)
	requireErr(t, err, ErrUnknown)
	if changed || !s.Uncertain() || f.writes != writes || !bytes.Equal(f.data, proved) {
		t.Fatal("wrong-target proof cleared the confirmation fence")
	}
	proof, changed, err := s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, nil)
	if !changed || s.Uncertain() || proof.AcceptedAt != accepted.AcceptedAt || proof.DeliveryID != accepted.DeliveryID ||
		s.Snapshot().TargetID != target || s.Snapshot().ConfigurationRevision != 1 || s.Snapshot().Pending != nil || len(s.Inbox()) != 1 {
		t.Fatal("exact positive-only settlement lost proof or configuration")
	}
	requireErr(t, s.BeginRun(), nil)
}
