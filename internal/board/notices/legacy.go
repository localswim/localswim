package notices

import (
	"encoding/json"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
)

// These wire types retain the exact v1/v2 field set and order. The revision-1
// read projection must still satisfy the original fixed-target invariants and
// round-trip the original wire bytes; it never writes a migration on read.
type legacyObservation struct {
	ID       A.UUID             `json:"id"`
	Lower    noticeinterval.Cut `json:"lower"`
	FirstAt  A.Timestamp        `json:"firstAt"`
	LatestAt A.Timestamp        `json:"latestAt"`
}
type legacyOutcome struct {
	ObservationID A.UUID              `json:"observationId"`
	Lower         noticeinterval.Cut  `json:"lower"`
	Upper         *noticeinterval.Cut `json:"upper"`
	Class         string              `json:"class"`
	Count         *A.Number           `json:"count"`
	DeliveryID    string              `json:"deliveryId"`
}
type legacyState struct {
	Binding     Binding            `json:"binding"`
	TargetID    A.UUID             `json:"targetId"`
	Checkpoint  noticeinterval.Cut `json:"checkpoint"`
	Dirty       bool               `json:"dirty"`
	Observation *legacyObservation `json:"observation"`
	Gaps        []Gap              `json:"gaps"`
	Pending     *Intent            `json:"pending"`
	Last        *legacyOutcome     `json:"last"`
}
type legacyDurable struct {
	Version int `json:"version"`
	legacyState
	Inbox          []entry `json:"inbox"`
	PendingAttempt *string `json:"pendingAttempt"`
}

func (d *durable) legacyWire() any {
	s := legacyState{Binding: d.Binding, TargetID: d.TargetID, Checkpoint: d.Checkpoint, Dirty: d.Dirty,
		Gaps: d.Gaps, Pending: d.Pending}
	if o := d.Observation; o != nil {
		s.Observation = &legacyObservation{o.ID, o.Lower, o.FirstAt, o.LatestAt}
	}
	if o := d.Last; o != nil {
		s.Last = &legacyOutcome{o.ObservationID, o.Lower, o.Upper, o.Class, o.Count, o.DeliveryID}
	}
	if d.Version == legacyVersion {
		return struct {
			Version int `json:"version"`
			legacyState
			Inbox []entry `json:"inbox"`
		}{d.Version, s, d.Inbox}
	}
	return legacyDurable{d.Version, s, d.Inbox, d.PendingAttempt}
}

func decodeLegacy(raw []byte) (*durable, error) {
	var old legacyDurable
	if json.Unmarshal(raw, &old) != nil {
		return nil, ErrInvalid
	}
	d := &durable{Version: old.Version, State: State{Binding: old.Binding, TargetID: old.TargetID, ConfigurationRevision: 1,
		Checkpoint: old.Checkpoint, Dirty: old.Dirty, Gaps: old.Gaps, Pending: old.Pending}, Inbox: old.Inbox, PendingAttempt: old.PendingAttempt}
	if o := old.Observation; o != nil {
		d.Observation = &Observation{o.ID, o.Lower, o.FirstAt, o.LatestAt, old.TargetID, 1}
	}
	if o := old.Last; o != nil {
		d.Last = &Outcome{o.ObservationID, o.Lower, o.Upper, o.Class, o.Count, o.DeliveryID, old.TargetID}
	}
	return d, nil
}

// upgrade is lossless and only runs as part of an authorized publication.
func (d *durable) upgrade() {
	if d.Version == legacyVersion && d.Pending != nil {
		marker := legacyUnknown
		d.PendingAttempt = &marker
	}
	d.Version = stateVersion
}
