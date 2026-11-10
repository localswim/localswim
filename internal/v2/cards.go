package v2

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

var errCardBoardETagRequired = errors.New("card creation requires expected_board_etag")

type cardCreation struct {
	id, boardID, expected, title, description, laneID, priorityID string
	ownerID, parentID                                             *string
}

func (x *request) cardsCollection() {
	if x.r.Method == http.MethodPost {
		x.createCard()
		return
	}
	x.listCards()
}

func (x *request) cardResource(id string) {
	state, value, found := x.findCard(id)
	if !found || !state.can(x.actor, "read") {
		x.fail(errMissing)
		return
	}
	x.respond(http.StatusOK, value, value.ETag)
}

func (x *request) findCard(id string) (snapshot, card, bool) {
	for _, state := range x.service.store.list() {
		if !slices.Contains(state.Board.UserIDs, x.actor) {
			continue
		}
		for _, value := range state.Cards {
			if value.ID == id {
				return state, value, true
			}
		}
	}
	return snapshot{}, card{}, false
}

func (x *request) listCards() {
	rawID, err := singleQuery(x.r, "board_id")
	if err != nil {
		x.fail(err)
		return
	}
	id, valid := normalizeID(rawID)
	if !valid {
		x.fail(invalid("board_id", "Expected a UUIDv4."))
		return
	}
	state, exists := x.service.store.accepted(id)
	if !exists || !state.can(x.actor, "read") {
		x.fail(errMissing)
		return
	}
	filters, err := readCardFilters(x.r)
	if err != nil {
		x.fail(err)
		return
	}
	rows := []card{}
	for _, value := range state.Cards {
		if filters.ticket != 0 && value.Ticket != filters.ticket ||
			filters.laneID != "" && value.LaneID != filters.laneID ||
			filters.archived != "include" && value.Archived != (filters.archived == "only") {
			continue
		}
		rows = append(rows, value)
	}
	slices.SortFunc(rows, func(a, b card) int {
		switch {
		case a.Ticket < b.Ticket:
			return -1
		case a.Ticket > b.Ticket:
			return 1
		default:
			return 0
		}
	})
	page(x, "cards", rows)
}

type cardFilters struct {
	ticket   uint64
	laneID   string
	archived string
}

func readCardFilters(r *http.Request) (cardFilters, error) {
	result := cardFilters{}
	fields := []string{"ticket", "lane_id", "archived", "sort", "q", "search_content"}
	for _, field := range fields {
		raw, err := singleQuery(r, field)
		if err != nil {
			return cardFilters{}, err
		}
		if raw == "" {
			if r.URL.Query().Has(field) {
				return cardFilters{}, invalid(field, "Expected a nonempty value.")
			}
			continue
		}
		switch field {
		case "ticket":
			value, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || value == 0 {
				return cardFilters{}, invalid(field, "Expected a positive integer.")
			}
			result.ticket = value
		case "lane_id":
			value, valid := normalizeID(raw)
			if !valid {
				return cardFilters{}, invalid(field, "Expected a UUIDv4.")
			}
			result.laneID = value
		case "archived":
			value := strings.ToLower(raw)
			if !slices.Contains([]string{"exclude", "include", "only"}, value) {
				return cardFilters{}, invalid(field, "Expected exclude, include, or only.")
			}
			result.archived = value
		case "sort":
			if strings.ToLower(raw) != "ticket" {
				return cardFilters{}, invalid(field, "This card order is unavailable.")
			}
		default:
			return cardFilters{}, invalid(field, "Card search is unavailable.")
		}
	}
	return result, nil
}

func readCardCreation(fields map[string]json.RawMessage) (cardCreation, error) {
	var in cardCreation
	allowed := []string{"id", "board_id", "expected_board_etag", "title", "description",
		"lane_id", "owner_id", "priority_id", "parent_id", "relationships"}
	if err := rejectFields(fields, allowed, false); err != nil {
		return in, err
	}
	var err error
	for _, field := range []struct {
		key      string
		dest     *string
		required bool
	}{{"board_id", &in.boardID, true}, {"lane_id", &in.laneID, true},
		{"id", &in.id, false}, {"priority_id", &in.priorityID, false}} {
		if raw, ok := fields[field.key]; ok || field.required {
			*field.dest, err = inputID(raw, field.key)
			if err != nil {
				return in, err
			}
		}
	}
	rawExpected, hasExpected := fields["expected_board_etag"]
	if !hasExpected {
		return in, errCardBoardETagRequired
	}
	in.expected, err = textField(rawExpected, "expected_board_etag", 2, 1024)
	if err != nil {
		return in, err
	}
	if tags, ok := parseTagList(in.expected); !ok || len(tags) != 1 || tags[0].weak || tags[0].opaque != in.expected || in.expected == "*" {
		return in, invalid("expected_board_etag", "Expected one strong entity tag.")
	}
	in.title, err = exactCardText(fields["title"], "title", 1, 1024)
	if err != nil {
		return in, err
	}
	if raw, ok := fields["description"]; ok && string(raw) != "null" {
		in.description, err = exactCardText(raw, "description", 0, 4194304)
		if err != nil {
			return in, err
		}
	}
	for _, field := range []struct {
		key  string
		dest **string
	}{{"owner_id", &in.ownerID}, {"parent_id", &in.parentID}} {
		if raw, ok := fields[field.key]; ok && string(raw) != "null" {
			id, err := inputID(raw, field.key)
			if err != nil {
				return in, err
			}
			*field.dest = &id
		}
	}
	if _, ok := fields["relationships"]; ok {
		return in, invalid("relationships", "Relationship creation is unavailable.")
	}
	return in, nil
}

