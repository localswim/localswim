package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/hierarchy"
)

// HierarchyStatements is the complete additive physical5 -> physical6 extension.
func HierarchyStatements() []string {
	return []string{expand(`CREATE TABLE card_parent (
 incarnation TEXT NOT NULL CHECK({{u:incarnation}}),
 child_id TEXT NOT NULL CHECK({{u:child_id}}),
 parent_id TEXT NOT NULL CHECK({{u:parent_id}}),
 PRIMARY KEY(incarnation,child_id),
 CHECK(child_id<>parent_id),
 FOREIGN KEY(incarnation,child_id) REFERENCES card(incarnation,card_id),
 FOREIGN KEY(incarnation,parent_id) REFERENCES card(incarnation,card_id)
) STRICT`), `CREATE INDEX card_parent_by_parent ON card_parent(incarnation,parent_id,child_id)`, `CREATE TRIGGER card_parent_no_update BEFORE UPDATE ON card_parent BEGIN SELECT RAISE(ABORT,'replace parent through explicit delete and insert'); END`}
}
func validateHierarchy(ctx context.Context, c *sql.Conn, id Identity) error {
	parents := map[string]string{}
	rows, err := c.QueryContext(ctx, `SELECT c.card_id,coalesce(p.parent_id,'') FROM card c LEFT JOIN card_parent p ON p.incarnation=c.incarnation AND p.child_id=c.card_id WHERE c.incarnation=?`, id.Incarnation)
	if err != nil {
		return err
	}
	for rows.Next() {
		var child, parent string
		if err = rows.Scan(&child, &parent); err != nil {
			rows.Close()
			return err
		}
		parents[child] = parent
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = hierarchy.Validate(parents); err != nil {
		return err
	}
	// Historical parents belong to snapshot Origin, not target CreatedIn. Registry
	// and ticket retention establish known identity without requiring a current body.
	rows, err = c.QueryContext(ctx, `SELECT h.origin_board,h.origin_incarnation,h.card_id,h.state FROM card_revision h WHERE h.state IS NOT NULL`)
	if err != nil {
		return err
	}
	type historical struct {
		board, inc, child string
		state             []byte
	}
	states := []historical{}
	for rows.Next() {
		var h historical
		if err = rows.Scan(&h.board, &h.inc, &h.child, &h.state); err != nil {
			rows.Close()
			return err
		}
		states = append(states, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, h := range states {
		// The accepted baseline treats retained state as opaque valid JSON.
		// Absence means root: do not reinterpret or tighten its other fields.
		var fields map[string]json.RawMessage
		if json.Unmarshal(h.state, &fields) != nil {
			continue
		}
		raw, present := fields["parent"]
		if !present {
			continue
		}
		// A hierarchy-bearing extension must be strict JSON and an exact ref;
		// this does not validate unrelated legacy SnapshotState properties.
		if _, e := contract.DecodeJSON(h.state); e != nil {
			return errors.New("invalid historical parent")
		}
		parent, decodeErr := A.Decode[A.CardRef](raw)
		if decodeErr != nil {
			return errors.New("invalid historical parent")
		}
		p := &parent

		if !ValidUUID(string(p.CardID)) || string(p.BoardID) != h.board || string(p.Incarnation) != h.inc || string(p.CardID) == h.child {
			return errors.New("historical parent scope")
		}
		var n int
		if err = c.QueryRowContext(ctx, `SELECT count(*) FROM entity e JOIN ticket t ON t.incarnation=e.incarnation AND t.card_id=e.entity_id WHERE e.incarnation=? AND e.kind='card' AND e.entity_id=? AND e.origin_board=?`, id.Incarnation, p.CardID, p.BoardID).Scan(&n); err != nil || n != 1 {
			return errors.New("historical parent registry")
		}
	}
	return nil
}
