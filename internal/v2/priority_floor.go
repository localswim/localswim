package v2

import (
	"cmp"
	"slices"
	"time"
)

// priorityFloor holds the effective priority of each card in snapshot order.
// Once a card receives the strongest reachable priority, it and its incoming
// edges need no further traversal. This also bounds work in blocking cycles.
type priorityFloor struct {
	ids   []string
	ranks map[string]int
}

func calculatePriorityFloor(s *snapshot, overrides map[string]string) priorityFloor {
	floor := priorityFloor{ids: make([]string, len(s.Cards)), ranks: make(map[string]int, len(s.Priorities))}
	for _, p := range s.Priorities {
		floor.ranks[p.ID] = p.Rank
	}
	unfinished := make(map[string]int, len(s.Cards))
	order := make([]int, 0, len(s.Cards))
	for i, item := range s.Cards {
		floor.ids[i] = item.PriorityID
		if id, ok := overrides[item.ID]; ok {
			floor.ids[i] = id
		}
		if item.CompletedAt == nil {
			unfinished[item.ID] = i
			order = append(order, i)
		}
	}
	blockers := make([][]int, len(s.Cards))
	for _, link := range s.Relationships {
		if link.Kind != "blocks" {
			continue
		}
		source, sourceUnfinished := unfinished[link.SourceCardID]
		target, targetUnfinished := unfinished[link.TargetCardID]
		if sourceUnfinished && targetUnfinished {
			blockers[target] = append(blockers[target], source)
		}
	}
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(floor.ranks[floor.ids[a]], floor.ranks[floor.ids[b]])
	})
	settled := make([]bool, len(s.Cards))
	pending := make([]int, 0)
	for _, root := range order {
		if settled[root] {
			continue
		}
		id := floor.ids[root]
		settled[root] = true
		pending = append(pending, root)
		for len(pending) > 0 {
			last := len(pending) - 1
			current := pending[last]
			pending = pending[:last]
			for _, blocker := range blockers[current] {
				if !settled[blocker] {
					settled[blocker] = true
					floor.ids[blocker] = id
					pending = append(pending, blocker)
				}
			}
		}
	}
	return floor
}

func promotePriorityFloor(s *snapshot, actor string, now time.Time) {
	calculatePriorityFloor(s, nil).apply(s, actor, now)
}

func applyCardPriority(s *snapshot, item *card, requested, actor string, reopened bool, now time.Time) error {
	if !s.can(actor, "set_priority") {
		return errForbidden
	}
	if !slices.Contains(s.Board.PriorityIDs, requested) {
		return invalid("priority_id", "Expected a priority on this board.")
	}
	floor := calculatePriorityFloor(s, map[string]string{item.ID: requested})
	index := slices.IndexFunc(s.Cards, func(c card) bool { return c.ID == item.ID })
	if id := floor.ids[index]; id != item.PriorityID {
		recordCardPriority(s, item, id, floor.ranks[id], actor, "patch", now)
	} else if !reopened {
		return nil
	}
	floor.apply(s, actor, now)
	return nil
}

func (floor priorityFloor) apply(s *snapshot, actor string, now time.Time) {
	for i := range s.Cards {
		item := &s.Cards[i]
		if id := floor.ids[i]; item.PriorityID != id {
			recordCardPriority(s, item, id, floor.ranks[id], actor, "promote", now)
		}
	}
}

func recordCardPriority(s *snapshot, item *card, id string, rank int, actor, operation string, now time.Time) {
	field, before := "priority_id", item.PriorityID
	s.Activity = append(s.Activity, activity{
		ID: newOperationID(), BoardID: s.Board.ID, Revision: s.Board.Revision + 1, At: now,
		ActorID: actor, Operation: operation, ResourceType: "card", ResourceID: item.ID,
		Field: &field, Before: &before, After: &id,
	})
	item.PriorityID = id
	item.TriageScore = (rank + 1) * 100
	item.Revision = s.Board.Revision + 1
	item.UpdatedAt = maxCardTime(item.UpdatedAt, now)
}
