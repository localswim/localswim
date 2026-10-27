package cards

import (
	"database/sql"
	"encoding/json"
	"errors"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

// Capture prior values before clearing approval or changing any supplied field.
func loadSnapshot(r writer.Read, e envelope, id string) (A.SnapshotState, error) {
	var s A.SnapshotState
	var archived int
	var approval, owner sql.NullString
	err := r.Scan(`SELECT t.ticket,c.title,c.description,c.priority,c.lane,c.archived,c.current_approval_id,c.owner_id FROM card c JOIN ticket t USING(incarnation,card_id) WHERE c.incarnation=? AND c.card_id=?`, []any{e.Incarnation, id}, &s.Ticket, &s.Title, &s.Description, &s.Priority, &s.Lane, &archived, &approval, &owner)
	s.Archived = archived != 0
	if owner.Valid {
		id := A.UUID(owner.String)
		s.OwnerID = &id
	}
	if approval.Valid {
		a := A.UUID(approval.String)
		s.CurrentApprovalID = &a
	}
	return s, err
}

func writeSnapshot(w writer.Write, e envelope, id string, revision A.Positive, s A.SnapshotState) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// Retained operation metadata survives payload pruning and restore. It is
	// historical provenance only; current authority still comes from G01.
	rows, err := w.Query(`SELECT o.origin_board,o.origin_incarnation,o.actor_id,o.terminal_at FROM operation o JOIN operation_reference r USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.incarnation=? AND o.terminal_class='COMMITTED' AND o.committed_card_revision=? AND r.kind='card' AND r.entity_id=? AND r.revision=?`, e.Incarnation, uint64(revision), id, uint64(revision))
	if err != nil {
		return err
	}
	var board, inc, actor, committed string
	count := 0
	for rows.Next() {
		count++
		if err = rows.Scan(&board, &inc, &actor, &committed); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if count != 1 {
		return errors.New("unique prior revision provenance unavailable")
	}
	if _, err = w.Exec(`INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,?,?,?,?)`, e.Incarnation, board, inc, id, uint64(revision), actor, committed, string(raw)); err != nil {
		return err
	}
	if _, err = w.Exec(`INSERT INTO revision_reference(incarnation,origin_board,origin_incarnation,card_id,card_revision,kind,entity_id) SELECT incarnation,?,?,card_id,?,kind,entity_id FROM current_reference WHERE incarnation=? AND card_id=?`, board, inc, uint64(revision), e.Incarnation, id); err != nil {
		return err
	}
	if s.CurrentApprovalID != nil {
		if _, err = w.Exec(`INSERT INTO revision_reference VALUES(?,?,?,?,?,'approval',?)`, e.Incarnation, board, inc, id, uint64(revision), string(*s.CurrentApprovalID)); err != nil {
			return err
		}
	}
	_, err = w.Exec(`INSERT INTO history_blob_pin(incarnation,origin_board,origin_incarnation,card_id,card_revision,attachment_id,sha256,byte_length) SELECT r.incarnation,?,?,r.card_id,?,a.attachment_id,a.sha256,a.byte_length FROM current_reference r JOIN attachment a ON a.incarnation=r.incarnation AND a.attachment_id=r.entity_id WHERE r.incarnation=? AND r.card_id=? AND r.kind='attachment'`, board, inc, uint64(revision), e.Incarnation, id)
	return err
}
