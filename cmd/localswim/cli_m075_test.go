package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/v2"
)

func TestM075CLIReportsMatchCapturedRevisionAfterWriterStops(t *testing.T) {
	dataDir := t.TempDir()
	human, bot := strings.Repeat("A", 43), strings.Repeat("B", 42)+"A"
	service, err := v2.Open(dataDir, human, bot)
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
	env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": human}
	liveEnv := func(key string) string { return env[key] }
	offlineEnv := func(key string) string {
		t.Fatalf("offline report tried to read %s", key)
		return ""
	}
	run := func(getenv func(string) string, input string, args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		code := runCLIWithInput(args, strings.NewReader(input), &out, &stderr, getenv)
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("%v exited %d: %s", args[:2], code, &stderr)
		}
		return out.Bytes()
	}
	decode := func(data []byte, result any) {
		t.Helper()
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatal(err)
		}
	}
	var board struct {
		ID       string   `json:"id"`
		LaneIDs  []string `json:"lane_ids"`
		Revision uint64   `json:"revision"`
	}
	decode(run(liveEnv, "", "board", "create", "--name", "Offline reports"), &board)
	if len(board.LaneIDs) < 2 {
		t.Fatal("fixture board needs two lanes")
	}
	backlog, ready := board.LaneIDs[0], board.LaneIDs[1]
	type cardRef struct {
		ID               string `json:"id"`
		Ticket           uint64 `json:"ticket"`
		BlockedCardCount int    `json:"blocked_card_count"`
	}
	create := func(laneID, title, description string) cardRef {
		t.Helper()
		input := fmt.Sprintf(`{"board_id":%q,"lane_id":%q,"title":%q,"description":%q}`, board.ID, laneID, title, description)
		var item cardRef
		decode(run(liveEnv, input, "card", "create", "-"), &item)
		return item
	}
	archived := create(backlog, "Archived work", "Archived description")
	blocker := create(ready, "Ready blocker", "Other card's prose")
	other := create(backlog, "Active work", "")
	link := fmt.Sprintf(`{"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`, blocker.ID, archived.ID)
	run(liveEnv, link, "relationship", "create", board.ID, "-")
	for _, post := range []struct{ cardID, text string }{
		{other.ID, "First active comment"},
		{blocker.ID, "Latest active comment"},
		{archived.ID, "Latest archived comment"},
	} {
		run(liveEnv, fmt.Sprintf(`{"text":%q}`, post.text), "comment", "post", post.cardID, "-")
	}
	run(liveEnv, "", "card", "archive", archived.ID)

	path := filepath.Join(dataDir, "boards", board.ID+".json")
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decode(run(liveEnv, "", "board", "get", board.ID), &board)
	type comparison struct {
		args []string
		live []byte
	}
	var comparisons []comparison
	capture := func(args ...string) []byte {
		t.Helper()
		result := run(liveEnv, "", args...)
		comparisons = append(comparisons, comparison{args, result})
		return result
	}

	var next struct {
		Cards      []cardRef `json:"cards"`
		NextCursor *string   `json:"next_cursor"`
	}
	decode(capture("card", "triage-order", "--lane", backlog, "--lane", ready, board.ID), &next)
	if len(next.Cards) != 1 || next.Cards[0].Ticket != blocker.Ticket ||
		next.Cards[0].BlockedCardCount != 1 || next.NextCursor == nil {
		t.Fatal("next did not apply the whole-board blocking score before the default limit")
	}
	cursor := *next.NextCursor
	decode(capture("card", "triage-order", "--lane", ready, "--lane", backlog, "--cursor", cursor, board.ID), &next)
	if len(next.Cards) != 1 || next.Cards[0].Ticket != other.Ticket || next.NextCursor != nil {
		t.Fatal("next did not continue the same lane selection in triage order")
	}

	decode(capture("card", "search", "--archived", "only", "--search", "Archived", board.ID), &next)
	if len(next.Cards) != 1 || next.Cards[0].Ticket != archived.Ticket {
		t.Fatal("search did not select archived work")
	}

	ticket := strconv.FormatUint(archived.Ticket, 10)
	var focus struct {
		BoardRevision uint64 `json:"board_revision"`
		Relationships []struct {
			Direction string  `json:"direction"`
			Card      cardRef `json:"card"`
		} `json:"relationships"`
		Prose json.RawMessage `json:"prose"`
	}
	decode(capture("card", "inspect", board.ID, ticket), &focus)
	if focus.BoardRevision != board.Revision || len(focus.Relationships) != 1 ||
		focus.Relationships[0].Direction != "incoming" || focus.Relationships[0].Card.ID != blocker.ID ||
		focus.Prose != nil {
		t.Fatal("focused inspection did not keep current relationship context and omit prose")
	}
	withProse := capture("card", "inspect", "--prose", board.ID, ticket)
	if !bytes.Contains(withProse, []byte("Archived description")) ||
		!bytes.Contains(withProse, []byte("Latest archived comment")) ||
		bytes.Contains(withProse, []byte("Other card's prose")) {
		t.Fatal("focused inspection did not restrict prose to the selected card")
	}

	var report struct {
		Comments []struct {
			Ticket   uint64 `json:"ticket"`
			Text     string `json:"text"`
			AuthorID string `json:"author_id"`
		} `json:"comments"`
		NextCursor *string `json:"next_cursor"`
	}
	decode(capture("comment", "list", "--limit", "1", board.ID), &report)
	if len(report.Comments) != 1 || report.Comments[0].Ticket != blocker.Ticket ||
		report.Comments[0].Text != "Latest active comment" || report.Comments[0].AuthorID == "" ||
		report.NextCursor == nil {
		t.Fatal("newest comments did not limit after archive filtering or retain attribution")
	}
	decode(capture("comment", "list", "--limit", "1", "--cursor", *report.NextCursor, board.ID), &report)
	if len(report.Comments) != 1 || report.Comments[0].Ticket != other.Ticket || report.NextCursor != nil {
		t.Fatal("newest comments did not continue in commit order")
	}
	for _, args := range [][]string{
		{"--archived", "include", "--limit", "1", board.ID},
		{board.ID, strings.ToUpper(archived.ID)},
	} {
		decode(capture(append([]string{"comment", "list"}, args...)...), &report)
		if len(report.Comments) != 1 || report.Comments[0].Ticket != archived.Ticket {
			t.Fatal("comment selection did not read the newest archived comment")
		}
	}

	server.Close()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	for _, item := range comparisons {
		args := slices.Insert(item.args, 2, "--data", dataDir)
		if got := run(offlineEnv, "", args...); !bytes.Equal(item.live, got) {
			t.Errorf("%v differed from the online output at board revision %d", item.args[:2], board.Revision)
		}
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, saved) {
		t.Fatalf("reports changed the captured snapshot: %v", err)
	}
}

func TestM075CLIRejectsEmptyDataAndOfflineWrites(t *testing.T) {
	for _, args := range [][]string{
		{"card", "triage-order", "--data=", t03Board},
		{"card", "search", "--data=", t03Board},
		{"comment", "list", "--data=", t03Board},
		{"card", "archive", "--data", t.TempDir(), t03Card},
		{"comment", "post", "--data", t.TempDir(), t03Card, "-"},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput(args, strings.NewReader(""), &out, &stderr, func(key string) string {
			t.Fatalf("%v tried to read live configuration %s", args[:2], key)
			return ""
		})
		if code != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), "--data") {
			t.Errorf("%v returned exit=%d, stdout=%q, stderr=%q", args[:2], code, &out, &stderr)
		}
	}
}
