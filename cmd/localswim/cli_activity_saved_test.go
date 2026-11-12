package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/v2"
)

func TestM101CLIReadsSavedActivityAfterWriterStops(t *testing.T) {
	root := t.TempDir()
	human, bot := strings.Repeat("A", 43), strings.Repeat("B", 42)+"A"
	app, err := v2.Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	stop := func() {
		server.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(stop)
	liveEnv := func(key string) string {
		return map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": human}[key]
	}
	savedEnv := func(key string) string {
		t.Fatalf("saved activity read %s", key)
		return ""
	}
	run := func(getenv func(string) string, want int, args ...string) []byte {
		t.Helper()
		var stdout, stderr bytes.Buffer
		got := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, getenv)
		if got != want || want != 0 && stdout.Len() != 0 {
			t.Fatalf("%v exited %d, want %d: %s", args[:2], got, want, stderr.String())
		}
		return stdout.Bytes()
	}
	var board struct {
		ID      string   `json:"id"`
		LaneIDs []string `json:"lane_ids"`
	}
	created := run(liveEnv, 0, "board", "create", "--name", "M101 fixture")
	if err := json.Unmarshal(created, &board); err != nil || len(board.LaneIDs) == 0 {
		t.Fatal("fixture board was not created")
	}
	var card struct {
		At       string `json:"created_at"`
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
	}
	created = run(liveEnv, 0, "card", "create", "--title", "Private card title", board.ID, board.LaneIDs[0])
	if err := json.Unmarshal(created, &card); err != nil || card.At == "" {
		t.Fatal("fixture card was not created")
	}
	stop()

	activityArgs := []string{"activity", "list", "--data", root, "--from", card.At, "--to", card.At,
		"--writer-id", t05Writer, "--ticket", "1", "--card-id", card.ID}
	raw := run(savedEnv, 0, append(activityArgs, strings.ToUpper(board.ID))...)
	var page struct {
		WriterID           json.RawMessage `json:"writer_id"`
		BoardRevision      uint64          `json:"board_revision"`
		BotOnly            bool            `json:"bot_only"`
		PaginationComplete bool            `json:"pagination_complete"`
		Coverage           struct {
			Code          string `json:"code"`
			Complete      bool   `json:"complete"`
			LatestEventAt string `json:"latest_event_at"`
		} `json:"coverage"`
		Activities []struct {
			ID        string   `json:"id"`
			ActorID   string   `json:"actor_id"`
			ActorKind string   `json:"actor_kind"`
			Operation string   `json:"operation"`
			Tickets   []uint64 `json:"tickets"`
		} `json:"activities"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("Private card title")) || string(page.WriterID) != "null" ||
		page.BoardRevision != card.Revision || page.BotOnly || !page.PaginationComplete ||
		page.Coverage.Complete || page.Coverage.Code != "saved_history" || page.Coverage.LatestEventAt != card.At ||
		len(page.Activities) != 1 {
		t.Fatal("unexpected saved activity envelope")
	}
	event := page.Activities[0]
	if !operationIDPattern.MatchString(event.ID) || !boardIDPattern.MatchString(event.ActorID) ||
		event.ActorKind != "human" || event.Operation != "create" || len(event.Tickets) != 1 || event.Tickets[0] != 1 {
		t.Fatal("saved activity omitted sanitized event attribution")
	}
	run(savedEnv, 2, append(activityArgs, "--view", "full", board.ID)...)
	run(savedEnv, 2, append(activityArgs, "--data=", board.ID)...)

	path := filepath.Join(root, "boards", board.ID+".json")
	if err := os.WriteFile(path, []byte("malformed disposable snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(savedEnv, 1, append(activityArgs, board.ID)...)
}