// JSON's decoder substitutes U+FFFD for unpaired UTF-16 escapes. Reject them
// explicitly so accepted title and description input is preserved exactly.
func exactCardText(raw json.RawMessage, field string, minLength, maxLength int) (string, error) {
	value, err := textField(raw, field, minLength, maxLength)
	if err != nil {
		return "", err
	}
	pending := false
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] == '\\' {
			i++
			if raw[i] == 'u' {
				code, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
				i += 4
				switch {
				case pending && code >= 0xdc00 && code <= 0xdfff:
					pending = false
					continue
				case !pending && code >= 0xd800 && code <= 0xdbff:
					pending = true
					continue
				case code >= 0xdc00 && code <= 0xdfff:
					return "", invalid(field, "Expected valid Unicode text.")
				}
			}
		}
		if pending {
			return "", invalid(field, "Expected valid Unicode text.")
		}
	}
	if pending {
		return "", invalid(field, "Expected valid Unicode text.")
	}
	return value, nil
}

func (x *request) createCard() {
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	in, err := readCardCreation(fields)
	if errors.Is(err, errCardBoardETagRequired) {
		x.problem(428, "precondition_required", "expected_board_etag is required.", "", nil)
		return
	}
	if err != nil {
		x.fail(err)
		return
	}
	current, exists := x.service.store.accepted(in.boardID)
	if !exists || !slices.Contains(current.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !current.can(x.actor, "create_card") {
		x.fail(errForbidden)
		return
	}
	if in.id == "" {
		in.id = newID()
	} else if snapshotIdentityExists(&current, in.id) {
		x.fail(errIdentityConflict)
		return
	}
	next, err := x.service.store.update(in.boardID, in.expected, func(s *snapshot) error {
		return in.apply(s, x.actor)
	})
	if err != nil {
		x.fail(err)
		return
	}
	value := next.Cards[len(next.Cards)-1]
	x.w.Header().Set("Location", Prefix+"/cards/"+value.ID)
	x.w.Header().Set("X-Board-ETag", next.Board.ETag)
	x.respond(http.StatusCreated, value, value.ETag)
}

func (in cardCreation) apply(s *snapshot, actor string) error {
	if !s.can(actor, "create_card") {
		return errForbidden
	}
	laneIndex := slices.IndexFunc(s.Lanes, func(l lane) bool { return l.ID == in.laneID })
	if laneIndex < 0 || s.Lanes[laneIndex].Retired {
		return invalid("lane_id", "Expected an active lane on this board.")
	}
	if !slices.Contains(s.Policy.CreateGrants, createGrant{UserID: actor, LaneID: in.laneID}) {
		return errForbidden
	}
	if in.ownerID != nil && !slices.Contains(s.Board.UserIDs, *in.ownerID) {
		return invalid("owner_id", "Expected a user on this board.")
	}
	if in.priorityID == "" {
		in.priorityID = s.Policy.DefaultPriorityID
	}
	priorityIndex := slices.IndexFunc(s.Priorities, func(p priority) bool { return p.ID == in.priorityID })
	if priorityIndex < 0 {
		return invalid("priority_id", "Expected a priority on this board.")
	}
	if snapshotIdentityExists(s, in.id) {
		return errIdentityConflict
	}
	var lastTicket uint64
	for _, existing := range s.Cards {
		lastTicket = max(lastTicket, existing.Ticket)
	}
	if lastTicket >= maxSnapshotInteger {
		return invalid("ticket", "The board's ticket range is exhausted.")
	}
	now := maxCardTime(canonicalTime(time.Now()), s.Board.UpdatedAt)
	value := card{entity: newEntity(in.id, now), BoardID: s.Board.ID, Ticket: lastTicket + 1, Title: in.title,
		Description: in.description, LaneID: in.laneID, OwnerID: in.ownerID, PriorityID: in.priorityID,
		ChildIDs: []string{}, TriageScore: (s.Priorities[priorityIndex].Rank + 1) * 100}
	if s.Lanes[laneIndex].Role == "completed" {
		value.CompletedAt = &now
	}
	s.Cards = append(s.Cards, value)
	s.Activity = append(s.Activity, activity{ID: newID(), BoardID: s.Board.ID, Revision: s.Board.Revision + 1, At: now,
		ActorID: actor, Operation: "create", ResourceType: "card", ResourceID: value.ID})
	if in.parentID != nil {
		_, err := patchParent(s, value.ID, value.ETag, in.parentID, actor, now)
		return err
	}
	return nil
}

func snapshotIdentityExists(s *snapshot, id string) bool {
	return id == s.Board.ID || id == s.Policy.ID ||
		slices.Contains(s.Board.UserIDs, id) || slices.Contains(s.Board.LaneIDs, id) ||
		slices.Contains(s.Board.PriorityIDs, id) ||
		slices.ContainsFunc(s.Cards, func(c card) bool { return c.ID == id }) ||
		slices.ContainsFunc(s.Comments, func(c comment) bool { return c.ID == id }) ||
		slices.ContainsFunc(s.Attachments, func(a attachment) bool { return a.ID == id }) ||
		slices.ContainsFunc(s.Activity, func(event activity) bool { return event.ID == id })
}

func maxCardTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}
