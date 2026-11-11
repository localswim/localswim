package v2

import (
	"slices"
	"testing"
	"time"
)

func TestPriorityFloorAuditRejectsIncompleteOrUnrelatedPromotions(t *testing.T) {
	now := canonicalTime(time.Now())
	field, high, low := "priority_id", "high", "low"
	s := snapshot{
		Priorities: []priority{{entity: newEntity(high, now), Rank: 0}, {entity: newEntity(low, now), Rank: 1}},
		Cards:      []card{{ID: "a", PriorityID: high}, {ID: "b", PriorityID: high}, {ID: "unrelated", PriorityID: low}},
	}
	for i, item := range s.Cards {
		s.Activity = append(s.Activity, activity{ResourceID: item.ID, ResourceType: "card", Operation: "create", Revision: uint64(i) + 2, At: now})
	}
	link := relationshipAuditValue(relationshipLink{SourceCardID: "a", TargetCardID: "b", Kind: "blocks"})
	s.Activity = append(s.Activity,
		activity{ResourceID: "link", ResourceType: "relationship", Operation: "create", Revision: 5, At: now, After: &link},
		activity{ResourceID: "b", ResourceType: "card", Operation: "patch", Revision: 6, At: now, Field: &field, Before: &low, After: &high},
		activity{ResourceID: "a", ResourceType: "card", Operation: "promote", Revision: 6, At: now, Field: &field, Before: &low, After: &high},
	)
	if err := validatePriorityFloorAudit(s); err != nil {
		t.Fatalf("valid cascade: %v", err)
	}
	missing := cloneSnapshot(s)
	missing.Activity = slices.Delete(missing.Activity, len(missing.Activity)-1, len(missing.Activity))
	missing.Cards[0].PriorityID = low
	if err := validatePriorityFloorAudit(missing); err == nil {
		t.Fatal("priority replay accepted an omitted cascade")
	}
	forged := cloneSnapshot(s)
	forged.Cards[2].PriorityID = high
	forged.Activity = append(forged.Activity,
		activity{ResourceID: "unrelated", ResourceType: "card", Operation: "promote", Revision: 6, At: now, Field: &field, Before: &low, After: &high})
	if err := validatePriorityFloorAudit(forged); err == nil {
		t.Fatal("priority replay accepted an unrelated promotion")
	}
}

func TestPriorityFloorAuditRequiresPromotionsOnMarkedGraphEdits(t *testing.T) {
	now := canonicalTime(time.Now())
	marker, field, high, low := priorityFloorAuditField, "priority_id", "high", "low"
	openLane, completedLane, laneField := "open", "done", "lane_id"
	link := relationshipAuditValue(relationshipLink{SourceCardID: "a", TargetCardID: "b", Kind: "blocks"})
	initial := snapshot{
		Priorities: []priority{{entity: newEntity(high, now), Rank: 0}, {entity: newEntity(low, now), Rank: 1}},
		Lanes:      []lane{{ID: openLane, Role: "ordinary"}, {ID: completedLane, Role: "completed"}},
		Cards:      []card{{ID: "a", PriorityID: high, LaneID: openLane}, {ID: "b", PriorityID: high, LaneID: openLane}},
		Activity: []activity{
			{ResourceID: "a", ResourceType: "card", Operation: "create", Revision: 2, At: now},
			{ResourceID: "b", ResourceType: "card", Operation: "create", Revision: 3, At: now},
			{ResourceID: "link", ResourceType: "relationship", Operation: "create", Revision: 4, At: now, After: &link},
		},
	}
	for _, trigger := range []string{"blocks", "reopen"} {
		t.Run(trigger, func(t *testing.T) {
			s := cloneSnapshot(initial)
			revision := uint64(4)
			if trigger == "blocks" {
				s.Activity[2].Field = &marker
			} else {
				revision++
				s.Activity = append(s.Activity, activity{ResourceID: "b", ResourceType: "card", Operation: reopenOperation,
					Revision: revision, At: now, Field: &laneField, Before: &completedLane, After: &openLane})
			}
			s.Activity = append(s.Activity, activity{ResourceID: "a", ResourceType: "card", Operation: "promote",
				Revision: revision, At: now, Field: &field, Before: &low, After: &high})
			if err := validatePriorityFloorAudit(s); err != nil {
				t.Fatalf("valid cascade: %v", err)
			}

			s.Activity = s.Activity[:len(s.Activity)-1]
			s.Cards[0].PriorityID = low
			if err := validatePriorityFloorAudit(s); err == nil {
				t.Fatal("priority replay accepted a graph change with every promotion omitted")
			}
			if trigger == "blocks" {
				s.Activity[2].Field = nil
			} else {
				s.Activity[3].Operation = "patch"
			}
			if err := validatePriorityFloorAudit(s); err != nil {
				t.Fatalf("legacy graph change: %v", err)
			}
		})
	}
}
