package v2

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
)

type ticketLane struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Label   string `json:"label"`
	Role    string `json:"role"`
	Retired bool   `json:"retired"`
}

type ticketOwner struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type ticketPriority struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Label string `json:"label"`
	Rank  int    `json:"rank"`
}

type ticketCard struct {
	ID       string         `json:"id"`
	Ticket   uint64         `json:"ticket"`
	Revision uint64         `json:"revision"`
	Title    string         `json:"title"`
	Archived bool           `json:"archived"`
	Lane     ticketLane     `json:"lane"`
	Owner    ticketOwner    `json:"owner"`
	Priority ticketPriority `json:"priority"`
}

type ticketFocus struct {
	ticketCard
	ETag string `json:"etag"`
}

type ticketRelationship struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Direction string     `json:"direction"`
	Card      ticketCard `json:"card"`
}

type ticketProse struct {
	Description string    `json:"description"`
	Comments    []comment `json:"comments"`
}

// TicketInspection contains one card and its direct relationship and hierarchy
// context from a single accepted or validated snapshot.
type TicketInspection struct {
	BoardID       string               `json:"board_id"`
	BoardRevision uint64               `json:"board_revision"`
	Card          ticketFocus          `json:"card"`
	Relationships []ticketRelationship `json:"relationships"`
	Parent        *ticketCard          `json:"parent"`
	Children      []ticketCard         `json:"children"`
	Prose         *ticketProse         `json:"prose,omitempty"`
}

// InspectTicket builds the same projection for the live and offline read paths.
// Its input is an already accepted or validated board snapshot.
func InspectTicket(state snapshot, ticket uint64, includeProse bool) (TicketInspection, error) {
	if ticket == 0 || ticket > maxSnapshotInteger {
		return TicketInspection{}, invalid("ticket", "Expected a positive safe integer.")
	}
	index := slices.IndexFunc(state.Cards, func(item card) bool { return item.Ticket == ticket })
	if index < 0 {
		return TicketInspection{}, errMissing
	}
	focus := state.Cards[index]
	cards := relationshipCards(&state)
	lanes := make(map[string]ticketLane, len(state.Lanes))
	for _, item := range state.Lanes {
		lanes[item.ID] = ticketLane{item.ID, item.Key, item.Label, item.Role, item.Retired}
	}
	owners := make(map[string]ticketOwner, len(state.Users))
	for _, item := range state.Users {
		owners[item.ID] = ticketOwner{item.ID, item.Label}
	}
	priorities := make(map[string]ticketPriority, len(state.Priorities))
	for _, item := range state.Priorities {
		priorities[item.ID] = ticketPriority{item.ID, item.Key, item.Label, item.Rank}
	}
	context := func(item *card) ticketCard {
		return ticketCard{
			ID: item.ID, Ticket: item.Ticket, Revision: item.Revision, Title: item.Title,
			Archived: item.Archived, Lane: lanes[item.LaneID], Owner: owners[item.OwnerID], Priority: priorities[item.PriorityID],
		}
	}
	result := TicketInspection{
		BoardID: state.Board.ID, BoardRevision: state.Board.Revision,
		Card:          ticketFocus{ticketCard: context(&focus), ETag: focus.entityTag()},
		Relationships: []ticketRelationship{}, Children: []ticketCard{},
	}
	if focus.ParentID != nil {
		parent := context(cards[*focus.ParentID])
		result.Parent = &parent
	}
	for _, id := range focus.ChildIDs {
		result.Children = append(result.Children, context(cards[id]))
	}
	slices.SortFunc(result.Children, func(a, b ticketCard) int { return cmp.Compare(a.Ticket, b.Ticket) })
	for _, link := range state.Relationships {
		var other, direction string
		switch focus.ID {
		case link.SourceCardID:
			other, direction = link.TargetCardID, "outgoing"
		case link.TargetCardID:
			other, direction = link.SourceCardID, "incoming"
		default:
			continue
		}
		if link.Kind == "relates_to" {
			direction = "symmetric"
		}
		result.Relationships = append(result.Relationships, ticketRelationship{link.ID, link.Kind, direction, context(cards[other])})
	}
	slices.SortFunc(result.Relationships, func(a, b ticketRelationship) int {
		return cmp.Or(cmp.Compare(a.Card.Ticket, b.Card.Ticket), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Direction, b.Direction))
	})
	if includeProse {
		prose := ticketProse{Description: focus.Description, Comments: []comment{}}
		for _, item := range state.Comments {
			if item.CardID == focus.ID {
				prose.Comments = append(prose.Comments, item)
			}
		}
		// As with the comment collection, newer commits win timestamp ties.
		slices.Reverse(prose.Comments)
		slices.SortStableFunc(prose.Comments, func(a, b comment) int { return b.CreatedAt.Compare(a.CreatedAt) })
		result.Prose = &prose
	}
	return result, nil
}

func (x *request) boardTicket(state snapshot) {
	rawTicket, err := singleQuery(x.r, "ticket")
	if err != nil {
		x.fail(err)
		return
	}
	ticket, err := strconv.ParseUint(rawTicket, 10, 64)
	if err != nil || ticket == 0 || ticket > maxSnapshotInteger {
		x.fail(invalid("ticket", "Expected a positive safe integer."))
		return
	}
	prose, err := singleQuery(x.r, "include_prose")
	if err != nil {
		x.fail(err)
		return
	}
	if x.r.URL.Query().Has("include_prose") && prose != "true" && prose != "false" {
		x.fail(invalid("include_prose", "Expected true or false."))
		return
	}
	result, err := InspectTicket(state, ticket, prose == "true")
	if err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("X-Board-ETag", state.Board.ETag)
	x.respond(http.StatusOK, result, "")
}
