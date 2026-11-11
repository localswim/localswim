package v2

import (
	"sort"
	"time"
)

// childrenOf derives direct children from their parent links, including archived cards.
func childrenOf(state *snapshot, cardID string) []string {
	children := []string{}
	for _, candidate := range state.Cards {
		if candidate.ParentID != nil && *candidate.ParentID == cardID {
			children = append(children, candidate.ID)
		}
	}
	sort.Strings(children)
	return children
}

// setParent updates the child and the direct child lists of the affected parents.
// Callers supply an isolated transaction candidate.
func setParent(state *snapshot, cardID string, parentID *string) (bool, error) {
	cards := make(map[string]*card, len(state.Cards))
	for i := range state.Cards {
		current := &state.Cards[i]
		cards[current.ID] = current
	}
	child := cards[cardID]
	if child == nil {
		return false, invalid("id", "card does not exist")
	}

	oldParent := child.ParentID
	if err := validateParentChain(cards, cardID, oldParent); err != nil {
		return false, err
	}
	var newParent *string
	if parentID != nil {
		id, ok := normalizeID(*parentID)
		if !ok {
			return false, invalid("parent_id", "must be a UUID")
		}
		newParent = &id
		if err := validateParentChain(cards, cardID, newParent); err != nil {
			return false, err
		}
	}
	if oldParent == nil && newParent == nil ||
		oldParent != nil && newParent != nil && *oldParent == *newParent {
		return false, nil
	}

	child.ParentID = newParent
	for _, affected := range []*string{oldParent, newParent} {
		if affected != nil {
			cards[*affected].ChildIDs = childrenOf(state, *affected)
		}
	}
	return true, nil
}

func validateParentChain(cards map[string]*card, cardID string, parentID *string) error {
	seen := map[string]bool{cardID: true}
	for parentID != nil {
		id := *parentID
		if seen[id] {
			return invalid("parent_id", "parent links would form or retain a cycle")
		}
		seen[id] = true
		parent := cards[id]
		if parent == nil {
			return invalid("parent_id", "parent card does not exist")
		}
		parentID = parent.ParentID
	}
	return nil
}

// patchParent applies the parent_id field to an isolated candidate after checking
// the card's original tag and the actor's current relationship permission.
func patchParent(s *snapshot, cardID, expectedCardETag string, parentID *string, actorID string, now time.Time) (bool, error) {
	now = canonicalTime(now)
	if !s.can(actorID, "manage_relationships") {
		return false, errForbidden
	}
	cardID, valid := normalizeID(cardID)
	if !valid {
		return false, invalid("id", "must be a UUID")
	}
	for i := range s.Cards {
		child := &s.Cards[i]
		if child.ID != cardID {
			continue
		}
		if !matchesStrongTag(expectedCardETag, child.entityTag()) {
			return false, errStale
		}
		oldParent := child.ParentID
		changed, err := setParent(s, cardID, parentID)
		if err != nil || !changed {
			return changed, err
		}

		revision := s.Board.Revision + 1
		for j := range s.Cards {
			affected := &s.Cards[j]
			if affected.ID == cardID ||
				oldParent != nil && affected.ID == *oldParent ||
				child.ParentID != nil && affected.ID == *child.ParentID {
				affected.Revision = revision
				if now.After(affected.UpdatedAt) {
					affected.UpdatedAt = now
				}
			}
		}

		field := "parent_id"
		var before, after *string
		if oldParent != nil {
			id := *oldParent
			before = &id
		}
		if child.ParentID != nil {
			id := *child.ParentID
			after = &id
		}
		s.Activity = append(s.Activity, activity{
			ID: newOperationID(), BoardID: s.Board.ID, Revision: revision, At: now, ActorID: actorID,
			Operation: "patch", ResourceType: "card", ResourceID: cardID, Field: &field,
			Before: before, After: after,
		})
		return true, nil
	}
	return false, invalid("id", "card does not exist")
}
