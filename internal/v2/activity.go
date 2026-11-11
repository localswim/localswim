package v2

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type activityQuery struct {
	from, to           time.Time
	full               bool
	actorID, operation string
	cardID, writerID   string
	ticket             uint64
}

type activityCard struct {
	CardID string `json:"card_id"`
	Ticket uint64 `json:"ticket"`
}

type activityMetadata struct {
	ID        string         `json:"id"`
	BoardID   string         `json:"board_id"`
	Revision  uint64         `json:"revision"`
	At        time.Time      `json:"at"`
	ActorID   string         `json:"actor_id"`
	ActorKind string         `json:"actor_kind"`
	Operation string         `json:"operation"`
	Cards     []activityCard `json:"cards"`
	Tickets   []uint64       `json:"tickets"`
}

type activityCoverage struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Complete bool      `json:"complete"`
	Code     string    `json:"code"`
}

type activityObservation struct {
	state      snapshot
	observedAt time.Time
	durable    bool
}

func activityBound(field, text string) (time.Time, error) {
	parsed, err := time.Parse(timestampLayout, text)
	if err != nil || parsed.Format(timestampLayout) != text {
		return time.Time{}, invalid(field, "Expected UTC timestamp YYYY-MM-DD HH:MM:SS+00:00.")
	}
	return parsed, nil
}

var activityOperationPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)

