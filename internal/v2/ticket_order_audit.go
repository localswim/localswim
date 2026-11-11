package v2

import "strconv"

func ticketAuditValue(value *string) (uint64, bool) {
	if value == nil {
		return 0, false
	}
	ticket, err := strconv.ParseUint(*value, 10, 64)
	return ticket, err == nil && ticket > 0 && ticket <= maxSnapshotInteger && strconv.FormatUint(ticket, 10) == *value
}

// Replay tickets from their service allocation, validating each atomic permutation.
// UUID-based attachment paths and all earlier activity remain independent of tickets.
func validateTicketOrderAudit(value snapshot) error {
	found := false
	for _, event := range value.Activity {
		found = found || event.Operation == ticketOrderOperation
	}
	if !found {
		return nil
	}
	tickets := make(map[string]uint64, len(value.Cards))
	lanes := originalCardLanes(value)
	archived := make(map[string]bool)
	retired := make(map[string]bool)
	var lastTicket uint64
	for start := 0; start < len(value.Activity); {
		end := start + 1
		for end < len(value.Activity) && value.Activity[end].Revision == value.Activity[start].Revision {
			end++
		}
		events := value.Activity[start:end]
		if events[0].Operation == ticketOrderOperation {
			if !validTicketOrderBatch(events, tickets, lanes, archived, retired) {
				return invalidSnapshot("activity", "Ticket audit must be one active-lane permutation of its current ticket pool.")
			}
		} else {
			for _, event := range events {
				if event.Operation == ticketOrderOperation {
					return invalidSnapshot("activity", "Ticket ordering cannot share a revision with another operation.")
				}
				field := snapshotString(event.Field)
				if event.ResourceType == "card" {
					switch {
					case event.Operation == "create":
						lastTicket++
						tickets[event.ResourceID] = lastTicket
					case field == "lane_id":
						lanes[event.ResourceID] = snapshotString(event.After)
					case field == "archived":
						archived[event.ResourceID] = snapshotString(event.After) == "true"
					}
				}
				if event.ResourceType == "lane" && field == "retired" {
					retired[event.ResourceID] = snapshotString(event.After) == "true"
				}
			}
		}
		start = end
	}
	for _, item := range value.Cards {
		if tickets[item.ID] != item.Ticket {
			return invalidSnapshot("cards.ticket", "Card ticket does not match its allocation and renumber audit.")
		}
	}
	return nil
}

func validTicketOrderBatch(events []activity, tickets map[string]uint64, lanes map[string]string, archived, retired map[string]bool) bool {
	beforePool := make(map[uint64]bool, len(events))
	afterPool := make(map[uint64]bool, len(events))
	laneID := lanes[events[0].ResourceID]
	for _, event := range events {
		before, beforeValid := ticketAuditValue(event.Before)
		after, afterValid := ticketAuditValue(event.After)
		if event.Operation != ticketOrderOperation || event.ResourceType != "card" || snapshotString(event.Field) != "ticket" ||
			!event.At.Equal(events[0].At) || !beforeValid || !afterValid || before == after || tickets[event.ResourceID] != before ||
			beforePool[before] || afterPool[after] || laneID == "" || lanes[event.ResourceID] != laneID ||
			archived[event.ResourceID] || retired[laneID] {
			return false
		}
		beforePool[before], afterPool[after] = true, true
	}
	for ticket := range beforePool {
		if !afterPool[ticket] {
			return false
		}
	}
	for _, event := range events {
		ticket, _ := ticketAuditValue(event.After)
		tickets[event.ResourceID] = ticket
	}
	return true
}
