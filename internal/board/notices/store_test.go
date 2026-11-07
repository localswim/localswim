package notices

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
)

const sample A.Timestamp = "2026-11-06T10:00:00Z"

var selected = Binding{
	RootKey: "89abcdef:0000000000000010", DatabaseKey: "89abcdef:0000000000000011",
	InstallationID: "87654321-1111-4111-8111-012345678900", BoardID: "87654321-1111-4111-8111-012345678901",
	Incarnation: "87654321-1111-4111-8111-012345678902",
}

var baseline = noticeinterval.Cut{Event: 23, Revision: 12, At: sample}

type memoryFile struct {
	data     []byte
	writes   int
	failNext bool
	land     bool
	readErr  error
}

func (f *memoryFile) read(limit int64) ([]byte, error) {
	if limit != MaxStateBytes {
		return nil, errors.New("invalid read limit")
	}
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.data == nil {
		return nil, fs.ErrNotExist
	}
	return bytes.Clone(f.data), nil
}

func (f *memoryFile) write(b Binding, data []byte) error {
	if b != selected {
		return ErrForeign
	}
	f.writes++
	if !f.failNext || f.land {
		f.data = bytes.Clone(data)
	}
	if f.failNext {
		f.failNext = false
		return errors.New("private-path/credential/card-text sentinel")
	}
	return nil
}

func (f *memoryFile) open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(selected, f.read, f.write)
	requireErr(t, err, nil)
	return s
}

func requireErr(t *testing.T, actual, expected error) {
	t.Helper()
	if !errors.Is(actual, expected) {
		t.Fatalf("error = %v, want %v", actual, expected)
	}
}

func start(t *testing.T) (*Store, *memoryFile, A.UUID) {
	t.Helper()
	f := &memoryFile{}
	s := f.open(t)
	id, err := s.Configure(baseline)
	requireErr(t, err, nil)
	requireErr(t, s.BeginRun(), nil)
	return s, f, id
}

func complete(class string, upper noticeinterval.Cut, count A.Number) noticeinterval.Evidence {
	return noticeinterval.Evidence{Upper: &upper, Class: class, Count: &count}
}

func prepare(t *testing.T, s *Store, evidence noticeinterval.Evidence) *Intent {
	t.Helper()
	requireErr(t, s.Observe(sample), nil)
	i, err := s.Prepare(evidence)
	requireErr(t, err, nil)
	return i
}

func accept(t *testing.T, s *Store, i *Intent, at A.Timestamp) *Pair {
	t.Helper()
	pair, err := s.Accept(i.TargetID, i.DeliveryID, at)
	requireErr(t, err, nil)
	return pair
}

// Accept exercises the ordinary two-step acceptance in store-only tests.
func (s *Store) Accept(target A.UUID, deliveryID string, at A.Timestamp) (*Pair, error) {
	s.mu.Lock()
	d, err := s.admitted()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if !d.pendingMatches(target, deliveryID) {
		s.mu.Unlock()
		return nil, ErrInvalid
	}
	intent := d.Pending.clone()
	s.mu.Unlock()
	accept, err := s.Fence(intent, at, false)
	if err != nil {
		return nil, err
	}
	return accept(at)
}

