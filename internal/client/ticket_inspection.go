package client

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const maxTicketInteger = 1<<53 - 1

// TicketTarget holds the resource identities and write validators from one read.
type TicketTarget struct {
	Ticket        uint64
	BoardID       string
	BoardRevision uint64
	BoardETag     string
	CardID        string
	CardRevision  uint64
	CardETag      string
}

// InspectTicket reads one board's focused ticket view with the selected prose mode.
func (c *Client) InspectTicket(boardID string, ticket uint64, includeProse bool) (Response, error) {
	reply, _, err := c.inspectTicket(boardID, ticket, 0, includeProse)
	return reply, err
}

// ResolveTicket reads one focused view and validates both resource validators.
func (c *Client) ResolveTicket(boardID string, ticket uint64) (TicketTarget, error) {
	_, target, err := c.inspectTicket(boardID, ticket, 0, false)
	return target, err
}

func (c *Client) inspectTicket(boardID string, ticket, relatedTicket uint64, includeProse bool) (Response, TicketTarget, error) {
	boardID = strings.ToLower(boardID)
	if !canonicalUUID4.MatchString(boardID) || ticket == 0 || ticket > maxTicketInteger {
		return Response{}, TicketTarget{}, errors.New("expected a board UUIDv4 and a positive safe ticket integer")
	}
	resource := "/boards/" + boardID + "?view=ticket&ticket=" + strconv.FormatUint(ticket, 10)
	if relatedTicket != 0 {
		resource += "&related_ticket=" + strconv.FormatUint(relatedTicket, 10)
	}
	if includeProse {
		resource += "&include_prose=true"
	}
	reply, err := c.Do(http.MethodGet, resource, nil, "")
	if err != nil {
		return Response{}, TicketTarget{}, err
	}
	var document struct {
		BoardID       string `json:"board_id"`
		BoardRevision uint64 `json:"board_revision"`
		Card          struct {
			ID       string `json:"id"`
			Ticket   uint64 `json:"ticket"`
			Revision uint64 `json:"revision"`
		} `json:"card"`
		Prose json.RawMessage `json:"prose"`
	}
	if jsonv2.Unmarshal(reply.JSON, &document) != nil ||
		document.BoardID != boardID || document.BoardRevision == 0 || document.BoardRevision > maxTicketInteger ||
		!canonicalUUID4.MatchString(document.Card.ID) || document.Card.Ticket != ticket ||
		document.Card.Revision == 0 || document.Card.Revision > document.BoardRevision ||
		(!includeProse && len(document.Prose) != 0) || (includeProse && !IsJSONObject(document.Prose)) {
		return Response{}, TicketTarget{}, errors.New("service did not return the requested ticket with valid board and card validators")
	}
	if !MatchesEntityTagRevision(reply.BoardETag, boardID, document.BoardRevision) {
		return Response{}, TicketTarget{}, errors.New("service did not confirm the ticket view's board validator")
	}
	target := TicketTarget{
		Ticket: ticket, BoardID: boardID, BoardRevision: document.BoardRevision, BoardETag: reply.BoardETag,
		CardID: document.Card.ID, CardRevision: document.Card.Revision,
		CardETag: ticketEntityTag(document.Card.ID, document.Card.Revision),
	}
	return reply, target, nil
}

// MatchesEntityTagRevision binds a header validator to a resource ID and revision.
func MatchesEntityTagRevision(tag, id string, revision uint64) bool {
	return canonicalUUID4.MatchString(id) && revision > 0 && revision <= maxTicketInteger &&
		tag == ticketEntityTag(id, revision)
}

func ticketEntityTag(id string, revision uint64) string {
	return fmt.Sprintf("\"%s-%d\"", id, revision)
}
