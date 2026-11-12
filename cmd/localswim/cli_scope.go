package main

import (
	"encoding/json"
	"errors"
)

// validateBoardResource binds a resource response to the explicitly selected board.
func validateBoardResource(data json.RawMessage, boardID, resourceID string) error {
	var resource struct {
		ID      string `json:"id"`
		BoardID string `json:"board_id"`
	}
	if json.Unmarshal(data, &resource) != nil || resource.BoardID != boardID ||
		(resourceID != "" && resource.ID != resourceID) {
		return errors.New("service did not confirm the requested resource in BOARD_ID")
	}
	return nil
}
