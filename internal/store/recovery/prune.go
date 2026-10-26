package recovery

import (
	"encoding/json"
	"errors"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

func exec(w writer.Write, q string, args ...any) error { _, e := w.Exec(q, args...); return e }
func nullable(t *A.Timestamp) any {
	if t == nil {
		return nil
	}
	return string(*t)
}

// prune executes only inside an already admitted writer transaction. The caller
// owns the single board revision increment. Metadata survives each compaction;
// compact facts, actor/approval facts, tickets and current content never expire.
func prune(w writer.Write, s selection, at A.Timestamp, reason A.Reason, exports []A.ByteFact) error {
	v := s.inventory
	rev := uint64(v.ProposedBoardRevision)
	for _, k := range v.History {
		args := []any{string(k.Origin.BoardID), string(k.Origin.Incarnation), string(k.CardID), uint64(k.CardRevision)}
		if e := exec(w, "DELETE FROM history_blob_pin WHERE origin_board=? AND origin_incarnation=? AND card_id=? AND card_revision=?", args...); e != nil {
			return e
		}
		if e := exec(w, "UPDATE card_revision SET state=NULL,removed_at=?,removal_reason=?,removal_revision=? WHERE origin_board=? AND origin_incarnation=? AND card_id=? AND card_revision=? AND state IS NOT NULL", append([]any{string(at), string(reason), rev}, args...)...); e != nil {
			return e
		}
	}
	for _, k := range v.Outcomes {
		args := []any{string(k.Origin.BoardID), string(k.Origin.Incarnation), string(k.OpID)}
		if e := exec(w, "DELETE FROM operation_blob_pin WHERE origin_board=? AND origin_incarnation=? AND op_id=?", args...); e != nil {
			return e
		}
		if e := exec(w, "DELETE FROM operation_full WHERE origin_board=? AND origin_incarnation=? AND op_id=?", args...); e != nil {
			return e
		}
		if e := exec(w, "UPDATE operation SET removed_at=?,removal_reason=?,removal_revision=? WHERE origin_board=? AND origin_incarnation=? AND op_id=? AND removed_at IS NULL", append([]any{string(at), string(reason), rev}, args...)...); e != nil {
			return e
		}
	}
	for _, k := range v.Activity {
		if e := exec(w, "DELETE FROM activity WHERE origin_board=? AND origin_incarnation=? AND event_id=?", string(k.Origin.BoardID), string(k.Origin.Incarnation), uint64(k.EventID)); e != nil {
			return e
		}
	}
	for _, g := range v.CoverageGaps {
		if e := exec(w, "INSERT INTO coverage_gap(incarnation,origin_board,origin_incarnation,class,reason,from_exclusive,through_inclusive,recorded_at,board_revision) SELECT incarnation,?,?,?,?,?,?,?,? FROM board WHERE singleton=1", string(g.Origin.BoardID), string(g.Origin.Incarnation), string(g.Class), string(g.Reason), nullable(g.FromExclusive), string(g.ThroughInclusive), string(at), rev); e != nil {
			return e
		}
	}
	if e := exec(w, "UPDATE board SET history_days=?,activity_days=?,outcome_days=?,retention_version=?,history_floor=?,activity_floor=?,outcome_floor=? WHERE singleton=1", uint64(v.ProposedRetention.HistoryDays), uint64(v.ProposedRetention.ActivityDays), uint64(v.ProposedRetention.OutcomeDays), uint64(v.ProposedRetentionVersion), nullable(v.ProposedFloors.HistoryFloor), nullable(v.ProposedFloors.ActivityFloor), nullable(v.ProposedFloors.OutcomePayloadFloor)); e != nil {
		return e
	}
	if e := pruneEntities(w, at, reason); e != nil {
		return e
	}
	// Actual protected references, including concurrent export snapshots, decide
	// deletion. Never infer pinning from compact or refused byte_fact rows.
	return collectOrphanBytes(w, exports)
}
func collectOrphanBytes(w writer.Write, exports []A.ByteFact) error {
	remove, e := orphanBytes(w, exports)
	if e != nil {
		return e
	}
	for _, h := range remove {
		if e = exec(w, "DELETE FROM blob WHERE sha256=?", string(h)); e != nil {
			return e
		}
	}
	return nil
}
func orphanBytes(w writer.Read, exports []A.ByteFact) ([]A.Digest, error) {
	protected, e := domainPins(w, nil)
	if e != nil {
		return nil, e
	}
	for _, f := range exports {
		if e = addFact(protected, f); e != nil {
			return nil, e
		}
	}
	rows, e := w.Query("SELECT sha256,byte_length FROM blob ORDER BY sha256")
	if e != nil {
		return nil, e
	}
	var remove []A.Digest
	for rows.Next() {
		var h A.Digest
		var n A.Number
		if e = rows.Scan(&h, &n); e != nil {
			rows.Close()
			return nil, e
		}
		if want, ok := protected[h]; ok {
			if want != n {
				rows.Close()
				return nil, errors.New("protected byte length mismatch")
			}
		} else {
			remove = append(remove, h)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	return remove, nil
}

// Metadata needed by a surviving full COMMITTED outcome remains complete. Its
// exact intent is read only to identify references, never copied to diagnostics.
func committedEntities(r writer.Read) (map[string]bool, error) {
	keep := map[string]bool{}
	rows, e := r.Query("SELECT r.kind,r.entity_id FROM operation_reference r JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.terminal_class='COMMITTED' AND o.removed_at IS NULL")
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var k, id string
		if e = rows.Scan(&k, &id); e != nil {
			rows.Close()
			return nil, e
		}
		keep[k+"/"+id] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = r.Query("SELECT f.intent FROM operation_full f JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.terminal_class='COMMITTED'")
	if e != nil {
		return nil, e
	}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				kind := map[string]string{"cardId": "card", "commentId": "comment", "linkId": "link", "attachmentId": "attachment", "approvalId": "approval"}[k]
				if kind != "" {
					if id, ok := v.(string); ok {
						keep[kind+"/"+id] = true
					}
				}
				visit(v)
			}
		case []any:
			for _, v := range x {
				visit(v)
			}
		}
	}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		var v any
		if e = json.Unmarshal([]byte(raw), &v); e != nil {
			rows.Close()
			return nil, e
		}
		visit(v)
	}
	e = rows.Err()
	rows.Close()
	return keep, e
}
func pruneEntities(w writer.Write, at A.Timestamp, reason A.Reason) error {
	keep, e := committedEntities(w)
	if e != nil {
		return e
	}
	// An entity qualifies only through known removed records. No arbitrary
	// missing row is manufactured as a tombstone to repair invalid evidence.
	rows, e := w.Query(`SELECT e.kind,e.entity_id FROM entity e WHERE e.kind IN ('comment','link','attachment') AND e.removed_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM current_reference r WHERE r.incarnation=e.incarnation AND r.kind=e.kind AND r.entity_id=e.entity_id)
 AND NOT EXISTS(SELECT 1 FROM current_attachment_pin p WHERE p.incarnation=e.incarnation AND e.kind='attachment' AND p.attachment_id=e.entity_id)
 AND NOT EXISTS(SELECT 1 FROM revision_reference r JOIN card_revision h USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) WHERE r.incarnation=e.incarnation AND r.kind=e.kind AND r.entity_id=e.entity_id AND h.state IS NOT NULL)
 AND (EXISTS(SELECT 1 FROM revision_reference r JOIN card_revision h USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) WHERE r.incarnation=e.incarnation AND r.kind=e.kind AND r.entity_id=e.entity_id AND h.state IS NULL)
 OR EXISTS(SELECT 1 FROM operation_reference r JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE r.incarnation=e.incarnation AND r.kind=e.kind AND r.entity_id=e.entity_id AND o.terminal_class='COMMITTED' AND o.removed_at IS NOT NULL)) ORDER BY e.kind,e.entity_id`)
	if e != nil {
		return e
	}
	type candidate struct{ kind, id string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if e = rows.Scan(&c.kind, &c.id); e != nil {
			rows.Close()
			return e
		}
		if !keep[c.kind+"/"+c.id] {
			candidates = append(candidates, c)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, c := range candidates {
		if e = exec(w, "UPDATE entity SET removed_at=?,removal_reason=? WHERE kind=? AND entity_id=? AND removed_at IS NULL", string(at), string(reason), c.kind, c.id); e != nil {
			return e
		}
		table := map[string]string{"comment": "comment", "link": "link", "attachment": "attachment"}[c.kind]
		if e = exec(w, "DELETE FROM "+table+" WHERE "+c.kind+"_id=?", c.id); e != nil {
			return e
		}
	}
	return nil
}
func activity(w writer.Write, b boardState, actor string, op *string, command string, at A.Timestamp, refs []A.FactRef) error {
	raw, e := A.Canonical(refs)
	if e != nil {
		return e
	}
	var opID any
	if op != nil {
		opID = *op
	}
	if e = exec(w, "UPDATE board SET next_event=next_event+1 WHERE singleton=1"); e != nil {
		return e
	}
	return exec(w, "INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,?,?,?,?,?,?,?)", string(b.identity.Incarnation), string(b.identity.BoardID), string(b.identity.Incarnation), b.nextEvent, actor, opID, command, uint64(b.revision), string(at), string(raw))
}
