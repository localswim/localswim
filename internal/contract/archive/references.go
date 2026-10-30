package archive

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Rows is a projection of shared immutable facts. C02 HistoryRead uses these
// exact row definitions and ValidateHistoryReferences; it must not clone them.
type Rows struct {
	Actors      []Actor           `json:"actors"`
	Comments    []Comment         `json:"comments"`
	Links       []Link            `json:"links"`
	Attachments []Attachment      `json:"attachments"`
	Approvals   []Approval        `json:"approvals"`
	Tombstones  []EntityTombstone `json:"tombstones"`
}

type registry struct {
	rows        Rows
	full        map[string]map[UUID]UUID
	dead        map[string]map[UUID]UUID
	actors      map[UUID]bool
	attachments map[UUID]Attachment
	approvals   map[UUID]Approval
	lengths     map[Digest]Number
}

func newRegistry(rows Rows) (*registry, error) {
	r := &registry{rows: rows, full: map[string]map[UUID]UUID{}, dead: map[string]map[UUID]UUID{}, actors: map[UUID]bool{}, attachments: map[UUID]Attachment{}, approvals: map[UUID]Approval{}, lengths: map[Digest]Number{}}
	for _, k := range []string{"card", "comment", "link", "attachment", "approval"} {
		r.full[k] = map[UUID]UUID{}
		r.dead[k] = map[UUID]UUID{}
	}
	if e := Validate(rows); e != nil {
		return nil, e
	}
	for _, a := range rows.Actors {
		if r.actors[a.ActorID] {
			return nil, fmt.Errorf("duplicate actor")
		}
		r.actors[a.ActorID] = true
	}
	add := func(k string, id, owner UUID) error {
		if _, ok := r.full[k][id]; ok {
			return fmt.Errorf("duplicate %s", k)
		}
		r.full[k][id] = owner
		return nil
	}
	for _, c := range rows.Comments {
		if e := add("comment", c.CommentID, c.CardID); e != nil {
			return nil, e
		}
		if !r.actors[c.ActorID] {
			return nil, fmt.Errorf("comment actor")
		}
	}
	for _, l := range rows.Links {
		if e := add("link", l.LinkID, l.CardID); e != nil {
			return nil, e
		}
	}
	for _, a := range rows.Attachments {
		if e := add("attachment", a.AttachmentID, a.CardID); e != nil {
			return nil, e
		}
		r.attachments[a.AttachmentID] = a
		if e := r.length(ByteFact{a.SHA256, a.ByteLength}); e != nil {
			return nil, e
		}
	}
	for _, a := range rows.Approvals {
		if e := add("approval", a.ApprovalID, a.CardID); e != nil {
			return nil, e
		}
		r.approvals[a.ApprovalID] = a
		if !r.actors[a.ActorID] {
			return nil, fmt.Errorf("approval actor")
		}
	}
	for _, t := range rows.Tombstones {
		k := string(t.Kind)
		if _, ok := r.full[k][t.ID]; ok {
			return nil, fmt.Errorf("full/tombstone collision")
		}
		if _, ok := r.dead[k][t.ID]; ok {
			return nil, fmt.Errorf("duplicate tombstone")
		}
		r.dead[k][t.ID] = t.CardID
		if t.ActorID != nil && !r.actors[*t.ActorID] {
			return nil, fmt.Errorf("tombstone actor")
		}
	}
	return r, nil
}
func (r *registry) length(f ByteFact) error {
	if n, ok := r.lengths[f.SHA256]; ok && n != f.ByteLength {
		return fmt.Errorf("BYTE_LENGTH_CONFLICT")
	}
	r.lengths[f.SHA256] = f.ByteLength
	return nil
}
func (r *registry) resolve(k string, id, owner UUID, removed bool) error {
	o, ok := r.full[k][id]
	if !ok && removed {
		o, ok = r.dead[k][id]
	}
	if !ok {
		return fmt.Errorf("unresolved %s reference", k)
	}
	if owner != "" && o != owner {
		return fmt.Errorf("%s reference ownership", k)
	}
	return nil
}
func (r *registry) history(records []Snapshot, current map[UUID]Positive, exact bool) error {
	needed := map[string]map[UUID]bool{}
	for _, k := range []string{"comment", "link", "attachment", "approval"} {
		needed[k] = map[UUID]bool{}
	}
	actors := map[UUID]bool{}
	for _, s := range records {
		if !r.actors[s.ActorID] {
			return fmt.Errorf("snapshot actor")
		}
		actors[s.ActorID] = true
		if s.State != nil && s.State.OwnerID != nil {
			if !r.actors[*s.State.OwnerID] {
				return fmt.Errorf("snapshot assignee")
			}
			actors[*s.State.OwnerID] = true
		}
		revision, ok := current[s.CardID]
		if !ok {
			return fmt.Errorf("snapshot card")
		}
		if s.CardRevision >= revision {
			return fmt.Errorf("snapshot is not earlier than current")
		}
		removed := s.State == nil
		refs := map[string][]UUID{"comment": s.References.CommentIDs, "link": s.References.LinkIDs, "attachment": s.References.AttachmentIDs, "approval": s.References.ApprovalIDs}
		for k, ids := range refs {
			for _, id := range ids {
				if e := r.resolve(k, id, s.CardID, removed); e != nil {
					return e
				}
				needed[k][id] = true
			}
		}
		if s.State != nil && s.State.CurrentApprovalID != nil {
			if e := r.resolve("approval", *s.State.CurrentApprovalID, s.CardID, false); e != nil {
				return e
			}
			found := false
			for _, id := range s.References.ApprovalIDs {
				found = found || id == *s.State.CurrentApprovalID
			}
			if !found {
				return fmt.Errorf("snapshot approval reference")
			}
		}
	}
	if exact {
		// Sweep every registered kind, including card tombstones. A history view
		// returns earlier snapshots of an existing current card; unrelated card
		// tombstones cannot satisfy any of its immutable reference collections.
		for k, full := range r.full {
			ids := needed[k]
			for id := range full {
				if !ids[id] {
					return fmt.Errorf("extra history %s row", k)
				}
			}
			for id := range r.dead[k] {
				if !ids[id] {
					return fmt.Errorf("extra history tombstone")
				}
			}
		}
		for _, c := range r.rows.Comments {
			actors[c.ActorID] = true
		}
		for _, a := range r.rows.Approvals {
			actors[a.ActorID] = true
		}
		for _, t := range r.rows.Tombstones {
			if t.ActorID != nil {
				actors[*t.ActorID] = true
			}
		}
		if len(actors) != len(r.actors) {
			return fmt.Errorf("extra history actor")
		}
	}
	return nil
}

