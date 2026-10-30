package transport

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
)

// A BoardView contains the complete current card collection. Resolve links in
// that same identity here; a CardView has no complete target-card collection.
// Inherited and foreign identities remain external references to that view.
func boardLinkTargets(identity A.Identity, cards []A.Card, links []A.Link) error {
	if e := A.ValidateForest(identity, cards); e != nil {
		return e
	}
	ids := make(map[A.UUID]bool, len(cards))
	for _, card := range cards {
		ids[card.CardID] = true
	}
	for _, link := range links {
		if link.Target.BoardID == identity.BoardID && link.Target.Incarnation == identity.Incarnation && !ids[link.Target.CardID] {
			return fmt.Errorf("current internal link target missing")
		}
	}
	return nil
}

// current validates projection membership using C03's exact rows. It introduces
// no alternative card model, admission policy or archive recovery behavior.
func current(identity A.Identity, cards []A.Card, actors []A.Actor, comments []A.Comment, links []A.Link, attachments []A.Attachment, approvals []A.Approval, exactActors bool) error {
	owners := map[A.UUID]A.Card{}
	tickets := map[A.Positive]bool{}
	for _, c := range cards {
		if _, ok := owners[c.CardID]; ok || tickets[c.Ticket] || c.CreatedIn.BoardID != identity.BoardID {
			return fmt.Errorf("current card scope/duplicate")
		}
		owners[c.CardID] = c
		tickets[c.Ticket] = true
	}
	actorIDs := map[A.UUID]A.Kind{}
	neededActors := map[A.UUID]bool{}
	for _, a := range actors {
		if a.CreatedIn.BoardID != identity.BoardID {
			return fmt.Errorf("actor provenance board")
		}
		actorIDs[a.ActorID] = a.Kind
	}
	type key struct {
		kind string
		id   A.UUID
	}
	required := map[key]A.UUID{}
	found := map[key]bool{}
	for _, c := range cards {
		if c.Parent != nil && (c.Parent.BoardID != identity.BoardID || c.Parent.Incarnation != identity.Incarnation || c.Parent.CardID == c.CardID) {
			return fmt.Errorf("current parent scope")
		}
		if c.OwnerID != nil {
			if _, ok := actorIDs[*c.OwnerID]; !ok {
				return fmt.Errorf("card assignee missing")
			}
			neededActors[*c.OwnerID] = true
		}
		for kind, ids := range map[string][]A.UUID{"comment": c.CommentIDs, "link": c.LinkIDs, "attachment": c.AttachmentIDs} {
			for _, id := range ids {
				k := key{kind, id}
				if _, ok := required[k]; ok {
					return fmt.Errorf("shared current row ownership")
				}
				required[k] = c.CardID
			}
		}
	}
	row := func(kind string, id, owner A.UUID, origin A.Identity) error {
		k := key{kind, id}
		if origin.BoardID != identity.BoardID || required[k] != owner || found[k] {
			return fmt.Errorf("current reference membership")
		}
		found[k] = true
		return nil
	}
	for _, c := range comments {
		if e := row("comment", c.CommentID, c.CardID, c.CreatedIn); e != nil {
			return e
		}
		if _, ok := actorIDs[c.ActorID]; !ok {
			return fmt.Errorf("comment actor missing")
		}
		neededActors[c.ActorID] = true
	}
	for _, l := range links {
		if e := row("link", l.LinkID, l.CardID, l.CreatedIn); e != nil {
			return e
		}
	}
	lengths := map[A.Digest]A.Number{}
	for _, a := range attachments {
		if e := row("attachment", a.AttachmentID, a.CardID, a.CreatedIn); e != nil {
			return e
		}
		if n, ok := lengths[a.SHA256]; ok && n != a.ByteLength {
			return fmt.Errorf("byte length conflict")
		}
		lengths[a.SHA256] = a.ByteLength
	}
	if len(found) != len(required) {
		return fmt.Errorf("current row missing")
	}
	approvalIDs := map[A.UUID]A.Approval{}
	for _, a := range approvals {
		c, ok := owners[a.CardID]
		if !ok || a.CreatedIn.BoardID != identity.BoardID || a.ReviewedCardRevision > c.Revision {
			return fmt.Errorf("approval scope")
		}
		if actorIDs[a.ActorID] != "human" {
			return fmt.Errorf("approval actor")
		}
		neededActors[a.ActorID] = true
		approvalIDs[a.ApprovalID] = a
	}
	for _, c := range cards {
		if c.CurrentApprovalID != nil {
			a, ok := approvalIDs[*c.CurrentApprovalID]
			if !ok || a.CardID != c.CardID {
				return fmt.Errorf("current approval missing")
			}
		}
	}
	if exactActors && len(neededActors) != len(actorIDs) {
		return fmt.Errorf("extra current actor")
	}
	return nil
}
