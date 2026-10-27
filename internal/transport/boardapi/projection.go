package boardapi

import (
	"database/sql"
	"errors"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

func rows(r writer.Read, q string, args []any, fn func(*sql.Rows) error) error {
	rs, e := r.Query(q, args...)
	if e != nil {
		return e
	}
	for rs.Next() {
		if e = fn(rs); e != nil {
			break
		}
	}
	return errors.Join(e, rs.Err(), rs.Close())
}
func stampPtr(v sql.NullString) *A.Timestamp {
	if !v.Valid {
		return nil
	}
	s := A.Timestamp(v.String)
	return &s
}
func uuidPtr(v sql.NullString) *A.UUID {
	if !v.Valid {
		return nil
	}
	s := A.UUID(v.String)
	return &s
}

type projection struct {
	board      T.BoardView
	records    []A.Snapshot
	needed     map[string]map[A.UUID]bool
	actors     map[A.UUID]bool
	tombstones []A.EntityTombstone
}

func (p *projection) need(kind string, id A.UUID) { p.needed[kind][id] = true }

// project emits only exact accepted DTOs, all from the caller's current read
// transaction. It never substitutes empty results after a failed query/decode.
func project(r writer.Read, q route) (any, error) {
	p := projection{board: T.BoardView{Coverage: A.Coverage{Gaps: []A.Gap{}}, Actors: []A.Actor{}, Cards: []A.Card{}, Comments: []A.Comment{}, Links: []A.Link{}, Attachments: []A.Attachment{}, Approvals: []A.Approval{}}, records: []A.Snapshot{}, needed: map[string]map[A.UUID]bool{}, actors: map[A.UUID]bool{}, tombstones: []A.EntityTombstone{}}
	for _, kind := range []string{"comment", "link", "attachment", "approval"} {
		p.needed[kind] = map[A.UUID]bool{}
	}
	var hf, af, of sql.NullString
	e := r.Scan(`SELECT b.board_id,b.incarnation,b.revision,p.policy_version,b.history_days,b.activity_days,b.outcome_days,b.retention_version,b.history_floor,b.activity_floor,b.outcome_floor FROM board b JOIN policy p USING(incarnation) WHERE b.singleton=1 AND b.board_id=?`, []any{q.board}, &p.board.Identity.BoardID, &p.board.Identity.Incarnation, &p.board.Revision, &p.board.PolicyVersion, &p.board.Retention.HistoryDays, &p.board.Retention.ActivityDays, &p.board.Retention.OutcomeDays, &p.board.RetentionVersion, &hf, &af, &of)
	if e != nil {
		return nil, e
	}
	p.board.Coverage.HistoryFloor = stampPtr(hf)
	p.board.Coverage.ActivityFloor = stampPtr(af)
	p.board.Coverage.OutcomePayloadFloor = stampPtr(of)
	inc := string(p.board.Identity.Incarnation)
	e = rows(r, `SELECT origin_board,origin_incarnation,class,reason,from_exclusive,through_inclusive,recorded_at,board_revision FROM coverage_gap WHERE incarnation=? ORDER BY origin_board,origin_incarnation,class,from_exclusive,through_inclusive,reason,board_revision,recorded_at`, []any{inc}, func(rs *sql.Rows) error {
		var g A.Gap
		var from sql.NullString
		e := rs.Scan(&g.Origin.BoardID, &g.Origin.Incarnation, &g.Class, &g.Reason, &from, &g.ThroughInclusive, &g.RecordedAt, &g.BoardRevision)
		g.FromExclusive = stampPtr(from)
		p.board.Coverage.Gaps = append(p.board.Coverage.Gaps, g)
		return e
	})
	if e != nil {
		return nil, e
	}
	e = rows(r, `SELECT c.card_id,t.ticket,c.revision,c.title,c.description,c.priority,c.lane,c.archived,c.current_approval_id,c.owner_id,e.origin_board,e.origin_incarnation FROM card c JOIN ticket t USING(incarnation,card_id) JOIN entity e ON e.incarnation=c.incarnation AND e.kind='card' AND e.entity_id=c.card_id WHERE c.incarnation=? ORDER BY t.ticket,c.card_id`, []any{inc}, func(rs *sql.Rows) error {
		var c A.Card
		var approval, owner sql.NullString
		var archived int
		e := rs.Scan(&c.CardID, &c.Ticket, &c.Revision, &c.Title, &c.Description, &c.Priority, &c.Lane, &archived, &approval, &owner, &c.CreatedIn.BoardID, &c.CreatedIn.Incarnation)
		if q.card != "" && string(c.CardID) != q.card {
			return e
		}
		c.Archived = archived != 0
		c.CurrentApprovalID = uuidPtr(approval)
		c.OwnerID = uuidPtr(owner)
		if q.kind != "history" && c.OwnerID != nil {
			p.actors[*c.OwnerID] = true
		}
		c.CommentIDs = []A.UUID{}
		c.LinkIDs = []A.UUID{}
		c.AttachmentIDs = []A.UUID{}
		p.board.Cards = append(p.board.Cards, c)
		return e
	})
	if e != nil {
		return nil, e
	}
	if q.card != "" && len(p.board.Cards) != 1 {
		return nil, sql.ErrNoRows
	}
	if q.kind == "history" {
		if e = p.history(r, inc, q.card); e != nil {
			return nil, e
		}
	} else {
		for j := range p.board.Cards {
			c := &p.board.Cards[j]
			e = rows(r, `SELECT kind,entity_id FROM current_reference WHERE incarnation=? AND card_id=? ORDER BY kind,entity_id`, []any{inc, string(c.CardID)}, func(rs *sql.Rows) error {
				var kind string
				var id A.UUID
				if e := rs.Scan(&kind, &id); e != nil {
					return e
				}
				p.need(kind, id)
				switch kind {
				case "comment":
					c.CommentIDs = append(c.CommentIDs, id)
				case "link":
					c.LinkIDs = append(c.LinkIDs, id)
				case "attachment":
					c.AttachmentIDs = append(c.AttachmentIDs, id)
				}
				return nil
			})
			if e != nil {
				return nil, e
			}
			if c.CurrentApprovalID != nil {
				p.need("approval", *c.CurrentApprovalID)
			}
		}
	}
	if q.kind == "board" {
		if e = rows(r, `SELECT actor_id FROM actor WHERE incarnation=?`, []any{inc}, func(rs *sql.Rows) error {
			var id A.UUID
			if e := rs.Scan(&id); e != nil {
				return e
			}
			p.actors[id] = true
			return nil
		}); e != nil {
			return nil, e
		}
	}
	if e = p.entities(r, inc); e != nil {
		return nil, e
	}
	if q.kind == "history" {
		return T.HistoryRead{APIVersion: 1, Scope: "history", Result: T.HistoryView{Identity: p.board.Identity, Revision: p.board.Revision, CardID: A.UUID(q.card), CardRevision: p.board.Cards[0].Revision, Coverage: p.board.Coverage, Records: p.records, Actors: p.board.Actors, Comments: p.board.Comments, Links: p.board.Links, Attachments: p.board.Attachments, Approvals: p.board.Approvals, Tombstones: p.tombstones}}, nil
	}
	if q.kind == "card" {
		refs := []T.HistoryReference{}
		e = rows(r, `SELECT origin_board,origin_incarnation,card_id,card_revision,committed_at,state IS NOT NULL FROM card_revision WHERE incarnation=? AND card_id=? ORDER BY origin_board,origin_incarnation,card_id,card_revision`, []any{inc, q.card}, func(rs *sql.Rows) error {
			var ref T.HistoryReference
			e := rs.Scan(&ref.Origin.BoardID, &ref.Origin.Incarnation, &ref.CardID, &ref.CardRevision, &ref.CommittedAt, &ref.Available)
			refs = append(refs, ref)
			return e
		})
		if e != nil {
			return nil, e
		}
		return T.CardRead{APIVersion: 1, Scope: "card", Result: T.CardView{Identity: p.board.Identity, Revision: p.board.Revision, Card: p.board.Cards[0], Actors: p.board.Actors, Comments: p.board.Comments, Links: p.board.Links, Attachments: p.board.Attachments, Approvals: p.board.Approvals, HistoryReferences: refs, Coverage: p.board.Coverage}}, nil
	}
	return T.BoardRead{APIVersion: 1, Scope: "board", Result: p.board}, nil
}

func (p *projection) history(r writer.Read, inc, card string) error {
	e := rows(r, `SELECT origin_board,origin_incarnation,card_id,card_revision,committed_at,actor_id,state,removed_at,removal_reason,removal_revision FROM card_revision WHERE incarnation=? AND card_id=? ORDER BY origin_board,origin_incarnation,card_id,card_revision`, []any{inc, card}, func(rs *sql.Rows) error {
		var s A.Snapshot
		var state, removed, reason sql.NullString
		var revision sql.NullInt64
		if e := rs.Scan(&s.Origin.BoardID, &s.Origin.Incarnation, &s.CardID, &s.CardRevision, &s.CommittedAt, &s.ActorID, &state, &removed, &reason, &revision); e != nil {
			return e
		}
		s.References = A.SnapshotReferences{CommentIDs: []A.UUID{}, LinkIDs: []A.UUID{}, AttachmentIDs: []A.UUID{}, ApprovalIDs: []A.UUID{}}
		if state.Valid {
			v, e := A.Decode[A.SnapshotState]([]byte(state.String))
			if e != nil {
				return e
			}
			s.State = &v
			if v.OwnerID != nil {
				p.actors[*v.OwnerID] = true
			}
		} else {
			if !removed.Valid || !reason.Valid || !revision.Valid {
				return ErrUnavailable
			}
			s.Removal = &A.Removal{Reason: A.Reason(reason.String), RemovedAt: A.Timestamp(removed.String), BoardRevision: A.Number(revision.Int64)}
		}
		p.actors[s.ActorID] = true
		p.records = append(p.records, s)
		return nil
	})
	if e != nil {
		return e
	}
	for j := range p.records {
		s := &p.records[j]
		e = rows(r, `SELECT kind,entity_id FROM revision_reference WHERE incarnation=? AND origin_board=? AND origin_incarnation=? AND card_id=? AND card_revision=? ORDER BY kind,entity_id`, []any{inc, string(s.Origin.BoardID), string(s.Origin.Incarnation), card, uint64(s.CardRevision)}, func(rs *sql.Rows) error {
			var kind string
			var id A.UUID
			if e := rs.Scan(&kind, &id); e != nil {
				return e
			}
			p.need(kind, id)
			switch kind {
			case "comment":
				s.References.CommentIDs = append(s.References.CommentIDs, id)
			case "link":
				s.References.LinkIDs = append(s.References.LinkIDs, id)
			case "attachment":
				s.References.AttachmentIDs = append(s.References.AttachmentIDs, id)
			case "approval":
				s.References.ApprovalIDs = append(s.References.ApprovalIDs, id)
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}

// entities includes exactly the immutable rows referenced by current content
// or returned history. Tombstones preserve removed history's known references.
func (p *projection) entities(r writer.Read, inc string) error {
	origin := `e.origin_board,e.origin_incarnation`
	e := rows(r, `SELECT c.comment_id,c.card_id,c.actor_id,c.text,c.created_at,`+origin+` FROM comment c JOIN entity e ON e.incarnation=c.incarnation AND e.kind='comment' AND e.entity_id=c.comment_id WHERE c.incarnation=? ORDER BY c.comment_id`, []any{inc}, func(rs *sql.Rows) error {
		var c A.Comment
		if e := rs.Scan(&c.CommentID, &c.CardID, &c.ActorID, &c.Text, &c.CreatedAt, &c.CreatedIn.BoardID, &c.CreatedIn.Incarnation); e != nil {
			return e
		}
		if p.needed["comment"][c.CommentID] {
			p.board.Comments = append(p.board.Comments, c)
			p.actors[c.ActorID] = true
		}
		return nil
	})
	if e != nil {
		return e
	}
	e = rows(r, `SELECT l.link_id,l.card_id,l.kind,l.target_board,l.target_incarnation,l.target_card_id,l.active,l.created_at,`+origin+` FROM link l JOIN entity e ON e.incarnation=l.incarnation AND e.kind='link' AND e.entity_id=l.link_id WHERE l.incarnation=? ORDER BY l.link_id`, []any{inc}, func(rs *sql.Rows) error {
		var l A.Link
		if e := rs.Scan(&l.LinkID, &l.CardID, &l.Kind, &l.Target.BoardID, &l.Target.Incarnation, &l.Target.CardID, &l.Active, &l.CreatedAt, &l.CreatedIn.BoardID, &l.CreatedIn.Incarnation); e != nil {
			return e
		}
		if p.needed["link"][l.LinkID] {
			p.board.Links = append(p.board.Links, l)
		}
		return nil
	})
	if e != nil {
		return e
	}
	e = rows(r, `SELECT a.attachment_id,a.card_id,a.name,a.media_label,a.sha256,a.byte_length,a.created_at,`+origin+` FROM attachment a JOIN entity e ON e.incarnation=a.incarnation AND e.kind='attachment' AND e.entity_id=a.attachment_id WHERE a.incarnation=? ORDER BY a.attachment_id`, []any{inc}, func(rs *sql.Rows) error {
		var a A.Attachment
		if e := rs.Scan(&a.AttachmentID, &a.CardID, &a.Name, &a.MediaLabel, &a.SHA256, &a.ByteLength, &a.CreatedAt, &a.CreatedIn.BoardID, &a.CreatedIn.Incarnation); e != nil {
			return e
		}
		if p.needed["attachment"][a.AttachmentID] {
			p.board.Attachments = append(p.board.Attachments, a)
		}
		return nil
	})
	if e != nil {
		return e
	}
	e = rows(r, `SELECT a.approval_id,a.card_id,a.actor_id,a.reviewed_card_revision,a.reviewed_board_revision,a.policy_version,a.created_at,`+origin+` FROM approval a JOIN entity e ON e.incarnation=a.incarnation AND e.kind='approval' AND e.entity_id=a.approval_id WHERE a.incarnation=? ORDER BY a.approval_id`, []any{inc}, func(rs *sql.Rows) error {
		var a A.Approval
		if e := rs.Scan(&a.ApprovalID, &a.CardID, &a.ActorID, &a.ReviewedCardRevision, &a.ReviewedBoardRevision, &a.PolicyVersion, &a.CreatedAt, &a.CreatedIn.BoardID, &a.CreatedIn.Incarnation); e != nil {
			return e
		}
		if p.needed["approval"][a.ApprovalID] {
			p.board.Approvals = append(p.board.Approvals, a)
			p.actors[a.ActorID] = true
		}
		return nil
	})
	if e != nil {
		return e
	}
	e = rows(r, `SELECT kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at,removed_at,removal_reason FROM entity WHERE incarnation=? AND removed_at IS NOT NULL ORDER BY kind,entity_id`, []any{inc}, func(rs *sql.Rows) error {
		var t A.EntityTombstone
		var actor, at sql.NullString
		if e := rs.Scan(&t.Kind, &t.ID, &t.CardID, &t.CreatedIn.BoardID, &t.CreatedIn.Incarnation, &actor, &at, &t.RemovedAt, &t.Reason); e != nil {
			return e
		}
		if p.needed[string(t.Kind)][t.ID] {
			// D02 exposes a tombstone creator only for comments. Registry
			// provenance on other kinds is not an alternate public row shape.
			if t.Kind == "comment" {
				t.ActorID = uuidPtr(actor)
			}
			if t.Kind != "card" {
				t.CreatedAt = stampPtr(at)
			}
			p.tombstones = append(p.tombstones, t)
			if t.ActorID != nil {
				p.actors[*t.ActorID] = true
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	return rows(r, `SELECT actor_id,kind,label,origin_board,origin_incarnation FROM actor WHERE incarnation=? ORDER BY actor_id`, []any{inc}, func(rs *sql.Rows) error {
		var a A.Actor
		if e := rs.Scan(&a.ActorID, &a.Kind, &a.Label, &a.CreatedIn.BoardID, &a.CreatedIn.Incarnation); e != nil {
			return e
		}
		if p.actors[a.ActorID] {
			p.board.Actors = append(p.board.Actors, a)
		}
		return nil
	})
}