// ValidateHistoryReferences validates the exact D03 history projection. Full
// snapshots require full entities; removed snapshots may use genuine tombstones.
// No current-card body, source authority, bytes or storage access is inferred.
func ValidateHistoryReferences(identity Identity, cardID UUID, currentRevision Positive, records []Snapshot, rows Rows) error {
	if e := Validate(identity); e != nil {
		return e
	}
	if e := Validate(cardID); e != nil {
		return e
	}
	if e := Validate(currentRevision); e != nil {
		return e
	}
	if e := Validate(records); e != nil {
		return e
	}
	r, e := newRegistry(rows)
	if e != nil {
		return e
	}
	for _, s := range records {
		if s.CardID != cardID || s.Origin.BoardID != identity.BoardID {
			return fmt.Errorf("history scope")
		}
	}
	if e = rowsBoard(rows, identity.BoardID); e != nil {
		return e
	}
	return r.history(records, map[UUID]Positive{cardID: currentRevision}, true)
}
func rowsBoard(rows Rows, boardID UUID) error {
	for _, a := range rows.Actors {
		if a.CreatedIn.BoardID != boardID {
			return fmt.Errorf("actor provenance board")
		}
	}
	for _, c := range rows.Comments {
		if c.CreatedIn.BoardID != boardID {
			return fmt.Errorf("comment provenance board")
		}
	}
	for _, l := range rows.Links {
		if l.CreatedIn.BoardID != boardID {
			return fmt.Errorf("link provenance board")
		}
	}
	for _, a := range rows.Attachments {
		if a.CreatedIn.BoardID != boardID {
			return fmt.Errorf("attachment provenance board")
		}
	}
	for _, a := range rows.Approvals {
		if a.CreatedIn.BoardID != boardID {
			return fmt.Errorf("approval provenance board")
		}
	}
	for _, t := range rows.Tombstones {
		if t.CreatedIn.BoardID != boardID {
			return fmt.Errorf("tombstone provenance board")
		}
	}
	return nil
}