func TestImmutableIntentAndSanitizedProjection(t *testing.T) {
	s, f, target := start(t)
	if reused, err := s.Configure(baseline); err != nil || reused != target || f.writes != 2 {
		t.Fatal("configure did not preserve its fixed target")
	}
	requireErr(t, s.Observe(sample), nil)
	first := s.Snapshot().Observation
	const latest A.Timestamp = "2026-11-06T09:59:59Z"
	requireErr(t, s.Observe(latest), nil)
	observed := s.Snapshot().Observation
	if observed.ID != first.ID || observed.Lower != first.Lower || observed.FirstAt != sample || observed.LatestAt != latest {
		t.Fatal("coalescing replaced original observation")
	}
	upper := baseline
	upper.Event++
	upper.At = latest
	i, err := s.Prepare(complete(Human, upper, 1))
	requireErr(t, err, nil)
	repeated, err := s.Prepare(complete(Human, upper, 1))
	requireErr(t, err, nil)
	if !i.exact(*repeated) || i.DeliveryID != i.identity() {
		t.Fatal("delivery identity changed")
	}
	before := f.writes
	_, err = s.Prepare(complete(UnknownActor, upper, 1))
	requireErr(t, err, ErrInvalid)
	if f.writes != before {
		t.Fatal("same ID with different content wrote state")
	}
	i.Upper.Event++
	*i.Count = 999
	*s.Snapshot().Pending.Upper = baseline
	i = s.Snapshot().Pending
	if *i.Upper != upper || *i.Count != 1 {
		t.Fatal("private state shared mutable pointers")
	}
	changed := *i
	changed.Class = UnknownActor
	if changed.identity() != i.DeliveryID || changed.exact(*i) {
		t.Fatal("identity encoding failed to distinguish immutable content validation")
	}
	_, err = s.Accept(target, strings.Repeat("f", 64), sample)
	requireErr(t, err, ErrInvalid)
	if got := len(s.Inbox()); got != 0 {
		t.Fatalf("inbox size before acceptance = %d", got)
	}
	pair := accept(t, s, i, sample)
	if pair.TargetID != target || pair.DeliveryID != i.DeliveryID || pair.Class != Human ||
		pair.AcceptedAt != sample || pair.LowerEvent != baseline.Event || *pair.UpperEvent != upper.Event || *pair.EventCount != 1 {
		t.Fatal("receipt projection did not match immutable intent")
	}
	*pair.UpperEvent = 99
	*s.Inbox()[0].EventCount = 99
	if p := s.Inbox()[0]; *p.UpperEvent != upper.Event || *p.EventCount != 1 {
		t.Fatal("projected pair shared mutable state")
	}
	if s.Snapshot().Checkpoint != baseline || s.Snapshot().Pending == nil {
		t.Fatal("acceptance prematurely finalized")
	}
	_, err = s.Accept(target, i.DeliveryID, sample)
	requireErr(t, err, ErrGuarded)
	requireErr(t, s.Finalize(target, i.DeliveryID), nil)
	if s.Snapshot().Checkpoint != upper || s.Snapshot().Pending != nil || len(s.Inbox()) != 1 {
		t.Fatal("finalization lost or duplicated evidence")
	}
	public, err := json.Marshal(s.Inbox()[0])
	requireErr(t, err, nil)
	if len(public) > 1024 {
		t.Fatal("pair exceeds projection budget")
	}
	for _, sentinel := range []string{selected.RootKey, selected.DatabaseKey, string(selected.InstallationID),
		string(selected.BoardID), string(selected.Incarnation), string(i.ObservationID), `"binding"`, `"intent"`} {
		if bytes.Contains(public, []byte(sentinel)) {
			t.Fatal("private field in public pair")
		}
	}
	for _, sentinel := range []string{"private-path", "credential", "card-text", "rawError", "grantId", "actorId"} {
		if bytes.Contains(f.data, []byte(sentinel)) {
			t.Fatal("unapproved data in durable state")
		}
	}
}

func TestDistinctEmptyObservationsAtSameCut(t *testing.T) {
	s, _, target := start(t)
	var prior *Intent
	for range 2 {
		i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
		if prior != nil && (prior.ObservationID == i.ObservationID || prior.DeliveryID == i.DeliveryID ||
			prior.Lower != i.Lower || *prior.Upper != *i.Upper) {
			t.Fatal("empty observations did not retain distinct identities on the same cut")
		}
		accept(t, s, i, sample)
		requireErr(t, s.Finalize(target, i.DeliveryID), nil)
		prior = i
	}
	requireErr(t, s.EndRun(), nil)
	if s.Snapshot().Dirty || len(s.Inbox()) != 2 {
		t.Fatal("clean stop lost empty notices")
	}
	pairs := s.Inbox()
	if pairs[0].DeliveryID >= pairs[1].DeliveryID {
		t.Fatal("acceptance tie was not ordered deterministically")
	}
}

func TestPublicationFaultBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name    string
		phase   string
		land    bool
		pending bool
		pairs   int
	}{
		{"before-intent", "intent", false, false, 0},
		{"before-acceptance", "accept", false, true, 0},
		{"after-acceptance-before-reply", "accept", true, true, 1},
		{"after-reply-before-checkpoint", "finalize", false, true, 1},
		{"after-checkpoint-before-reply", "finalize", true, false, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, f, target := start(t)
			upper := baseline
			upper.Event++
			requireErr(t, s.Observe(sample), nil)
			f.failNext, f.land = tt.phase == "intent", tt.land
			i, err := s.Prepare(complete(Human, upper, 1))
			if tt.phase == "intent" {
				requireErr(t, err, ErrStorage)
			} else {
				requireErr(t, err, nil)
				invoke, err := s.Fence(i, sample, false)
				requireErr(t, err, nil)
				f.failNext = tt.phase == "accept"
				pair, err := invoke(sample)
				if tt.phase == "accept" {
					requireErr(t, err, ErrUnknown)
					if pair != nil {
						t.Fatal("uncertain write returned acceptance")
					}
				} else {
					requireErr(t, err, nil)
					f.failNext = true
					requireErr(t, s.Finalize(target, i.DeliveryID), ErrStorage)
				}
			}
			if !s.Uncertain() {
				t.Fatal("writer fault did not latch uncertainty")
			}
			writes := f.writes
			reopened := f.open(t)
			state := reopened.Snapshot()
			if !state.Dirty || (state.Pending != nil) != tt.pending || len(reopened.Inbox()) != tt.pairs ||
				(state.Pending != nil && !i.exact(*state.Pending)) {
				t.Fatal("durable state did not match publication boundary")
			}
			wantCut := baseline
			if tt.phase == "finalize" && tt.land {
				wantCut = upper
			}
			if state.Checkpoint != wantCut {
				t.Fatal("checkpoint moved at wrong boundary")
			}
			requireErr(t, reopened.BeginRun(), ErrGuarded)
			requireErr(t, reopened.EndRun(), ErrGuarded)
			if f.writes != writes {
				t.Fatal("guarded reopen wrote state")
			}
			if i == nil {
				return
			}
			_, err = s.Accept(target, i.DeliveryID, sample)
			requireErr(t, err, ErrGuarded)
			_, err = reopened.Accept(target, i.DeliveryID, sample)
			requireErr(t, err, ErrGuarded)
			pair, changed, err := reopened.Lookup(target, i.DeliveryID, false)
			if tt.pairs == 0 {
				requireErr(t, err, ErrUnknown)
				if pair != nil || changed || f.writes != writes {
					t.Fatal("missing evidence changed state")
				}
				return
			}
			requireErr(t, err, nil)
			if pair == nil || changed || f.writes != writes {
				t.Fatal("read-only lookup changed state or lost positive proof")
			}
			pair, changed, err = reopened.Lookup(target, i.DeliveryID, true)
			requireErr(t, err, nil)
			if pair == nil || changed != tt.pending || reopened.Snapshot().Checkpoint != upper || len(reopened.Inbox()) != 1 {
				t.Fatal("quiescent lookup did not reconcile exactly one pending receipt")
			}
			after := f.writes
			pair, changed, err = reopened.Lookup(target, i.DeliveryID, true)
			requireErr(t, err, nil)
			if pair == nil || changed || f.writes != after {
				t.Fatal("lookup of finalized receipt changed state")
			}
		})
	}
}

func TestLookupRequiresExactFreshProof(t *testing.T) {
	s, f, target := start(t)
	i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
	accept(t, s, i, sample)
	good := bytes.Clone(f.data)
	writes := f.writes
	requireErr(t, s.EndRun(), ErrGuarded)
	foreign := selected
	foreign.DatabaseKey = "89abcdef:0000000000000012"
	_, err := Open(foreign, f.read, f.write)
	requireErr(t, err, ErrForeign)
	wrongTarget := A.UUID("87654321-1111-4111-8111-012345678903")
	for _, key := range []struct {
		target   A.UUID
		delivery string
	}{{wrongTarget, i.DeliveryID}, {target, strings.Repeat("f", 64)}, {target, "../private-path"}} {
		p, changed, err := s.Lookup(key.target, key.delivery, true)
		requireErr(t, err, ErrUnknown)
		if p != nil || changed || f.writes != writes {
			t.Fatal("incorrect lookup caused state change")
		}
	}
	for _, bad := range [][]byte{
		nil,
		[]byte(`{"version":1,"version":1}`),
		bytes.Replace(good, []byte(`"dirty":true`), []byte(`"dirty":true,"dirty":true`), 1),
		bytes.Replace(good, []byte(`"dirty":true`), []byte(`"unknown":true`), 1),
		bytes.Replace(good, []byte(`,"floor":""`), nil, 1),
		bytes.Replace(good, []byte(selected.DatabaseKey), []byte(foreign.DatabaseKey), 1),
		bytes.Replace(good, []byte(`"count":0`), []byte(`"count":1`), 1),
		make([]byte, MaxStateBytes+1),
	} {
		f.data = bad
		for _, quiescent := range []bool{false, true} {
			pair, changed, err := s.Lookup(target, i.DeliveryID, quiescent)
			requireErr(t, err, ErrUnknown)
			if pair != nil || changed || f.writes != writes || s.Snapshot().Pending == nil {
				t.Fatal("unverified bytes exposed proof or caused settlement")
			}
		}
	}
	f.data = good
	f.readErr = errors.New("private-path/credential/card-text sentinel")
	_, _, err = s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, ErrUnknown)
	f.readErr = nil
	f.failNext = true
	pair, changed, err := s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, ErrStorage)
	if pair == nil || changed || !s.Uncertain() || s.Snapshot().Pending == nil {
		t.Fatal("failed settlement obscured acceptance or lost guard")
	}
	pair, changed, err = s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, nil)
	if pair == nil || !changed || s.Uncertain() || s.Snapshot().Dirty || s.Snapshot().Pending != nil {
		t.Fatal("fresh matching proof failed to clear resolved guard")
	}
	requireErr(t, s.BeginRun(), nil)
}

