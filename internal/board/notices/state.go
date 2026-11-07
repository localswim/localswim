// Package notices stores revisioned target configuration and original delivery evidence for one selected board.
// The host owns native exclusion, authority checks and worker quiescence.
package notices

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/contract/noticetext"
)

const (
	MaxStateBytes                     = 256 * 1024
	MaxPairs                          = 64
	MaxGaps                           = 64
	intentVersion                     = 2
	legacyIntentVersion               = 1
	legacyVersion                     = 1
	attemptVersion                    = 2
	configurationVersion              = 3
	stateVersion                      = 4
	MaxConfigurationRevision A.Number = 9007199254740991

	prepared      = "prepared"
	attempted     = "attempted"
	legacyUnknown = "legacy-unknown"

	Human         = "human"
	Mixed         = "mixed"
	UnknownActor  = "unknown-actor"
	OtherActor    = "other-actor"
	ObservedEmpty = "observed-empty"
	Expired       = "expired"
	Gapped        = "gapped"
	Unavailable   = "unavailable"
	Suppressed    = "suppressed"
)

var fileKey = regexp.MustCompile(`^[0-9a-f]{8}:[0-9a-f]{16}$`)

type Binding struct {
	RootKey        string `json:"rootKey"`
	DatabaseKey    string `json:"databaseKey"`
	InstallationID A.UUID `json:"installationId"`
	BoardID        A.UUID `json:"boardId"`
	Incarnation    A.UUID `json:"incarnation"`
}

func validUUID4(id A.UUID) bool {
	return A.Validate(id) == nil && id[14] == '4' &&
		(id[19] == '8' || id[19] == '9' || id[19] == 'a' || id[19] == 'b')
}

func (b Binding) valid() bool {
	return fileKey.MatchString(b.RootKey) && fileKey.MatchString(b.DatabaseKey) &&
		b.RootKey[:8] == b.DatabaseKey[:8] && b.RootKey != b.DatabaseKey &&
		b.RootKey[9:] != "0000000000000000" && b.DatabaseKey[9:] != "0000000000000000" &&
		validUUID4(b.InstallationID) && A.Validate(b.BoardID) == nil && A.Validate(b.Incarnation) == nil &&
		b.InstallationID != b.BoardID && b.InstallationID != b.Incarnation && b.BoardID != b.Incarnation
}

type Observation struct {
	ID                    A.UUID             `json:"id"`
	Lower                 noticeinterval.Cut `json:"lower"`
	FirstAt               A.Timestamp        `json:"firstAt"`
	LatestAt              A.Timestamp        `json:"latestAt"`
	TargetID              A.UUID             `json:"targetId"`
	ConfigurationRevision A.Number           `json:"configurationRevision"`
}

type Gap struct {
	ObservationID A.UUID              `json:"observationId"`
	Lower         noticeinterval.Cut  `json:"lower"`
	Upper         *noticeinterval.Cut `json:"upper"`
	Class         string              `json:"class"`
}

type Intent struct {
	Version       int                    `json:"version"`
	Binding       Binding                `json:"binding"`
	ObservationID A.UUID                 `json:"observationId"`
	Lower         noticeinterval.Cut     `json:"lower"`
	Upper         *noticeinterval.Cut    `json:"upper"`
	TargetID      A.UUID                 `json:"targetId"`
	Class         string                 `json:"class"`
	Count         *A.Number              `json:"count"`
	Payload       *noticetext.NoticeText `json:"payload,omitempty"`
	DeliveryID    string                 `json:"deliveryId"`
}

