package core

import (
	"fmt"
	"sort"
	"strings"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

// focusedRead renders only a validated public read, never private drafts or
// pending operations. JSON mode continues to emit the complete original envelope.
func focusedRead(value any) (string, bool) {
	var out strings.Builder
	switch x := value.(type) {
	case T.CardRead:
		b := x.Result
		c := b.Card
		fmt.Fprintf(&out, "Committed current card #%d: %s\nCard UUID: %s\nBoard/incarnation: %s\nBoard revision: %d; card revision: %d\nCreated in: %s\n", c.Ticket, advisoryText(c.Title), c.CardID, originText(b.Identity), b.Revision, c.Revision, originText(c.CreatedIn))
		writeState(&out, c.Description, c.Lane, c.Priority, c.Archived, c.OwnerID, c.CurrentApprovalID, b.Actors)
		writeFacts(&out, b.Actors, b.Comments, b.Links, b.Attachments, b.Approvals)
		refs := append([]T.HistoryReference(nil), b.HistoryReferences...)
		sort.Slice(refs, func(i, j int) bool {
			return priorLess(b.Identity, refs[i].Origin, refs[i].CardRevision, refs[j].Origin, refs[j].CardRevision)
		})
		fmt.Fprintf(&out, "Prior state references returned: %d (read card history for retained old state text).\n", len(refs))
		for _, h := range refs {
			availability := "old state text unavailable"
			if h.Available {
				availability = "old state text retained"
			}
			fmt.Fprintf(&out, "  %s/%s revision %d at %s: %s\n", originText(h.Origin), h.CardID, h.CardRevision, h.CommittedAt, availability)
		}
		writeCoverage(&out, b.Coverage)
	case T.HistoryRead:
		b := x.Result
		fmt.Fprintf(&out, "Committed prior card states for %s\nBoard/incarnation: %s\nBoard revision: %d; current card revision: %d\nCurrent state is separate; use card read for current fields.\nPrior states returned: %d (returned collection, not a claim of complete lifetime history).\n", b.CardID, originText(b.Identity), b.Revision, b.CardRevision, len(b.Records))
		records := append([]A.Snapshot(nil), b.Records...)
		sort.Slice(records, func(i, j int) bool {
			return priorLess(b.Identity, records[i].Origin, records[i].CardRevision, records[j].Origin, records[j].CardRevision)
		})
		for _, h := range records {
			fmt.Fprintf(&out, "\nPrior state %s/%s revision %d\nCommitted at: %s; actor: %s\n", originText(h.Origin), h.CardID, h.CardRevision, h.CommittedAt, actorText(h.ActorID, b.Actors))
			if h.State != nil {
				s := h.State
				fmt.Fprintf(&out, "Ticket #%d: %s\n", s.Ticket, advisoryText(s.Title))
				writeState(&out, s.Description, s.Lane, s.Priority, s.Archived, s.OwnerID, s.CurrentApprovalID, b.Actors)
			} else {
				out.WriteString("Old state text unavailable.\n")
			}
			if h.Removal != nil {
				fmt.Fprintf(&out, "Removal: %s at %s; board revision %d\n", h.Removal.Reason, h.Removal.RemovedAt, h.Removal.BoardRevision)
			}
			fmt.Fprintf(&out, "Referenced comment IDs: %s\nReferenced link IDs: %s\nReferenced attachment IDs: %s\nReferenced approval IDs: %s\n", idsText(h.References.CommentIDs), idsText(h.References.LinkIDs), idsText(h.References.AttachmentIDs), idsText(h.References.ApprovalIDs))
		}
		out.WriteString("\nReturned referenced facts (not current card collections):\n")
		writeFacts(&out, b.Actors, b.Comments, b.Links, b.Attachments, b.Approvals)
		fmt.Fprintf(&out, "Entity tombstones returned: %d\n", len(b.Tombstones))
		for _, t := range b.Tombstones {
			fmt.Fprintf(&out, "  %s %s; card %s; created in %s; actor %s; created at %s; removed at %s; reason %s\n", t.Kind, t.ID, t.CardID, originText(t.CreatedIn), optionalActor(t.ActorID, b.Actors), timestampText(t.CreatedAt), t.RemovedAt, t.Reason)
		}
		writeCoverage(&out, b.Coverage)
	default:
		return "", false
	}
	return out.String(), true
}

func originText(i A.Identity) string { return string(i.BoardID) + "/" + string(i.Incarnation) }
func priorLess(current, a A.Identity, ar A.Positive, b A.Identity, br A.Positive) bool {
	if (a == current) != (b == current) {
		return a == current
	}
	if a.BoardID != b.BoardID {
		return a.BoardID < b.BoardID
	}
	if a.Incarnation != b.Incarnation {
		return a.Incarnation < b.Incarnation
	}
	return ar > br
}
func actorText(id A.UUID, actors []A.Actor) string {
	for _, a := range actors {
		if a.ActorID == id && a.Label != "" {
			return fmt.Sprintf("%s (%s; %s)", advisoryText(a.Label), id, a.Kind)
		}
	}
	return string(id)
}
func optionalActor(id *A.UUID, actors []A.Actor) string {
	if id == nil {
		return "none"
	}
	return actorText(*id, actors)
}
func timestampText(t *A.Timestamp) string {
	if t == nil {
		return "not supplied"
	}
	return string(*t)
}
func idsText(ids []A.UUID) string {
	if len(ids) == 0 {
		return "none"
	}
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = string(id)
	}
	sort.Strings(s)
	return strings.Join(s, ", ")
}

