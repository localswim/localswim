package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func activityFixture() ActivityRead {
	selected := A.Identity{BoardID: "10000000-0000-0000-0000-000000000001", Incarnation: "10000000-0000-0000-0000-000000000002"}
	source := A.Identity{BoardID: selected.BoardID, Incarnation: "20000000-0000-0000-0000-000000000002"}
	actor := A.UUID("10000000-0000-0000-0000-000000000003")
	at := A.Timestamp("2026-11-06T00:00:00Z")
	return ActivityRead{APIVersion: 1, Scope: "activity", Result: ActivityView{
		Identity: selected, From: "2026-10-30T00:00:00Z", Through: at, Revision: 2, LocalEventUpper: 1,
		Coverage: ActivityCoverage{Status: "gapped", Gaps: []ActivityGap{
			{Origin: source, Reason: "cleared", ThroughInclusive: "2026-11-05T00:00:00Z"},
			{Origin: source, Reason: "expired", ThroughInclusive: "2026-11-05T00:00:00Z"},
		}},
		Events: []ActivityEvent{
			{Origin: source, EventID: 150, At: at, Type: "recovery.expire", BoardRevision: 80, ActorKind: "unknown"},
			{Origin: selected, EventID: 1, At: at, Type: "card.edit", BoardRevision: 2, ActorID: &actor, ActorKind: "human"},
		},
	}}
}

func TestActivityReadWire(t *testing.T) {
	page := activityFixture()
	raw, err := Readable(page)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode[ActivityRead](raw); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode[ActivityRead](append(bytes.Repeat([]byte{' '}, ActivityReadResponseLimit-len(raw)), raw...)); err != nil {
		t.Fatalf("exact response limit: %v", err)
	}
	for name, malformed := range map[string][]byte{
		"oversized formatting": append(bytes.Repeat([]byte{' '}, ActivityReadResponseLimit-len(raw)+1), raw...),
		"private field":        bytes.Replace(raw, []byte(`"actorId": null`), []byte(`"actorId": null, "opId": null`), 1),
		"duplicate key":        bytes.Replace(raw, []byte(`"actorId": null`), []byte(`"actorId": null, "actorId": null`), 1),
		"missing cursor":       bytes.Replace(raw, []byte(`"nextCursor": null,`), nil, 1),
		"unsafe integer":       bytes.Replace(raw, []byte(`"eventId": 150`), []byte(`"eventId": 9007199254740992`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode[ActivityRead](malformed); err == nil {
				t.Fatal("malformed response accepted")
			}
		})
	}
}

func TestActivityReadPageInvariants(t *testing.T) {
	for name, change := range map[string]func(*ActivityView){
		"interval longer than seven days": func(v *ActivityView) { v.From = "2026-10-29T23:59:59Z" },
		"lower event endpoint":            func(v *ActivityView) { v.Events[1].At = v.From },
		"event after upper endpoint":      func(v *ActivityView) { v.Events[0].At = "2026-11-06T00:00:01Z" },
		"local high-water":                func(v *ActivityView) { v.Events[1].EventID = 2 },
		"invalid calendar":                func(v *ActivityView) { v.Events[1].At = "2026-11-00T00:00:00Z" },
		"unsupported event":               func(v *ActivityView) { v.Events[1].Type = "card.delete" },
		"invalid actor":                   func(v *ActivityView) { v.Events[0].ActorKind = "automation" },
		"noncanonical actor":              func(v *ActivityView) { *v.Events[1].ActorID = "A0000000-0000-0000-0000-000000000003" },
		"null events":                     func(v *ActivityView) { v.Events = nil },
		"null gaps":                       func(v *ActivityView) { v.Coverage.Gaps = nil },
		"false completeness":              func(v *ActivityView) { v.Coverage.Status = "complete" },
		"false expiry":                    func(v *ActivityView) { v.Coverage.Status = "expired" },
		"cursor on short page":            func(v *ActivityView) { cursor := "AA.AA"; v.NextCursor = &cursor },
		"event order":                     func(v *ActivityView) { v.Events[0], v.Events[1] = v.Events[1], v.Events[0] },
		"duplicate identity at another time": func(v *ActivityView) {
			v.Events[1] = v.Events[0]
			v.Events[1].At = "2026-11-05T00:00:00Z"
		},
		"gap order": func(v *ActivityView) { v.Coverage.Gaps[0], v.Coverage.Gaps[1] = v.Coverage.Gaps[1], v.Coverage.Gaps[0] },
		"duplicate gap": func(v *ActivityView) {
			v.Coverage.Gaps = append(v.Coverage.Gaps[:1], v.Coverage.Gaps...)
		},
		"empty gap": func(v *ActivityView) {
			v.Coverage.Gaps[1].FromExclusive = &v.Coverage.Gaps[1].ThroughInclusive
		},
		"nonintersecting gap": func(v *ActivityView) { v.Coverage.Gaps[0].ThroughInclusive = v.From },
	} {
		t.Run(name, func(t *testing.T) {
			page := activityFixture()
			change(&page.Result)
			if err := Validate(page); err == nil {
				t.Fatal("invalid page passed in-memory validation")
			}
			raw, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode[ActivityRead](raw); err == nil {
				t.Fatal("invalid page passed wire validation")
			}
		})
	}
}

func TestActivityReadMaximumPageAndExpiredCoverage(t *testing.T) {
	page := activityFixture()
	view := &page.Result
	event := view.Events[1]
	view.Events = make([]ActivityEvent, ActivityEventLimit)
	view.LocalEventUpper = ActivityEventLimit
	for i := range view.Events {
		event.EventID = A.Positive(ActivityEventLimit - i)
		view.Events[i] = event
	}
	gap := view.Coverage.Gaps[0]
	view.Coverage.Gaps = make([]ActivityGap, ActivityGapLimit)
	for i := range view.Coverage.Gaps {
		gap.ThroughInclusive = A.Timestamp(fmt.Sprintf("2026-11-05T00:00:%02dZ", i))
		view.Coverage.Gaps[i] = gap
	}
	view.Coverage.Status = "expired"
	view.Coverage.ActivityFloor = &view.Through
	cursor := strings.Repeat("A", ActivityCursorLimit-4) + ".AAA"
	view.NextCursor = &cursor
	raw, err := Readable(page)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode[ActivityRead](raw); err != nil {
		t.Fatal(err)
	}
	view.Events = append(view.Events, event)
	if err := Validate(page); err == nil {
		t.Fatal("oversized event list accepted")
	}
	view.Events = view.Events[:ActivityEventLimit]
	gap.ThroughInclusive = "2026-11-05T00:00:32Z"
	view.Coverage.Gaps = append(view.Coverage.Gaps, gap)
	if err := Validate(page); err == nil {
		t.Fatal("oversized gap list accepted")
	}
}

func TestActivityCursorEncoding(t *testing.T) {
	if !ValidActivityCursor("AA.AA") {
		t.Fatal("canonical encoding refused")
	}
	for _, malformed := range []string{"", "AA", "A.AA", "AA.AA.", "AA.AA=", "AA.AB", "AA.AA\n", strings.Repeat("A", 2048) + ".AA"} {
		if ValidActivityCursor(malformed) {
			t.Fatal("malformed encoding accepted")
		}
	}
}
