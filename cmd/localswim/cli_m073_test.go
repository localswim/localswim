package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/v2"
)

func TestM073ProvisionAndGrantWorkflow(t *testing.T) {
	human, bot := strings.Repeat("A", 43), strings.Repeat("B", 42)+"A"
	service, err := v2.Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = service.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)

	run := func(token, refusal string, args ...string) []byte {
		t.Helper()
		env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": token}
		var out, stderr bytes.Buffer
		code := runCLIWithInput(args, nil, &out, &stderr, func(key string) string { return env[key] })
		if refusal == "" {
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("%v exited %d: %s", args, code, &stderr)
			}
		} else if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), refusal) {
			t.Fatalf("%v should refuse with %q, exit %d: %s", args, refusal, code, &stderr)
		}
		return out.Bytes()
	}
	decode := func(data []byte, result any) {
		t.Helper()
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatal(err)
		}
	}
	type boardDetails struct {
		ID             string   `json:"id"`
		Name           string   `json:"name"`
		DefaultOwnerID string   `json:"default_owner_id"`
		LaneIDs        []string `json:"lane_ids"`
		Revision       uint64   `json:"revision"`
	}
	const name = "Pool projects café"
	var board, other boardDetails
	decode(run(human, "", "board", "create", "--preset", "terry", "name", name), &board)
	if board.Name != name || board.Revision != 1 || len(board.LaneIDs) != 7 {
		t.Fatalf("incorrect Terry preset board: %+v", board)
	}
	decode(run(human, "", "board", "create", "name", "Other board"), &other)
	if len(other.LaneIDs) != 5 {
		t.Fatalf("incorrect generic board: %+v", other)
	}
	var view struct {
		Lanes []struct{ ID, Key string } `json:"lanes"`
	}
	decode(run(human, "", "board", "get", "--view", "lanes", board.ID), &view)
	lanes := make(map[string]string, len(view.Lanes))
	for _, lane := range view.Lanes {
		lanes[lane.Key] = lane.ID
	}
	backlog, progress, completed := lanes["backlog"], lanes["in_progress"], lanes["completed"]
	botID := board.DefaultOwnerID
	if backlog == "" || progress == "" || completed == "" || botID == "" {
		t.Fatal("preset should expose the grant targets")
	}
	revision := func() string {
		t.Helper()
		var current boardDetails
		decode(run(human, "", "board", "get", board.ID), &current)
		return strconv.FormatUint(current.Revision, 10)
	}
	type policyPermissions struct {
		DefaultPriorityID string              `json:"default_priority_id"`
		CreateGrants      []map[string]string `json:"create_grants"`
		MoveGrants        []map[string]string `json:"move_grants"`
		ResourceGrants    []json.RawMessage   `json:"resource_grants"`
	}
	readPolicy := func() policyPermissions {
		t.Helper()
		var permissions policyPermissions
		decode(run(human, "", "policy", "get", board.ID), &permissions)
		return permissions
	}
	want := readPolicy()
	assertPolicy := func() {
		t.Helper()
		if current := readPolicy(); !reflect.DeepEqual(current, want) {
			t.Fatal("the policy changed beyond the requested grant")
		}
	}

	run(bot, "HTTP 403", "card", "create", board.ID, progress, "In Progress card")
	var card struct {
		ID     string `json:"id"`
		LaneID string `json:"lane_id"`
	}
	decode(run(bot, "", "card", "create", board.ID, backlog, "Backlog card"), &card)
	run(bot, "HTTP 403", "card", "move", board.ID, card.ID, progress)

	current := revision()
	run(human, "revision mismatch", "policy", "grant", board.ID, "create", "1", botID, progress)
	run(human, "HTTP 400", "policy", "grant", board.ID, "create", current, botID, other.LaneIDs[0])
	run(human, "HTTP 400", "policy", "grant", board.ID, "create", current, "ffffffff-ffff-4fff-8fff-ffffffffffff", progress)
	assertPolicy()
	run(human, "", "policy", "grant", strings.ToUpper(board.ID), "create", current,
		strings.ToUpper(botID), strings.ToUpper(progress))
	want.CreateGrants = append(want.CreateGrants, map[string]string{"user_id": botID, "lane_id": progress})
	assertPolicy()
	run(bot, "", "card", "create", board.ID, progress, "In Progress card")
	run(bot, "HTTP 403", "card", "move", board.ID, card.ID, progress)

	run(human, "", "policy", "grant", board.ID, "move", revision(), botID, backlog, progress)
	want.MoveGrants = append(want.MoveGrants, map[string]string{"user_id": botID, "from_lane_id": backlog, "to_lane_id": progress})
	assertPolicy()
	decode(run(bot, "", "card", "move", board.ID, card.ID, progress), &card)
	if card.LaneID != progress {
		t.Fatal("the granted movement did not put the card in In Progress")
	}
	run(bot, "HTTP 403", "card", "move", board.ID, card.ID, completed)
}

func TestM073ContextualHelpAndInvalidInputs(t *testing.T) {
	noService := func(string) string {
		t.Helper()
		t.Fatal("help or invalid input reached execution")
		return ""
	}
	for _, tc := range []struct {
		group, action string
		required      []string
	}{
		{"board", "create", []string{"--preset terry", "json <JSON_FILE|->", "custom workflow"}},
		{"policy", "grant", []string{"BOARD_ID create REVISION", "BOARD_ID move REVISION", "<JSON_FILE|->"}},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput([]string{tc.group, tc.action, "--help"}, nil, &out, &stderr, noService)
		if code != 0 || stderr.Len() != 0 || strings.Contains(out.String(), "workspace info") {
			t.Fatalf("unexpected %s %s help: %d, %s, %s", tc.group, tc.action, code, &out, &stderr)
		}
		for _, required := range tc.required {
			if !strings.Contains(out.String(), required) {
				t.Errorf("%s %s help missing %q", tc.group, tc.action, required)
			}
		}
	}
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, args := range [][]string{
		{"board", "create", "--preset", "terry"},
		{"board", "create", "--preset", "other", "name", "Unsupported"},
		{"board", "create", "--preset=", "name", "Empty"},
		{"board", "create", "--preset", "terry", "json", "-"},
		{"board", "create", "unknown", "Name"},
		{"policy", "grant", id, "create", id, id},
		{"policy", "grant", id, "unknown", "1", id, id},
		{"policy", "grant", "--revision", "1", id, "create", "1", id, id},
		{"policy", "grant", id, "create", "1", "unknown-user", id},
		{"policy", "grant", id, "create", "1", id, "lane-key"},
		{"policy", "grant", id, "create", "1", id, id, "-"},
		{"policy", "grant", id, "move", "1", id, id},
		{"policy", "grant", id, "move", "1", id, id, strings.ToUpper(id)},
		{"policy", "grant", id, "json", ""},
		{"policy", "grant", "--create", id, id, id},
	} {
		var out, stderr bytes.Buffer
		if code := runCLIWithInput(args, nil, &out, &stderr, noService); code != 2 || out.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}
