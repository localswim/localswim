package main

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

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
