package boardapi

import (
	"database/sql"
	"sort"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/contract/noticetext"
	"github.com/localswim/localswim/internal/store/writer"
)

// These private projections deliberately exclude activity prose, mutable card
// state and operation_full. All associations are checked within the caller's
// admitted snapshot, using retained terminal operations and immutable references.
// BLOB lengths bound bytes before Go scans; SQLite TEXT lengths stop at NUL.
type noticeTextEvent struct {
	id       A.Number
	at       A.Timestamp
	command  string
	revision A.Number
	actor    A.UUID
	op       sql.NullString
	affected sql.NullString // recovery.expire only, bounded to one compact fact
}

func captureNoticeTextFacts(r writer.Read, identity A.Identity, lower, upper noticeinterval.Cut, label string, low, high A.Timestamp) (*noticetext.NoticeTextFacts, error) {
	var events []noticeTextEvent
	valid := true
	lastRevision := lower.Revision
	seenOperations := make(map[string]bool)
	err := searchRows(r, `SELECT event_id,CASE WHEN length(CAST(at AS BLOB))=20 THEN at ELSE '' END,
		CASE WHEN length(CAST(type AS BLOB))<=32 THEN type ELSE '' END,board_revision,
		CASE WHEN length(CAST(actor_id AS BLOB))=36 THEN actor_id END,
		CASE WHEN length(CAST(op_id AS BLOB))=36 THEN op_id END,
		CASE WHEN type='recovery.expire' AND length(CAST(affected AS BLOB))<=512 THEN affected END,
		op_id IS NULL
		FROM activity WHERE `+noticeOrigin+` AND event_id>? AND event_id<=? ORDER BY event_id LIMIT ?`,
		[]any{identity.Incarnation, identity.BoardID, identity.Incarnation, lower.Event, upper.Event, noticeExpectedLimit + 1}, func(rows *sql.Rows) error {
			var event noticeTextEvent
			var actor sql.NullString
			var nullOp bool
			if err := rows.Scan(&event.id, &event.at, &event.command, &event.revision, &actor, &event.op, &event.affected, &nullOp); err != nil {
				return err
			}
			event.actor = A.UUID(actor.String)
			valid = valid && len(events) < noticeExpectedLimit && actor.Valid && A.Validate(event.actor) == nil &&
				A.Validate(event.id) == nil && A.Validate(event.at) == nil && A.Validate(event.revision) == nil &&
				event.id == lower.Event+A.Number(len(events))+1 && event.revision > lastRevision && event.revision <= upper.Revision &&
				(nullOp == (event.command == "recovery.expire")) && (nullOp || (event.op.Valid && A.Validate(A.UUID(event.op.String)) == nil))
			if !nullOp {
				valid = valid && !seenOperations[event.op.String]
				seenOperations[event.op.String] = true
			}
			lastRevision = event.revision
			events = append(events, event)
			return nil
		})
	if err != nil {
		return nil, err
	}
	if !valid || len(events) == 0 || A.Number(len(events)) != upper.Event-lower.Event {
		return nil, nil
	}
	tickets := make(map[A.Positive]bool)
	for _, event := range events {
		if event.command == "recovery.expire" {
			refs, err := A.Decode[[]A.FactRef]([]byte(event.affected.String))
			if !event.affected.Valid || err != nil || len(refs) != 1 || refs[0].Kind != "board" ||
				refs[0].ID != identity.BoardID || refs[0].Revision == nil || *refs[0].Revision != event.revision {
				return nil, nil
			}
			continue
		}
		ticket, ok, err := noticeOperationTicket(r, identity, event)
		if err != nil || !ok {
			return nil, err
		}
		if ticket != 0 {
			tickets[ticket] = true
			if len(tickets) > noticetext.MaxTickets {
				return nil, nil
			}
		}
	}
	facts := &noticetext.NoticeTextFacts{Label: label, From: string(low), Through: string(high), Tickets: make([]A.Positive, 0, len(tickets))}
	for ticket := range tickets {
		facts.Tickets = append(facts.Tickets, ticket)
	}
	sort.Slice(facts.Tickets, func(i, j int) bool { return facts.Tickets[i] < facts.Tickets[j] })
	if noticetext.ValidateFacts(*facts) != nil {
		return nil, nil
	}
	return facts, nil
}

