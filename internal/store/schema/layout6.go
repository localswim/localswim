// Frozen accepted physical-six validation. Do not evolve with the current layout.
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

func StatementsV6() []string { return frozenStatements(frozenSix) }

func validateHierarchyV6(ctx context.Context, c *sql.Conn, id Identity) error {
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

var consistencyChecksV6 = []string{
	`SELECT count(*) FROM source_archive_group g JOIN board b USING(incarnation) WHERE g.origin_board<>b.board_id OR g.origin_incarnation=b.incarnation`,
	`SELECT count(*) FROM cli_credential c WHERE NOT EXISTS(SELECT 1 FROM board b WHERE b.board_id=c.board_id AND b.incarnation=c.incarnation) OR NOT EXISTS(SELECT 1 FROM grant_record g WHERE g.incarnation=c.incarnation AND g.grant_id=c.grant_id AND g.actor_id=c.actor_id)`,
	`SELECT count(*) FROM entity e WHERE (e.removed_at IS NULL) <> CASE e.kind WHEN 'card' THEN EXISTS(SELECT 1 FROM card x WHERE x.incarnation=e.incarnation AND x.card_id=e.entity_id) WHEN 'comment' THEN EXISTS(SELECT 1 FROM comment x WHERE x.incarnation=e.incarnation AND x.comment_id=e.entity_id) WHEN 'link' THEN EXISTS(SELECT 1 FROM link x WHERE x.incarnation=e.incarnation AND x.link_id=e.entity_id) WHEN 'attachment' THEN EXISTS(SELECT 1 FROM attachment x WHERE x.incarnation=e.incarnation AND x.attachment_id=e.entity_id) WHEN 'approval' THEN EXISTS(SELECT 1 FROM approval x WHERE x.incarnation=e.incarnation AND x.approval_id=e.entity_id) END`,
	`SELECT count(*) FROM card c JOIN board b USING(incarnation) WHERE c.revision>b.revision OR b.revision=0`,
	`SELECT count(*) FROM ticket t JOIN board b USING(incarnation) WHERE t.ticket>=b.next_ticket`,
	`SELECT count(*) FROM card_revision h JOIN card c USING(incarnation,card_id) WHERE h.card_revision>=c.revision`,
	`SELECT count(*) FROM current_reference r JOIN entity e ON e.incarnation=r.incarnation AND e.kind=r.kind AND e.entity_id=r.entity_id WHERE e.removed_at IS NOT NULL`,
	`SELECT count(*) FROM revision_reference r JOIN card_revision h USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) JOIN entity e ON e.incarnation=r.incarnation AND e.kind=r.kind AND e.entity_id=r.entity_id WHERE e.card_id<>r.card_id OR (h.state IS NOT NULL AND e.removed_at IS NOT NULL)`,
	`SELECT count(*) FROM operation o JOIN board b USING(incarnation) WHERE (o.current_ledger=1)<>(o.origin_board=b.board_id AND o.origin_incarnation=b.incarnation) OR (o.removed_at IS NULL)<>EXISTS(SELECT 1 FROM operation_full f WHERE f.incarnation=o.incarnation AND f.origin_board=o.origin_board AND f.origin_incarnation=o.origin_incarnation AND f.op_id=o.op_id) OR (o.command='attachment.add')<>EXISTS(SELECT 1 FROM byte_fact f WHERE f.incarnation=o.incarnation AND f.origin_board=o.origin_board AND f.origin_incarnation=o.origin_incarnation AND f.op_id=o.op_id)`,
	`SELECT count(*) FROM operation_reference r JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.terminal_class<>'COMMITTED'`,
	`SELECT count(*) FROM operation_blob_pin p JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) JOIN attachment a USING(incarnation,attachment_id) LEFT JOIN byte_fact f USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.terminal_class<>'COMMITTED' OR o.command<>'attachment.add' OR o.removed_at IS NOT NULL OR a.card_id<>o.target_id OR a.sha256<>p.sha256 OR a.byte_length<>p.byte_length OR f.sha256 IS NULL OR f.sha256<>p.sha256 OR f.byte_length<>p.byte_length`,
	`SELECT count(*) FROM operation o WHERE o.terminal_class='COMMITTED' AND o.command='attachment.add' AND o.removed_at IS NULL AND NOT EXISTS(SELECT 1 FROM operation_blob_pin p WHERE p.incarnation=o.incarnation AND p.origin_board=o.origin_board AND p.origin_incarnation=o.origin_incarnation AND p.op_id=o.op_id)`,
	`SELECT count(*) FROM current_reference r WHERE r.kind='attachment' AND NOT EXISTS(SELECT 1 FROM current_attachment_pin p WHERE p.incarnation=r.incarnation AND p.attachment_id=r.entity_id)`,
	`SELECT count(*) FROM current_attachment_pin p JOIN attachment a USING(incarnation,attachment_id) WHERE p.sha256<>a.sha256 OR p.byte_length<>a.byte_length OR NOT EXISTS(SELECT 1 FROM current_reference r WHERE r.incarnation=p.incarnation AND r.kind='attachment' AND r.entity_id=p.attachment_id)`,
	`SELECT count(*) FROM revision_reference r JOIN card_revision h USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) WHERE r.kind='attachment' AND h.state IS NOT NULL AND NOT EXISTS(SELECT 1 FROM history_blob_pin p WHERE p.incarnation=r.incarnation AND p.origin_board=r.origin_board AND p.origin_incarnation=r.origin_incarnation AND p.card_id=r.card_id AND p.card_revision=r.card_revision AND p.attachment_id=r.entity_id)`,
	`SELECT count(*) FROM history_blob_pin p JOIN card_revision h USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) JOIN attachment a USING(incarnation,attachment_id) WHERE h.state IS NULL OR a.card_id<>p.card_id OR a.sha256<>p.sha256 OR a.byte_length<>p.byte_length`,
	`SELECT count(*) FROM (SELECT incarnation,sha256 FROM (SELECT incarnation,sha256,byte_length FROM blob UNION ALL SELECT incarnation,sha256,byte_length FROM attachment UNION ALL SELECT incarnation,sha256,byte_length FROM byte_fact) GROUP BY incarnation,sha256 HAVING min(byte_length)<>max(byte_length))`,
	`SELECT CASE WHEN coalesce(sum(byte_length),0)>67108864 THEN 1 ELSE 0 END FROM blob b WHERE EXISTS(SELECT 1 FROM current_attachment_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256) OR EXISTS(SELECT 1 FROM history_blob_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256) OR EXISTS(SELECT 1 FROM operation_blob_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256)`,
}
