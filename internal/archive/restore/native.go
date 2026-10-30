package restore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	E "github.com/localswim/localswim/internal/archive/export"
	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

// Construct requires an existing, exclusively owned private EMPTY data directory.
// S01 creates the database exclusively and checks native ACL/path constraints.
// Failures retain the candidate for investigation; this function never retries,
// deletes, adopts a database or changes the source. Publication is separate.
func (p *Prepared) Construct(ctx context.Context, dataDir string) (err error) {
	if p == nil || ctx == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, e := S.Create(ctx, dataDir, schema.Identity{BoardID: string(p.board.Identity.BoardID), Incarnation: string(p.board.Identity.Incarnation)})
	if e != nil {
		return e
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	l, e := s.Writer(ctx)
	if e != nil {
		return e
	}
	err = l.Do(ctx, func(c *sql.Conn) (err error) {
		if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		defer func() {
			if err != nil {
				cleanup, release := context.WithTimeout(context.Background(), 2*time.Second)
				defer release()
				_, _ = c.ExecContext(cleanup, "ROLLBACK")
			}
		}()
		if err = p.insert(ctx, c); err != nil {
			return err
		}
		if err = schema.Validate(ctx, c, schema.Identity{BoardID: string(p.board.Identity.BoardID), Incarnation: string(p.board.Identity.Incarnation)}); err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, "COMMIT")
		return err
	})
	ce := l.Close()
	if err != nil || ce != nil {
		return errors.Join(err, ce)
	}
	actual, e := projectStore(ctx, s, p.board.Identity, p.manifest.Revision, p.created)
	if e != nil {
		return e
	}
	_, e = p.VerifyProjection(actual)
	return e
}

// NativeInventory independently reopens and validates the prepared database,
// reprojects its complete logical state and checks the expected transformation.
// Its fresh return value is the only anchor publication may persist.
func (p *Prepared) NativeInventory(ctx context.Context, dataDir string) (inventory A.TargetInventory, err error) {
	if p == nil || ctx == nil {
		return inventory, ErrInvalid
	}
	err = S.InspectExisting(ctx, dataDir, schema.Identity{BoardID: string(p.board.Identity.BoardID), Incarnation: string(p.board.Identity.Incarnation)}, func(read S.InspectionRead) error {
		actual, e := projectRead(read, p.board.Identity, p.manifest.Revision, p.created)
		if e != nil {
			return e
		}
		inventory, e = p.VerifyProjection(actual)
		return e
	})
	if err != nil {
		inventory = A.TargetInventory{}
	}
	return inventory, err
}