type noticeTextOperation struct {
	command, terminal, code, actor, at string
	targetKind, target                 sql.NullString
	expected, committed                sql.NullInt64
	expectedCard, committedCard        sql.NullInt64
}

func noticeOperationTicket(r writer.Read, identity A.Identity, event noticeTextEvent) (A.Positive, bool, error) {
	var op noticeTextOperation
	count := 0
	args := []any{identity.Incarnation, identity.BoardID, identity.Incarnation, event.op.String}
	err := searchRows(r, `SELECT CASE WHEN length(CAST(command AS BLOB))<=32 THEN command ELSE '' END,
		CASE WHEN length(CAST(terminal_class AS BLOB))<=16 THEN terminal_class ELSE '' END,
		CASE WHEN length(CAST(code AS BLOB))<=32 THEN code ELSE '' END,
		CASE WHEN length(CAST(actor_id AS BLOB))=36 THEN actor_id ELSE '' END,
		CASE WHEN length(CAST(terminal_at AS BLOB))=20 THEN terminal_at ELSE '' END,
		CASE WHEN target_kind IS NULL OR length(CAST(target_kind AS BLOB))<=16 THEN target_kind ELSE '' END,
		CASE WHEN target_id IS NULL OR length(CAST(target_id AS BLOB))=36 THEN target_id ELSE '' END,
		expected_revision,committed_revision,expected_card_revision,committed_card_revision
		FROM operation WHERE `+noticeOrigin+` AND op_id=? AND current_ledger=1 LIMIT 2`, args, func(rows *sql.Rows) error {
		count++
		return rows.Scan(&op.command, &op.terminal, &op.code, &op.actor, &op.at, &op.targetKind, &op.target,
			&op.expected, &op.committed, &op.expectedCard, &op.committedCard)
	})
	if err != nil {
		return 0, false, err
	}
	if count != 1 || op.command != event.command || op.terminal != "COMMITTED" || op.code != "OK" ||
		op.actor != string(event.actor) || op.at != string(event.at) || !op.expected.Valid || A.Validate(A.Number(op.expected.Int64)) != nil ||
		!op.committed.Valid || A.Validate(A.Positive(op.committed.Int64)) != nil || A.Number(op.committed.Int64) != event.revision || op.expected.Int64+1 != op.committed.Int64 {
		return 0, false, nil
	}
	kinds := noticeOperationKinds(op.command)
	if kinds == nil {
		return 0, false, nil
	}
	card := kinds[0] == "card"
	if card {
		if !op.committedCard.Valid || A.Validate(A.Positive(op.committedCard.Int64)) != nil {
			return 0, false, nil
		}
		if op.command == "card.create" {
			if op.targetKind.Valid || op.target.Valid || op.expectedCard.Valid || op.committedCard.Int64 != 1 {
				return 0, false, nil
			}
		} else if op.targetKind.String != "card" || !op.target.Valid || A.Validate(A.UUID(op.target.String)) != nil ||
			!op.expectedCard.Valid || A.Validate(A.Positive(op.expectedCard.Int64)) != nil || op.expectedCard.Int64+1 != op.committedCard.Int64 {
			return 0, false, nil
		}
	} else if op.expectedCard.Valid || op.committedCard.Valid ||
		(op.command != "grant.revoke" && (op.targetKind.Valid || op.target.Valid)) ||
		(op.command == "grant.revoke" && (op.targetKind.String != "grant" || !op.target.Valid || A.Validate(A.UUID(op.target.String)) != nil)) {
		return 0, false, nil
	}
	var refs []A.FactRef
	err = searchRows(r, `SELECT CASE WHEN length(CAST(kind AS BLOB))<=16 THEN kind ELSE '' END,
		CASE WHEN length(CAST(entity_id AS BLOB))=36 THEN entity_id ELSE '' END,revision
		FROM operation_reference WHERE `+noticeOrigin+` AND op_id=? ORDER BY kind,entity_id LIMIT 3`, args, func(rows *sql.Rows) error {
		var ref A.FactRef
		var revision sql.NullInt64
		if err := rows.Scan(&ref.Kind, &ref.ID, &revision); err != nil {
			return err
		}
		if revision.Valid {
			v := A.Number(revision.Int64)
			ref.Revision = &v
		}
		refs = append(refs, ref)
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	if len(refs) != len(kinds) {
		return 0, false, nil
	}
	seen := make(map[string]bool)
	var cardID A.UUID
	for _, ref := range refs {
		kind := string(ref.Kind)
		allowed := false
		for _, expected := range kinds {
			allowed = allowed || kind == expected
		}
		if !allowed || seen[kind] || A.Validate(ref) != nil {
			return 0, false, nil
		}
		seen[kind] = true
		switch kind {
		case "card":
			if ref.Revision == nil || *ref.Revision != A.Number(op.committedCard.Int64) ||
				(op.command != "card.create" && string(ref.ID) != op.target.String) {
				return 0, false, nil
			}
			cardID = ref.ID
		case "board":
			if ref.ID != identity.BoardID || ref.Revision == nil || *ref.Revision != event.revision {
				return 0, false, nil
			}
		case "workflow":
			if ref.Revision == nil || *ref.Revision < 2 {
				return 0, false, nil
			}
		default:
			if ref.Revision != nil || (kind == "grant" && op.command == "grant.revoke" && string(ref.ID) != op.target.String) {
				return 0, false, nil
			}
		}
	}
	for _, ref := range refs {
		var exists int
		var query string
		var values []any
		switch ref.Kind {
		case "comment", "attachment", "link", "approval":
			query = `SELECT EXISTS(SELECT 1 FROM entity WHERE incarnation=? AND kind=? AND entity_id=? AND card_id=?)`
			values = []any{identity.Incarnation, ref.Kind, ref.ID, cardID}
		case "grant":
			query = `SELECT EXISTS(SELECT 1 FROM grant_fact WHERE incarnation=? AND grant_id=?)`
			values = []any{identity.Incarnation, ref.ID}
		case "workflow":
			query = `SELECT EXISTS(SELECT 1 FROM workflow_policy WHERE incarnation=? AND workflow_id=? AND policy_version=?)`
			values = []any{identity.Incarnation, ref.ID, *ref.Revision}
		default:
			continue
		}
		if err := r.Scan(query, values, &exists); err != nil {
			return 0, false, err
		}
		if exists != 1 {
			return 0, false, nil
		}
	}
	if !card {
		return 0, true, nil
	}
	var ticket A.Positive
	count = 0
	err = searchRows(r, `SELECT t.ticket FROM ticket t JOIN entity e ON e.incarnation=t.incarnation AND e.kind='card' AND e.entity_id=t.card_id
		WHERE t.incarnation=? AND t.card_id=? AND t.kind='card' AND e.card_id=t.card_id LIMIT 2`,
		[]any{identity.Incarnation, cardID}, func(rows *sql.Rows) error {
			count++
			return rows.Scan(&ticket)
		})
	return ticket, err == nil && count == 1 && A.Validate(ticket) == nil, err
}

func noticeOperationKinds(command string) []string {
	switch command {
	case "card.create", "card.edit", "review-handoff":
		return []string{"card"}
	case "comment.add":
		return []string{"card", "comment"}
	case "attachment.add", "attachment.detach":
		return []string{"card", "attachment"}
	case "link.add", "link.remove":
		return []string{"card", "link"}
	case "approve":
		return []string{"card", "approval"}
	case "grant.create", "grant.revoke":
		return []string{"grant"}
	case "history.clear", "retention.set":
		return []string{"board"}
	case "workflow.edit":
		return []string{"workflow"}
	default:
		return nil
	}
}
