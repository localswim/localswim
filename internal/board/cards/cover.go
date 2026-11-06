package cards

import (
	"database/sql"
	"errors"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/coverimage"
	"github.com/localswim/localswim/internal/store/attachmentblob"
	"github.com/localswim/localswim/internal/store/writer"
)

func admitCover(r writer.Read, e envelope, card string) (*T.RefusalResult, error) {
	candidate := e.Payload.CoverAttachmentID.Value
	if candidate != nil {
		var hash string
		var length uint64
		err := r.Scan(`SELECT a.sha256,a.byte_length FROM attachment a
			JOIN current_reference r ON r.incarnation=a.incarnation AND r.card_id=a.card_id AND r.kind='attachment' AND r.entity_id=a.attachment_id
			JOIN entity e ON e.incarnation=a.incarnation AND e.card_id=a.card_id AND e.kind='attachment' AND e.entity_id=a.attachment_id
			WHERE a.incarnation=? AND a.card_id=? AND a.attachment_id=? AND e.removed_at IS NULL`, []any{e.Incarnation, card, *candidate}, &hash, &length)
		if errors.Is(err, sql.ErrNoRows) {
			return deny("NOT_FOUND"), nil
		}
		if err != nil {
			return nil, err
		}
		b, err := attachmentblob.Read(r, e.Incarnation, hash, length)
		if errors.Is(err, attachmentblob.ErrIntegrity) {
			return nil, writer.ErrIntegrity
		}
		if err != nil {
			return nil, err
		}
		if !coverimage.Eligible(b) {
			return deny("INVALID_INPUT"), nil
		}
	}
	prior, err := loadSnapshot(r, e, card)
	if err != nil {
		return nil, err
	}
	if sameOptionalUUID(candidate, prior.CoverAttachmentID) {
		return deny("NO_CHANGE"), nil
	}
	return nil, nil
}

func writeCover(w writer.Write, inc, card string, attachment *string) error {
	if attachment == nil {
		_, err := w.Exec("DELETE FROM card_cover WHERE incarnation=? AND card_id=?", inc, card)
		return err
	}
	_, err := w.Exec(`INSERT INTO card_cover(incarnation,card_id,attachment_id) VALUES(?,?,?)
		ON CONFLICT(incarnation,card_id) DO UPDATE SET attachment_id=excluded.attachment_id`, inc, card, *attachment)
	return err
}
