package main

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const cardCreateHelp = `Usage:
  localswim card create [--revision N] --title TITLE [--description TEXT] BOARD_ID LANE_ID
  localswim card create [--revision N] <JSON_FILE|->

The concise form uses the board's default owner and priority. Use a JSON file or
- for standard input to supply additional fields. The document must include
board_id and may include an explicit card ID for readback after a lost reply.
--revision checks the board revision.

Examples:
  localswim card create --title "Check pool hours" $board $lane
  localswim card create --revision 12 card.json
`

const commentPostHelp = `Usage:
  localswim comment post [--revision N] --text TEXT CARD_ID
  localswim comment post [--revision N] CARD_ID <JSON_FILE|->

Quote literal comment text. The document form requires a file path or - for
standard input and reads a JSON object with text and an optional id.
--revision checks the card revision.

Examples:
  localswim comment post --text "Confirmed weekend hours." $card
  localswim comment post --revision 13 $card comment.json
`

const priorityBatchHelp = `Usage:
  localswim card set-priorities [--revision N] BOARD_ID PRIORITY_ID CARD_ID [CARD_ID ...]
  localswim card set-priorities [--revision N] --tickets BOARD_ID PRIORITY_ID TICKET [TICKET ...]

Supply 1 to 1000 distinct card UUIDs or, with --tickets, distinct ticket numbers.
All selected cards must belong to the board. The batch checks the board revision
and applies the effective blocking floor to all selections together.

Examples:
  localswim card set-priorities $board $priority $card1 $card2
  localswim card set-priorities --tickets $board $priority 12 17
  localswim card set-priorities --revision 14 $board $priority $card1
`

func routineWriteHelp(name string) string {
	switch name {
	case "card create":
		return cardCreateHelp
	case "comment post":
		return commentPostHelp
	case "card set-priorities":
		return priorityBatchHelp
	default:
		return ""
	}
}

func cardCreateInput(args []string, hasTitle, hasDescription bool, title, description string) (string, json.RawMessage, error) {
	if !hasTitle && !hasDescription {
		if len(args) != 1 || args[0] == "" {
			return "", nil, errors.New("card create accepts one JSON file or - for standard input")
		}
		return args[0], nil, nil
	}
	if !hasTitle || !utf8.ValidString(title) || !utf8.ValidString(description) {
		return "", nil, errors.New("card create requires --title with valid UTF-8 text and a valid UTF-8 description if supplied")
	}
	if len(args) != 2 || !boardIDPattern.MatchString(args[0]) || !boardIDPattern.MatchString(args[1]) {
		return "", nil, errors.New("card create --title requires a UUIDv4 board ID followed by a UUIDv4 lane ID")
	}
	fields := map[string]string{
		"board_id": strings.ToLower(args[0]),
		"lane_id":  strings.ToLower(args[1]),
		"title":    title,
	}
	if hasDescription {
		fields["description"] = description
	}
	body, err := json.Marshal(fields)
	return "", body, err
}

func priorityBatchBody(priority string, ids []string) (json.RawMessage, error) {
	if !boardIDPattern.MatchString(priority) {
		return nil, errors.New("priority batch requires a UUIDv4 priority ID")
	}
	if len(ids) < 1 || len(ids) > 1000 {
		return nil, errors.New("priority batch requires 1 to 1000 UUIDv4 card IDs")
	}
	cards := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.ToLower(id)
		if !boardIDPattern.MatchString(id) || seen[id] {
			return nil, errors.New("priority batch requires distinct UUIDv4 card IDs")
		}
		seen[id] = true
		cards = append(cards, id)
	}
	return json.Marshal(struct {
		CardIDs    []string `json:"card_ids"`
		PriorityID string   `json:"priority_id"`
	}{cards, strings.ToLower(priority)})
}
