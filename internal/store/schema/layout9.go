// Frozen physical-nine capability boundary. Do not widen its workflow vocabulary.
package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ValidateV9 retains the exact pre-attention layout and guards JSON before any
// current shared decoder can admit newly introduced workflow members.
func ValidateV9(ctx context.Context, c *sql.Conn, id Identity) error {
	if err := validateLayout(ctx, c, id, 9, StatementsV9(), consistencyChecksV9, true); err != nil {
		return err
	}
	if err := validateV9Vocabulary(ctx, c); err != nil {
		return err
	}
	return validateWorkflow(ctx, c, id)
}

func validateV9Vocabulary(ctx context.Context, c *sql.Conn) error {
	rows, err := c.QueryContext(ctx, `SELECT definition FROM workflow_definition UNION ALL SELECT definition FROM workflow_policy WHERE definition IS NOT NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = validateV9WorkflowVocabulary(raw); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Only workflow candidates are interpreted. Other originals and legacy
	// snapshot state remain opaque at their established storage boundary.
	rows, err = c.QueryContext(ctx, `SELECT f.intent FROM operation_full f JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.command='workflow.edit'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		var original struct {
			Payload struct {
				Workflow json.RawMessage `json:"workflow"`
			} `json:"payload"`
		}
		if json.Unmarshal(raw, &original) == nil && len(original.Payload.Workflow) != 0 {
			if err = validateV9WorkflowVocabulary(original.Payload.Workflow); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func validateV9WorkflowVocabulary(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return errors.New("invalid frozen workflow JSON")
	}
	for key := range fields {
		switch key {
		case "profile", "workflowId", "revision", "lanes", "transitions", "actorRights":
		default:
			return errors.New("unsupported frozen workflow member")
		}
	}
	return nil
}

var consistencyChecksV9 = []string{
	`SELECT count(*) FROM operation_reference r WHERE r.kind='workflow' AND NOT EXISTS(SELECT 1 FROM workflow_policy p WHERE p.incarnation=r.incarnation AND p.workflow_id=r.entity_id AND p.policy_version=r.revision)`,
	// Current new kinds require full exact local peers. Historical/unavailable and
	// legacy rows keep their accepted evidence scope. The related index uses this
	// verified internal target to avoid conflating distinct foreign endpoint pairs.
	`SELECT count(*) FROM current_reference r JOIN link l ON l.incarnation=r.incarnation AND l.link_id=r.entity_id JOIN board b ON b.incarnation=l.incarnation WHERE r.kind='link' AND l.kind IN ('related','reference','duplicate') AND (l.target_board<>b.board_id OR l.target_incarnation<>b.incarnation OR l.internal_target_id IS NOT l.target_card_id OR NOT EXISTS(SELECT 1 FROM card c WHERE c.incarnation=l.incarnation AND c.card_id=l.target_card_id))`,
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