// Prefix each prose line so accepted text cannot impersonate structural output.
// Preserve newlines, escaping carriage returns, terminal escapes and bidi controls.
func writeProse(out *strings.Builder, label, text string) {
	if text == "" {
		fmt.Fprintf(out, "%s: empty\n", label)
		return
	}
	fmt.Fprintf(out, "%s:\n", label)
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(out, "  | %s\n", advisoryText(line))
	}
}
func writeState(out *strings.Builder, description string, lane A.Lane, priority A.Priority, archived bool, owner, approval *A.UUID, actors []A.Actor) {
	fmt.Fprintf(out, "Lane: %s; explicit priority: %s; archived: %t\nOwner: %s\nCurrent approval ID: ", lane, priority, archived, optionalActor(owner, actors))
	if approval == nil {
		out.WriteString("none\n")
	} else {
		fmt.Fprintf(out, "%s\n", *approval)
	}
	writeProse(out, "Description", description)
}
func writeFacts(out *strings.Builder, actors []A.Actor, comments []A.Comment, links []A.Link, attachments []A.Attachment, approvals []A.Approval) {
	ordered := append([]A.Comment(nil), comments...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt != ordered[j].CreatedAt {
			return ordered[i].CreatedAt < ordered[j].CreatedAt
		}
		return ordered[i].CommentID < ordered[j].CommentID
	})
	fmt.Fprintf(out, "Comments returned: %d\n", len(ordered))
	for _, c := range ordered {
		fmt.Fprintf(out, "  Comment %s at %s by %s; created in %s\n", c.CommentID, c.CreatedAt, actorText(c.ActorID, actors), originText(c.CreatedIn))
		writeProse(out, "Comment text", c.Text)
	}
	fmt.Fprintf(out, "Relationships returned: %d\n", len(links))
	for _, l := range links {
		fmt.Fprintf(out, "  %s %s -> %s/%s; active: %t; at %s; created in %s\n", l.LinkID, l.Kind, originText(A.Identity{BoardID: l.Target.BoardID, Incarnation: l.Target.Incarnation}), l.Target.CardID, l.Active, l.CreatedAt, originText(l.CreatedIn))
	}
	fmt.Fprintf(out, "Attachments returned: %d\n", len(attachments))
	for _, a := range attachments {
		fmt.Fprintf(out, "  %s: %s (%s; %d bytes; sha256 %s); at %s; created in %s\n", a.AttachmentID, advisoryText(a.Name), advisoryText(a.MediaLabel), a.ByteLength, a.SHA256, a.CreatedAt, originText(a.CreatedIn))
	}
	fmt.Fprintf(out, "Approvals returned: %d\n", len(approvals))
	for _, a := range approvals {
		fmt.Fprintf(out, "  %s by %s at %s; reviewed card revision %d, board revision %d, policy %d; created in %s\n", a.ApprovalID, actorText(a.ActorID, actors), a.CreatedAt, a.ReviewedCardRevision, a.ReviewedBoardRevision, a.PolicyVersion, originText(a.CreatedIn))
	}
}
func writeCoverage(out *strings.Builder, c A.Coverage) {
	fmt.Fprintf(out, "Coverage floors (not supplied does not prove full history): history %s; activity %s; outcome payload %s\nCoverage gaps returned: %d\n", timestampText(c.HistoryFloor), timestampText(c.ActivityFloor), timestampText(c.OutcomePayloadFloor), len(c.Gaps))
	for _, g := range c.Gaps {
		fmt.Fprintf(out, "  %s: %s, %s; after %s through %s; recorded %s at board revision %d\n", originText(g.Origin), g.Class, g.Reason, timestampText(g.FromExclusive), g.ThroughInclusive, g.RecordedAt, g.BoardRevision)
	}
}
