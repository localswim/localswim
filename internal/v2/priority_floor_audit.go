package v2

import "encoding/json"

const priorityFloorAuditField = "priority_floor"

// validatePriorityFloorAudit runs after card and relationship audit validation.
// A marked graph change or priority edit must contain exactly the promotions
// derived from its final graph and explicit priority assignments.
func validatePriorityFloorAudit(value snapshot) error {
	initial := make(map[string]string, len(value.Cards))
	marked := false
	for _, event := range value.Activity {
		if event.ResourceType == "card" && snapshotString(event.Field) == "priority_id" {
			if _, found := initial[event.ResourceID]; !found {
				initial[event.ResourceID] = snapshotString(event.Before)
			}
		}
		marked = marked || marksPriorityFloor(event)
	}
	if len(initial) == 0 && !marked {
		return nil
	}
	for _, item := range value.Cards {
		if _, found := initial[item.ID]; !found {
			initial[item.ID] = item.PriorityID
		}
	}
	completed := make(map[string]bool, len(value.Lanes))
	for _, lane := range value.Lanes {
		completed[lane.ID] = lane.Role == "completed"
	}
	replay := priorityFloorReplay{
		state: snapshot{Priorities: value.Priorities, Cards: make([]card, 0, len(value.Cards))},
		cards: make(map[string]int, len(value.Cards)), links: make(map[string]relationshipLink),
		ranks: make(map[string]int, len(value.Priorities)),
	}
	for _, p := range value.Priorities {
		replay.ranks[p.ID] = p.Rank
	}
	lanes := originalCardLanes(value)
	for start := 0; start < len(value.Activity); {
		end := start + 1
		for end < len(value.Activity) && value.Activity[end].Revision == value.Activity[start].Revision {
			end++
		}
		if err := replay.apply(value.Activity[start:end], initial, lanes, completed); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func marksPriorityFloor(event activity) bool {
	return event.ResourceType == "relationship" && snapshotString(event.Field) == priorityFloorAuditField ||
		event.ResourceType == "card" && event.Operation == reopenOperation
}

type priorityFloorReplay struct {
	state snapshot
	cards map[string]int
	links map[string]relationshipLink
	ranks map[string]int
}

func (replay *priorityFloorReplay) apply(events []activity, initial, lanes map[string]string, completed map[string]bool) error {
	var claims map[string]string
	check := false
	for _, event := range events {
		check = check || marksPriorityFloor(event)
		id := event.ResourceID
		switch event.ResourceType {
		case "relationship":
			if event.After == nil {
				delete(replay.links, id)
				continue
			}
			var link relationshipLink
			if err := json.Unmarshal([]byte(*event.After), &link); err != nil {
				return invalidSnapshot("activity", "Priority audit has an invalid relationship.")
			}
			replay.links[id] = link
		case "card":
			if event.Operation == "create" {
				replay.cards[id] = len(replay.state.Cards)
				item := card{ID: id, PriorityID: initial[id]}
				if completed[lanes[id]] {
					item.CompletedAt = &event.At
				}
				replay.state.Cards = append(replay.state.Cards, item)
				continue
			}
			switch snapshotString(event.Field) {
			case "lane_id":
				item := &replay.state.Cards[replay.cards[id]]
				item.CompletedAt = nil
				if completed[snapshotString(event.After)] {
					item.CompletedAt = &event.At
				}
			case "priority_id":
				if event.Operation == "promote" {
					if claims == nil {
						claims = make(map[string]string)
					}
					claims[id] = snapshotString(event.After)
				} else {
					replay.state.Cards[replay.cards[id]].PriorityID = snapshotString(event.After)
					check = true
				}
			}
		}
	}
	if !check && len(claims) == 0 {
		return nil
	}
	return replay.checkPromotions(claims)
}

func (replay *priorityFloorReplay) checkPromotions(claims map[string]string) error {
	if len(claims) == 0 {
		// If every active blocking edge already satisfies the floor, so does
		// every path. No transitive calculation is needed for this revision.
		for _, link := range replay.links {
			if link.Kind != "blocks" {
				continue
			}
			source := replay.state.Cards[replay.cards[link.SourceCardID]]
			target := replay.state.Cards[replay.cards[link.TargetCardID]]
			if source.CompletedAt == nil && target.CompletedAt == nil &&
				replay.ranks[source.PriorityID] > replay.ranks[target.PriorityID] {
				return invalidSnapshot("activity", "Priority audit is missing a required promotion.")
			}
		}
		return nil
	}
	replay.state.Relationships = replay.state.Relationships[:0]
	for _, link := range replay.links {
		replay.state.Relationships = append(replay.state.Relationships, relationship{relationshipLink: link})
	}
	floor := calculatePriorityFloor(&replay.state, nil)
	for i := range replay.state.Cards {
		item := &replay.state.Cards[i]
		if effective := floor.ids[i]; effective != item.PriorityID {
			if claims[item.ID] != effective {
				return invalidSnapshot("activity", "Priority audit is missing a required promotion.")
			}
			item.PriorityID = effective
			delete(claims, item.ID)
		}
	}
	if len(claims) > 0 {
		return invalidSnapshot("activity", "Priority audit contains an unrelated promotion.")
	}
	return nil
}
