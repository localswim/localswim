package noticemonitor

import (
	"context"
	"errors"
	"time"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

type observation struct {
	cut         noticeinterval.Cut
	signature   string
	sampled     bool
	signalError bool
	sourceError bool
	first       time.Time
	latest      time.Time
}

func (o *observation) freeze(t timings) time.Time {
	if o.first.IsZero() {
		return time.Time{}
	}
	return minTime(o.first.Add(t.maxAge), o.latest.Add(t.quiet))
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func stamp(t time.Time) A.Timestamp { return A.Timestamp(t.UTC().Format(time.RFC3339)) }

func (m *Monitor) work(r *run, auth Authority, initial observation) {
	err := m.observe(r, auth, initial)
	if !errors.Is(err, notices.ErrForeign) {
		// Foreign scope must leave the durable state untouched. All other
		// exits may clear the dirty marker only if no effect is unresolved.
		endErr := m.authorize(context.Background(), auth)
		if endErr == nil {
			endErr = m.store.EndRun()
		} else {
			m.store.AbandonRun()
		}
		if err == nil || (endErr != nil && !errors.Is(endErr, notices.ErrGuarded)) {
			err = endErr
			if errors.Is(endErr, notices.ErrGuarded) {
				state := m.store.Snapshot()
				if state != nil && state.Last != nil && state.Last.Upper == nil {
					err = ErrUnavailable
				}
			}
		}
	}
	m.gate.Lock()
	if !r.stopAt.IsZero() && r.partial.Phase == "" && !time.Now().Before(r.stopAt.Add(m.timing.stop)) {
		m.recordPartialDrain(r)
	}
	m.run = nil
	if !m.reserved {
		m.generation = r.completion
	}
	m.phase, m.reason = m.durableStatus()
	if err != nil {
		m.phase, m.reason = faultStatus(err)
	}
	r.terminal = m.snapshotLocked()
	close(r.done)
	m.closeIfQuiescent()
	m.gate.Unlock()
}

func (m *Monitor) observe(r *run, auth Authority, o observation) error {
	// Stop fences new observations, so its signal never cancels an admitted
	// reader or a pending write. Each native source call is separately bounded.
	ctx := context.Background()
	lower := o.cut
	nextPoll := time.Now()
	for {
		select {
		case <-r.stop:
			if !o.first.IsZero() {
				return m.disposition(ctx, auth, &o, lower)
			}
			return nil
		default:
		}
		now := time.Now()
		freeze := o.freeze(m.timing)
		if !freeze.IsZero() && !now.Before(freeze) {
			if err := m.disposition(ctx, auth, &o, lower); err != nil {
				return err
			}
			lower = o.cut
			o.first, o.latest = time.Time{}, time.Time{}
			continue
		}
		if !now.Before(nextPoll) {
			// Avoid starting another source read after Stop. Discovering a
			// wake still needs its own admission before durable publication.
			m.gate.Lock()
			admitted := r.stopAt.IsZero()
			m.gate.Unlock()
			if !admitted {
				continue
			}
			nextPoll = now.Add(m.timing.poll)
			pollCtx := ctx
			var cancel context.CancelFunc
			if !freeze.IsZero() {
				// A read for another wake must yield to the active batch's
				// deadline. Native file calls or admitted durable writes
				// can still delay the cut, so Stop has its own bound.
				pollCtx, cancel = context.WithDeadline(ctx, freeze)
			}
			err := m.poll(pollCtx, r, auth, &o)
			if cancel != nil {
				cancel()
			}
			if err != nil {
				return err
			}
			continue
		}
		deadline := nextPoll
		if !freeze.IsZero() {
			deadline = minTime(deadline, freeze)
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-r.stop:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (m *Monitor) poll(ctx context.Context, r *run, auth Authority, o *observation) error {
	signature, signalErr := m.source.Signal()
	if errors.Is(signalErr, notices.ErrForeign) {
		return signalErr
	}
	if ctx.Err() != nil {
		return nil
	}
	changed := signalErr != nil && !o.signalError
	o.signalError = signalErr != nil
	if signalErr == nil {
		changed = o.sampled && signature != o.signature
		o.signature, o.sampled = signature, true
	}
	authorized := false
	check := func(ctx context.Context, read writer.Read) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		authErr := auth(ctx, read)
		// The grant service can classify a canceled authority check as
		// denial. A coincident expiry needs fresh authority at capture.
		if ctx.Err() != nil && isAuthority(authErr) {
			return ctx.Err()
		}
		authorized = authErr == nil
		return authErr
	}
	cut, err := m.source.Observe(ctx, check)
	switch {
	case isAuthority(err), errors.Is(err, notices.ErrForeign):
		return err
	case ctx.Err() != nil:
		// The quiet/max timer is ready. This deliberate cancellation
		// says nothing about evidence in the final authorized capture.
		return nil
	case err != nil || !cut.Valid():
		if !authorized {
			return ErrUnavailable
		}
		// A source read may fail after calling the credential checker.
		// Confirm current admission independently of that failed read.
		if !o.sourceError {
			if err := m.authorize(ctx, check); err != nil {
				if ctx.Err() != nil && !errors.Is(err, notices.ErrForeign) {
					return nil
				}
				return err
			}
			changed = true
		}
		o.sourceError = true
	case !cut.Follows(o.cut):
		return notices.ErrForeign
	default:
		changed = changed || cut.Changed(o.cut)
		o.cut, o.sourceError = cut, false
	}
	if !changed {
		return nil
	}
	m.gate.Lock()
	admitted := r.stopAt.IsZero()
	m.gate.Unlock()
	if !admitted {
		return nil
	}
	now := time.Now()
	if err := m.store.Observe(stamp(now)); err != nil {
		return err
	}
	if o.first.IsZero() {
		o.first = now
	}
	o.latest = now
	return nil
}

func (m *Monitor) disposition(ctx context.Context, auth Authority, o *observation, lower noticeinterval.Cut) error {
	// A signature sampled before the pinned cut can be consumed with it.
	// A later file change is still eligible as a wake for the next interval.
	signature, err := m.source.Signal()
	if errors.Is(err, notices.ErrForeign) {
		return err
	}
	if err == nil {
		o.signature, o.sampled, o.signalError = signature, true, false
	}
	evidence, err := m.source.Capture(ctx, lower, auth)
	switch {
	case isAuthority(err), errors.Is(err, notices.ErrForeign):
		return err
	case err != nil, evidence.Upper != nil && !evidence.Upper.Valid():
		evidence = noticeinterval.Evidence{Class: notices.Unavailable}
	case evidence.Upper != nil && !evidence.Upper.Follows(o.cut):
		return notices.ErrForeign
	}
	if err := m.authorize(ctx, auth); err != nil {
		return err
	}
	if evidence.Class == "all-automation" {
		if !validSuppression(evidence, lower) {
			return notices.ErrInvalid
		}
		if err := m.store.Suppress(*evidence.Upper); err != nil {
			return err
		}
	} else {
		if err := m.deliver(ctx, auth, evidence); err != nil {
			return err
		}
	}
	if evidence.Upper == nil {
		return ErrUnavailable
	}
	o.cut = *evidence.Upper
	return nil
}

func validSuppression(evidence noticeinterval.Evidence, lower noticeinterval.Cut) bool {
	return evidence.Upper != nil && evidence.Count != nil && *evidence.Count > 0 &&
		*evidence.Count <= 1000 && *evidence.Count == evidence.Upper.Event-lower.Event
}

func (m *Monitor) deliver(ctx context.Context, auth Authority, evidence noticeinterval.Evidence) error {
	intent, err := m.store.Prepare(evidence)
	if err != nil {
		return err
	}
	if err := m.acceptIntent(ctx, auth, intent, false); err != nil {
		return err
	}
	if err := m.authorize(ctx, auth); err != nil {
		return err
	}
	return m.store.Finalize(intent.TargetID, intent.DeliveryID)
}

func (m *Monitor) acceptIntent(ctx context.Context, auth Authority, intent *notices.Intent, recovery bool) error {
	check := func() error {
		if recovery {
			if _, err := m.recoveryCut(ctx, auth, pendingBound(intent)); err != nil {
				return err
			}
			return ctx.Err()
		}
		return m.authorize(ctx, auth)
	}
	if err := check(); err != nil {
		return err
	}
	accept, err := m.store.Fence(intent, stamp(time.Now()), recovery)
	if err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	_, err = accept(stamp(time.Now()))
	return err
}
