package archive

import (
	"fmt"
	"github.com/localswim/localswim/internal/hierarchy"
)

// ValidateForest requires complete current cards in the selected identity.
func ValidateForest(identity Identity, cards []Card) error {
	parents := map[string]string{}
	for _, c := range cards {
		if _, ok := parents[string(c.CardID)]; ok {
			return fmt.Errorf("duplicate hierarchy card")
		}
		parent := ""
		if c.Parent != nil {
			if c.Parent.BoardID != identity.BoardID || c.Parent.Incarnation != identity.Incarnation {
				return fmt.Errorf("current parent scope")
			}
			parent = string(c.Parent.CardID)
		}
		parents[string(c.CardID)] = parent
	}
	return hierarchy.Validate(parents)
}

// ValidateHistoricalParent checks snapshot scope; source availability is a
// server/archive registry check and is not invented by a standalone client.
func ValidateHistoricalParent(s Snapshot) error {
	if s.State != nil && s.State.Parent != nil {
		p := s.State.Parent
		if p.BoardID != s.Origin.BoardID || p.Incarnation != s.Origin.Incarnation || p.CardID == s.CardID {
			return fmt.Errorf("historical parent scope")
		}
	}
	return nil
}