func TestQuiescentLookupOfUnconfirmedAcceptance(t *testing.T) {
	for _, lands := range []bool{false, true} {
		name := "settlement-before-publication"
		if lands {
			name = "settlement-after-publication"
		}
		t.Run(name, func(t *testing.T) {
			s, f, target := start(t)
			i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
			invoke, err := s.Fence(i, sample, false)
			requireErr(t, err, nil)
			f.failNext, f.land = true, true
			_, err = invoke(sample)
			requireErr(t, err, ErrUnknown)
			requireErr(t, s.EndRun(), ErrGuarded)
			f.failNext, f.land = true, lands
			p, changed, err := s.Lookup(target, i.DeliveryID, true)
			requireErr(t, err, ErrUnknown)
			if p != nil || changed || s.Snapshot().Pending == nil || !s.Uncertain() {
				t.Fatal("unconfirmed acceptance and settlement exposed positive proof")
			}
			if lands {
				f.failNext = true
				p, changed, err = s.Lookup(target, i.DeliveryID, true)
				requireErr(t, err, ErrUnknown)
				if p != nil || changed {
					t.Fatal("visible but unconfirmed settlement exposed positive proof")
				}
			}
			p, changed, err = s.Lookup(target, i.DeliveryID, true)
			requireErr(t, err, nil)
			if p == nil || !changed || s.Snapshot().Pending != nil || s.Snapshot().Dirty || len(s.Inbox()) != 1 {
				t.Fatal("durable recovery lost the original exact receipt")
			}
		})
	}
}

func TestQuiescentLookupConfirmsCheckpointAfterLostWriteResult(t *testing.T) {
	s, f, target := start(t)
	upper := baseline
	upper.Event++
	i := prepare(t, s, complete(Human, upper, 1))
	accepted, err := s.Accept(target, i.DeliveryID, sample)
	requireErr(t, err, nil)
	f.failNext, f.land = true, true
	requireErr(t, s.Finalize(target, i.DeliveryID), ErrStorage)
	requireErr(t, s.EndRun(), ErrGuarded)

	p, changed, err := s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, nil)
	state := s.Snapshot()
	if !changed || p == nil || p.DeliveryID != accepted.DeliveryID || p.AcceptedAt != accepted.AcceptedAt ||
		state.Dirty || state.Pending != nil || state.Checkpoint != upper || s.Uncertain() || len(s.Inbox()) != 1 {
		t.Fatal("exact lookup did not confirm the original checkpoint and receipt")
	}
	writes := f.writes
	_, changed, err = s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, nil)
	if changed || f.writes != writes {
		t.Fatal("finalized lookup rewrote the notice state")
	}
	requireErr(t, s.BeginRun(), nil)
}

