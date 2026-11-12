package client

import (
	jsonv2 "encoding/json/v2"
	"errors"
)

type TicketRelationshipTarget struct {
	TicketTarget
	RelatedCardID string
	LinkID        string
	LinkRevision  uint64
	LinkETag      string
}

// ResolveTicketRelationship reads the selected tickets and, for removal, the
// exact edge from one board revision. A zero related ticket addresses only the
// child for a parent clear; an empty kind does not select an existing edge.
func (c *Client) ResolveTicketRelationship(boardID string, ticket, related uint64, kind string) (TicketRelationshipTarget, error) {
	if related > maxTicketInteger || related == ticket || (kind != "" && related == 0) {
		return TicketRelationshipTarget{}, errors.New("expected a different positive safe related ticket")
	}
	reply, focus, err := c.inspectTicket(boardID, ticket, related, false)
	if err != nil {
		return TicketRelationshipTarget{}, err
	}
	result := TicketRelationshipTarget{TicketTarget: focus}
	if related == 0 {
		return result, nil
	}

	type reference struct {
		ID       string `json:"id"`
		Ticket   uint64 `json:"ticket"`
		Revision uint64 `json:"revision"`
	}
	var view struct {
		RelatedCard   reference `json:"related_card"`
		Relationships []struct {
			ID        string    `json:"id"`
			Revision  uint64    `json:"revision"`
			Kind      string    `json:"kind"`
			Direction string    `json:"direction"`
			Card      reference `json:"card"`
		} `json:"relationships"`
	}
	if jsonv2.Unmarshal(reply.JSON, &view) != nil || view.RelatedCard.Ticket != related ||
		!canonicalUUID4.MatchString(view.RelatedCard.ID) || view.RelatedCard.ID == focus.CardID ||
		view.RelatedCard.Revision == 0 || view.RelatedCard.Revision > focus.BoardRevision {
		return TicketRelationshipTarget{}, errors.New("service did not confirm the related ticket in the same board view")
	}
	result.RelatedCardID = view.RelatedCard.ID
	if kind == "" {
		return result, nil
	}
	direction := "outgoing"
	if kind == "relates_to" {
		direction = "symmetric"
	}
	seen := make(map[string]bool, len(view.Relationships))
	for _, link := range view.Relationships {
		if !canonicalUUID4.MatchString(link.ID) || seen[link.ID] ||
			link.Revision == 0 || link.Revision > focus.BoardRevision {
			return TicketRelationshipTarget{}, errors.New("service returned ambiguous or invalid relationship identities")
		}
		seen[link.ID] = true
		if link.Kind != kind || (link.Card.ID != result.RelatedCardID && link.Card.Ticket != related) {
			continue
		}
		if link.Card != view.RelatedCard ||
			(link.Direction != direction && !(direction == "outgoing" && link.Direction == "incoming")) {
			return TicketRelationshipTarget{}, errors.New("service did not confirm the relationship endpoint and direction")
		}
		if link.Direction != direction {
			continue
		}
		if result.LinkID != "" {
			return TicketRelationshipTarget{}, errors.New("more than one relationship matches the requested kind and direction")
		}
		result.LinkID, result.LinkRevision = link.ID, link.Revision
		result.LinkETag = ticketEntityTag(link.ID, link.Revision)
	}
	if result.LinkID == "" {
		return TicketRelationshipTarget{}, errors.New("no relationship matches the requested kind and direction")
	}
	return result, nil
}
