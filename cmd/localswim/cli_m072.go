package main

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

func cardCreateInput(args []string, hasDescription bool, description string) (string, json.RawMessage, error) {
	if len(args) == 2 && !hasDescription && boardIDPattern.MatchString(args[0]) && args[1] != "" {
		return args[1], nil, nil
	}
	if len(args) != 3 || !boardIDPattern.MatchString(args[0]) || !boardIDPattern.MatchString(args[1]) {
		return "", nil, errors.New("card create requires BOARD_ID LANE_ID TITLE or BOARD_ID FILE|-")
	}
	title := args[2]
	if strings.TrimSpace(title) == "" || !utf8.ValidString(title) || !utf8.ValidString(description) {
		return "", nil, errors.New("card create requires a nonblank valid UTF-8 title and a valid UTF-8 description if supplied")
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
