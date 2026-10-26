package recovery

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

const transactionLimit = 10 * time.Second
const stamp = "2006-01-02T15:04:05Z"

type boardState struct {
	identity  A.Identity
	revision  A.Number
	retention A.Retention
	version   A.Positive
	floors    A.Floors
	nextEvent uint64
}

func board(r writer.Read) (boardState, error) {
	var b boardState
	var h, a, o sql.NullString
	e := r.Scan("SELECT board_id,incarnation,revision,retention_version,history_days,activity_days,outcome_days,history_floor,activity_floor,outcome_floor,next_event FROM board WHERE singleton=1", nil, &b.identity.BoardID, &b.identity.Incarnation, &b.revision, &b.version, &b.retention.HistoryDays, &b.retention.ActivityDays, &b.retention.OutcomeDays, &h, &a, &o, &b.nextEvent)
	for _, v := range []struct {
		from sql.NullString
		to   **A.Timestamp
	}{{h, &b.floors.HistoryFloor}, {a, &b.floors.ActivityFloor}, {o, &b.floors.OutcomePayloadFloor}} {
		if v.from.Valid {
			t := A.Timestamp(v.from.String)
			*v.to = &t
		}
	}
	return b, e
}

type selection struct {
	inventory A.PreviewInventory
	hist      map[string]bool
	ops       map[string]bool
}

func historyID(b, i, id string, r uint64) string { return fmt.Sprintf("%s/%s/%s/%016d", b, i, id, r) }
func outcomeID(b, i, id string) string           { return b + "/" + i + "/" + id }
func idKey(i A.Identity) string                  { return string(i.BoardID) + "/" + string(i.Incarnation) }
func floor(t time.Time, d A.Days) (A.Timestamp, error) {
	v := t.Add(-time.Duration(d) * 24 * time.Hour)
	if v.Year() < 1 || v.Year() > 9999 {
		return "", errors.New("retention cutoff outside timestamp range")
	}
	return A.Timestamp(v.Format(stamp)), nil
}
func later(old *A.Timestamp, next A.Timestamp) *A.Timestamp {
	if old != nil && *old > next {
		v := *old
		return &v
	}
	return &next
}

