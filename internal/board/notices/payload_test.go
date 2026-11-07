package notices

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/contract/noticetext"
)

// Deliberately manufacture genuine legacy records, rather than changing only
// the container version around a modern payload-bearing delivery identity.
func legacyFixtureIntent(i *Intent) *Intent {
	if i == nil {
		return nil
	}
	i = i.clone()
	i.Version, i.Payload = legacyIntentVersion, nil
	i.DeliveryID = i.identity()
	return i
}

func legacyFixture(d *durable) {
	d.Pending = legacyFixtureIntent(d.Pending)
	d.Inbox = append([]entry{}, d.Inbox...)
	for n := range d.Inbox {
		d.Inbox[n].Intent = *legacyFixtureIntent(&d.Inbox[n].Intent)
	}
	if d.Last != nil {
		o := *d.Last
		o.Payload = nil
		if o.Class != Suppressed {
			i := Intent{Version: legacyIntentVersion, Binding: d.Binding, ObservationID: o.ObservationID,
				Lower: o.Lower, Upper: o.Upper, TargetID: o.TargetID, Class: o.Class, Count: o.Count}
			o.DeliveryID = i.identity()
		}
		d.Last = &o
	}
}

func richFacts() *noticetext.NoticeTextFacts {
	return &noticetext.NoticeTextFacts{Label: "Original 🏊", From: string(sample), Through: string(sample), Tickets: []A.Positive{1, 7}}
}

func richEvidence(upper noticeinterval.Cut) noticeinterval.Evidence {
	e := complete(Human, upper, 1)
	e.Facts = richFacts()
	return e
}

func TestSharedDeliveryIdentityVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../fixtures/contracts/notice-text/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		IdentityVectors []struct{ ID, CanonicalJSON, SHA256 string }
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.IdentityVectors) < 2 {
		t.Fatal("missing identity vectors")
	}
	for _, v := range vectors.IdentityVectors {
		t.Run(v.ID, func(t *testing.T) {
			var i Intent
			if err := json.Unmarshal([]byte(v.CanonicalJSON), &i); err != nil {
				t.Fatal(err)
			}
			i.DeliveryID = i.identity()
			if i.DeliveryID != v.SHA256 {
				t.Fatal("canonical identity changed")
			}
			if i.Version == legacyIntentVersion {
				i.Class = Human
				count := A.Number(1)
				i.Count = &count
			}
			if !i.valid(i.Binding, i.TargetID) {
				t.Fatal("identity vector is not a valid original")
			}
		})
	}
}

func TestOriginalPayloadOwnershipRetargetRecoveryAndLookup(t *testing.T) {
	s, f, target := start(t)
	upper := baseline
	upper.Event++
	e := richEvidence(upper)
	i := prepare(t, s, e)
	original := noticetext.Clone(i.Payload)
	if i.Version != 2 || original == nil || original.Kind != "complete" || s.current.Load().Version != 4 {
		t.Fatal("new capture is not modern")
	}
	e.Facts.Label = "Changed"
	e.Facts.Tickets[0] = 99
	i.Payload.Facts.Tickets[0] = 99
	snapshot := s.Snapshot()
	snapshot.Pending.Payload.Facts.Label = "Changed snapshot"
	i = s.Snapshot().Pending
	if !noticetext.Equal(i.Payload, original) {
		t.Fatal("input or snapshot escaped ownership")
	}
	for _, mutate := range []func(*Intent){
		func(j *Intent) {
			j.Payload.Facts.Label = "Different"
			rebuilt := noticetext.Build(j.Payload.Facts)
			j.Payload = &rebuilt
		},
		func(j *Intent) { j.Class = UnknownActor },
		func(j *Intent) { *j.Count = 0 },
	} {
		changed := i.clone()
		mutate(changed)
		if changed.exact(*i) || changed.identity() == i.identity() {
			t.Fatal("new identity/exactness omitted immutable content")
		}
	}
	s = f.open(t)
	requireErr(t, s.Retarget(futureTarget, 1), nil)
	settleRecovered(t, s, i, sample)
	if !noticetext.Equal(s.Snapshot().Last.Payload, original) || s.Snapshot().Last.TargetID != target {
		t.Fatal("outcome changed original")
	}
	s.Snapshot().Last.Payload.Facts.Tickets[0] = 99
	s = f.open(t)
	pair := s.Inbox()[0]
	if !noticetext.Equal(pair.Payload, original) || pair.TargetID != target || pair.AcceptedAt != sample {
		t.Fatal("reopen/retarget changed original pair")
	}
	pair.Payload.Facts.Label = "Changed inbox"
	lookup, reconciled, err := s.Lookup(target, i.DeliveryID, true)
	requireErr(t, err, nil)
	if reconciled || !noticetext.Equal(lookup.Payload, original) {
		t.Fatal("lookup changed original")
	}
	lookup.Payload.Facts.Tickets[0] = 99
	if !noticetext.Equal(s.Inbox()[0].Payload, original) || !noticetext.Equal(s.Snapshot().Last.Payload, original) {
		t.Fatal("returned evidence mutated private state")
	}
}

