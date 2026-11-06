package notices

import (
	"bytes"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
)

var (
	ErrStorage  = errors.New("notice state storage unavailable")
	ErrInvalid  = errors.New("invalid notice state or input")
	ErrForeign  = errors.New("foreign notice scope")
	ErrGuarded  = errors.New("notice state guarded")
	ErrCapacity = errors.New("notice capacity reached")
	ErrUnknown  = errors.New("notice acceptance unknown")
)

// Store publishes immutable snapshots independently of its serialized writer.
// Only the host holding the native board lease may construct or use a Store.
type Store struct {
	binding Binding
	read    func(limit int64) ([]byte, error)
	write   func(binding Binding, data []byte) error
	current atomic.Pointer[durable]
	fault   atomic.Bool

	mu      sync.Mutex
	running bool
	refused bool
	// settlement retains the one exact pending proof a quiescent lookup used
	// when its disposition publication returned an uncertain result.
	settlement *durable
}

// Open reads the pinned board's state without mutating it. The host callbacks
// recheck physical lineage, refuse unvalidated files, and enforce the read
// limit and durable atomic publication. Only fs.ErrNotExist means unconfigured.
func Open(binding Binding, read func(limit int64) ([]byte, error), write func(Binding, []byte) error) (*Store, error) {
	if !binding.valid() {
		return nil, ErrForeign
	}
	if read == nil || write == nil {
		return nil, ErrStorage
	}
	s := &Store{binding: binding, read: read, write: write}
	d, err := s.load()
	if err != nil {
		return nil, err
	}
	s.current.Store(d)
	return s, nil
}

func (s *Store) load() (*durable, error) {
	data, err := s.read(MaxStateBytes)
	switch {
	case errors.Is(err, ErrForeign):
		return nil, ErrForeign
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, ErrStorage
	default:
		return decode(data, s.binding)
	}
}

func (s *Store) Snapshot() *State {
	if s != nil {
		if d := s.current.Load(); d != nil {
			return d.snapshot()
		}
	}
	return nil
}

func (s *Store) Uncertain() bool { return s == nil || s.fault.Load() }

func (s *Store) publish(next *durable) error {
	if next.Binding != s.binding {
		return ErrForeign
	}
	encoded, err := next.encode()
	if err != nil {
		return err
	}
	if err = s.write(s.binding, encoded); err != nil {
		s.fault.Store(true)
		if errors.Is(err, ErrForeign) {
			return ErrForeign
		}
		return ErrStorage
	}
	s.current.Store(next)
	return nil
}

func allocateID() (A.UUID, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", ErrStorage
	}
	return A.UUID(id.String()), nil
}

