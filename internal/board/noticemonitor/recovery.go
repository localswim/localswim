package noticemonitor

import (
	"context"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	"github.com/localswim/localswim/internal/board/notices"
)

// recoveryCut proves current lineage through a read that validates coverage,
// including when ordinary notice status may tolerate unavailable metadata.
func (m *Monitor) recoveryCut(ctx context.Context, auth Authority, lower noticeinterval.Cut) (noticeinterval.Cut, error) {
	cut, err := m.source.Observe(ctx, auth)
	if err != nil {
		return noticeinterval.Cut{}, err
	}
	if !cut.Valid() {
		return noticeinterval.Cut{}, ErrUnavailable
	}
	if !cut.Follows(lower) {
		return noticeinterval.Cut{}, notices.ErrForeign
	}
	return cut, nil
}

func pendingBound(intent *notices.Intent) noticeinterval.Cut {
	if intent.Upper != nil {
		return *intent.Upper
	}
	return intent.Lower
}

func (m *Monitor) recover(ctx context.Context, auth Authority) error {
	action, state, err := m.store.RecoveryPlan()
	if err != nil {
		return err
	}
	switch action {
	case notices.RecoveryNoop:
		return nil
	case notices.RecoveryUnknown:
		return notices.ErrUnknown
	case notices.RecoveryUnavailable:
		return ErrUnavailable
	}
	lower := state.Checkpoint
	if state.Pending != nil {
		lower = pendingBound(state.Pending)
	}
	cut, err := m.recoveryCut(ctx, auth, lower)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch action {
	case notices.RecoveryFinalize:
		return m.store.FinalizeRecovery(state.Pending)
	case notices.RecoveryAttempt:
		return m.recoverAttempt(ctx, auth, state.Pending)
	case notices.RecoveryCapture:
		return m.recoverObservation(ctx, auth, state.Observation.Lower, cut)
	case notices.RecoveryClearDirty:
		return m.store.ClearDirty()
	default:
		return notices.ErrGuarded
	}
}

func (m *Monitor) recoverObservation(ctx context.Context, auth Authority, lower, cut noticeinterval.Cut) error {
	evidence, err := m.source.Capture(ctx, lower, auth)
	if err != nil {
		return err
	}
	if evidence.Upper != nil {
		if !evidence.Upper.Valid() {
			return ErrUnavailable
		}
		if !evidence.Upper.Follows(cut) {
			return notices.ErrForeign
		}
	}
	if err := m.authorize(ctx, auth); err != nil {
		return err
	}
	if evidence.Class == "all-automation" {
		if !validSuppression(evidence, lower) {
			return notices.ErrInvalid
		}
		return m.store.SuppressRecovery(*evidence.Upper)
	}
	intent, err := m.store.PrepareRecovery(evidence)
	if err != nil {
		return err
	}
	return m.recoverAttempt(ctx, auth, intent)
}

func (m *Monitor) recoverAttempt(ctx context.Context, auth Authority, intent *notices.Intent) error {
	if err := m.acceptIntent(ctx, auth, intent, true); err != nil {
		return err
	}
	if _, err := m.recoveryCut(ctx, auth, pendingBound(intent)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.store.FinalizeRecovery(intent)
}
