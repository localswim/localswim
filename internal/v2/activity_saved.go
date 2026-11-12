package v2

import (
	"encoding/json"
	"net/url"
)

// ListActivitiesJSON projects verified saved audit records without a live writer.
func (b NativeBoard) ListActivitiesJSON(rawQuery string) (json.RawMessage, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, invalid("from", "Malformed query string.")
	}
	query, err := readActivityQuery(values)
	if err != nil {
		return nil, err
	}
	if query.full {
		return nil, invalid("view", "Saved activity supports the monitor view.")
	}
	result, err := query.page(b.state, rawQuery, "", query.savedCoverage(b.state))
	if err != nil {
		return nil, err
	}
	return marshalNativePage(result)
}

func (query activityQuery) savedCoverage(state snapshot) activityCoverage {
	latest := state.Board.CreatedAt
	var recoveryRevision uint64
	for _, event := range state.Activity {
		if event.At.After(latest) {
			latest = event.At
		}
		if event.Operation == recoveryOperation && event.ResourceType == "board" {
			recoveryRevision = event.Revision
		}
	}
	code := "saved_history"
	if query.to.After(latest) {
		code = "beyond_saved_history"
	}
	return activityCoverage{
		From: query.from, To: query.to, Code: code,
		LatestEventAt: &latest, LatestRecoveryRevision: recoveryRevision,
	}
}