func (i Intent) identity() string {
	if i.Version == intentVersion {
		encoded, err := json.Marshal(struct {
			Version       int                    `json:"version"`
			Binding       Binding                `json:"binding"`
			ObservationID A.UUID                 `json:"observationId"`
			Lower         noticeinterval.Cut     `json:"lower"`
			Upper         *noticeinterval.Cut    `json:"upper"`
			TargetID      A.UUID                 `json:"targetId"`
			Class         string                 `json:"class"`
			Count         *A.Number              `json:"count"`
			Payload       *noticetext.NoticeText `json:"payload"`
		}{i.Version, i.Binding, i.ObservationID, i.Lower, i.Upper, i.TargetID, i.Class, i.Count, i.Payload})
		if err != nil {
			return ""
		}
		digest := sha256.Sum256(encoded)
		return hex.EncodeToString(digest[:])
	}
	// Field order and the explicit null upper are part of version 1's encoding.
	encoded, err := json.Marshal(struct {
		Version       int                 `json:"version"`
		Binding       Binding             `json:"binding"`
		ObservationID A.UUID              `json:"observationId"`
		Lower         noticeinterval.Cut  `json:"lower"`
		Upper         *noticeinterval.Cut `json:"upper"`
		TargetID      A.UUID              `json:"targetId"`
	}{i.Version, i.Binding, i.ObservationID, i.Lower, i.Upper, i.TargetID})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (i Intent) valid(binding Binding, target A.UUID) bool {
	validPayload := i.Version == legacyIntentVersion && i.Payload == nil ||
		i.Version == intentVersion && i.Class != Suppressed && i.Payload != nil && noticetext.Validate(*i.Payload) == nil &&
			(!(isLoss(i.Class) || i.Class == ObservedEmpty) || i.Payload.Kind == "fallback")
	return validPayload && i.Binding == binding && validUUID4(target) && i.TargetID == target &&
		validUUID4(i.ObservationID) && intervalValid(i.Class, i.Lower, i.Upper, i.Count) &&
		A.Validate(A.Digest(i.DeliveryID)) == nil && i.DeliveryID == i.identity()
}

func (i Intent) outcome() Outcome {
	return Outcome{i.ObservationID, i.Lower, copyValue(i.Upper), i.Class, copyValue(i.Count), i.DeliveryID, i.TargetID, noticetext.Clone(i.Payload)}
}

func (i Intent) exact(other Intent) bool {
	iUpper, iCount, otherUpper, otherCount := i.Upper, i.Count, other.Upper, other.Count
	iPayload, otherPayload := i.Payload, other.Payload
	i.Upper, i.Count, other.Upper, other.Count = nil, nil, nil, nil
	i.Payload, other.Payload = nil, nil
	return i == other && same(iUpper, otherUpper) && same(iCount, otherCount) && noticetext.Equal(iPayload, otherPayload)
}

type Outcome struct {
	ObservationID A.UUID                 `json:"observationId"`
	Lower         noticeinterval.Cut     `json:"lower"`
	Upper         *noticeinterval.Cut    `json:"upper"`
	Class         string                 `json:"class"`
	Count         *A.Number              `json:"count"`
	DeliveryID    string                 `json:"deliveryId"`
	TargetID      A.UUID                 `json:"targetId"`
	Payload       *noticetext.NoticeText `json:"payload,omitempty"`
}

// State is a private monitor snapshot. Use Pair for operator projection.
type State struct {
	Binding               Binding            `json:"binding"`
	TargetID              A.UUID             `json:"targetId"`
	ConfigurationRevision A.Number           `json:"configurationRevision"`
	Checkpoint            noticeinterval.Cut `json:"checkpoint"`
	Dirty                 bool               `json:"dirty"`
	Observation           *Observation       `json:"observation"`
	Gaps                  []Gap              `json:"gaps"`
	Pending               *Intent            `json:"pending"`
	Last                  *Outcome           `json:"last"`
}

// entry is one notice and receipt. Both share the exact intent and one
// acceptance time, and are created and reclaimed as one durable pair.
type entry struct {
	Intent     Intent      `json:"intent"`
	AcceptedAt A.Timestamp `json:"acceptedAt"`
	Finalized  bool        `json:"finalized"`
}

type durable struct {
	Version int `json:"version"`
	State
	Inbox          []entry `json:"inbox"`
	PendingAttempt *string `json:"pendingAttempt"`
}

type Pair struct {
	TargetID   A.UUID                 `json:"targetId"`
	DeliveryID string                 `json:"deliveryId"`
	AcceptedAt A.Timestamp            `json:"acceptedAt"`
	Class      string                 `json:"class"`
	LowerEvent A.Number               `json:"lowerEvent"`
	UpperEvent *A.Number              `json:"upperEvent"`
	EventCount *A.Number              `json:"eventCount"`
	Payload    *noticetext.NoticeText `json:"payload,omitempty"`
}

func (e entry) project() Pair {
	p := Pair{TargetID: e.Intent.TargetID, DeliveryID: e.Intent.DeliveryID, AcceptedAt: e.AcceptedAt,
		Class: e.Intent.Class, LowerEvent: e.Intent.Lower.Event, EventCount: copyValue(e.Intent.Count), Payload: noticetext.Clone(e.Intent.Payload)}
	if e.Intent.Upper != nil {
		upper := e.Intent.Upper.Event
		p.UpperEvent = &upper
	}
	return p
}

func same[T comparable](a, b *T) bool {
	return a == b || (a != nil && b != nil && *a == *b)
}

func copyValue[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (i *Intent) clone() *Intent {
	p := copyValue(i)
	if p != nil {
		p.Upper, p.Count = copyValue(p.Upper), copyValue(p.Count)
		p.Payload = noticetext.Clone(p.Payload)
	}
	return p
}

func (d *durable) snapshot() *State {
	s := d.State
	s.Observation = copyValue(s.Observation)
	s.Pending = s.Pending.clone()
	s.Gaps = slices.Clone(s.Gaps)
	for i := range s.Gaps {
		s.Gaps[i].Upper = copyValue(s.Gaps[i].Upper)
	}
	s.Last = copyValue(s.Last)
	if s.Last != nil {
		s.Last.Upper, s.Last.Count = copyValue(s.Last.Upper), copyValue(s.Last.Count)
		s.Last.Payload = noticetext.Clone(s.Last.Payload)
	}
	return &s
}

func isLoss(class string) bool { return class == Expired || class == Gapped || class == Unavailable }

func intervalValid(class string, lower noticeinterval.Cut, upper *noticeinterval.Cut, count *A.Number) bool {
	if !lower.Valid() || (upper != nil && !upper.Follows(lower)) {
		return false
	}
	if isLoss(class) {
		return count == nil && (upper != nil || class == Unavailable)
	}
	if upper == nil || count == nil || *count != upper.Event-lower.Event || *count > 1000 {
		return false
	}
	switch class {
	case ObservedEmpty:
		return *count == 0
	case Mixed:
		return *count >= 2
	case Human, OtherActor, UnknownActor, Suppressed:
		return *count > 0
	default:
		return false
	}
}