func readActivityQuery(values url.Values) (activityQuery, error) {
	var query activityQuery
	for _, field := range []string{"from", "to", "view", "ticket", "card_id", "writer_id", "actor_id", "operation", "limit", "cursor"} {
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
	case view == "", strings.EqualFold(view, "monitor"):
	case strings.EqualFold(view, "full"):
		query.full = true
	default:
		return query, invalid("view", "Expected full or monitor.")
	}
	if values.Has("ticket") {
		query.ticket, err = strconv.ParseUint(values.Get("ticket"), 10, 64)
		if err != nil || query.ticket == 0 || query.ticket > maxSnapshotInteger {
			return query, invalid("ticket", "Expected a positive safe integer.")
		}
	}
	if values.Has("actor_id") {
		var valid bool
		query.actorID, valid = normalizeID(values.Get("actor_id"))
		if !valid {
			return query, invalid("actor_id", "Expected a UUIDv4.")
		}
	}
	if values.Has("card_id") {
		var valid bool
		query.cardID, valid = normalizeID(values.Get("card_id"))
		if !valid {
			return query, invalid("card_id", "Expected a UUIDv4.")
		}
	}
	if values.Has("writer_id") {
		var valid bool
		query.writerID, valid = normalizeOperationID(values.Get("writer_id"))
		if !valid {
			return query, invalid("writer_id", "Expected a UUIDv7.")
		}
	}
	if values.Has("operation") {
		query.operation = values.Get("operation")
		if !activityOperationPattern.MatchString(query.operation) {
			return query, invalid("operation", "Expected a lowercase operation identifier of 1 to 80 characters.")
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
	observation, err := x.service.store.observeActivity(boardID)
	if err != nil {
		x.fail(err)
		return
	}
	if !observation.state.can(x.actor, "read") {
		x.fail(errMissing)
		return
	}
	writeActivityPage(x, observation, query)
}

func (s *store) observeActivity(id string) (activityObservation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lease == nil {
		return activityObservation{}, errStoreClosed
	}
	current, exists := s.boards[id]
	if !exists {
		return activityObservation{}, errBoardNotFound
	}
	observation := activityObservation{state: cloneSnapshot(current), observedAt: canonicalTime(time.Now())}
	if s.writeUncertain && s.uncertainBoardID == id {
		// Preserve durable inspection after an uncertain write, without
		// certifying the result as continuous accepted writer activity.
		if saved, err := s.readDurableLocked(id); err == nil {
			observation.state = saved
		}
		return observation, nil
	}
	observation.durable = s.checkAcceptedDurableLocked(current) == nil
	return observation, nil
}

type activityIndex struct {
	cards       map[string]uint64
	comments    map[string]string
	attachments map[string]string
	kinds       map[string]string
}

func indexActivity(state snapshot) activityIndex {
	index := activityIndex{
		cards:       make(map[string]uint64, len(state.Cards)),
		comments:    make(map[string]string, len(state.Comments)),
		attachments: make(map[string]string, len(state.Attachments)),
		// The runtime principals can create a board without joining it.
		kinds: map[string]string{humanID: "human", botID: "bot"},
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
	for _, member := range state.Users {
		index.kinds[member.ID] = member.Kind
	}
	return index
}

func (index activityIndex) metadata(event activity) activityMetadata {
	kind := index.kinds[event.ActorID]
	if event.Operation == recoveryOperation && event.ResourceType == "board" {
		if evidence, ok := recoveryEventEvidence(event); ok {
			kind = evidence.ActorKind
		}
	}
	cards := index.cardReferences(event)
	return activityMetadata{
		ID: event.ID, BoardID: event.BoardID, Revision: event.Revision, At: event.At,
		ActorID: event.ActorID, ActorKind: kind, Operation: event.Operation, Cards: cards, Tickets: activityTickets(cards),
	}
}

func (index activityIndex) cardReferences(event activity) []activityCard {
	cards := []activityCard{}
	add := func(id string) {
		if ticket := index.cards[id]; ticket != 0 {
			cards = append(cards, activityCard{CardID: id, Ticket: ticket})
		}
	}
	switch event.ResourceType {
	case "card":
		add(event.ResourceID)
	case "comment":
		add(index.comments[event.ResourceID])
	case "attachment":
		add(index.attachments[event.ResourceID])
	case "relationship":
		// Verified audit values retain both endpoints after a relationship is deleted.
		linkText := event.After
		if linkText == nil {
			linkText = event.Before
		}
		if linkText != nil {
			var link relationshipLink
			if json.Unmarshal([]byte(*linkText), &link) == nil {
				add(link.SourceCardID)
				add(link.TargetCardID)
			}
		}
	}
	slices.SortFunc(cards, compareActivityCards)
	return slices.Compact(cards)
}

func compareActivityCards(a, b activityCard) int {
	if a.Ticket < b.Ticket {
		return -1
	}
	if a.Ticket > b.Ticket {
		return 1
	}
	return strings.Compare(a.CardID, b.CardID)
}

func activityTickets(cards []activityCard) []uint64 {
	tickets := make([]uint64, 0, len(cards))
	for _, card := range cards {
		tickets = append(tickets, card.Ticket)
	}
	return slices.Compact(tickets)
}

func (query activityQuery) coverage(observation activityObservation, writerID string, since time.Time) activityCoverage {
	code := "complete"
	switch {
	case query.writerID != "" && query.writerID != writerID:
		code = "writer_mismatch"
	case !observation.durable:
		code = "durable_state_unavailable"
	case query.from.Before(since):
		code = "startup_gap"
	case !query.to.Before(observation.observedAt):
		code = "interval_open"
	default:
		// Recovery can replace the entire accepted history. The writer can
		// certify full UTC seconds only after the latest replacement.
		if i := slices.LastIndexFunc(observation.state.Activity, func(event activity) bool {
			return event.ResourceType == "board" && event.Operation == recoveryOperation
		}); i >= 0 && !query.from.After(observation.state.Activity[i].At) {
			code = "recovery_gap"
		}
	}
	return activityCoverage{From: query.from, To: query.to, Complete: code == "complete", Code: code}
}

func writeActivityPage(x *request, observation activityObservation, query activityQuery) {
	state := observation.state
	writerID := x.service.store.activityWriterID
	coverage := query.coverage(observation, writerID, x.service.store.activitySince)
	var full []activity
	var metadata []activityMetadata
	var index activityIndex
	withCards := !query.full || query.ticket != 0 || query.cardID != ""
	if withCards {
		index = indexActivity(state)
	}
	botOnly := coverage.Complete && query.actorID == "" && query.operation == "" && query.ticket == 0 && query.cardID == ""
	// Start with current ticket assignments and unwind each audited renumber
	// after reading its event. A renumber event carries its post-commit ticket.
	for i := len(state.Activity) - 1; i >= 0; i-- {
		event := state.Activity[i]
		matches := !event.At.Before(query.from) && !event.At.After(query.to) &&
			(query.actorID == "" || event.ActorID == query.actorID) &&
			(query.operation == "" || event.Operation == query.operation)
		var row activityMetadata
		if withCards {
			if matches {
				row = index.metadata(event)
			}
			if event.Operation == ticketOrderOperation {
				ticket, _ := ticketAuditValue(event.Before)
				index.cards[event.ResourceID] = ticket
			}
		}
		if !matches {
			continue
		}
		if (query.ticket != 0 || query.cardID != "") && !slices.ContainsFunc(row.Cards, func(card activityCard) bool {
			return (query.ticket == 0 || card.Ticket == query.ticket) && (query.cardID == "" || card.CardID == query.cardID)
		}) {
			continue
		}
		botOnly = botOnly && event.ActorID == botID
		if query.full {
			full = append(full, event)
		} else {
			metadata = append(metadata, row)
		}
	}
	scope := []string{
		state.Board.ID, query.from.Format(timestampLayout), query.to.Format(timestampLayout),
		strconv.FormatBool(query.full), strconv.FormatUint(query.ticket, 10), query.actorID, query.operation,
		query.cardID, query.writerID, writerID, strconv.FormatUint(state.Board.Revision, 10),
	}
	result := map[string]any{"writer_id": writerID, "board_revision": state.Board.Revision, "coverage": coverage}
	if query.full {
		slices.Reverse(full)
		respondActivity(x, full, result, botOnly, scope)
	} else {
		slices.Reverse(metadata)
		respondActivity(x, metadata, result, botOnly, scope)
	}
}

func respondActivity[T any](x *request, items []T, result map[string]any, botOnly bool, scope []string) {
	values, cursor, err := collectionSlice(x.r, items, scope...)
	if err != nil {
		x.fail(err)
		return
	}
	result["activities"] = values
	result["next_cursor"] = cursor
	result["pagination_complete"] = cursor == nil
	result["bot_only"] = cursor == nil && botOnly && len(items) != 0
	x.respond(http.StatusOK, result, "")
}