// ValidateNative opens through S01 exclusive offline ownership, performs its
// exact schema/FK/revision/byte checks, and independently projects the destination
// via the shared A02 reader. Operational tables must remain empty. No authority
// verifier is inferred: this local read belongs only to offline restore/status.
func ValidateNative(ctx context.Context, dataDir string, r A.Receipt) (err error) {
	if ctx == nil || A.Validate(r) != nil || r.TargetInventory == nil {
		return ErrInvalid
	}
	return S.InspectExisting(ctx, dataDir, schema.Identity{BoardID: string(r.Destination.BoardID), Incarnation: string(r.Destination.Incarnation)}, func(read S.InspectionRead) error {
		var nextEvent int
		if e := read.Scan("SELECT next_event FROM board WHERE singleton=1", nil, &nextEvent); e != nil || nextEvent != 1 {
			return ErrInvalid
		}
		var currentActivity int
		if e := read.Scan("SELECT count(*) FROM activity WHERE origin_board=? AND origin_incarnation=?", []any{r.Destination.BoardID, r.Destination.Incarnation}, &currentActivity); e != nil || currentActivity != 0 {
			return ErrInvalid
		}
		for _, table := range []string{"grant_record", "grant_right", "cli_credential", "recovery_preview"} {
			var count int
			if e := read.Scan("SELECT count(*) FROM "+table, nil, &count); e != nil || count != 0 {
				return ErrInvalid
			}
		}
		var n int
		if e := read.Scan("SELECT count(*) FROM operation WHERE current_ledger<>0 OR (origin_board=? AND origin_incarnation=?)", []any{r.Destination.BoardID, r.Destination.Incarnation}, &n); e != nil || n != 0 {
			return ErrInvalid
		}
		p, e := projectRead(read, r.Destination, r.SourceManifest.Revision, r.CreatedAt)
		if e != nil {
			return e
		}
		m, e := p.Manifest()
		if e != nil {
			return e
		}
		if m.Source != r.Destination || m.Revision != r.SourceManifest.Revision {
			return ErrInvalid
		}
		entries, e := A.Canonical(m.Inventory)
		if e != nil {
			return e
		}
		want, e := A.Canonical(r.TargetInventory.Entries)
		if e != nil || !bytes.Equal(entries, want) || hash(entries) != r.TargetInventory.SHA256 {
			return ErrInvalid
		}
		// Lineage is operational provenance outside TargetInventory; bind it to the
		// exact immutable source manifest and immediate source tuple as well.
		lineage := append([]A.Lineage{}, r.SourceManifest.Lineage...)
		lineage = append(lineage, A.Lineage{Identity: r.SourceManifest.Source, ManifestSHA256: r.SourceManifestSHA256, Revision: r.SourceManifest.Revision})
		actual := map[A.Lineage]bool{}
		for _, v := range m.Lineage {
			actual[v] = true
		}
		if len(actual) != len(lineage) {
			return ErrInvalid
		}
		for _, v := range lineage {
			if !actual[v] {
				return ErrInvalid
			}
		}
		return nil
	})
}

type projection struct {
	manifest A.Manifest
	members  map[string][]byte
}

func (p *projection) Manifest() (A.Manifest, error) { return p.manifest, nil }
func (p *projection) Member(name string) ([]byte, error) {
	b, ok := p.members[name]
	if !ok {
		return nil, ErrInvalid
	}
	return bytes.Clone(b), nil
}