func (s *Store) Configure(baseline noticeinterval.Cut) (A.UUID, error) {
	if s == nil {
		return "", ErrStorage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Uncertain() {
		return "", ErrGuarded
	}
	if !baseline.Valid() {
		return "", ErrInvalid
	}
	if d := s.current.Load(); d != nil {
		if !baseline.Follows(d.Checkpoint) {
			return "", ErrForeign
		}
		return d.TargetID, nil
	}
	id, err := allocateID()
	if err != nil {
		return "", err
	}
	next := &durable{Version: version, State: State{Binding: s.binding, TargetID: id, Checkpoint: baseline, Gaps: []Gap{}}, Inbox: []entry{}}
	if err := s.publish(next); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) BeginRun() error {
	if s == nil {
		return ErrStorage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.current.Load()
	if s.Uncertain() || s.running || d == nil || d.Dirty || d.Observation != nil || d.Pending != nil {
		return ErrGuarded
	}
	next := *d
	next.Dirty = true
	if err := s.publish(&next); err != nil {
		return err
	}
	s.running = true
	return nil
}

// EndRun is called after the worker and admitted effects are quiescent.
func (s *Store) EndRun() error {
	if s == nil {
		return ErrStorage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.current.Load()
	wasRunning := s.running
	s.running = false
	if s.Uncertain() || d == nil || d.Observation != nil || d.Pending != nil ||
		(d.Last != nil && d.Last.Upper == nil) || (d.Dirty && !wasRunning) {
		return ErrGuarded
	}
	if !d.Dirty {
		return nil
	}
	next := *d
	next.Dirty = false
	return s.publish(&next)
}

func (s *Store) admitted() (*durable, error) {
	d := s.current.Load()
	if !s.running || s.Uncertain() || s.refused || d == nil || !d.Dirty ||
		(d.Last != nil && d.Last.Upper == nil) {
		return nil, ErrGuarded
	}
	return d, nil
}

// Observe records an admitted new signal, preserving its first cut and ID when
// coalescing. The worker determines whether a signal is genuinely new.
func (s *Store) Observe(at A.Timestamp) error {
	if s == nil {
		return ErrStorage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.admitted()
	if err != nil {
		return err
	}
	if A.Validate(at) != nil || d.Pending != nil {
		return ErrInvalid
	}
	next := *d
	if d.Observation != nil {
		if d.Observation.LatestAt == at {
			return nil
		}
		next.Observation = copyValue(d.Observation)
		next.Observation.LatestAt = at
	} else {
		id, err := allocateID()
		if err != nil {
			return err
		}
		next.Observation = &Observation{id, d.Checkpoint, at, at}
	}
	return s.publish(&next)
}

func (s *Store) Suppress(upper noticeinterval.Cut) error {
	if s == nil {
		return ErrStorage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.admitted()
	if err != nil {
		return err
	}
	if d.Observation == nil || d.Pending != nil || !upper.Follows(d.Checkpoint) || upper.Event <= d.Checkpoint.Event {
		return ErrInvalid
	}
	count := upper.Event - d.Checkpoint.Event
	next := *d
	next.Last = &Outcome{d.Observation.ID, d.Checkpoint, &upper, Suppressed, &count, ""}
	next.Checkpoint, next.Observation = upper, nil
	return s.publish(&next)
}

// Prepare freezes both the intent and any applicable gap before acceptance.
func (s *Store) Prepare(evidence noticeinterval.Evidence) (*Intent, error) {
	if s == nil {
		return nil, ErrStorage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.admitted()
	if err != nil {
		return nil, err
	}
	if d.Observation == nil || evidence.Class == Suppressed || !intervalValid(evidence.Class, d.Checkpoint, evidence.Upper, evidence.Count) {
		return nil, ErrInvalid
	}
	i := Intent{Version: version, Binding: d.Binding, ObservationID: d.Observation.ID, Lower: d.Checkpoint,
		Upper: copyValue(evidence.Upper), TargetID: d.TargetID, Class: evidence.Class, Count: copyValue(evidence.Count)}
	i.DeliveryID = i.identity()
	if d.Pending != nil {
		if !i.exact(*d.Pending) {
			return nil, ErrInvalid
		}
		return i.clone(), nil
	}
	next := *d
	next.Pending = &i
	if isLoss(i.Class) {
		if len(d.Gaps) == MaxGaps {
			s.refused = true
			return nil, ErrCapacity
		}
		next.Gaps = append(slices.Clone(d.Gaps), Gap{i.ObservationID, i.Lower, i.Upper, i.Class})
	}
	if err := s.publish(&next); err != nil {
		return nil, err
	}
	return i.clone(), nil
}

func (d *durable) pendingMatches(target A.UUID, deliveryID string) bool {
	return d.Pending != nil && d.Pending.TargetID == target && d.Pending.DeliveryID == deliveryID
}

func (d *durable) find(target A.UUID, deliveryID string) int {
	if target == d.TargetID {
		for idx, e := range d.Inbox {
			if e.Intent.DeliveryID == deliveryID {
				return idx
			}
		}
	}
	return -1
}

func (s *Store) Accept(target A.UUID, deliveryID string, at A.Timestamp) (*Pair, error) {
	if s == nil {
		return nil, ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.admitted()
	if err != nil {
		return nil, err
	}
	if !d.pendingMatches(target, deliveryID) || A.Validate(at) != nil {
		return nil, ErrInvalid
	}
	if d.find(target, deliveryID) >= 0 {
		return nil, ErrGuarded
	}
	now, err := time.Parse(time.RFC3339, string(at))
	if err != nil {
		return nil, ErrInvalid
	}
	cutoff := now.Add(-7 * 24 * time.Hour)
	retained := make([]entry, 0, len(d.Inbox))
	for _, e := range d.Inbox {
		accepted, err := time.Parse(time.RFC3339, string(e.AcceptedAt))
		if err != nil {
			return nil, ErrInvalid
		}
		if !e.Finalized || accepted.After(cutoff) {
			retained = append(retained, e)
		}
	}
	if len(retained) == MaxPairs {
		s.refused = true
		return nil, ErrCapacity
	}
	e := entry{Intent: *d.Pending, AcceptedAt: at}
	next := *d
	next.Inbox = append(retained, e)
	if err := s.publish(&next); err != nil {
		if s.Uncertain() {
			return nil, ErrUnknown
		}
		return nil, err
	}
	pair := e.project()
	return &pair, nil
}

func finalizedState(d *durable, idx int, quiescent bool) (*durable, error) {
	e := d.Inbox[idx]
	if e.Finalized || d.Pending == nil || !e.Intent.exact(*d.Pending) {
		return nil, ErrUnknown
	}
	next := *d
	next.Inbox = slices.Clone(d.Inbox)
	next.Inbox[idx].Finalized = true
	outcome := e.Intent.outcome()
	next.Observation, next.Pending, next.Last = nil, nil, &outcome
	if outcome.Upper != nil {
		next.Checkpoint = *outcome.Upper
		next.Dirty = !quiescent
	}
	return &next, nil
}

func (s *Store) finalize(d *durable, idx int, quiescent bool) error {
	next, err := finalizedState(d, idx, quiescent)
	if err != nil {
		return err
	}
	return s.publish(next)
}

// confirmFinalization settles an ambiguous checkpoint write for an already
// confirmed acceptance. Only the exact expected disposition can be adopted.
func (s *Store) confirmFinalization(current, observed *durable, idx int) error {
	expected, err := finalizedState(current, idx, !observed.Dirty)
	if err != nil {
		return err
	}
	want, wantErr := expected.encode()
	got, gotErr := observed.encode()
	if wantErr != nil || gotErr != nil || !bytes.Equal(want, got) {
		return ErrUnknown
	}
	if expected.Last.Upper != nil {
		expected.Dirty = false
	}
	return s.publish(expected)
}

func (s *Store) Finalize(target A.UUID, deliveryID string) error {
	if s == nil {
		return ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.admitted()
	if err != nil {
		return err
	}
	if idx := d.find(target, deliveryID); idx >= 0 && d.pendingMatches(target, deliveryID) {
		return s.finalize(d, idx, false)
	}
	return ErrUnknown
}

// Lookup verifies fresh durable proof and only returns a previously confirmed
// receipt during a run. A host admitting quiescent lookup holds its lifecycle
// reservation so a worker or control cannot race disposition publication.
func (s *Store) Lookup(target A.UUID, deliveryID string, quiescent bool) (*Pair, bool, error) {
	if s == nil || !validUUID4(target) || A.Validate(A.Digest(deliveryID)) != nil {
		return nil, false, ErrUnknown
	}
	if quiescent {
		s.mu.Lock()
	} else if !s.mu.TryLock() {
		return nil, false, ErrUnknown
	}
	defer s.mu.Unlock()
	current := s.current.Load()
	d, err := s.load()
	if err != nil || d == nil || current == nil {
		return nil, false, ErrUnknown
	}
	idx := d.find(target, deliveryID)
	if idx < 0 {
		return nil, false, ErrUnknown
	}
	e := d.Inbox[idx]
	confirmed := false
	priorIdx := current.find(target, deliveryID)
	if priorIdx >= 0 {
		prior := current.Inbox[priorIdx]
		if prior.AcceptedAt != e.AcceptedAt || !prior.Intent.exact(e.Intent) {
			return nil, false, ErrUnknown
		}
		confirmed = true
	}
	pair := e.project()
	if !quiescent {
		if !confirmed {
			return nil, false, ErrUnknown
		}
		return &pair, false, nil
	}
	if e.Finalized {
		return s.lookupFinalized(current, d, priorIdx, e)
	}
	if current.Pending == nil || d.Pending == nil || !current.Pending.exact(*d.Pending) {
		return nil, false, ErrUnknown
	}
	s.running = false
	s.settlement = d
	if err := s.finalize(d, idx, true); err != nil {
		// When acceptance publication itself was ambiguous, visible bytes
		// alone do not establish a newly accepted effect in this engine.
		if !confirmed {
			return nil, false, ErrUnknown
		}
		return &pair, false, err
	}
	s.clearGuard()
	return &pair, true, nil
}

func (s *Store) lookupFinalized(current, observed *durable, priorIdx int, e entry) (*Pair, bool, error) {
	pair := e.project()
	confirmed := priorIdx >= 0
	if confirmed && current.Inbox[priorIdx].Finalized {
		return &pair, false, nil
	}
	proof := current
	if !confirmed {
		proof = s.settlement
		if proof == nil || proof.Pending == nil || current.Pending == nil || !proof.Pending.exact(*current.Pending) {
			return nil, false, ErrUnknown
		}
		priorIdx = proof.find(e.Intent.TargetID, e.Intent.DeliveryID)
		if priorIdx < 0 || proof.Inbox[priorIdx].AcceptedAt != e.AcceptedAt || !proof.Inbox[priorIdx].Intent.exact(e.Intent) {
			return nil, false, ErrUnknown
		}
	}
	if err := s.confirmFinalization(proof, observed, priorIdx); err != nil {
		if !confirmed || errors.Is(err, ErrUnknown) {
			return nil, false, ErrUnknown
		}
		return &pair, false, err
	}
	s.clearGuard()
	return &pair, true, nil
}

func (s *Store) clearGuard() {
	s.running = false
	s.fault.Store(false)
	s.refused = false
	s.settlement = nil
}

func (s *Store) Inbox() []Pair {
	pairs := []Pair{}
	if s != nil {
		if d := s.current.Load(); d != nil {
			pairs = make([]Pair, 0, len(d.Inbox))
			for _, e := range d.Inbox {
				pairs = append(pairs, e.project())
			}
		}
	}
	slices.SortFunc(pairs, func(a, b Pair) int {
		if a.AcceptedAt != b.AcceptedAt {
			return strings.Compare(string(b.AcceptedAt), string(a.AcceptedAt))
		}
		return strings.Compare(a.DeliveryID, b.DeliveryID)
	})
	return pairs
}
