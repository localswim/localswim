package export

import (
	"database/sql"
	"fmt"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

// SQL constructs only exact DTO shapes, never a raw database dump. Each row is
// size-gated in SQLite before crossing the driver and decoded by the shared DTO
// validator. A per-document input budget bounds collection allocation; final R
// encoding is checked separately because indentation/escaping can expand it.
type projectionBudget struct{ document, total int }

func collection[T any](r writer.Read, budget *projectionBudget, q string, args ...any) ([]T, error) {
	out := []T{}
	// Preflight the complete collection in the pinned reader before retrieving
	// row text. Aggregate overflow or SQL failure refuses without partial staging.
	var declared, largest int64
	if e := r.Scan("SELECT coalesce(sum(length(CAST(value AS BLOB))),0),coalesce(max(length(CAST(value AS BLOB))),0) FROM ("+q+")", args, &declared, &largest); e != nil {
		return nil, e
	}
	if declared < 0 || largest < 0 || largest > MaxJSONBytes || declared > int64(MaxJSONBytes-budget.document) || declared > int64(MaxJSONTotalBytes-budget.total) {
		return nil, ErrLimit
	}
	rows, e := r.Query("SELECT length(CAST(value AS BLOB)),CASE WHEN length(CAST(value AS BLOB))<=33554432 THEN value END FROM ("+q+")", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var n int64
		var raw sql.NullString
		if e = rows.Scan(&n, &raw); e != nil {
			return nil, e
		}
		if n < 0 || n > MaxJSONBytes || !raw.Valid || budget.document > MaxJSONBytes-int(n) || budget.total > MaxJSONTotalBytes-int(n) {
			return nil, ErrLimit
		}
		budget.document += int(n)
		budget.total += int(n)
		v, e := A.Decode[T]([]byte(raw.String))
		if e != nil {
			return nil, fmt.Errorf("archive row: %w", e)
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if e = rows.Close(); e != nil {
		return nil, e
	}
	return out, nil
}

func origin(prefix string) string {
	return "json_object('boardId'," + prefix + "origin_board,'incarnation'," + prefix + "origin_incarnation)"
}
func ids(table, alias, kind string) string {
	where := "r.incarnation=" + alias + ".incarnation AND r.card_id=" + alias + ".card_id AND r.kind='" + kind + "'"
	if table == "revision_reference" {
		where += " AND r.origin_board=" + alias + ".origin_board AND r.origin_incarnation=" + alias + ".origin_incarnation AND r.card_revision=" + alias + ".card_revision"
	}
	return "(SELECT json_group_array(entity_id) FROM (SELECT r.entity_id FROM " + table + " r WHERE " + where + " ORDER BY r.entity_id))"
}
func removal(alias string) string {
	return "CASE WHEN " + alias + ".removed_at IS NULL THEN NULL ELSE json_object('reason'," + alias + ".removal_reason,'removedAt'," + alias + ".removed_at,'boardRevision'," + alias + ".removal_revision) END"
}
func stampPtr(s sql.NullString) *A.Timestamp {
	if !s.Valid {
		return nil
	}
	v := A.Timestamp(s.String)
	return &v
}

func project(r writer.Read, id A.Identity, revision A.Number) (b A.Board, h A.History, a A.Activity, o A.Outcomes, lineage []A.Lineage, err error) {
	b = A.Board{SchemaVersion: 1, Identity: id, Revision: revision, Coverage: A.Coverage{Gaps: []A.Gap{}}, Actors: []A.Actor{}, Cards: []A.Card{}, Comments: []A.Comment{}, Links: []A.Link{}, Attachments: []A.Attachment{}, Approvals: []A.Approval{}, Tombstones: []A.EntityTombstone{}}
	h = A.History{SchemaVersion: 1, Identity: id, Revision: revision, Records: []A.Snapshot{}}
	a = A.Activity{SchemaVersion: 1, Identity: id, Revision: revision, Events: []A.Event{}}
	o = A.Outcomes{SchemaVersion: 1, Identity: id, Revision: revision, Operations: []A.Operation{}, SourceArchives: []A.SourceArchive{}}
	var hf, af, of sql.NullString
	err = r.Scan("SELECT b.next_ticket,p.policy_version,b.retention_version,b.history_days,b.activity_days,b.outcome_days,b.history_floor,b.activity_floor,b.outcome_floor FROM board b JOIN policy p USING(incarnation) WHERE b.singleton=1", nil, &b.NextTicket, &b.PolicyVersion, &b.RetentionVersion, &b.Retention.HistoryDays, &b.Retention.ActivityDays, &b.Retention.OutcomeDays, &hf, &af, &of)
	if err != nil {
		return
	}
	b.Coverage.HistoryFloor = stampPtr(hf)
	b.Coverage.ActivityFloor = stampPtr(af)
	b.Coverage.OutcomePayloadFloor = stampPtr(of)
	budget := projectionBudget{}
	b.Coverage.Gaps, err = collection[A.Gap](r, &budget, "SELECT json_object('origin',"+origin("")+",'class',class,'reason',reason,'fromExclusive',from_exclusive,'throughInclusive',through_inclusive,'recordedAt',recorded_at,'boardRevision',board_revision) value FROM coverage_gap ORDER BY origin_board,origin_incarnation,class,coalesce(from_exclusive,''),through_inclusive,reason,board_revision,recorded_at")
	if err != nil {
		return
	}
	b.Actors, err = collection[A.Actor](r, &budget, "SELECT json_object('actorId',actor_id,'kind',kind,'label',label,'createdIn',"+origin("")+") value FROM actor ORDER BY actor_id")
	if err != nil {
		return
	}
	b.Cards, err = collection[A.Card](r, &budget, "SELECT json_patch(json_object('cardId',c.card_id,'ticket',t.ticket,'revision',c.revision,'title',c.title,'description',c.description,'priority',c.priority,'lane',c.lane,'archived',json(CASE c.archived WHEN 0 THEN 'false' ELSE 'true' END),'currentApprovalId',c.current_approval_id,'ownerId',c.owner_id,'commentIds',json("+ids("current_reference", "c", "comment")+"),'linkIds',json("+ids("current_reference", "c", "link")+"),'attachmentIds',json("+ids("current_reference", "c", "attachment")+"),'createdIn',"+origin("e.")+"),CASE WHEN p.parent_id IS NULL THEN '{}' ELSE json_object('parent',json_object('boardId',(SELECT board_id FROM board),'incarnation',c.incarnation,'cardId',p.parent_id)) END) value FROM card c JOIN ticket t USING(incarnation,card_id) JOIN entity e ON e.incarnation=c.incarnation AND e.kind='card' AND e.entity_id=c.card_id LEFT JOIN card_parent p ON p.incarnation=c.incarnation AND p.child_id=c.card_id ORDER BY t.ticket,c.card_id")
	if err != nil {
		return
	}
	b.Comments, err = collection[A.Comment](r, &budget, "SELECT json_object('commentId',c.comment_id,'cardId',c.card_id,'actorId',c.actor_id,'text',c.text,'createdAt',c.created_at,'createdIn',"+origin("e.")+") value FROM comment c JOIN entity e ON e.incarnation=c.incarnation AND e.kind='comment' AND e.entity_id=c.comment_id WHERE e.removed_at IS NULL ORDER BY c.comment_id")
	if err != nil {
		return
	}
	b.Links, err = collection[A.Link](r, &budget, "SELECT json_object('linkId',c.link_id,'cardId',c.card_id,'kind',c.kind,'target',json_object('boardId',c.target_board,'incarnation',c.target_incarnation,'cardId',c.target_card_id),'active',json(CASE c.active WHEN 0 THEN 'false' ELSE 'true' END),'createdAt',c.created_at,'createdIn',"+origin("e.")+") value FROM link c JOIN entity e ON e.incarnation=c.incarnation AND e.kind='link' AND e.entity_id=c.link_id WHERE e.removed_at IS NULL ORDER BY c.link_id")
	if err != nil {
		return
	}
	b.Attachments, err = collection[A.Attachment](r, &budget, "SELECT json_object('attachmentId',c.attachment_id,'cardId',c.card_id,'name',c.name,'mediaLabel',c.media_label,'sha256',c.sha256,'byteLength',c.byte_length,'createdAt',c.created_at,'createdIn',"+origin("e.")+") value FROM attachment c JOIN entity e ON e.incarnation=c.incarnation AND e.kind='attachment' AND e.entity_id=c.attachment_id WHERE e.removed_at IS NULL ORDER BY c.attachment_id")
	if err != nil {
		return
	}
	b.Approvals, err = collection[A.Approval](r, &budget, "SELECT json_object('approvalId',c.approval_id,'cardId',c.card_id,'actorId',c.actor_id,'reviewedCardRevision',c.reviewed_card_revision,'reviewedBoardRevision',c.reviewed_board_revision,'policyVersion',c.policy_version,'createdAt',c.created_at,'createdIn',"+origin("e.")+") value FROM approval c JOIN entity e ON e.incarnation=c.incarnation AND e.kind='approval' AND e.entity_id=c.approval_id ORDER BY c.approval_id")
	if err != nil {
		return
	}
	// Physical creator metadata may exist for links too; D02 exposes creator
	// attribution only for comment tombstones, including retained source facts.
	b.Tombstones, err = collection[A.EntityTombstone](r, &budget, "SELECT json_object('kind',kind,'id',entity_id,'cardId',card_id,'createdIn',"+origin("")+",'actorId',CASE WHEN kind='comment' THEN creator_id ELSE NULL END,'createdAt',created_at,'removedAt',removed_at,'reason',removal_reason) value FROM entity WHERE removed_at IS NOT NULL ORDER BY kind,entity_id")
	if err != nil {
		return
	}
	budget.document = 0
	h.Records, err = collection[A.Snapshot](r, &budget, "SELECT json_object('origin',"+origin("c.")+",'cardId',c.card_id,'cardRevision',c.card_revision,'committedAt',c.committed_at,'actorId',c.actor_id,'state',json(c.state),'references',json_object('commentIds',json("+ids("revision_reference", "c", "comment")+"),'linkIds',json("+ids("revision_reference", "c", "link")+"),'attachmentIds',json("+ids("revision_reference", "c", "attachment")+"),'approvalIds',json("+ids("revision_reference", "c", "approval")+")),'removal',"+removal("c")+") value FROM card_revision c ORDER BY c.origin_board,c.origin_incarnation,c.card_id,c.card_revision")
	if err != nil {
		return
	}
	budget.document = 0
	a.Events, err = collection[A.Event](r, &budget, "SELECT json_object('origin',"+origin("")+",'eventId',event_id,'actorId',actor_id,'opId',op_id,'type',type,'boardRevision',board_revision,'affected',json(affected),'at',at) value FROM activity ORDER BY origin_board,origin_incarnation,event_id")
	if err != nil {
		return
	}
	budget.document = 0
	var identities []A.Identity
	identities, err = collection[A.Identity](r, &budget, "SELECT json_object('boardId',origin_board,'incarnation',origin_incarnation) value FROM (SELECT origin_board,origin_incarnation FROM source_archive_group UNION SELECT origin_board,origin_incarnation FROM operation WHERE current_ledger=0) ORDER BY origin_board,origin_incarnation")
	if err != nil {
		return
	}
	groups := map[A.Identity]int{}
	for _, identity := range identities {
		groups[identity] = len(o.SourceArchives)
		o.SourceArchives = append(o.SourceArchives, A.SourceArchive{Identity: identity, Operations: []A.Operation{}})
	}
	var ops []A.Operation
	ops, err = collection[A.Operation](r, &budget, operationQuery)
	if err != nil {
		return
	}
	for _, op := range ops {
		if op.Origin == id {
			o.Operations = append(o.Operations, op)
		} else {
			index, ok := groups[op.Origin]
			if !ok {
				err = fmt.Errorf("source archive group unavailable")
				return
			}
			o.SourceArchives[index].Operations = append(o.SourceArchives[index].Operations, op)
		}
	}
	budget.document = 0
	lineage, err = collection[A.Lineage](r, &budget, "SELECT json_object('identity',json_object('boardId',source_board,'incarnation',source_incarnation),'manifestSha256',manifest_sha256,'revision',revision) value FROM lineage ORDER BY source_board,source_incarnation,manifest_sha256,revision")
	return
}

const operationQuery = `SELECT json_object(
 'origin',json_object('boardId',c.origin_board,'incarnation',c.origin_incarnation),
 'opId',c.op_id,'actorId',c.actor_id,'fingerprintVersion',c.fingerprint_version,'fingerprint',c.fingerprint,
 'command',c.command,'target',CASE c.target_kind WHEN 'card' THEN json_object('cardId',c.target_id) WHEN 'grant' THEN json_object('grantId',c.target_id) ELSE NULL END,
 'expectedRevision',c.expected_revision,'expectedCardRevision',c.expected_card_revision,
 'terminalClass',c.terminal_class,'code',c.code,'terminalAt',c.terminal_at,
 'committedRevision',c.committed_revision,'committedCardRevision',c.committed_card_revision,
 'resultRefs',json((SELECT json_group_array(json(v)) FROM (SELECT json_object('kind',r.kind,'id',r.entity_id,'revision',r.revision) v FROM operation_reference r WHERE r.incarnation=c.incarnation AND r.origin_board=c.origin_board AND r.origin_incarnation=c.origin_incarnation AND r.op_id=c.op_id ORDER BY r.kind,r.entity_id,r.revision))),
 'byteFacts',json((SELECT json_group_array(json(v)) FROM (SELECT json_object('sha256',r.sha256,'byteLength',r.byte_length) v FROM byte_fact r WHERE r.incarnation=c.incarnation AND r.origin_board=c.origin_board AND r.origin_incarnation=c.origin_incarnation AND r.op_id=c.op_id ORDER BY r.sha256))),
 'full',CASE WHEN f.op_id IS NULL THEN NULL ELSE json_object('intent',json(f.intent),'result',json(f.result)) END,
 'removal',CASE WHEN c.removed_at IS NULL THEN NULL ELSE json_object('reason',c.removal_reason,'removedAt',c.removed_at,'boardRevision',c.removal_revision) END
 ) value FROM operation c LEFT JOIN operation_full f USING(incarnation,origin_board,origin_incarnation,op_id)
 ORDER BY c.origin_board,c.origin_incarnation,c.op_id`