func TestNewLossEmptyAndUnprovableCaptureFreezeFallback(t *testing.T) {
	for _, class := range []string{ObservedEmpty, Expired, Gapped, Unavailable, Human} {
		t.Run(class, func(t *testing.T) {
			s, f, _ := start(t)
			upper := baseline
			var e noticeinterval.Evidence
			switch class {
			case ObservedEmpty:
				e = complete(class, upper, 0)
				e.Facts = richFacts()
			case Unavailable:
				e = noticeinterval.Evidence{Class: class, Facts: richFacts()}
			case Expired, Gapped:
				upper.Event++
				e = noticeinterval.Evidence{Class: class, Upper: &upper, Facts: richFacts()}
			default:
				upper.Event++
				e = complete(class, upper, 1)
			}
			i := prepare(t, s, e)
			if i.Payload == nil || i.Payload.Kind != "fallback" || i.Payload.Facts != nil {
				t.Fatal("unproved complete text")
			}
			accept(t, s, i, sample)
			requireErr(t, s.Finalize(i.TargetID, i.DeliveryID), nil)
			if !noticetext.Equal(f.open(t).Inbox()[0].Payload, i.Payload) {
				t.Fatal("fallback was not persisted")
			}
		})
	}
	s, _, _ := start(t)
	requireErr(t, s.Observe(sample), nil)
	upper := baseline
	upper.Event++
	requireErr(t, s.Suppress(upper), nil)
	if s.Snapshot().Last.Payload != nil || s.Snapshot().Last.DeliveryID != "" || len(s.Inbox()) != 0 {
		t.Fatal("suppression emitted payload")
	}
}

func TestStrictPayloadVersionAndCanonicalPrivateDecoding(t *testing.T) {
	s, f, _ := start(t)
	upper := baseline
	upper.Event++
	i := prepare(t, s, richEvidence(upper))
	canonical := bytes.Clone(f.data)
	for _, replacement := range [][2]string{
		{`"version":4`, `"version":3`},
		{`"version":2,"binding"`, `"version":1,"binding"`},
		{`"payload":{"version":1`, `"payload":{"version":2`},
		{`"kind":"complete"`, `"kind":"complete","kind":"complete"`},
		{`"tickets":[1,7]`, `"tickets":[7,1]`},
		{`"Original 🏊"`, `"Different"`},
		{`"payload":`, `"unknown":null,"payload":`},
	} {
		raw := bytes.Replace(canonical, []byte(replacement[0]), []byte(replacement[1]), 1)
		if bytes.Equal(raw, canonical) {
			t.Fatal("mutation did not alter fixture")
		}
		if _, err := decode(raw, selected); err != ErrInvalid {
			t.Fatal("accepted malformed or identity-mismatched private payload")
		}
	}
	changed := i.clone()
	changed.Payload = nil
	if changed.valid(selected, i.TargetID) {
		t.Fatal("modern payload missing")
	}
	changed = legacyFixtureIntent(i)
	changed.Payload = noticetext.Clone(i.Payload)
	if changed.valid(selected, i.TargetID) {
		t.Fatal("legacy intent gained a payload")
	}
	if _, err := decode(append(canonical, '\n'), selected); err != ErrInvalid {
		t.Fatal("noncanonical private bytes accepted")
	}
}

