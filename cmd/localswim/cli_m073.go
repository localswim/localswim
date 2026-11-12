package main

import (
	"encoding/json"
	"errors"
	"strings"
)

const boardCreateHelp = `Usage:
  localswim board create --name NAME [--preset terry]
  localswim board create --json <JSON_FILE|->

Use --name alone for the generic workflow, or add --preset terry for Terry's
seven-lane workflow. Use --json with a file path or - for standard input to
supply a full custom workflow document.

Examples
  localswim board create --name "Pool projects" --preset terry
  localswim board create --json workflow.json
`

const policyGrantHelp = `Usage:
  localswim policy grant --revision N --create BOARD_ID USER_ID LANE_ID
  localswim policy grant --revision N --move BOARD_ID USER_ID FROM_LANE_ID TO_LANE_ID
  localswim policy grant [--revision N] BOARD_ID <JSON_FILE|->

Add one creation or movement grant using UUIDv4 IDs from the selected board.
Use board get for the board revision and user IDs, and board get --view lanes
for lane IDs. A stale revision refuses the edit. All grant forms preserve
the other permissions and the default priority.

The JSON form requires a file path or - for standard input and accepts
create_grants and/or move_grants. Without --revision it uses the current board
revision. Inspect the policy with policy get after an unknown write outcome.

Examples
  localswim policy grant --revision 4 --create $board $user $lane
  localswim policy grant --revision 5 --move $board $user $from $to
`

func scopedPolicyGrantInput(args []string, create bool) (string, json.RawMessage, error) {
	key := "move_grants"
	lanes := []string{"from_lane_id", "to_lane_id"}
	expected := "--move requires UUIDv4 board, user, source lane, and destination lane IDs"
	if create {
		key = "create_grants"
		lanes = []string{"lane_id"}
		expected = "--create requires UUIDv4 board, user, and lane IDs"
	}
	if len(args) != len(lanes)+2 {
		return "", nil, errors.New(expected)
	}
	for _, id := range args {
		if !boardIDPattern.MatchString(id) {
			return "", nil, errors.New(expected)
		}
	}
	if !create && strings.EqualFold(args[2], args[3]) {
		return "", nil, errors.New("--move requires different source and destination lane IDs")
	}
	grant := map[string]string{"user_id": strings.ToLower(args[1])}
	for index, field := range lanes {
		grant[field] = strings.ToLower(args[index+2])
	}
	body, err := json.Marshal(map[string][]map[string]string{key: {grant}})
	return strings.ToLower(args[0]), body, err
}