// ValidateDocuments checks only pure in-memory document/graph consistency and
// returns the distinct protected byte facts. Callers still must qualify member
// parsing, actual bytes/hash IO, safe ZIP/native publication and persistence.
func ValidateDocuments(m Manifest, b Board, h History, a Activity, o Outcomes) ([]ByteFact, error) {
	if err := ValidateForest(b.Identity, b.Cards); err != nil {
		return nil, err
	}
	for _, v := range []any{m, b, h, a, o} {
		if e := Validate(v); e != nil {
			return nil, e
		}
	}
	if b.Identity != m.Source || h.Identity != m.Source || a.Identity != m.Source || o.Identity != m.Source || b.Revision != m.Revision || h.Revision != m.Revision || a.Revision != m.Revision || o.Revision != m.Revision {
		return nil, fmt.Errorf("document snapshot identity/revision")
	}
	if !equal(m.Coverage, b.Coverage) || m.Retention != b.Retention || m.RetentionVersion != b.RetentionVersion {
		return nil, fmt.Errorf("manifest board policy/coverage")
	}
	rows := Rows{b.Actors, b.Comments, b.Links, b.Attachments, b.Approvals, b.Tombstones}
	r, e := newRegistry(rows)
	if e != nil {
		return nil, e
	}
	if e = rowsBoard(rows, m.Source.BoardID); e != nil {
		return nil, e
	}
	cards := map[UUID]Card{}
	current := map[UUID]Positive{}
	tickets := map[Positive]bool{}
	currentLinks := map[UUID]bool{}
	for _, c := range b.Cards {
		if _, ok := cards[c.CardID]; ok || tickets[c.Ticket] {
			return nil, fmt.Errorf("duplicate card/ticket")
		}
		if c.CreatedIn.BoardID != b.Identity.BoardID || c.Ticket >= b.NextTicket {
			return nil, fmt.Errorf("card provenance/allocator")
		}
		if c.OwnerID != nil && !r.actors[*c.OwnerID] {
			return nil, fmt.Errorf("card assignee")
		}
		cards[c.CardID] = c
		current[c.CardID] = c.Revision
		tickets[c.Ticket] = true
		if _, dead := r.dead["card"][c.CardID]; dead {
			return nil, fmt.Errorf("card tombstone collision")
		}
		r.full["card"][c.CardID] = c.CardID
		for k, ids := range map[string][]UUID{"comment": c.CommentIDs, "link": c.LinkIDs, "attachment": c.AttachmentIDs} {
			for _, id := range ids {
				if e = r.resolve(k, id, c.CardID, false); e != nil {
					return nil, e
				}
				if k == "link" {
					currentLinks[id] = true
				}
			}
		}
		if c.CurrentApprovalID != nil {
			if e = r.resolve("approval", *c.CurrentApprovalID, c.CardID, false); e != nil {
				return nil, e
			}
		}
	}
	if b.Revision == 0 && (len(b.Cards) > 0 || len(b.Comments) > 0 || len(b.Attachments) > 0 || len(b.Links) > 0 || len(b.Approvals) > 0 || len(h.Records) > 0 || len(a.Events) > 0 || len(o.Operations) > 0) {
		return nil, fmt.Errorf("nonempty revision zero")
	}
	for _, s := range h.Records {
		if s.Origin.BoardID != m.Source.BoardID {
			return nil, fmt.Errorf("snapshot provenance board")
		}
		if s.State != nil && s.State.Parent != nil {
			if err := r.resolve("card", s.State.Parent.CardID, "", true); err != nil {
				return nil, err
			}
		}
		if s.State != nil && s.State.Ticket >= b.NextTicket {
			return nil, fmt.Errorf("snapshot ticket allocator")
		}
	}
	if e = r.history(h.Records, current, false); e != nil {
		return nil, e
	}
	for _, g := range b.Coverage.Gaps {
		if g.Origin.BoardID != m.Source.BoardID {
			return nil, fmt.Errorf("gap provenance board")
		}
	}
	for _, ev := range a.Events {
		if ev.Origin.BoardID != m.Source.BoardID || !r.actors[ev.ActorID] {
			return nil, fmt.Errorf("event provenance/actor")
		}
		for _, f := range ev.Affected {
			if e = r.fact(f, m.Source.BoardID); e != nil {
				return nil, e
			}
		}
	}
	for k, ids := range r.full {
		if k == "card" {
			continue
		}
		for _, owner := range ids {
			if e = r.resolve("card", owner, "", true); e != nil {
				return nil, e
			}
		}
	}
	for _, l := range b.Links {
		if l.Target.BoardID == m.Source.BoardID && l.Target.Incarnation == m.Source.Incarnation {
			if e = r.resolve("card", l.Target.CardID, "", !currentLinks[l.LinkID]); e != nil {
				return nil, e
			}
		}
	}
	pins := map[Digest]Number{}
	pin := func(f ByteFact) { pins[f.SHA256] = f.ByteLength }
	for _, c := range b.Cards {
		for _, id := range c.AttachmentIDs {
			v := r.attachments[id]
			pin(ByteFact{v.SHA256, v.ByteLength})
		}
	}
	for _, s := range h.Records {
		if s.State != nil {
			for _, id := range s.References.AttachmentIDs {
				v := r.attachments[id]
				pin(ByteFact{v.SHA256, v.ByteLength})
			}
		}
	}
	all := append([]Operation{}, o.Operations...)
	for _, s := range o.SourceArchives {
		all = append(all, s.Operations...)
	}
	for _, op := range all {
		if !r.actors[op.ActorID] {
			return nil, fmt.Errorf("operation actor")
		}
		if e = r.operation(op, m.Source.BoardID); e != nil {
			return nil, e
		}
		for _, f := range op.ByteFacts {
			if e = r.length(f); e != nil {
				return nil, e
			}
			if op.Full != nil && op.TerminalClass == "COMMITTED" {
				pin(f)
			}
		}
	}
	for _, entry := range m.Inventory {
		if e = r.length(ByteFact{entry.SHA256, entry.ByteLength}); e != nil {
			return nil, e
		}
		if strings.HasPrefix(entry.Path, "blobs/") {
			if n, ok := pins[entry.SHA256]; !ok || n != entry.ByteLength {
				return nil, fmt.Errorf("unprotected inventory blob")
			}
		}
	}
	result := make([]ByteFact, 0, len(pins))
	for digest, n := range pins {
		found := false
		for _, entry := range m.Inventory {
			found = found || entry.Path == "blobs/sha256-"+string(digest)
		}
		if !found {
			return nil, fmt.Errorf("missing protected blob entry")
		}
		result = append(result, ByteFact{digest, n})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SHA256 < result[j].SHA256 })
	// JSON member lengths and hashes are pure projections, not raw IO assertions.
	for path, doc := range map[string]any{"board.json": b, "history.json": h, "activity.json": a, "outcomes.json": o} {
		raw, e := Readable(doc)
		if e != nil {
			return nil, e
		}
		for _, entry := range m.Inventory {
			if entry.Path == path && (entry.ByteLength != Number(len(raw)) || entry.SHA256 != hash(raw)) {
				return nil, fmt.Errorf("payload inventory projection")
			}
		}
	}
	return result, nil
}
func (r *registry) fact(f FactRef, board UUID) error {
	if f.Kind == "board" {
		if f.ID != board {
			return fmt.Errorf("board fact identity")
		}
		return nil
	}
	if f.Kind == "grant" {
		return nil
	}
	return r.resolve(string(f.Kind), f.ID, "", true)
}
func (r *registry) operation(o Operation, board UUID) error {
	if o.TerminalClass != "COMMITTED" {
		return nil
	} // attempted refused IDs need never exist
	owner := UUID("")
	if o.Target != nil && o.Target.CardID != nil {
		owner = *o.Target.CardID
		if e := r.resolve("card", *o.Target.CardID, "", true); e != nil {
			return fmt.Errorf("COMMITTED_REFERENCE: %w", e)
		}
	}
	for _, f := range o.ResultRefs {
		if e := r.fact(f, board); e != nil {
			return fmt.Errorf("COMMITTED_REFERENCE: %w", e)
		}
		// Ownership is a compact terminal fact, independent of retained full
		// intent/result payloads. Allowed tombstones retain the same owner.
		switch f.Kind {
		case "comment", "link", "attachment", "approval":
			if e := r.resolve(string(f.Kind), f.ID, owner, true); e != nil {
				return fmt.Errorf("COMMITTED_REFERENCE: %w", e)
			}
		}
	}
	if o.Full != nil {
		raw, _ := jsonValue(o.Full.Intent)
		intent := raw.(map[string]any)
		payload := intent["payload"].(map[string]any)
		for key, kind := range map[string]string{"targetCardId": "card", "linkId": "link"} {
			if id, ok := payload[key].(string); ok {
				requiredOwner := UUID("")
				if kind == "link" {
					requiredOwner = owner
				}
				if e := r.resolve(kind, UUID(id), requiredOwner, true); e != nil {
					return fmt.Errorf("COMMITTED_REFERENCE: %w", e)
				}
			}
		}
		if o.Command == "grant.create" {
			if id, ok := payload["granteeActorId"].(string); ok && !r.actors[UUID(id)] {
				return fmt.Errorf("committed grantee actor")
			}
		}
		var result map[string]json.RawMessage
		json.Unmarshal(o.Full.Result, &result)
		expected := []FactRef{}
		var resultCard UUID
		for key, kind := range map[string]string{"cardId": "card", "commentId": "comment", "attachmentId": "attachment", "linkId": "link", "approvalId": "approval", "grantId": "grant"} {
			if value, ok := result[key]; ok {
				var id UUID
				json.Unmarshal(value, &id)
				if kind == "card" {
					resultCard = id
					if owner != "" && id != owner {
						return fmt.Errorf("result target card")
					}
					var rev Number = Number(*o.CommittedCardRevision)
					expected = append(expected, FactRef{FactKind(kind), id, &rev})
				} else {
					expected = append(expected, FactRef{FactKind(kind), id, nil})
					if kind != "grant" {
						if e := r.resolve(kind, id, owner, true); e != nil {
							return e
						}
					}
				}
			}
		}
		if o.Command == "history.clear" || o.Command == "retention.set" {
			var rev Number
			json.Unmarshal(result["coverageRevision"], &rev)
			if rev != Number(*o.CommittedRevision) {
				return fmt.Errorf("coverage revision binding")
			}
			expected = append(expected, FactRef{"board", board, &rev})
		}
		sort.Slice(expected, func(i, j int) bool { a, _ := rowkey(expected[i]); b, _ := rowkey(expected[j]); return a < b })
		if !equal(expected, o.ResultRefs) {
			return fmt.Errorf("result reference mapping")
		}
		_ = resultCard
	}
	if o.Command == "attachment.add" {
		var attachment *FactRef
		var card *FactRef
		for i := range o.ResultRefs {
			f := &o.ResultRefs[i]
			if f.Kind == "attachment" {
				attachment = f
			}
			if f.Kind == "card" {
				card = f
			}
		}
		if len(o.ResultRefs) != 2 || attachment == nil || card == nil || attachment.Revision != nil || card.Revision == nil || *card.Revision != Number(*o.CommittedCardRevision) || card.ID != *o.Target.CardID {
			return fmt.Errorf("ATTACHMENT_RESULT_MAPPING")
		}
		a, ok := r.attachments[attachment.ID]
		if o.Full != nil && !ok {
			return fmt.Errorf("COMMITTED_REFERENCE")
		}
		if ok && (a.CardID != *o.Target.CardID || a.SHA256 != o.ByteFacts[0].SHA256 || a.ByteLength != o.ByteFacts[0].ByteLength) {
			return fmt.Errorf("ATTACHMENT_RESULT_MAPPING")
		}
	}
	return nil
}

