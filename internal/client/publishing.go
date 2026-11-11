package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PublishingEnabled verifies the response identifies the addressed resource and
// contains its publishing status. Callers retain the original JSON for output.
func (r Response) PublishingEnabled(boardID string) (bool, error) {
	var resource struct {
		ID             string   `json:"id"`
		Revision       uint64   `json:"revision"`
		PinnedBoardIDs []string `json:"pinned_board_ids"`
		Publishing     struct {
			Enabled       *bool           `json:"enabled"`
			Repository    json.RawMessage `json:"repository"`
			LastSuccessAt json.RawMessage `json:"last_success_at"`
			Pending       *bool           `json:"pending"`
			ErrorCode     json.RawMessage `json:"error_code"`
		} `json:"publishing"`
	}
	if r.ETag == "" || json.Unmarshal(r.JSON, &resource) != nil ||
		resource.Revision == 0 || resource.Revision > 1<<53-1 ||
		!strings.EqualFold(resource.ID, boardID) || (boardID == "" && resource.PinnedBoardIDs == nil) {
		return false, errors.New("service did not return the addressed board or workspace with its revision and validator")
	}
	if boardID != "" && r.ETag != fmt.Sprintf("\"%s-%d\"", strings.ToLower(resource.ID), resource.Revision) {
		return false, errors.New("service board revision did not match its HTTP validator")
	}
	state := resource.Publishing
	if state.Enabled == nil || state.Pending == nil || !isNullableString(state.Repository) ||
		!isNullableString(state.LastSuccessAt) || !isNullableString(state.ErrorCode) {
		return false, errors.New("service returned invalid publishing status")
	}
	return *state.Enabled, nil
}

func isNullableString(value json.RawMessage) bool {
	var s *string
	return json.Unmarshal(value, &s) == nil
}
