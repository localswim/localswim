package schema

import "fmt"

func guardsCurrent() []string {
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