// ValidateOperationReferences checks one retained operation against shared rows
// and current card IDs, without requiring an archive manifest or actual bytes.
func ValidateOperationReferences(o Operation, board UUID, cards []Card, rows Rows) error {
	if e := Validate(board); e != nil {
		return e
	}
	if e := Validate(cards); e != nil {
		return e
	}
	if e := Validate(o); e != nil {
		return e
	}
	if o.Origin.BoardID != board {
		return fmt.Errorf("operation board scope")
	}
	r, e := newRegistry(rows)
	if e != nil {
		return e
	}
	if e = rowsBoard(rows, board); e != nil {
		return e
	}
	if !r.actors[o.ActorID] {
		return fmt.Errorf("operation actor")
	}
	tickets := map[Positive]bool{}
	for _, c := range cards {
		if c.CreatedIn.BoardID != board {
			return fmt.Errorf("card provenance board")
		}
		if _, exists := r.full["card"][c.CardID]; exists || tickets[c.Ticket] {
			return fmt.Errorf("duplicate card/ticket")
		}
		if _, exists := r.dead["card"][c.CardID]; exists {
			return fmt.Errorf("card tombstone collision")
		}
		r.full["card"][c.CardID] = c.CardID
		tickets[c.Ticket] = true
	}
	for _, f := range o.ByteFacts {
		if e = r.length(f); e != nil {
			return e
		}
	}
	return r.operation(o, board)
}