func TestCapacityRetentionAndPinnedAcceptance(t *testing.T) {
	t.Run("fresh-capacity", func(t *testing.T) {
		s, f, target := start(t)
		for range MaxPairs {
			i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
			accept(t, s, i, sample)
			requireErr(t, s.Finalize(target, i.DeliveryID), nil)
		}
		pending := prepare(t, s, complete(ObservedEmpty, baseline, 0))
		writes := f.writes
		_, err := s.Accept(target, pending.DeliveryID, "2026-11-05T10:00:00Z")
		requireErr(t, err, ErrCapacity)
		_, err = s.Accept(target, pending.DeliveryID, "2026-11-20T10:00:00Z")
		requireErr(t, err, ErrGuarded)
		if len(s.Inbox()) != MaxPairs || f.writes != writes || !s.Snapshot().Pending.exact(*pending) {
			t.Fatal("capacity changed retained pairs or pending")
		}
	})
	t.Run("expiry-and-pin", func(t *testing.T) {
		s, f, target := start(t)
		first := prepare(t, s, complete(ObservedEmpty, baseline, 0))
		accept(t, s, first, sample)
		requireErr(t, s.Finalize(target, first.DeliveryID), nil)
		second := prepare(t, s, complete(ObservedEmpty, baseline, 0))
		accept(t, s, second, "2026-11-13T09:59:59Z")
		if len(s.Inbox()) != 2 {
			t.Fatal("finalized pair reclaimed before seven days")
		}
		requireErr(t, s.Finalize(target, second.DeliveryID), nil)
		third := prepare(t, s, complete(ObservedEmpty, baseline, 0))
		accept(t, s, third, "2026-11-13T10:00:00Z")
		if len(s.Inbox()) != 2 {
			t.Fatal("expired finalized pair was not reclaimed")
		}
		reopened := f.open(t)
		_, _, err := reopened.Lookup(target, first.DeliveryID, true)
		requireErr(t, err, ErrUnknown)
		// Lookup has no clock or prune path. The unresolved pair remains
		// pinned even if its recorded acceptance predates every other pair.
		d, err := decode(f.data, selected)
		requireErr(t, err, nil)
		d.Inbox[len(d.Inbox)-1].AcceptedAt = sample
		f.data, err = d.encode()
		requireErr(t, err, nil)
		reopened = f.open(t)
		pair, changed, err := reopened.Lookup(target, third.DeliveryID, true)
		requireErr(t, err, nil)
		if pair == nil || pair.AcceptedAt != sample || !changed || len(reopened.Inbox()) != 2 {
			t.Fatal("unresolved accepted pair expired or changed identity")
		}
	})
}

func TestGapCapacityUnknownUpperAndSuppression(t *testing.T) {
	t.Run("known-and-capacity", func(t *testing.T) {
		s, f, target := start(t)
		upper := baseline
		for range MaxGaps {
			upper.Gap++
			i := prepare(t, s, noticeinterval.Evidence{Upper: &upper, Class: Gapped})
			accept(t, s, i, sample)
			requireErr(t, s.Finalize(target, i.DeliveryID), nil)
		}
		if s.Snapshot().Checkpoint != upper {
			t.Fatal("equal event cut failed to retain full coverage")
		}
		upper.Gap++
		requireErr(t, s.Observe(sample), nil)
		before := s.Snapshot().Observation
		writes := f.writes
		_, err := s.Prepare(noticeinterval.Evidence{Upper: &upper, Class: Gapped})
		requireErr(t, err, ErrCapacity)
		state := s.Snapshot()
		if len(state.Gaps) != MaxGaps || f.writes != writes || *state.Observation != *before || state.Pending != nil {
			t.Fatal("gap capacity displaced unresolved evidence")
		}
		reopened := f.open(t)
		recoveryPlan(t, reopened, RecoveryCapture)
		_, err = reopened.PrepareRecovery(noticeinterval.Evidence{Upper: &upper, Class: Gapped})
		requireErr(t, err, ErrCapacity)
		if after := reopened.Snapshot(); f.writes != writes || *after.Observation != *before ||
			len(after.Gaps) != MaxGaps || after.Pending != nil || after.Checkpoint != state.Checkpoint {
			t.Fatal("recovery gap capacity changed unresolved evidence")
		}
	})
	t.Run("unknown-and-suppression", func(t *testing.T) {
		s, f, target := start(t)
		upper := baseline
		upper.Event++
		requireErr(t, s.Observe(sample), nil)
		requireErr(t, s.Suppress(upper), nil)
		state := s.Snapshot()
		if state.Checkpoint != upper || state.Last.Class != Suppressed || state.Observation != nil || len(s.Inbox()) != 0 {
			t.Fatal("suppression failed to advance without acceptance")
		}
		i := prepare(t, s, noticeinterval.Evidence{Class: Unavailable})
		pair := accept(t, s, i, sample)
		if pair.UpperEvent != nil || pair.EventCount != nil {
			t.Fatal("unknown upper projection invented evidence")
		}
		requireErr(t, s.Finalize(target, i.DeliveryID), nil)
		requireErr(t, s.EndRun(), ErrGuarded)
		reopened := f.open(t)
		state = reopened.Snapshot()
		if state.Checkpoint != upper || !state.Dirty || len(state.Gaps) != 1 || state.Gaps[0].Upper != nil || state.Pending != nil {
			t.Fatal("unknown upper advanced or lost durable guard")
		}
		requireErr(t, reopened.BeginRun(), ErrGuarded)
	})
}