func projectStore(ctx context.Context, s *S.Store, id A.Identity, revision A.Number, at A.Timestamp) (out *projection, err error) {
	// Offline S01 ownership excludes other writers. Take only a query-only reader
	// transaction: the live-export pin registry has GC on close and must never be
	// used by observational verification, including rejected/cancelled reads.
	if ctx == nil || s == nil {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l, e := s.Reader(ctx)
	if e != nil {
		return nil, e
	}
	defer func() {
		if e := l.Close(); e != nil {
			out = nil
			err = errors.Join(err, e)
		}
	}()
	err = l.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		var e error
		out, e = projectRead(offlineRead{ctx: ctx, conn: c}, id, revision, at)
		return e
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func projectRead(r writer.Read, id A.Identity, revision A.Number, at A.Timestamp) (out *projection, err error) {
	err = func() error {
		d, e := E.ProjectOffline(r, id, revision)
		if e != nil {
			return e
		}
		m := A.Manifest{FormatVersion: 1, SchemaVersion: 1, HashAlgorithm: "sha256", Source: id, Revision: revision, ExportedAt: at, Lineage: d.Lineage, Retention: d.Board.Retention, RetentionVersion: d.Board.RetentionVersion, Coverage: d.Board.Coverage, Inventory: []A.Entry{}}
		p := &projection{members: map[string][]byte{}}
		total := 0
		for _, v := range []struct {
			name  string
			value any
		}{{"board.json", d.Board}, {"history.json", d.History}, {"activity.json", d.Activity}, {"outcomes.json", d.Outcomes}} {
			raw, e := A.Readable(v.value)
			if e != nil {
				return e
			}
			if len(raw) > V.MaxJSONBytes || total > V.MaxJSONTotalBytes-len(raw) {
				return ErrScope
			}
			total += len(raw)
			p.members[v.name] = raw
			m.Inventory = append(m.Inventory, A.Entry{Path: v.name, ByteLength: A.Number(len(raw)), SHA256: hash(raw)})
		}
		facts, e := protectedFacts(r)
		if e != nil {
			return e
		}
		// A prepared offline restore contains only its protected BLOB union; an
		// unexpected unprotected BLOB is domain corruption, not ignored metadata.
		var blobCount int
		if e = r.Scan("SELECT count(*) FROM blob", nil, &blobCount); e != nil || blobCount != len(facts) {
			return ErrInvalid
		}
		for _, f := range facts {
			m.Inventory = append(m.Inventory, A.Entry{Path: "blobs/sha256-" + string(f.SHA256), ByteLength: f.ByteLength, SHA256: f.SHA256})
		}
		sort.Slice(m.Inventory, func(i, j int) bool { return m.Inventory[i].Path < m.Inventory[j].Path })
		raw, e := A.Readable(m)
		if e != nil {
			return e
		}
		if len(raw) > V.MaxJSONBytes || total > V.MaxJSONTotalBytes-len(raw) {
			return ErrScope
		}
		pins, e := A.ValidateDocuments(m, d.Board, d.History, d.Activity, d.Outcomes)
		if e != nil {
			return e
		}
		a, _ := A.Canonical(pins)
		b, _ := A.Canonical(facts)
		if !bytes.Equal(a, b) {
			return ErrInvalid
		}
		var blobTotal A.Number
		for _, f := range facts {
			if f.ByteLength > V.MaxBlobBytes || blobTotal > V.MaxBlobTotalBytes-f.ByteLength {
				return ErrScope
			}
			blobTotal += f.ByteLength
			var actual int64
			if e = r.Scan("SELECT length(bytes) FROM blob WHERE incarnation=? AND sha256=? AND byte_length=?", []any{id.Incarnation, f.SHA256, f.ByteLength}, &actual); e != nil || actual != int64(f.ByteLength) {
				return ErrInvalid
			}
			var data []byte
			if e = r.Scan("SELECT bytes FROM blob WHERE incarnation=? AND sha256=? AND byte_length=? AND length(bytes)=?", []any{id.Incarnation, f.SHA256, f.ByteLength, actual}, &data); e != nil || len(data) != int(f.ByteLength) || hash(data) != f.SHA256 {
				return ErrInvalid
			}
			p.members["blobs/sha256-"+string(f.SHA256)] = data
		}
		p.manifest = m
		out = p
		return nil
	}()
	if err != nil {
		return nil, err
	}
	return out, nil
}

type offlineRead struct {
	ctx  context.Context
	conn *sql.Conn
}

func (r offlineRead) Query(q string, args ...any) (*sql.Rows, error) {
	if !offlineSelect(q) {
		return nil, ErrInvalid
	}
	return r.conn.QueryContext(r.ctx, q, args...)
}
func (r offlineRead) Scan(q string, args []any, dest ...any) error {
	if !offlineSelect(q) {
		return ErrInvalid
	}
	return r.conn.QueryRowContext(r.ctx, q, args...).Scan(dest...)
}
func offlineSelect(q string) bool {
	words := strings.Fields(q)
	return len(words) > 0 && strings.EqualFold(words[0], "SELECT") && !strings.ContainsAny(q, ";\x00")
}

// These are the three schema-owned protected pin classes. Read their exact
// distinct union within the same offline transaction as DTOs and BLOBs. No live
// export lease, registry change, writer checkout or orphan reclamation occurs.
func protectedFacts(r writer.Read) ([]A.ByteFact, error) {
	const union = "SELECT sha256,byte_length FROM current_attachment_pin UNION SELECT sha256,byte_length FROM history_blob_pin UNION SELECT sha256,byte_length FROM operation_blob_pin"
	var count, total int64
	if e := r.Scan("SELECT count(*),coalesce(sum(byte_length),0) FROM ("+union+")", nil, &count, &total); e != nil {
		return nil, e
	}
	if count < 0 || count > V.MaxMembers-6 || total < 0 || total > V.MaxBlobTotalBytes {
		return nil, ErrScope
	}
	rows, e := r.Query("SELECT sha256,byte_length FROM (" + union + ") ORDER BY sha256,byte_length")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	facts := make([]A.ByteFact, 0, int(count))
	for rows.Next() {
		var f A.ByteFact
		if e = rows.Scan(&f.SHA256, &f.ByteLength); e != nil {
			return nil, e
		}
		if len(facts) >= int(count) || f.ByteLength > V.MaxBlobBytes || (len(facts) > 0 && facts[len(facts)-1].SHA256 >= f.SHA256) {
			return nil, ErrInvalid
		}
		facts = append(facts, f)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(facts) != int(count) {
		return nil, ErrInvalid
	}
	if e = rows.Close(); e != nil {
		return nil, e
	}
	return facts, nil
}

func (p *Prepared) insert(ctx context.Context, c *sql.Conn) error {
	i := p.board.Identity.Incarnation
	run := func(q string, args ...any) error { _, e := c.ExecContext(ctx, q, args...); return e }
	put := func(table, columns string, values ...any) error {
		return run("INSERT INTO "+table+"("+columns+") VALUES("+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+")", values...)
	}
	jsonText := func(v any) string { raw, _ := A.Canonical(v); return string(raw) }
	if e := run("UPDATE board SET revision=?,next_ticket=?,retention_version=?,history_days=?,activity_days=?,outcome_days=? WHERE singleton=1", p.board.Revision, p.board.NextTicket, p.board.RetentionVersion, p.board.Retention.HistoryDays, p.board.Retention.ActivityDays, p.board.Retention.OutcomeDays); e != nil {
		return e
	}
	for _, v := range p.board.Actors {
		if e := put("actor", "incarnation,actor_id,kind,label,origin_board,origin_incarnation", i, v.ActorID, v.Kind, v.Label, v.CreatedIn.BoardID, v.CreatedIn.Incarnation); e != nil {
			return e
		}
	}
	entity := func(kind string, id, card A.UUID, origin A.Identity, actor any, created any, removed any, reason any) error {
		return put("entity", "incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at,removed_at,removal_reason", i, kind, id, card, origin.BoardID, origin.Incarnation, actor, created, removed, reason)
	}
	for _, v := range p.board.Cards {
		if e := entity("card", v.CardID, v.CardID, v.CreatedIn, nil, nil, nil, nil); e != nil {
			return e
		}
		if e := put("ticket", "incarnation,ticket,card_id", i, v.Ticket, v.CardID); e != nil {
			return e
		}
	}
	for _, v := range p.board.Comments {
		if e := entity("comment", v.CommentID, v.CardID, v.CreatedIn, v.ActorID, v.CreatedAt, nil, nil); e != nil {
			return e
		}
	}
	for _, v := range p.board.Links {
		if e := entity("link", v.LinkID, v.CardID, v.CreatedIn, nil, v.CreatedAt, nil, nil); e != nil {
			return e
		}
	}
	for _, v := range p.board.Attachments {
		if e := entity("attachment", v.AttachmentID, v.CardID, v.CreatedIn, nil, v.CreatedAt, nil, nil); e != nil {
			return e
		}
	}
	for _, v := range p.board.Approvals {
		if e := entity("approval", v.ApprovalID, v.CardID, v.CreatedIn, v.ActorID, v.CreatedAt, nil, nil); e != nil {
			return e
		}
	}
	for _, v := range p.board.Tombstones {
		if e := entity(string(v.Kind), v.ID, v.CardID, v.CreatedIn, v.ActorID, v.CreatedAt, v.RemovedAt, v.Reason); e != nil {
			return e
		}
	}
	for _, v := range p.board.Cards {
		if e := put("card", "incarnation,card_id,revision,title,description,priority,lane,archived,current_approval_id,owner_id", i, v.CardID, v.Revision, v.Title, v.Description, v.Priority, v.Lane, v.Archived, v.CurrentApprovalID, v.OwnerID); e != nil {
			return e
		}
	}
	for _, v := range p.board.Cards {
		if v.Parent != nil {
			if e := put("card_parent", "incarnation,child_id,parent_id", i, v.CardID, v.Parent.CardID); e != nil {
				return e
			}
		}
	}

	for _, v := range p.board.Comments {
		if e := put("comment", "incarnation,comment_id,card_id,actor_id,text,created_at", i, v.CommentID, v.CardID, v.ActorID, v.Text, v.CreatedAt); e != nil {
			return e
		}
	}
	for _, v := range p.board.Links {
		var internal any
		if v.Target.BoardID == p.board.Identity.BoardID && v.Target.Incarnation == i {
			internal = v.Target.CardID
		}
		if e := put("link", "incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at", i, v.LinkID, v.CardID, v.Kind, v.Target.BoardID, v.Target.Incarnation, v.Target.CardID, internal, v.Active, v.CreatedAt); e != nil {
			return e
		}
	}
	attachments := map[A.UUID]A.Attachment{}
	for _, v := range p.board.Attachments {
		attachments[v.AttachmentID] = v
		if e := put("attachment", "incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at", i, v.AttachmentID, v.CardID, v.Name, v.MediaLabel, v.SHA256, v.ByteLength, v.CreatedAt); e != nil {
			return e
		}
	}
	for _, v := range p.board.Approvals {
		if e := put("approval", "incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at", i, v.ApprovalID, v.CardID, v.ActorID, v.ReviewedCardRevision, v.ReviewedBoardRevision, v.PolicyVersion, v.CreatedAt); e != nil {
			return e
		}
	}
	for _, v := range p.inventory.Entries {
		if strings.HasPrefix(v.Path, "blobs/") {
			raw, e := p.source.Member(v.Path)
			if e != nil {
				return e
			}
			if e = put("blob", "incarnation,sha256,byte_length,bytes", i, v.SHA256, v.ByteLength, raw); e != nil {
				return e
			}
		}
	}
	for _, v := range p.board.Cards {
		for kind, ids := range map[string][]A.UUID{"comment": v.CommentIDs, "link": v.LinkIDs, "attachment": v.AttachmentIDs} {
			for _, id := range ids {
				if e := put("current_reference", "incarnation,card_id,kind,entity_id", i, v.CardID, kind, id); e != nil {
					return e
				}
				if kind == "attachment" {
					a := attachments[id]
					if e := put("current_attachment_pin", "incarnation,attachment_id,sha256,byte_length", i, id, a.SHA256, a.ByteLength); e != nil {
						return e
					}
				}
			}
		}
	}
	for _, v := range p.history.Records {
		var state, removed, reason, rev any
		if v.State != nil {
			state = jsonText(v.State)
		}
		if v.Removal != nil {
			removed = v.Removal.RemovedAt
			reason = v.Removal.Reason
			rev = v.Removal.BoardRevision
		}
		if e := put("card_revision", "incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state,removed_at,removal_reason,removal_revision", i, v.Origin.BoardID, v.Origin.Incarnation, v.CardID, v.CardRevision, v.ActorID, v.CommittedAt, state, removed, reason, rev); e != nil {
			return e
		}
		for kind, ids := range map[string][]A.UUID{"comment": v.References.CommentIDs, "link": v.References.LinkIDs, "attachment": v.References.AttachmentIDs, "approval": v.References.ApprovalIDs} {
			for _, id := range ids {
				if e := put("revision_reference", "incarnation,origin_board,origin_incarnation,card_id,card_revision,kind,entity_id", i, v.Origin.BoardID, v.Origin.Incarnation, v.CardID, v.CardRevision, kind, id); e != nil {
					return e
				}
				if kind == "attachment" && v.State != nil {
					a := attachments[id]
					if e := put("history_blob_pin", "incarnation,origin_board,origin_incarnation,card_id,card_revision,attachment_id,sha256,byte_length", i, v.Origin.BoardID, v.Origin.Incarnation, v.CardID, v.CardRevision, id, a.SHA256, a.ByteLength); e != nil {
						return e
					}
				}
			}
		}
	}
	grants := map[A.UUID]bool{}
	for _, g := range p.outcomes.SourceArchives {
		for _, v := range g.Operations {
			if v.TerminalClass == "COMMITTED" && v.Target != nil && v.Target.GrantID != nil {
				grants[*v.Target.GrantID] = true
			}
			for _, f := range v.ResultRefs {
				if f.Kind == "grant" {
					grants[f.ID] = true
				}
			}
		}
	}
	for id := range grants {
		if e := put("grant_fact", "incarnation,grant_id", i, id); e != nil {
			return e
		}
	}
	// Persist declared provenance groups independently of their operation rows.
	// This includes explicitly empty inherited groups; lineage is not a group
	// registry and must never manufacture a group absent from the source.
	for _, g := range p.outcomes.SourceArchives {
		if e := put("source_archive_group", "incarnation,origin_board,origin_incarnation", i, g.Identity.BoardID, g.Identity.Incarnation); e != nil {
			return e
		}
	}
	for _, g := range p.outcomes.SourceArchives {
		for _, v := range g.Operations {
			var tk, tid, removed, reason, rev any
			if v.Target != nil {
				if v.Target.CardID != nil {
					tk = "card"
					tid = *v.Target.CardID
				} else {
					tk = "grant"
					tid = *v.Target.GrantID
				}
			}
			if v.Removal != nil {
				removed = v.Removal.RemovedAt
				reason = v.Removal.Reason
				rev = v.Removal.BoardRevision
			}
			if e := put("operation", "incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint_version,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision", i, v.Origin.BoardID, v.Origin.Incarnation, v.OpID, 0, v.ActorID, v.FingerprintVersion, v.Fingerprint, v.Command, tk, tid, v.ExpectedRevision, v.ExpectedCardRevision, v.TerminalClass, v.Code, v.TerminalAt, v.CommittedRevision, v.CommittedCardRevision, removed, reason, rev); e != nil {
				return e
			}
			if v.Full != nil {
				if e := put("operation_full", "incarnation,origin_board,origin_incarnation,op_id,intent,result", i, v.Origin.BoardID, v.Origin.Incarnation, v.OpID, string(v.Full.Intent), string(v.Full.Result)); e != nil {
					return e
				}
			}
			for _, f := range v.ResultRefs {
				if e := put("operation_reference", "incarnation,origin_board,origin_incarnation,op_id,kind,entity_id,revision", i, v.Origin.BoardID, v.Origin.Incarnation, v.OpID, f.Kind, f.ID, f.Revision); e != nil {
					return e
				}
			}
			for _, f := range v.ByteFacts {
				if e := put("byte_fact", "incarnation,origin_board,origin_incarnation,op_id,sha256,byte_length", i, v.Origin.BoardID, v.Origin.Incarnation, v.OpID, f.SHA256, f.ByteLength); e != nil {
					return e
				}
			}
			if v.Full != nil && v.TerminalClass == "COMMITTED" && v.Command == "attachment.add" {
				for _, f := range v.ResultRefs {
					if f.Kind == "attachment" {
						a := attachments[f.ID]
						if e := put("operation_blob_pin", "incarnation,origin_board,origin_incarnation,op_id,attachment_id,sha256,byte_length", i, v.Origin.BoardID, v.Origin.Incarnation, v.OpID, f.ID, a.SHA256, a.ByteLength); e != nil {
							return e
						}
					}
				}
			}
		}
	}
	for _, v := range p.activity.Events {
		if e := put("activity", "incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,affected,at", i, v.Origin.BoardID, v.Origin.Incarnation, v.EventID, v.ActorID, v.OpID, v.Type, v.BoardRevision, jsonText(v.Affected), v.At); e != nil {
			return e
		}
	}
	for _, v := range p.board.Coverage.Gaps {
		if e := put("coverage_gap", "incarnation,origin_board,origin_incarnation,class,reason,from_exclusive,through_inclusive,recorded_at,board_revision", i, v.Origin.BoardID, v.Origin.Incarnation, v.Class, v.Reason, v.FromExclusive, v.ThroughInclusive, v.RecordedAt, v.BoardRevision); e != nil {
			return e
		}
	}
	for _, v := range p.lineage {
		if e := put("lineage", "incarnation,source_board,source_incarnation,manifest_sha256,revision", i, v.Identity.BoardID, v.Identity.Incarnation, v.ManifestSHA256, v.Revision); e != nil {
			return e
		}
	}
	return nil
}
