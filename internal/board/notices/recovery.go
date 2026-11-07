package notices

import (
	"bytes"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
)

// RecoveryAction identifies the one transition allowed by confirmed evidence.
// The host checks current authority and lineage and serializes that transition.
type RecoveryAction uint8

const (
	RecoveryNoop RecoveryAction = iota
	RecoveryFinalize
	RecoveryAttempt
	RecoveryCapture
	RecoveryClearDirty
	RecoveryUnknown
	RecoveryUnavailable
)

// confirmed rereads the sidecar and requires the exact last confirmed state.
// A mutation never adopts bytes from an unconfirmed or unexpected write.
// Every caller holds the writer mutex. Once confirmation fails, cached facts
// cannot authorize later effects, even if a transient read fault disappears.
// Safe reopen or the existing exact positive-only Lookup may resolve the guard.
func (s *Store) confirmed() (*durable, error) {
	if s.Uncertain() {
		return nil, ErrUnknown
	}
	observed, err := s.load()
	if err != nil {
		s.fault.Store(true)
		return nil, err
	}
	current := s.current.Load()
	if !statesExact(current, observed) {
		s.fault.Store(true)
		return nil, ErrUnknown
	}
	return current, nil
}

func statesExact(want, got *durable) bool {
	if want == nil || got == nil {
		return want == got
	}
	expected, wantErr := want.encode()
	observed, gotErr := got.encode()
	return wantErr == nil && gotErr == nil && bytes.Equal(expected, observed)
}

func (d *durable) recoveryAction() RecoveryAction {
	switch {
	case d == nil || !d.Dirty:
		return RecoveryNoop
	case d.Pending != nil:
		if d.find(d.Pending.TargetID, d.Pending.DeliveryID) >= 0 {
			return RecoveryFinalize
		}
		if d.Version != legacyVersion && *d.PendingAttempt == prepared {
			return RecoveryAttempt
		}
		return RecoveryUnknown
	case d.Last != nil && d.Last.Upper == nil:
		return RecoveryUnavailable
	case d.Observation != nil:
		return RecoveryCapture
	default:
		return RecoveryClearDirty
	}
}

func (s *Store) recoveryState() (*durable, error) {
	if s.running {
		return nil, ErrGuarded
	}
	return s.confirmed()
}

// RecoveryPlan performs a fresh read without mutating the sidecar.
func (s *Store) RecoveryPlan() (RecoveryAction, *State, error) {
	if s == nil {
		return RecoveryUnknown, nil, ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.recoveryState()
	if err != nil || d == nil {
		if err != nil {
			return RecoveryUnknown, nil, err
		}
		return RecoveryNoop, nil, nil
	}
	return d.recoveryAction(), d.snapshot(), nil
}

func (s *Store) PrepareRecovery(evidence noticeinterval.Evidence) (*Intent, error) {
	if s == nil {
		return nil, ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.recoveryState()
	if err != nil {
		return nil, err
	}
	if d.recoveryAction() != RecoveryCapture {
		return nil, ErrGuarded
	}
	return s.prepare(d, evidence)
}

func (s *Store) SuppressRecovery(upper noticeinterval.Cut) error {
	if s == nil {
		return ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.recoveryState()
	if err != nil {
		return err
	}
	if d.recoveryAction() != RecoveryCapture {
		return ErrGuarded
	}
	if err := s.suppress(d, upper, true); err != nil {
		return err
	}
	s.clearGuard()
	return nil
}

func (s *Store) ClearDirty() error {
	if s == nil {
		return ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.recoveryState()
	if err != nil {
		return err
	}
	if d.recoveryAction() != RecoveryClearDirty {
		return ErrGuarded
	}
	next := *d
	next.Dirty = false
	if err := s.publish(&next); err != nil {
		return err
	}
	s.clearGuard()
	return nil
}

func (s *Store) attemptState(recovery bool) (*durable, error) {
	if recovery {
		return s.recoveryState()
	}
	if _, err := s.admitted(); err != nil {
		return nil, err
	}
	return s.confirmed()
}

// Fence checks capacity before publishing the attempted marker. The private
// permit is single use and belongs to the host's current reservation or worker
// lease. Drop it if acceptance admission is denied; it cannot be recreated.
func (s *Store) Fence(intent *Intent, at A.Timestamp, recovery bool) (func(A.Timestamp) (*Pair, error), error) {
	if s == nil || intent == nil {
		return nil, ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.attemptState(recovery)
	if err != nil {
		return nil, err
	}
	if d.recoveryAction() != RecoveryAttempt || !d.Pending.exact(*intent) {
		return nil, ErrGuarded
	}
	retained, err := retainedPairs(d, at)
	if err != nil {
		if err == ErrCapacity && !recovery {
			s.refused = true
		}
		return nil, err
	}
	next := *d
	marker := attempted
	next.PendingAttempt = &marker
	// Prove the combined acceptance fits before consuming the one-use attempt.
	// Expiry may reclaim only the existing retention policy's eligible pairs.
	prospective := next
	prospective.Inbox = append(retained, entry{Intent: *d.Pending, AcceptedAt: at})
	if _, err := prospective.encode(); err != nil {
		if err == ErrCapacity && !recovery {
			s.refused = true
		}
		return nil, err
	}
	if err := s.publish(&next); err != nil {
		return nil, err
	}
	return s.acceptancePermit(&next, recovery), nil
}

func (s *Store) acceptancePermit(fenced *durable, recovery bool) func(A.Timestamp) (*Pair, error) {
	used := false
	return func(at A.Timestamp) (*Pair, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if used {
			return nil, ErrGuarded
		}
		used = true
		d, err := s.attemptState(recovery)
		if err != nil {
			return nil, err
		}
		if d != fenced {
			return nil, ErrUnknown
		}
		retained, err := retainedPairs(d, at)
		if err != nil {
			return nil, err
		}
		e := entry{Intent: *fenced.Pending, AcceptedAt: at}
		next := *fenced
		next.Inbox = append(retained, e)
		if err := s.publish(&next); err != nil {
			if err == ErrForeign {
				return nil, err
			}
			if s.Uncertain() {
				// Pin the exact acceptance publication, not permission to
				// attempt it again. Lookup still requires durable settlement.
				s.acceptance = &next
				return nil, ErrUnknown
			}
			return nil, err
		}
		pair := e.project()
		return &pair, nil
	}
}

// FinalizeRecovery only disposes the exact, freshly confirmed durable receipt.
// A null upper remains guarded even though the accepted receipt is finalized.
func (s *Store) FinalizeRecovery(intent *Intent) error {
	if s == nil || intent == nil {
		return ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.recoveryState()
	if err != nil {
		return err
	}
	if d.recoveryAction() != RecoveryFinalize || !d.Pending.exact(*intent) {
		return ErrUnknown
	}
	if err := s.finalize(d, d.find(intent.TargetID, intent.DeliveryID), true); err != nil {
		return err
	}
	s.clearGuard()
	return nil
}
