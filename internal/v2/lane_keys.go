package v2

// Replay ownership as keys change so a later rename cannot hide an earlier collision.
func validLaneKeyHistory(value snapshot, initial map[string]bool) bool {
	keys := make(map[string]string, len(value.Lanes))
	for _, item := range value.Lanes {
		keys[item.ID] = item.Key
	}
	for i := len(value.Activity) - 1; i >= 0; i-- {
		event := value.Activity[i]
		if event.ResourceType == "lane" && snapshotString(event.Field) == "key" {
			keys[event.ResourceID] = snapshotString(event.Before)
		}
	}
	owners := make(map[string]string, len(keys))
	introduced := make(map[string]bool, len(keys))
	introduce := func(id string) bool {
		key := keys[id]
		if owner := owners[key]; owner != "" && owner != id {
			return false
		}
		owners[key], introduced[id] = id, true
		return true
	}
	for id := range initial {
		if !introduce(id) {
			return false
		}
	}
	for _, event := range value.Activity {
		if event.ResourceType != "lane" {
			continue
		}
		if event.Operation == "create" && !introduced[event.ResourceID] {
			if !introduce(event.ResourceID) {
				return false
			}
		}
		if snapshotString(event.Field) == "key" {
			next := snapshotString(event.After)
			if owner := owners[next]; owner != "" && owner != event.ResourceID {
				return false
			}
			delete(owners, keys[event.ResourceID])
			keys[event.ResourceID] = next
			owners[next] = event.ResourceID
		}
	}
	return true
}
