// Package relationships admits and writes source-owned relationship facts inside
// the existing sole-writer transaction. Reciprocal views never create facts.
package relationships

import (
	"crypto/rand"
	"fmt"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

type fact struct {
	id, owner, kind string
	target          contract.CardRef
}

// Plan preserves matching IDs, removes omitted facts and creates only new facts.
// It is used only in the snapshot that produced it, never across transactions.
type Plan struct {
	remove []string
	add    []contract.RelationshipSpec
}

func (p Plan) Changed() bool { return len(p.remove) != 0 || len(p.add) != 0 }

func validKind(k string) bool {
	switch k {
	case "blocking", "informational", "related", "reference", "duplicate":
		return true
	}
	return false
}

func key(k string, t contract.CardRef) string {
	return k + "/" + t.BoardID + "/" + t.Incarnation + "/" + t.CardID
}

// Prepare admits a complete replacement (replace=true) or one additive fact.
// Existing matching members are conserved without re-admitting historical refs.
// A nonempty code is a domain refusal; errors are storage failures.
func Prepare(r writer.Read, board, inc, source string, specs []contract.RelationshipSpec, replace bool) (Plan, string, error) {
	var p Plan
	if len(specs) > 100 {
		return p, "INVALID_INPUT", nil
	}
	seen := map[string]bool{}
	for _, s := range specs {
		if !validKind(s.Kind) || !schema.ValidUUID(s.Target.BoardID) || !schema.ValidUUID(s.Target.Incarnation) || !schema.ValidUUID(s.Target.CardID) || seen[key(s.Kind, s.Target)] {
			return p, "INVALID_INPUT", nil
		}
		seen[key(s.Kind, s.Target)] = true
	}
	rows, err := r.Query("SELECT link_id,card_id,kind,target_board,target_incarnation,target_card_id FROM link WHERE incarnation=? AND active=1", inc)
	if err != nil {
		return p, "", err
	}
	var all []fact
	owned := map[string]fact{}
	for rows.Next() {
		var f fact
		if err = rows.Scan(&f.id, &f.owner, &f.kind, &f.target.BoardID, &f.target.Incarnation, &f.target.CardID); err != nil {
			rows.Close()
			return p, "", err
		}
		all = append(all, f)
		if f.owner == source {
			owned[key(f.kind, f.target)] = f
		}
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return p, "", err
	}
	if closeErr != nil {
		return p, "", closeErr
	}
	if replace {
		for k, f := range owned {
			if !seen[k] {
				p.remove = append(p.remove, f.id)
			}
		}
	}
	for _, s := range specs {
		if _, exists := owned[key(s.Kind, s.Target)]; exists && replace {
			continue
		}
		if s.Target.BoardID != board || s.Target.Incarnation != inc {
			return Plan{}, "INVALID_INPUT", nil
		}
		if s.Target.CardID == source {
			return Plan{}, "SELF_LINK", nil
		}
		var n int
		if err := r.Scan("SELECT count(*) FROM card WHERE incarnation=? AND card_id=?", []any{inc, s.Target.CardID}, &n); err != nil {
			return Plan{}, "", err
		}
		if n != 1 {
			return Plan{}, "NOT_FOUND", nil
		}
		// Add preserves legacy self/missing-target precedence before the equal
		// pair refusal. Replacement alone may conserve a historical member.
		if _, exists := owned[key(s.Kind, s.Target)]; exists {
			return Plan{}, "LINK_ALREADY_EXISTS", nil
		}
		if s.Kind == "related" {
			for _, f := range all {
				if f.kind == "related" && f.owner == s.Target.CardID && f.target == (contract.CardRef{BoardID: board, Incarnation: inc, CardID: source}) {
					return Plan{}, "LINK_ALREADY_EXISTS", nil
				}
			}
		}
		p.add = append(p.add, s)
	}
	// Construct the final graph before checking any newly introduced edge. Only
	// new edges are constrained: unrelated edits conserve accepted legacy cycles.
	final := make([]fact, 0, len(all)+len(p.add))
	for _, f := range all {
		if replace && f.owner == source && !seen[key(f.kind, f.target)] {
			continue
		}
		final = append(final, f)
	}
	for _, s := range p.add {
		final = append(final, fact{owner: source, kind: s.Kind, target: s.Target})
	}
	for _, s := range p.add {
		if s.Kind != "blocking" && s.Kind != "duplicate" {
			continue
		}
		if s.Kind == "duplicate" {
			n := 0
			for _, f := range final {
				if f.owner == source && f.kind == "duplicate" {
					n++
				}
			}
			if n > 1 {
				return Plan{}, "INVALID_INPUT", nil
			}
		}
		adj := map[string][]string{}
		for _, f := range final {
			if f.kind == s.Kind && f.target.BoardID == board && f.target.Incarnation == inc {
				adj[f.owner] = append(adj[f.owner], f.target.CardID)
			}
		}
		visited := map[string]bool{}
		queue := []string{s.Target.CardID}
		for len(queue) > 0 {
			id := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			if id == source {
				if s.Kind == "blocking" {
					return Plan{}, "BLOCKING_CYCLE", nil
				}
				return Plan{}, "DUPLICATE_CYCLE", nil
			}
			if !visited[id] {
				visited[id] = true
				queue = append(queue, adj[id]...)
			}
		}
	}
	return p, "", nil
}

// Apply writes admitted facts after the caller has captured its prior snapshot.
// The caller owns the single card revision/activity and terminal result shape.
func (p Plan) Apply(w writer.Write, board, inc, source, actor string, at A.Timestamp) ([]string, error) {
	for _, id := range p.remove {
		if _, err := w.Exec("UPDATE link SET active=0 WHERE incarnation=? AND link_id=?", inc, id); err != nil {
			return nil, err
		}
		if _, err := w.Exec("DELETE FROM current_reference WHERE incarnation=? AND card_id=? AND kind='link' AND entity_id=?", inc, source, id); err != nil {
			return nil, err
		}
	}
	var ids []string
	for _, s := range p.add {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		b[6], b[8] = b[6]&15|64, b[8]&63|128
		id := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
		if _, err := w.Exec("INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at) VALUES(?,'link',?,?,?,?,?,?)", inc, id, source, board, inc, actor, string(at)); err != nil {
			return nil, err
		}
		if _, err := w.Exec("INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,?,?,?,?,?,1,?)", inc, id, source, s.Kind, s.Target.BoardID, s.Target.Incarnation, s.Target.CardID, s.Target.CardID, string(at)); err != nil {
			return nil, err
		}
		if _, err := w.Exec("INSERT INTO current_reference VALUES(?,?,'link',?)", inc, source, id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
