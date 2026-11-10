package v2

import "testing"

func workflowPathFixture() ([]lane, policy) {
	lanes := []lane{
		{entity: entity{ID: "inbox"}, Label: "Inbox"},
		{entity: entity{ID: "doing"}, Label: "Doing"},
		{entity: entity{ID: "review"}, Label: "Review"},
		{entity: entity{ID: "done"}, Label: "Done", Role: "completed"},
	}
	rules := policy{
		CreateGrants:   []createGrant{{UserID: "creator", LaneID: "inbox"}},
		ResourceGrants: []resourceGrant{{UserID: "creator", Capabilities: []string{"create_card"}}},
		MoveGrants: []moveGrant{
			{UserID: "creator", FromLaneID: "inbox", ToLaneID: "doing"},
			{UserID: "bot", FromLaneID: "doing", ToLaneID: "review"},
			{UserID: "human", FromLaneID: "review", ToLaneID: "done"},
		},
	}
	return lanes, rules
}

func TestWorkflowPaths(t *testing.T) {
	for _, test := range []struct {
		name  string
		edit  func(*[]lane, *policy)
		field string
	}{
		{"actor handoff", func(*[]lane, *policy) {}, ""},
		{"no creation grants", func(_ *[]lane, p *policy) { p.CreateGrants = nil }, "policy.create_grants"},
		{"no creation capability", func(_ *[]lane, p *policy) { p.ResourceGrants = nil }, "policy.create_grants"},
		{"creation in retired lane", func(l *[]lane, _ *policy) { (*l)[0].Retired = true }, "policy.create_grants"},
		{"no route to completed", func(_ *[]lane, p *policy) { p.MoveGrants = p.MoveGrants[:2] }, "policy.move_grants"},
		{"wrong direction", func(_ *[]lane, p *policy) {
			p.MoveGrants[2].FromLaneID, p.MoveGrants[2].ToLaneID = "done", "review"
		}, "policy.move_grants"},
		{"reachable dead end", func(l *[]lane, p *policy) {
			*l = append(*l, lane{entity: entity{ID: "trap"}, Label: "Trap"})
			p.MoveGrants = append(p.MoveGrants, moveGrant{UserID: "bot", FromLaneID: "doing", ToLaneID: "trap"})
		}, "policy.move_grants"},
		{"isolated active lane", func(l *[]lane, _ *policy) {
			*l = append(*l, lane{entity: entity{ID: "island"}, Label: "Island"})
		}, "policy.move_grants"},
		{"retired lane", func(l *[]lane, _ *policy) {
			*l = append(*l, lane{entity: entity{ID: "retired"}, Label: "Retired", Retired: true})
		}, ""},
		{"route through retired lane", func(l *[]lane, _ *policy) {
			(*l)[1].Retired = true
		}, "policy.move_grants"},
		{"cycle with exit", func(_ *[]lane, p *policy) {
			p.MoveGrants = append(p.MoveGrants, moveGrant{UserID: "bot", FromLaneID: "doing", ToLaneID: "inbox"})
		}, ""},
		{"cycle without exit", func(_ *[]lane, p *policy) {
			p.MoveGrants = []moveGrant{
				{UserID: "bot", FromLaneID: "inbox", ToLaneID: "doing"},
				{UserID: "bot", FromLaneID: "doing", ToLaneID: "inbox"},
				{UserID: "human", FromLaneID: "review", ToLaneID: "done"},
			}
		}, "policy.move_grants"},
		{"creation directly in completed", func(_ *[]lane, p *policy) { p.CreateGrants[0].LaneID = "done" }, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			lanes, rules := workflowPathFixture()
			test.edit(&lanes, &rules)
			issue := validateWorkflowPaths(lanes, rules)
			if test.field == "" {
				if issue != nil {
					t.Fatal(issue)
				}
			} else if issue == nil || issue.Field != test.field {
				t.Fatalf("issue = %v, want field %s", issue, test.field)
			}
		})
	}
}
