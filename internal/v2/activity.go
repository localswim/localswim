package v2

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type activityQuery struct {
	from, to time.Time
	monitor  bool
	ticket   uint64
}

type activityMetadata struct {
	ID        string    `json:"id"`
	BoardID   string    `json:"board_id"`
	Revision  uint64    `json:"revision"`
	At        time.Time `json:"at"`
	ActorID   string    `json:"actor_id"`
	ActorKind string    `json:"actor_kind"`
	Operation string    `json:"operation"`
	Tickets   []uint64  `json:"tickets"`
}

func activityBound(field, text string) (time.Time, error) {
	parsed, err := time.Parse(timestampLayout, text)
	if err != nil || parsed.Format(timestampLayout) != text {
		return time.Time{}, invalid(field, "Expected UTC timestamp YYYY-MM-DD HH:MM:SS+00:00.")
	}
	return parsed, nil
}

func readActivityQuery(values url.Values) (activityQuery, error) {
	var query activityQuery
	for _, field := range []string{"from", "to", "view", "ticket", "limit", "cursor"} {
		if len(values[field]) > 1 {
			return query, invalid(field, "Repeated query parameter.")
		}
		if values.Has(field) && values.Get(field) == "" {
			return query, invalid(field, "Expected a nonempty value.")
		}
	}
	var err error
	query.from, err = activityBound("from", values.Get("from"))
	if err != nil {
		return query, err
	}
	query.to, err = activityBound("to", values.Get("to"))
	if err != nil {
		return query, err
	}
	if query.to.Before(query.from) {
		return query, invalid("to", "Expected a timestamp at or after from.")
	}
	view := values.Get("view")
	switch {
	case view == "", strings.EqualFold(view, "full"):
	case strings.EqualFold(view, "monitor"):
		query.monitor = true
	default:
		return query, invalid("view", "Expected full or monitor.")
	}
	if values.Has("ticket") {
		query.ticket, err = strconv.ParseUint(values.Get("ticket"), 10, 64)
		if err != nil || query.ticket == 0 || query.ticket > maxSnapshotInteger {
			return query, invalid("ticket", "Expected a positive safe integer.")
		}
	}
	return query, nil
}

func (x *request) listActivities() {
	values, err := url.ParseQuery(x.r.URL.RawQuery)
	if err != nil {
		x.fail(invalid("board_id", "Malformed query string."))
		return
	}
	if len(values["board_id"]) > 1 {
		x.fail(invalid("board_id", "Repeated query parameter."))
		return
	}
	boardID, valid := normalizeID(values.Get("board_id"))
	if !valid {
		x.fail(invalid("board_id", "Expected a UUIDv4."))
		return
	}
	known, exists := x.service.store.accepted(boardID)
	if !exists || !slices.Contains(known.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !known.can(x.actor, "read") {
		x.fail(errForbidden)
		return
	}
	query, err := readActivityQuery(values)
	if err != nil {
		x.fail(err)
		return
	}
	state, err := x.service.store.readback(boardID)
	if err != nil {
		x.fail(err)
		return
	}
	if !state.can(x.actor, "read") {
		x.fail(errMissing)
		return
	}
	writeActivityPage(x, state, query)
}

type activityTicketIndex struct {
	cards       map[string]uint64
	comments    map[string]string
	attachments map[string]string
}

func indexActivityTickets(state snapshot) activityTicketIndex {
	index := activityTicketIndex{
		cards:       make(map[string]uint64, len(state.Cards)),
		comments:    make(map[string]string, len(state.Comments)),
		attachments: make(map[string]string, len(state.Attachments)),
	}
	for _, item := range state.Cards {
		index.cards[item.ID] = item.Ticket
	}
	for _, item := range state.Comments {
		index.comments[item.ID] = item.CardID
	}
	for _, item := range state.Attachments {
		index.attachments[item.ID] = item.CardID
	}
	return index
}

func (index activityTicketIndex) tickets(event activity) []uint64 {
	tickets := []uint64{}
	var cardID string
	switch event.ResourceType {
	case "card":
		cardID = event.ResourceID
	case "comment":
		cardID = index.comments[event.ResourceID]
	case "attachment":
		cardID = index.attachments[event.ResourceID]
	case "relationship":
		// Verified audit values retain both endpoints after a relationship is deleted.
		linkText := event.After
		if linkText == nil {
			linkText = event.Before
		}
		if linkText != nil {
			var link relationshipLink
			if json.Unmarshal([]byte(*linkText), &link) == nil {
				tickets = append(tickets, index.cards[link.SourceCardID], index.cards[link.TargetCardID])
			}
		}
	}
	if ticket := index.cards[cardID]; ticket != 0 {
		tickets = append(tickets, ticket)
	}
	slices.Sort(tickets)
	return tickets
}

func writeActivityPage(x *request, state snapshot, query activityQuery) {
	var full []activity
	var monitor []activityMetadata
	var index activityTicketIndex
	if query.monitor || query.ticket != 0 {
		index = indexActivityTickets(state)
	}
	// A board's creator can be a runtime principal outside the board membership.
	kinds := map[string]string{humanID: "human", botID: "bot"}
	for _, member := range state.Users {
		kinds[member.ID] = member.Kind
	}
	for _, event := range state.Activity {
		if event.At.Before(query.from) || event.At.After(query.to) {
			continue
		}
		var tickets []uint64
		if query.monitor || query.ticket != 0 {
			tickets = index.tickets(event)
		}
		if query.ticket != 0 && !slices.Contains(tickets, query.ticket) {
			continue
		}
		if query.monitor {
			monitor = append(monitor, activityMetadata{
				ID: event.ID, BoardID: event.BoardID, Revision: event.Revision, At: event.At,
				ActorID: event.ActorID, ActorKind: kinds[event.ActorID], Operation: event.Operation, Tickets: tickets,
			})
		} else {
			full = append(full, event)
		}
	}
	view := "full"
	if query.monitor {
		view = "monitor"
	}
	scope := []string{state.Board.ID, query.from.Format(timestampLayout), query.to.Format(timestampLayout), view, strconv.FormatUint(query.ticket, 10)}
	if query.monitor {
		page(x, "activities", monitor, scope...)
	} else {
		page(x, "activities", full, scope...)
	}
}
