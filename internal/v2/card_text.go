package v2

import (
	"encoding/json"
	"time"
)

type cardTextPatch struct {
	title, description *string
}

func readCardText(fields map[string]json.RawMessage) (cardTextPatch, error) {
	var in cardTextPatch
	if raw, ok := fields["title"]; ok {
		title, err := exactCardText(raw, "title", 1, 1024)
		if err != nil {
			return in, err
		}
		in.title = &title
	}
	if raw, ok := fields["description"]; ok {
		description := ""
		if string(raw) != "null" {
			var err error
			description, err = exactCardText(raw, "description", 0, 4194304)
			if err != nil {
				return in, err
			}
		}
		in.description = &description
	}
	return in, nil
}

// apply edits text and its audit on an isolated candidate. The caller checks
// the original card tag and updates the entity once for the full PATCH.
func (in cardTextPatch) apply(s *snapshot, value *card, actor string, now time.Time) (bool, error) {
	if in.title == nil && in.description == nil {
		return false, nil
	}
	if !s.can(actor, "edit_card") {
		return false, errForbidden
	}
	changed := false
	for _, edit := range []struct {
		field          string
		current, input *string
	}{
		{"title", &value.Title, in.title},
		{"description", &value.Description, in.description},
	} {
		if edit.input == nil || *edit.current == *edit.input {
			continue
		}
		field, before, after := edit.field, *edit.current, *edit.input
		*edit.current = after
		s.Activity = append(s.Activity, activity{
			ID: newID(), BoardID: s.Board.ID, Revision: s.Board.Revision + 1, At: now,
			ActorID: actor, Operation: "patch", ResourceType: "card", ResourceID: value.ID,
			Field: &field, Before: &before, After: &after,
		})
		changed = true
	}
	return changed, nil
}
