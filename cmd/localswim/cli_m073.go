package main

import (
	"encoding/json"
	"errors"
	"strings"
)

func scopedPolicyGrantInput(args []string, create bool) (string, json.RawMessage, error) {
	key := "move_grants"
	lanes := []string{"from_lane_id", "to_lane_id"}
	expected := "move requires UUIDv4 board, user, source lane, and destination lane IDs"
	if create {
		key = "create_grants"
		lanes = []string{"lane_id"}
		expected = "create requires UUIDv4 board, user, and lane IDs"
	}
	if len(args) != len(lanes)+2 {
		return "", nil, errors.New(expected)
	}
	for _, id := range args {
		if !boardIDPattern.MatchString(id) {
			return "", nil, errors.New(expected)
		}
	}
	if !create && strings.EqualFold(args[2], args[3]) {
		return "", nil, errors.New("move requires different source and destination lane IDs")
	}
	grant := map[string]string{"user_id": strings.ToLower(args[1])}
	for index, field := range lanes {
		grant[field] = strings.ToLower(args[index+2])
	}
	body, err := json.Marshal(map[string][]map[string]string{key: {grant}})
	return strings.ToLower(args[0]), body, err
}
