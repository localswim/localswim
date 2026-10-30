// Frozen accepted V2 physical layout five. Do not evolve with layout six.
package schema

import "fmt"

func StatementsV5() []string               { return statementsFromV5(ddl) }
func statementsFromV5(ddl string) []string { return statementsFrom(ddl) }
func guardsV5() []string {
	var out []string
	for _, v := range []struct{ table, id, kind string }{{"card", "card_id", "card"}, {"comment", "comment_id", "comment"}, {"link", "link_id", "link"}, {"attachment", "attachment_id", "attachment"}, {"approval", "approval_id", "approval"}} {
		out = append(out, fmt.Sprintf("CREATE TRIGGER %s_full_insert BEFORE INSERT ON %s BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM entity WHERE incarnation=NEW.incarnation AND kind='%s' AND entity_id=NEW.%s AND removed_at IS NULL) THEN RAISE(ABORT,'full entity registry mismatch') END; END", v.table, v.table, v.kind, v.id))
		out = append(out, fmt.Sprintf("CREATE TRIGGER %s_full_delete BEFORE DELETE ON %s BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM entity WHERE incarnation=OLD.incarnation AND kind='%s' AND entity_id=OLD.%s AND removed_at IS NULL) THEN RAISE(ABORT,'full entity requires authorized tombstone first') END; END", v.table, v.table, v.kind, v.id))
		if v.kind == "comment" || v.kind == "attachment" || v.kind == "approval" {
			out = append(out, fmt.Sprintf("CREATE TRIGGER %s_immutable BEFORE UPDATE ON %s BEGIN SELECT RAISE(ABORT,'immutable entity'); END", v.table, v.table))
		}
	}
	for _, table := range []string{"operation_reference", "byte_fact", "operation_full", "current_attachment_pin", "history_blob_pin", "operation_blob_pin", "revision_reference"} {
		out = append(out, fmt.Sprintf("CREATE TRIGGER %s_no_update BEFORE UPDATE ON %s BEGIN SELECT RAISE(ABORT,'replace references through explicit delete and insert'); END", table, table))
	}
	for _, table := range []string{"current_attachment_pin", "history_blob_pin", "operation_blob_pin"} {
		out = append(out, fmt.Sprintf("CREATE TRIGGER %s_quota AFTER INSERT ON %s BEGIN SELECT CASE WHEN (SELECT coalesce(sum(byte_length),0) FROM blob b WHERE EXISTS(SELECT 1 FROM current_attachment_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256) OR EXISTS(SELECT 1 FROM history_blob_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256) OR EXISTS(SELECT 1 FROM operation_blob_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256))>67108864 THEN RAISE(ABORT,'protected byte quota exceeded') END; END", table, table))
	}
	return out
}

var consistencyChecksV5 = []string{
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
