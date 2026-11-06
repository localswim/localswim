package schema

import (
	"context"
	"database/sql"
	"errors"
)

// CoverStatement is the complete additive physical11 -> physical12 extension.
// Exact current membership also supplies the transaction-local detach cascade.
func CoverStatement() string {
	return `CREATE TABLE card_cover (
 incarnation TEXT NOT NULL, card_id TEXT NOT NULL, attachment_id TEXT NOT NULL,
 kind TEXT NOT NULL DEFAULT 'attachment' CHECK(kind='attachment'),
 PRIMARY KEY(incarnation,card_id),
 FOREIGN KEY(incarnation,attachment_id) REFERENCES attachment(incarnation,attachment_id),
 FOREIGN KEY(incarnation,card_id,kind,attachment_id) REFERENCES current_reference(incarnation,card_id,kind,entity_id) ON DELETE CASCADE
) STRICT`
}

func ValidateV11(ctx context.Context, c *sql.Conn, id Identity) error {
	if err := validateLayout(ctx, c, id, 11, StatementsV11(), consistencyChecksV11, true); err != nil {
		return err
	}
	return validateWorkflow(ctx, c, id)
}

// Check the predecessor vocabulary independently of the shared decoders. The
// full-original table includes both ledgers and both terminal classes. Checking
// keys directly catches null and repeated keys without changing opaque JSON.
func validateV11Vocabulary(ctx context.Context, c *sql.Conn) error {
	var found bool
	err := c.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM card_revision h, json_each(h.state) field WHERE field.key='coverAttachmentId'
 UNION ALL
 SELECT 1 FROM operation_full f, json_each(f.intent) payload,
 json_each(CASE WHEN payload.type='object' THEN payload.value ELSE '{}' END) field
 WHERE payload.key='payload' AND field.key='coverAttachmentId'
)`).Scan(&found)
	if err != nil {
		return err
	}
	if found {
		return errors.New("unsupported physical predecessor cover vocabulary")
	}
	return nil
}

// Snapshot JSON is written before its revision references. Audit the complete
// state against only the owning snapshot's full attachment membership.
func validateHistoricalCovers(ctx context.Context, c *sql.Conn) error {
	var invalid bool
	err := c.QueryRowContext(ctx, expand(`SELECT EXISTS(
 SELECT 1 FROM card_revision h, json_each(h.state) cover
 WHERE cover.key='coverAttachmentId' AND (
   cover.type<>'text' OR NOT {{u:cover.atom}} OR length(CAST(cover.atom AS BLOB))<>36
   OR (SELECT count(*) FROM json_each(h.state) field WHERE field.key='coverAttachmentId')<>1
   OR NOT EXISTS(
     SELECT 1 FROM revision_reference r JOIN attachment a
     ON a.incarnation=r.incarnation AND a.attachment_id=r.entity_id AND a.card_id=r.card_id
     WHERE r.incarnation=h.incarnation AND r.origin_board=h.origin_board
       AND r.origin_incarnation=h.origin_incarnation AND r.card_id=h.card_id
       AND r.card_revision=h.card_revision AND r.kind='attachment' AND r.entity_id=cover.atom
   )
 )
)`)).Scan(&invalid)
	if err != nil {
		return err
	}
	if invalid {
		return errors.New("invalid historical cover membership")
	}
	return nil
}
