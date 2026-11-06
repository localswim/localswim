package transport

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
)

const (
	ActivityEventLimit        = 20
	ActivityGapLimit          = 32
	ActivityCursorLimit       = 2048
	ActivityReadResponseLimit = 32768
	ActivityWindowSeconds     = 604800
)

type ActivityScope string
type ActivityEventType string
type ActivityActorKind string
type ActivityCoverageStatus string
type ActivityGapReason string

type ActivityRead struct {
	APIVersion A.Version     `json:"apiVersion"`
	Scope      ActivityScope `json:"scope"`
	Result     ActivityView  `json:"result"`
}

type ActivityView struct {
	Identity        A.Identity       `json:"identity"`
	From            A.Timestamp      `json:"from"`
	Through         A.Timestamp      `json:"through"`
	Revision        A.Number         `json:"revision"`
	LocalEventUpper A.Number         `json:"localEventUpper"`
	Coverage        ActivityCoverage `json:"coverage"`
	Events          []ActivityEvent  `json:"events"`
	NextCursor      *string          `json:"nextCursor"`
}

type ActivityEvent struct {
	Origin        A.Identity        `json:"origin"`
	EventID       A.Positive        `json:"eventId"`
	At            A.Timestamp       `json:"at"`
	Type          ActivityEventType `json:"type"`
	BoardRevision A.Number          `json:"boardRevision"`
	ActorID       *A.UUID           `json:"actorId"`
	ActorKind     ActivityActorKind `json:"actorKind"`
}

type ActivityCoverage struct {
	Status        ActivityCoverageStatus `json:"status"`
	ActivityFloor *A.Timestamp           `json:"activityFloor"`
	Gaps          []ActivityGap          `json:"gaps"`
}

type ActivityGap struct {
	Origin           A.Identity        `json:"origin"`
	Reason           ActivityGapReason `json:"reason"`
	FromExclusive    *A.Timestamp      `json:"fromExclusive"`
	ThroughInclusive A.Timestamp       `json:"throughInclusive"`
}

var activityCursor = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// ValidActivityCursor validates the outer encoding only. Signature, payload and
// current authority are checked by the host.
func ValidActivityCursor(cursor string) bool {
	if len(cursor) > ActivityCursorLimit || !activityCursor.MatchString(cursor) {
		return false
	}
	payload, tag, _ := strings.Cut(cursor, ".")
	encoding := base64.RawURLEncoding.Strict()
	_, payloadErr := encoding.DecodeString(payload)
	_, tagErr := encoding.DecodeString(tag)
	return payloadErr == nil && tagErr == nil
}

func activityView(v ActivityView) error {
	const layout = "2006-01-02T15:04:05Z"
	from, fromErr := time.Parse(layout, string(v.From))
	through, throughErr := time.Parse(layout, string(v.Through))
	if span := through.Sub(from); fromErr != nil || throughErr != nil || span <= 0 || span > ActivityWindowSeconds*time.Second {
		return fmt.Errorf("invalid activity interval")
	}
	if len(v.Events) > ActivityEventLimit || (v.NextCursor != nil && (len(v.Events) != ActivityEventLimit || !ValidActivityCursor(*v.NextCursor))) {
		return fmt.Errorf("invalid activity page limit or continuation")
	}
	type eventIdentity struct {
		origin A.Identity
		id     A.Positive
	}
	seen := make(map[eventIdentity]bool, len(v.Events))
	for i, event := range v.Events {
		key := eventIdentity{event.Origin, event.EventID}
		if seen[key] || (i > 0 && compareActivityEvents(v.Events[i-1], event) <= 0) {
			return fmt.Errorf("invalid activity event order or identity")
		}
		seen[key] = true
		if event.At <= v.From || event.At > v.Through || (event.Origin == v.Identity && A.Number(event.EventID) > v.LocalEventUpper) {
			return fmt.Errorf("activity event outside captured interval")
		}
	}
	status := ActivityCoverageStatus("complete")
	floor := v.Coverage.ActivityFloor
	if floor != nil && *floor >= v.Through {
		status = "expired"
	} else if (floor != nil && *floor > v.From) || len(v.Coverage.Gaps) != 0 {
		status = "gapped"
	}
	if v.Coverage.Status != status {
		return fmt.Errorf("invalid activity coverage status")
	}
	for _, gap := range v.Coverage.Gaps {
		if gap.ThroughInclusive <= v.From || (gap.FromExclusive != nil && *gap.FromExclusive >= v.Through) {
			return fmt.Errorf("activity gap outside captured interval")
		}
	}
	return nil
}

func activityCoverage(c ActivityCoverage) error {
	if len(c.Gaps) > ActivityGapLimit {
		return fmt.Errorf("activity gap limit")
	}
	for i := 1; i < len(c.Gaps); i++ {
		if compareActivityGaps(c.Gaps[i-1], c.Gaps[i]) >= 0 {
			return fmt.Errorf("invalid activity gap order or identity")
		}
	}
	return nil
}

func compareActivityEvents(a, b ActivityEvent) int {
	return cmp.Or(cmp.Compare(a.At, b.At), cmp.Compare(a.Origin.BoardID, b.Origin.BoardID),
		cmp.Compare(a.Origin.Incarnation, b.Origin.Incarnation), cmp.Compare(a.EventID, b.EventID))
}

func compareActivityGaps(a, b ActivityGap) int {
	// An absent lower bound sorts before any canonical timestamp.
	var lowerA, lowerB A.Timestamp
	if a.FromExclusive != nil {
		lowerA = *a.FromExclusive
	}
	if b.FromExclusive != nil {
		lowerB = *b.FromExclusive
	}
	return cmp.Or(cmp.Compare(a.Origin.BoardID, b.Origin.BoardID), cmp.Compare(a.Origin.Incarnation, b.Origin.Incarnation),
		cmp.Compare(lowerA, lowerB), cmp.Compare(a.ThroughInclusive, b.ThroughInclusive), cmp.Compare(a.Reason, b.Reason))
}

func activitySchema(t reflect.Type, m map[string]any) {
	p := m["properties"].(map[string]any)
	switch t {
	case reflect.TypeFor[ActivityRead]():
		m["x-max-utf8-bytes"] = ActivityReadResponseLimit
	case reflect.TypeFor[ActivityView]():
		p["events"].(map[string]any)["maxItems"] = ActivityEventLimit
		p["nextCursor"] = map[string]any{"anyOf": []any{
			map[string]any{"type": "string", "maxLength": ActivityCursorLimit, "pattern": activityCursor.String()},
			map[string]any{"type": "null"},
		}}
	case reflect.TypeFor[ActivityCoverage]():
		p["gaps"].(map[string]any)["maxItems"] = ActivityGapLimit
		p["gaps"].(map[string]any)["uniqueItems"] = true
	}
}
