package v2

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"
)

func readArchived(raw json.RawMessage) (*bool, error) {
	var value *bool
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, invalid("archived", "Expected a Boolean.")
	}
	return value, nil
}

// applyCardArchive changes visibility and records the transition on the transaction candidate.
func applyCardArchive(s *snapshot, item *card, archived bool, actor string, now time.Time) (bool, error) {
	if !s.can(actor, "archive_card") {
		return false, errForbidden
	}
	if item.Archived == archived {
		return false, nil
	}
	if !archived && !slices.ContainsFunc(s.Lanes, func(l lane) bool {
		return l.ID == item.LaneID && !l.Retired
	}) {
		return false, invalid("archived", "Restoring requires an active lane.")
	}

	before := archiveAuditValue(item.Archived)
	item.Archived = archived
	recordCardAssignment(s, item.ID, actor, "archived", before, archiveAuditValue(archived), now)
	return true, nil
}

func archiveAuditValue(archived bool) *string {
	value := strconv.FormatBool(archived)
	return &value
}
