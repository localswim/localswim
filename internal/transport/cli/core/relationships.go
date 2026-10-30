package core

import (
	"fmt"
	"sort"
	"strings"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

// RelationshipRead is a CLI-only projection of one complete BoardRead. It does
// not widen HTTP scope or imply settlement of an original mutation.
type RelationshipRead struct {
	APIVersion A.Version        `json:"apiVersion"`
	Scope      string           `json:"scope"`
	Result     RelationshipView `json:"result"`
}
type RelationshipView struct {
	Identity      A.Identity        `json:"identity"`
	Revision      A.Number          `json:"revision"`
	Card          A.CardRef         `json:"card"`
	OwnedCount    A.Number          `json:"ownedCount"`
	IncomingCount A.Number          `json:"incomingCount"`
	Relationships []RelationshipRow `json:"relationships"`
	Complete      bool              `json:"complete"`
}
type RelationshipRow struct {
	LinkID    A.UUID     `json:"linkId"`
	Owner     A.CardRef  `json:"owner"`
	Peer      A.CardRef  `json:"peer"`
	Kind      A.LinkKind `json:"kind"`
	Direction string     `json:"direction"`
	Label     string     `json:"label"`
	Owned     bool       `json:"owned"`
}

// Presentation details remain local to the same validated snapshot and are not
// additional RelationshipRead fields. Exact references prevent UUID collisions
// in foreign/historical evidence from borrowing a local card's title or ticket.
type relationshipObservation struct {
	read  RelationshipRead
	peers map[A.CardRef]A.Card
}

func relationshipRead(board T.BoardRead, id string) (relationshipObservation, error) {
	var out relationshipObservation
	if err := T.Validate(board); err != nil {
		return out, err
	}
	b := board.Result
	ref := func(id A.UUID) A.CardRef {
		return A.CardRef{BoardID: b.Identity.BoardID, Incarnation: b.Identity.Incarnation, CardID: id}
	}
	peers := map[A.CardRef]A.Card{}
	for _, c := range b.Cards {
		peers[ref(c.CardID)] = c
	}
	selected := ref(A.UUID(id))
	if _, ok := peers[selected]; !ok {
		return out, fmt.Errorf("card unavailable")
	}
	v := RelationshipView{Identity: b.Identity, Revision: b.Revision, Card: selected, Relationships: []RelationshipRow{}, Complete: true}
	for _, l := range b.Links {
		owner := ref(l.CardID)
		owned := owner == selected
		if !l.Active || !owned && l.Target != selected {
			continue
		}
		row := RelationshipRow{LinkID: l.LinkID, Owner: owner, Peer: l.Target, Kind: l.Kind, Direction: "outgoing", Owned: owned}
		if owned {
			v.OwnedCount++
		} else {
			v.IncomingCount++
			row.Peer = owner
			row.Direction = "incoming"
		}
		switch l.Kind {
		case "blocking":
			row.Label = "blocked by"
			if !owned {
				row.Label = "blocks"
			}
		case "informational":
			row.Label = "informs"
			if !owned {
				row.Label = "informed by"
			}
		case "reference":
			row.Label = "references"
			if !owned {
				row.Label = "referenced by"
			}
		case "duplicate":
			row.Label = "duplicates"
			if !owned {
				row.Label = "duplicated by"
			}
		case "related":
			row.Label, row.Direction = "relates to", "symmetric"
		}
		v.Relationships = append(v.Relationships, row)
	}
	sort.Slice(v.Relationships, func(i, j int) bool {
		a, z := v.Relationships[i], v.Relationships[j]
		ac, alocal := peers[a.Peer]
		zc, zlocal := peers[z.Peer]
		if alocal != zlocal {
			return alocal
		}
		if alocal && ac.Ticket != zc.Ticket {
			return ac.Ticket < zc.Ticket
		}
		if a.Peer != z.Peer {
			return refText(a.Peer) < refText(z.Peer)
		}
		if a.Kind != z.Kind {
			return a.Kind < z.Kind
		}
		if a.Owner.CardID != z.Owner.CardID {
			return a.Owner.CardID < z.Owner.CardID
		}
		return a.LinkID < z.LinkID
	})
	return relationshipObservation{RelationshipRead{APIVersion: 1, Scope: "relationships", Result: v}, peers}, nil
}

func refText(r A.CardRef) string {
	return string(r.BoardID) + "/" + string(r.Incarnation) + "/" + string(r.CardID)
}

func readableRelationships(h relationshipObservation, page int) string {
	b := h.read.Result
	var out strings.Builder
	pages := (len(b.Relationships) + 19) / 20
	if pages == 0 {
		pages = 1
	}
	start, end := (page-1)*20, page*20
	if end > len(b.Relationships) {
		end = len(b.Relationships)
	}
	first := start + 1
	if end == 0 {
		first = 0
	}
	fmt.Fprintf(&out, "Relationships %s; board revision %d\nOwned: %d; incoming: %d; total: %d\nRows %d-%d of %d; page %d of %d\n", refText(b.Card), b.Revision, b.OwnedCount, b.IncomingCount, len(b.Relationships), first, end, len(b.Relationships), page, pages)
	for _, r := range b.Relationships[start:end] {
		peer := "unavailable"
		if c, ok := h.peers[r.Peer]; ok {
			peer = fmt.Sprintf("#%d %s (lane: %s; archived: %t)", c.Ticket, advisoryText(c.Title), c.Lane, c.Archived)
		}
		fmt.Fprintf(&out, "%s [%s; %s; owned: %t] %s; peer %s; owner %s; link %s\n", r.Label, r.Kind, r.Direction, r.Owned, peer, refText(r.Peer), refText(r.Owner), r.LinkID)
	}
	if page == pages {
		out.WriteString("End of relationships.\n")
	} else {
		fmt.Fprintf(&out, "Next local page: %d (a new invocation reads a fresh board).\n", page+1)
	}
	return out.String()
}
