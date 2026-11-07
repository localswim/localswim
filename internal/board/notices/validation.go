package notices

import (
	"bytes"
	"encoding/json"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func (d *durable) encode() ([]byte, error) {
	if !d.valid() {
		return nil, ErrInvalid
	}
	var wire any = d
	if d.Version == legacyVersion || d.Version == attemptVersion {
		wire = d.legacyWire()
	}
	b, err := json.Marshal(wire)
	if err != nil || len(b) > MaxStateBytes {
		return nil, ErrInvalid
	}
	return b, nil
}

func decode(raw []byte, binding Binding) (*durable, error) {
	var d durable
	if len(raw) > MaxStateBytes || json.Unmarshal(raw, &d) != nil {
		return nil, ErrInvalid
	}
	if d.Version == legacyVersion || d.Version == attemptVersion {
		projected, err := decodeLegacy(raw)
		if err != nil {
			return nil, err
		}
		d = *projected
	}
	// The private format only accepts its own encoding. This requires every
	// field (including nulls) and rejects duplicate/unknown keys and aliases.
	encoded, err := d.encode()
	if err != nil || !bytes.Equal(raw, encoded) {
		return nil, ErrInvalid
	}
	if d.Binding != binding {
		return nil, ErrForeign
	}
	return &d, nil
}

func (d *durable) valid() bool {
	if !d.validAttempt() || !d.Binding.valid() || !validUUID4(d.TargetID) || !d.Checkpoint.Valid() ||
		d.ConfigurationRevision < 1 || d.ConfigurationRevision > MaxConfigurationRevision ||
		(d.Version != stateVersion && d.ConfigurationRevision != 1) ||
		d.Gaps == nil || len(d.Gaps) > MaxGaps || d.Inbox == nil || len(d.Inbox) > MaxPairs {
		return false
	}
	if o := d.Observation; o != nil && (!d.Dirty || !validUUID4(o.ID) || o.Lower != d.Checkpoint ||
		A.Validate(o.FirstAt) != nil || A.Validate(o.LatestAt) != nil || !validUUID4(o.TargetID) ||
		o.ConfigurationRevision < 1 || o.ConfigurationRevision > d.ConfigurationRevision ||
		(o.ConfigurationRevision == d.ConfigurationRevision && o.TargetID != d.TargetID) ||
		(d.Version != stateVersion && (o.ConfigurationRevision != 1 || o.TargetID != d.TargetID))) {
		return false
	}
	if p := d.Pending; p != nil && (d.Observation == nil || p.ObservationID != d.Observation.ID ||
		p.Lower != d.Observation.Lower || p.Class == Suppressed || !p.valid(d.Binding, d.Observation.TargetID)) {
		return false
	}
	gaps, ok := d.validGaps()
	return ok && d.validOutcome(gaps) && d.validInbox(gaps)
}

func (d *durable) validAttempt() bool {
	switch d.Version {
	case legacyVersion:
		return d.PendingAttempt == nil
	case attemptVersion, stateVersion:
		if d.Pending == nil {
			return d.PendingAttempt == nil
		}
		return d.PendingAttempt != nil && (*d.PendingAttempt == prepared || *d.PendingAttempt == attempted ||
			d.Version == stateVersion && *d.PendingAttempt == legacyUnknown)
	default:
		return false
	}
}

func (d *durable) validGaps() (map[A.UUID]Gap, bool) {
	gaps := make(map[A.UUID]Gap, len(d.Gaps))
	var prior *noticeinterval.Cut
	for idx, g := range d.Gaps {
		if !validUUID4(g.ObservationID) || !isLoss(g.Class) || !intervalValid(g.Class, g.Lower, g.Upper, nil) ||
			(idx > 0 && (prior == nil || !g.Lower.Follows(*prior))) {
			return nil, false
		}
		if _, exists := gaps[g.ObservationID]; exists {
			return nil, false
		}
		gaps[g.ObservationID], prior = g, g.Upper
		if p := d.Pending; p != nil && p.ObservationID == g.ObservationID {
			if g.Lower != p.Lower || !same(g.Upper, p.Upper) || g.Class != p.Class {
				return nil, false
			}
		} else if g.Upper == nil {
			if !d.Dirty || d.Observation != nil || d.Last == nil || d.Last.ObservationID != g.ObservationID ||
				d.Checkpoint != g.Lower || d.Last.Upper != nil || d.Last.Class != g.Class {
				return nil, false
			}
		} else if !d.Checkpoint.Follows(*g.Upper) {
			return nil, false
		}
	}
	if d.Pending != nil && !matchesGap(d.Pending.outcome(), gaps) {
		return nil, false
	}
	if d.Observation != nil && d.Pending == nil {
		if _, exists := gaps[d.Observation.ID]; exists {
			return nil, false
		}
	}
	return gaps, true
}

func matchesGap(o Outcome, gaps map[A.UUID]Gap) bool {
	g, ok := gaps[o.ObservationID]
	if !isLoss(o.Class) {
		return !ok
	}
	return ok && g.Class == o.Class && g.Lower == o.Lower && same(g.Upper, o.Upper)
}

func (d *durable) validOutcome(gaps map[A.UUID]Gap) bool {
	o := d.Last
	if o == nil {
		return len(d.Inbox) == 0 || (len(d.Inbox) == 1 && !d.Inbox[0].Finalized)
	}
	if !validUUID4(o.ObservationID) || !validUUID4(o.TargetID) ||
		(d.Version != stateVersion && o.TargetID != d.TargetID) ||
		!intervalValid(o.Class, o.Lower, o.Upper, o.Count) || !matchesGap(*o, gaps) ||
		(d.Observation != nil && d.Observation.ID == o.ObservationID) {
		return false
	}
	if o.Upper == nil {
		if !d.Dirty || d.Checkpoint != o.Lower || d.Observation != nil {
			return false
		}
	} else if *o.Upper != d.Checkpoint {
		return false
	}
	if o.Class == Suppressed {
		return o.DeliveryID == ""
	}
	i := Intent{intentVersion, d.Binding, o.ObservationID, o.Lower, o.Upper, o.TargetID, o.Class, o.Count, o.DeliveryID}
	return i.valid(d.Binding, o.TargetID)
}

func (d *durable) validInbox(gaps map[A.UUID]Gap) bool {
	seen := make(map[A.UUID]bool, len(d.Inbox))
	var prior *noticeinterval.Cut
	lastFound := false
	for idx, e := range d.Inbox {
		i := e.Intent
		if i.Class == Suppressed || !i.valid(d.Binding, i.TargetID) ||
			(d.Version != stateVersion && i.TargetID != d.TargetID) || !matchesGap(i.outcome(), gaps) ||
			A.Validate(e.AcceptedAt) != nil || seen[i.ObservationID] ||
			(idx > 0 && (prior == nil || !i.Lower.Follows(*prior))) {
			return false
		}
		seen[i.ObservationID], prior = true, i.Upper
		if !e.Finalized {
			if idx != len(d.Inbox)-1 || d.Pending == nil || !i.exact(*d.Pending) ||
				(d.Version != legacyVersion && *d.PendingAttempt != attempted &&
					!(d.Version == stateVersion && *d.PendingAttempt == legacyUnknown)) {
				return false
			}
			continue
		}
		if d.Observation != nil && i.ObservationID == d.Observation.ID {
			return false
		}
		if d.Last != nil && i.ObservationID == d.Last.ObservationID {
			o := d.Last
			if o.TargetID != i.TargetID || o.DeliveryID != i.DeliveryID || o.Class != i.Class || !same(o.Count, i.Count) ||
				o.Lower != i.Lower || !same(o.Upper, i.Upper) ||
				(idx < len(d.Inbox)-1 && d.Inbox[idx+1].Finalized) {
				return false
			}
			lastFound = true
		} else if i.Upper == nil || !d.Checkpoint.Follows(*i.Upper) {
			return false
		}
	}
	// An acceptance may reclaim the prior last pair before its own finalize.
	return d.Version == stateVersion || d.Last == nil || d.Last.DeliveryID == "" || lastFound ||
		(len(d.Inbox) > 0 && !d.Inbox[len(d.Inbox)-1].Finalized)
}