func TestSnapshotAvailableDuringWrite(t *testing.T) {
	f := &memoryFile{}
	entered, release := make(chan struct{}), make(chan struct{})
	s, err := Open(selected, f.read, func(b Binding, data []byte) error {
		if f.writes == 1 {
			close(entered)
			<-release
		}
		return f.write(b, data)
	})
	requireErr(t, err, nil)
	_, err = s.Configure(baseline)
	requireErr(t, err, nil)
	started := make(chan error, 1)
	go func() { started <- s.BeginRun() }()
	<-entered
	snapshot := make(chan *State, 1)
	go func() { snapshot <- s.Snapshot() }()
	select {
	case state := <-snapshot:
		close(release)
		if state.Dirty || state.Checkpoint != baseline {
			t.Fatal("snapshot included unconfirmed publication")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("snapshot waited on native writer")
	}
	requireErr(t, <-started, nil)
	if !s.Snapshot().Dirty {
		t.Fatal("snapshot did not advance after publication")
	}
}

func TestActiveLookupDuringUnconfirmedAcceptance(t *testing.T) {
	f := &memoryFile{}
	entered, release := make(chan struct{}), make(chan struct{})
	s, err := Open(selected, f.read, func(b Binding, data []byte) error {
		err := f.write(b, data)
		if f.writes == 6 {
			// The bytes are visible to a concurrent reader before the
			// host confirms its write-through acceptance boundary.
			close(entered)
			<-release
		}
		return err
	})
	requireErr(t, err, nil)
	target, err := s.Configure(baseline)
	requireErr(t, err, nil)
	requireErr(t, s.BeginRun(), nil)
	i := prepare(t, s, complete(ObservedEmpty, baseline, 0))
	accepted := make(chan error, 1)
	go func() {
		_, err := s.Accept(target, i.DeliveryID, sample)
		accepted <- err
	}()
	<-entered
	type lookupResult struct {
		p       *Pair
		changed bool
		err     error
	}
	result := make(chan lookupResult, 1)
	go func() {
		p, changed, err := s.Lookup(target, i.DeliveryID, false)
		result <- lookupResult{p, changed, err}
	}()
	select {
	case r := <-result:
		close(release)
		requireErr(t, r.err, ErrUnknown)
		if r.p != nil || r.changed {
			t.Fatal("active lookup exposed acceptance before durable confirmation")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("active lookup waited for writer")
	}
	requireErr(t, <-accepted, nil)
	p, changed, err := s.Lookup(target, i.DeliveryID, false)
	requireErr(t, err, nil)
	if p == nil || changed || s.Snapshot().Pending == nil {
		t.Fatal("active lookup failed to report confirmed receipt read-only")
	}
}

func TestAbandonRunRetainsEveryDurableFact(t *testing.T) {
	for _, stage := range []string{"idle", "observation", "prepared", "attempted", "accepted"} {
		t.Run(stage, func(t *testing.T) {
			s, f, _ := start(t)
			var intent *Intent
			if stage != "idle" {
				upper := baseline
				upper.Event++
				requireErr(t, s.Observe(sample), nil)
				if stage != "observation" {
					var err error
					intent, err = s.Prepare(complete(Human, upper, 1))
					requireErr(t, err, nil)
					if stage == "attempted" || stage == "accepted" {
						attempt, err := s.Fence(intent, sample, false)
						requireErr(t, err, nil)
						if stage == "accepted" {
							_, err = attempt(sample)
							requireErr(t, err, nil)
						}
					}
				}
			}
			original, writes := bytes.Clone(f.data), f.writes
			s.AbandonRun()
			if s.running || !bytes.Equal(original, f.data) || f.writes != writes || !s.Snapshot().Dirty {
				t.Fatal("runtime release changed dirty evidence")
			}
			requireErr(t, s.BeginRun(), ErrGuarded)
			if intent != nil && s.Snapshot().Pending.DeliveryID != intent.DeliveryID {
				t.Fatal("runtime release lost original pending")
			}
			requireErr(t, s.Retarget("20000000-0000-4000-8000-000000000099", 1), nil)
			if !s.Snapshot().Dirty || intent != nil && s.Snapshot().Pending.TargetID != intent.TargetID {
				t.Fatal("future-only configuration lent old attempt authority")
			}
		})
	}
}