func TestVersionThreeHistoricalTargetRevisionAndUnavailablePayload(t *testing.T) {
	s, f, target := start(t)
	upper := baseline
	upper.Event++
	i := prepare(t, s, richEvidence(upper))
	accept(t, s, i, sample)
	requireErr(t, s.Finalize(target, i.DeliveryID), nil)
	requireErr(t, s.EndRun(), nil)
	requireErr(t, s.Retarget(futureTarget, 1), nil)
	d := *s.current.Load()
	legacyFixture(&d)
	d.Version = configurationVersion
	var err error
	f.data, err = d.encode()
	requireErr(t, err, nil)
	original := bytes.Clone(f.data)
	s = f.open(t)
	if !bytes.Equal(f.data, original) || s.Snapshot().ConfigurationRevision != 2 || s.Snapshot().TargetID != futureTarget || s.Snapshot().Last.TargetID != target {
		t.Fatal("v3 historical target/revision projection changed")
	}
	pair := s.Inbox()[0]
	if pair.Payload != nil || s.Snapshot().Last.Payload != nil || pair.AcceptedAt != sample {
		t.Fatal("legacy payload/acceptance invented")
	}
	lookup, _, err := s.Lookup(target, pair.DeliveryID, true)
	requireErr(t, err, nil)
	if lookup.Payload != nil || !bytes.Equal(original, f.data) {
		t.Fatal("legacy lookup rewrote bytes")
	}
	requireErr(t, s.Retarget(target, 2), nil)
	s = f.open(t)
	if s.current.Load().Version != 4 || s.Inbox()[0].DeliveryID != pair.DeliveryID || s.Inbox()[0].Payload != nil || s.Inbox()[0].AcceptedAt != sample {
		t.Fatal("upgrade relabeled legacy receipt")
	}
}

func TestMaximumRichInboxUsesCombinedPrivateLimitWithoutDropping(t *testing.T) {
	s, f, _ := start(t)
	for n := range MaxPairs {
		upper := s.Snapshot().Checkpoint
		upper.Event++
		e := richEvidence(upper)
		e.Facts.Label = strings.Repeat("&", noticetext.MaxLabelBytes)
		e.Facts.Tickets = make([]A.Positive, 16)
		for j := range e.Facts.Tickets {
			e.Facts.Tickets[j] = A.Positive(9007199254740976 + j)
		}
		i := prepare(t, s, e)
		if i.Payload.Kind != "complete" {
			t.Fatal("maximum rich fixture unexpectedly fell back")
		}
		accept(t, s, i, sample)
		requireErr(t, s.Finalize(i.TargetID, i.DeliveryID), nil)
		if len(s.Inbox()) != n+1 || len(f.data) > MaxStateBytes {
			t.Fatal("combined bounds or retained history violated")
		}
	}
	// Also combine the richest retained inbox with all legal historical gaps.
	// The fixed hard byte cap still applies to the complete private layout;
	// do not manufacture an invalid record just to make that cap reachable.
	full := *s.current.Load()
	for range MaxGaps {
		id, err := allocateID()
		requireErr(t, err, nil)
		upper := baseline
		full.Gaps = append(full.Gaps, Gap{ObservationID: id, Lower: baseline, Upper: &upper, Class: Gapped})
	}
	encoded, err := full.encode()
	requireErr(t, err, nil)
	if len(full.Inbox) != MaxPairs || len(full.Gaps) != MaxGaps || len(encoded) > MaxStateBytes {
		t.Fatal("full private layout exceeded fixed capacity")
	}
	t.Logf("64 rich pairs plus 64 historical gaps: %d bytes of %d", len(encoded), MaxStateBytes)
	upper := s.Snapshot().Checkpoint
	upper.Event++
	i := prepare(t, s, richEvidence(upper))
	original := bytes.Clone(f.data)
	writes := f.writes
	permit, err := s.Fence(i, sample, false)
	requireErr(t, err, ErrCapacity)
	if permit != nil || f.writes != writes || !bytes.Equal(original, f.data) || len(s.Inbox()) != 64 {
		t.Fatal("full inbox silently dropped or accepted history")
	}
}
