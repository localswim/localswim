package v2

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const workflowTestBoardID = "a0000000-0000-4000-8000-000000000001"

const workflowTestInput = `{
  "users": [
    {"id":"A0000000-0000-4000-8000-000000000002","label":" Owner ","kind":"HuMaN"}
  ],
  "lanes": [
    {"id":"A0000000-0000-4000-8000-000000000003","label":"To Do","future":{"flag":true}},
    {"id":"a0000000-0000-4000-8000-000000000004","label":"Done","role":"CoMpLeTeD"}
  ],
  "priorities": [
    {"id":"a0000000-0000-4000-8000-000000000005","key":"normal","label":"Normal","rank":0}
  ],
  "policy": {
    "defaultPriorityId":"A0000000-0000-4000-8000-000000000005",
    "createGrants":[{"userId":"a0000000-0000-4000-8000-000000000002","laneId":"a0000000-0000-4000-8000-000000000003"}],
    "moveGrants":[{"userId":"A0000000-0000-4000-8000-000000000002","fromLaneId":"a0000000-0000-4000-8000-000000000003","toLaneId":"a0000000-0000-4000-8000-000000000004"}],
    "resourceGrants":[{"userId":"a0000000-0000-4000-8000-000000000002","capabilities":["read","approve"]}]
  }
}`

func TestCustomWorkflowResolvesDefinitions(t *testing.T) {
	now := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
	got, err := customWorkflow(json.RawMessage(workflowTestInput), workflowTestBoardID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Users) != 1 || got.Users[0].Kind != "human" || got.Users[0].Label != " Owner " ||
		got.Users[0].CreatedAt != now || got.Users[0].ID != "a0000000-0000-4000-8000-000000000002" {
		t.Fatalf("incorrect initial user: %+v", got.Users)
	}
	if len(got.Lanes) != 2 || got.Lanes[0].Role != "ordinary" || got.Lanes[0].Key != "to_do" ||
		got.Lanes[1].Role != "completed" || got.Lanes[1].Key != "completed" ||
		got.Lanes[0].Position >= got.Lanes[1].Position {
		t.Fatalf("incorrect initial lane definitions: %+v", got.Lanes)
	}
	if got.Policy.BoardID != workflowTestBoardID || got.Policy.DefaultPriorityID != got.Priorities[0].ID ||
		got.Policy.CreateGrants[0].LaneID != got.Lanes[0].ID || got.Policy.MoveGrants[0].ToLaneID != got.Lanes[1].ID ||
		got.Policy.ResourceGrants[0].UserID != got.Users[0].ID {
		t.Fatalf("incorrect initial policy: %+v", got.Policy)
	}
}

func TestCustomWorkflowRefusesInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
	}{
		{"duplicate object member", `"label":" Owner "`, `"label":"Owner","label":"Duplicate"`},
		{"unknown closed member", `"kind":"HuMaN"`, `"kind":"HuMaN","future":true`},
		{"protected open member", `"future":{"flag":true}`, `"token":"unexpected"`},
		{"wrong known type", `"label":"To Do"`, `"label":null`},
		{"null definition array", `"createGrants":[{"userId":"a0000000-0000-4000-8000-000000000002","laneId":"a0000000-0000-4000-8000-000000000003"}]`, `"createGrants":null`},
		{"cross-type duplicate ID", `"id":"A0000000-0000-4000-8000-000000000003"`, `"id":"a0000000-0000-4000-8000-000000000002"`},
		{"duplicate normalized user", `"kind":"HuMaN"}`, `"kind":"HuMaN"},{"id":"a0000000-0000-4000-8000-000000000002","kind":"bot","label":"Second"}`},
		{"invalid UUID version", `"id":"A0000000-0000-4000-8000-000000000003"`, `"id":"A0000000-0000-5000-8000-000000000003"`},
		{"missing terminal role", `"role":"CoMpLeTeD"`, `"role":"ordinary"`},
		{"unknown reference", `"defaultPriorityId":"A0000000-0000-4000-8000-000000000005"`, `"defaultPriorityId":"A0000000-0000-4000-8000-000000000004"`},
		{"wrong capability spelling", `"read","approve"`, `"READ","approve"`},
		{"duplicate capability", `"read","approve"`, `"read","read"`},
		{"bot approval", `"kind":"HuMaN"`, `"kind":"bot"`},
		{"protected grant member", `"capabilities":["read","approve"]`, `"capabilities":["read"],"actor":"human"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(workflowTestInput, tc.before, tc.after, 1)
			if input == workflowTestInput {
				t.Fatal("fixture was not changed")
			}
			if _, err := customWorkflow(json.RawMessage(input), workflowTestBoardID, time.Now()); err == nil {
				t.Fatal("invalid workflow accepted")
			}
		})
	}
}

func TestCustomWorkflowExpandsLaneGrants(t *testing.T) {
	const userID = "a0000000-0000-4000-8000-000000000002"
	const ordinaryID = "a0000000-0000-4000-8000-000000000003"
	const completedID = "a0000000-0000-4000-8000-000000000004"
	raw := strings.Replace(workflowTestInput, `"future":{"flag":true}`,
		`"createFor":["A0000000-0000-4000-8000-000000000002"],"connectFor":["A0000000-0000-4000-8000-000000000002"]`, 1)
	got, err := customWorkflow(json.RawMessage(raw), workflowTestBoardID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Policy.CreateGrants) != 1 || got.Policy.CreateGrants[0] != (createGrant{UserID: userID, LaneID: ordinaryID}) {
		t.Fatalf("expected deduplicated creation grant, got %+v", got.Policy.CreateGrants)
	}
	if len(got.Policy.MoveGrants) != 2 ||
		got.Policy.MoveGrants[0] != (moveGrant{UserID: userID, FromLaneID: ordinaryID, ToLaneID: completedID}) ||
		got.Policy.MoveGrants[1] != (moveGrant{UserID: userID, FromLaneID: completedID, ToLaneID: ordinaryID}) {
		t.Fatalf("expected both move directions without duplication, got %+v", got.Policy.MoveGrants)
	}
}
