package main

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

func cardCreateInput(args []string, hasTitle, hasDescription bool, title, description string) (string, json.RawMessage, error) {
	if !hasTitle && !hasDescription {
		if len(args) != 2 || !boardIDPattern.MatchString(args[0]) || args[1] == "" {
			return "", nil, errors.New("card create requires BOARD_ID FILE|-")
		}
		return args[1], nil, nil
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