// selectRecords uses the ORIGINAL immutable preview time, including at confirm.
// Expiry is exactly storedTime+days*86400 <= sampledTime (inclusive), expressed
// as storedTime <= sampledTime-days because every timestamp is a UTC second.
func selectRecords(r writer.Read, b boardState, command A.RecoveryCommand, input []byte, at A.Timestamp, exports []A.ByteFact, advancePolicy bool) (selection, error) {
	s := selection{hist: map[string]bool{}, ops: map[string]bool{}}
	v := A.PreviewInventory{History: []A.HistoryKey{}, Activity: []A.ActivityKey{}, Outcomes: []A.OutcomeKey{}, CoverageGaps: []A.GapSelection{}, ProposedFloors: b.floors, ProposedRetention: b.retention, ProposedRetentionVersion: b.version}
	if uint64(b.revision) >= contract.MaxInteger || b.nextEvent >= contract.MaxInteger {
		return s, fail("COUNTER_OVERFLOW")
	}
	v.ProposedBoardRevision = A.Positive(b.revision + 1)
	var hc, ac, oc A.Timestamp
	reason := A.Reason("cleared")
	switch command {
	case "history.clear":
		x, e := A.Decode[A.ClearInput](input)
		if e != nil {
			return s, e
		}
		if x.ThroughTime > at {
			return s, fail("INVALID_INPUT")
		}
		hc = x.ThroughTime
		oc = hc
	case "retention.set":
		x, e := A.Decode[A.Retention](input)
		if e != nil {
			return s, e
		}
		if advancePolicy && uint64(b.version) >= contract.MaxInteger {
			return s, fail("COUNTER_OVERFLOW")
		}
		v.ProposedRetention = x
		if advancePolicy {
			v.ProposedRetentionVersion++
		}
		reason = "expired"
		t, e := time.Parse(stamp, string(at))
		if e != nil {
			return s, e
		}
		if hc, e = floor(t, x.HistoryDays); e != nil {
			return s, e
		}
		if ac, e = floor(t, x.ActivityDays); e != nil {
			return s, e
		}
		if oc, e = floor(t, x.OutcomeDays); e != nil {
			return s, e
		}
		v.ProposedFloors = A.Floors{HistoryFloor: later(b.floors.HistoryFloor, hc), ActivityFloor: later(b.floors.ActivityFloor, ac), OutcomePayloadFloor: later(b.floors.OutcomePayloadFloor, oc)}
	default:
		return s, errors.New("unsupported recovery command")
	}
	gaps := map[string]A.GapSelection{}
	gap := func(origin A.Identity, class A.GapClass, cut A.Timestamp) {
		k := idKey(origin) + "/" + string(class)
		gaps[k] = A.GapSelection{Origin: origin, Class: class, Reason: reason, ThroughInclusive: cut}
	}
	rows, e := r.Query("SELECT origin_board,origin_incarnation,card_id,card_revision FROM card_revision WHERE state IS NOT NULL AND committed_at<=? ORDER BY origin_board,origin_incarnation,card_id,card_revision", string(hc))
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var k A.HistoryKey
		if e = rows.Scan(&k.Origin.BoardID, &k.Origin.Incarnation, &k.CardID, &k.CardRevision); e != nil {
			rows.Close()
			return s, e
		}
		v.History = append(v.History, k)
		s.hist[historyID(string(k.Origin.BoardID), string(k.Origin.Incarnation), string(k.CardID), uint64(k.CardRevision))] = true
		gap(k.Origin, "history", hc)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	if ac != "" {
		rows, e = r.Query("SELECT origin_board,origin_incarnation,event_id FROM activity WHERE at<=? ORDER BY origin_board,origin_incarnation,event_id", string(ac))
		if e != nil {
			return s, e
		}
		for rows.Next() {
			var k A.ActivityKey
			if e = rows.Scan(&k.Origin.BoardID, &k.Origin.Incarnation, &k.EventID); e != nil {
				rows.Close()
				return s, e
			}
			v.Activity = append(v.Activity, k)
			gap(k.Origin, "activity", ac)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return s, e
		}
	}
	rows, e = r.Query("SELECT origin_board,origin_incarnation,op_id FROM operation WHERE removed_at IS NULL AND terminal_at<=? ORDER BY origin_board,origin_incarnation,op_id", string(oc))
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var k A.OutcomeKey
		if e = rows.Scan(&k.Origin.BoardID, &k.Origin.Incarnation, &k.OpID); e != nil {
			rows.Close()
			return s, e
		}
		v.Outcomes = append(v.Outcomes, k)
		s.ops[outcomeID(string(k.Origin.BoardID), string(k.Origin.Incarnation), string(k.OpID))] = true
		gap(k.Origin, "outcome_payload", oc)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	for _, g := range gaps {
		v.CoverageGaps = append(v.CoverageGaps, g)
	}
	sort.Slice(v.CoverageGaps, func(i, j int) bool {
		a, c := v.CoverageGaps[i], v.CoverageGaps[j]
		return idKey(a.Origin)+"/"+string(a.Class) < idKey(c.Origin)+"/"+string(c.Class)
	})
	v.Counts = A.PreviewCounts{HistorySnapshots: A.Number(len(v.History)), ActivityEvents: A.Number(len(v.Activity)), OutcomePayloads: A.Number(len(v.Outcomes))}
	before, e := domainPins(r, nil)
	if e != nil {
		return s, e
	}
	after, e := domainPins(r, &s)
	if e != nil {
		return s, e
	}
	ex := map[A.Digest]A.Number{}
	for _, f := range exports {
		if e = addFact(ex, f); e != nil {
			return s, e
		}
		if n, ok := before[f.SHA256]; ok && n != f.ByteLength {
			return s, errors.New("export length disagreement")
		}
	}
	// Quotas apply to the distinct union, even if current domain and export
	// sets individually fit. Zero-length entries are preserved in all sets.
	union := map[A.Digest]A.Number{}
	for h, n := range before {
		union[h] = n
	}
	for h, n := range ex {
		if e = addFact(union, A.ByteFact{SHA256: h, ByteLength: n}); e != nil {
			return s, e
		}
	}
	if _, e = factSum(factList(union)); e != nil {
		return s, e
	}
	reclaim := map[A.Digest]A.Number{}
	for h, n := range before {
		_, a := after[h]
		_, x := ex[h]
		if !a && !x {
			reclaim[h] = n
		}
	}
	v.Bytes = A.PreviewBytes{DomainBefore: factList(before), DomainAfter: factList(after), ExportPinned: factList(ex), Reclaimable: factList(reclaim)}
	for _, p := range []struct {
		a []A.ByteFact
		n *A.Number
	}{{v.Bytes.DomainBefore, &v.Bytes.DomainBeforeBytes}, {v.Bytes.DomainAfter, &v.Bytes.DomainAfterBytes}, {v.Bytes.ExportPinned, &v.Bytes.ExportPinnedBytes}, {v.Bytes.Reclaimable, &v.Bytes.ReclaimableBytes}} {
		if *p.n, e = factSum(p.a); e != nil {
			return s, e
		}
	}
	s.inventory = v
	return s, A.Validate(v)
}
