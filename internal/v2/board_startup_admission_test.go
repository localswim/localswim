package v2

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestM83StartsWithOneDamagedBoard(t *testing.T) {
	for _, name := range []string{"invalid JSON", "mismatched board identity"} {
		t.Run(name, func(t *testing.T) {
			initial, human := attachmentHTTPServer(t)
			raw, failedHeaders := human.call("POST", "/boards", `{"name":"Private failed board name"}`, "", 201)
			failed := turnOneDecode[board](t, raw)
			raw, healthyHeaders := human.call("POST", "/boards", `{"name":"Healthy board"}`, "", 201)
			healthy := turnOneDecode[board](t, raw)
			source, headers := human.call("GET", "/snapshots/"+failed.ID, "", "", 200)
			sourceTag := headers.Get("ETag")
			damaged := []byte(`{"board":{"name":"Untrusted partial name"`)
			if name == "mismatched board identity" {
				damaged, _ = human.call("GET", "/snapshots/"+healthy.ID, "", "", 200)
			}
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			path := initial.store.snapshotPath(failed.ID)
			if err := writeAtomic(path, damaged); err != nil {
				t.Fatal(err)
			}

			bot := human
			bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
			app, err := Open(initial.DataRoot(), human.token, bot.token)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := app.Close(); err != nil {
					t.Error(err)
				}
			}()
			server := httptest.NewUnstartedServer(nil)
			server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
			server.Start()
			defer server.Close()
			human.url, bot.url = server.URL, server.URL
			if competing, err := Open(app.DataRoot(), human.token, bot.token); !errors.Is(err, errStoreBusy) {
				if competing != nil {
					_ = competing.Close()
				}
				t.Fatalf("startup isolation lost the workspace lease: %v", err)
			}

			raw, _ = human.call("GET", "/boards", "", "", 200)
			rows := turnOnePage[boardSummary](t, raw, "boards").Values
			index := slices.IndexFunc(rows, func(row boardSummary) bool { return row.ID == failed.ID })
			if len(rows) != 2 || index < 0 {
				t.Fatalf("catalog does not contain both board identities: %+v", rows)
			}
			row := rows[index]
			if row.Name != nil || row.Revision != nil || row.Attention != nil || row.LastCardEditAt != nil ||
				row.Availability != "unhealthy" || row.ErrorCode == nil || *row.ErrorCode != durableStateUnavailable {
				t.Fatalf("expected a filename-only diagnostic with fixed status, got %+v", row)
			}
			raw, _ = bot.call("GET", "/boards", "", "", 200)
			rows = turnOnePage[boardSummary](t, raw, "boards").Values
			if len(rows) != 1 || rows[0].ID != healthy.ID {
				t.Fatal("Bot catalog disclosed an unadmitted board identity")
			}
			bot.call("GET", "/boards/"+failed.ID, "", "", 404)
			raw, headers = human.call("GET", "/boards/"+failed.ID, "", "", 503)
			problem := turnOneDecode[problemBody](t, raw)
			if problem.Code != "unavailable" || problem.Detail != "Durable state is unavailable." || headers.Get("ETag") != "" {
				t.Fatalf("unexpected board diagnostic: %+v", problem)
			}
			human.call("PATCH", "/boards/"+failed.ID, `{"name":"Attempted change"}`, failedHeaders.Get("ETag"), 503)
			recoveryPut(t, human, failed.ID, sourceTag, "application/json", source, 404)

			_, headers = human.call("GET", "/workspace", "", "", 200)
			pins := fmt.Sprintf(`{"pinned_board_ids":[%q,%q]}`, failed.ID, healthy.ID)
			raw, _ = human.call("PATCH", "/workspace", pins, headers.Get("ETag"), 200)
			if got := turnOneDecode[workspace](t, raw); !slices.Equal(got.PinnedBoardIDs, []string{failed.ID, healthy.ID}) {
				t.Fatal("a diagnostic row prevented pin ordering")
			}

			human.call("GET", "/boards/"+healthy.ID+"?view=lanes", "", "", 200)
			raw, headers = human.call("PATCH", "/boards/"+healthy.ID, `{"name":"Healthy board edited"}`, healthyHeaders.Get("ETag"), 200)
			edited := turnOneDecode[board](t, raw)
			saved, err := app.store.durable(healthy.ID)
			if err != nil || saved.Board.Name != "Healthy board edited" || saved.Board.Revision != healthy.Revision+1 ||
				saved.Board.Revision != edited.Revision || headers.Get("ETag") != saved.Board.entityTag() {
				t.Fatalf("healthy board edit was not committed: %v", err)
			}
			savedBytes, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(savedBytes, damaged) {
				t.Fatalf("startup or refused writes changed the failed snapshot: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			create := fmt.Sprintf(`{"id":%q,"name":"Attempted identity reuse"}`, failed.ID)
			human.call("POST", "/boards", create, "", 409)
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed identity was re-created: %v", err)
			}
		})
	}
}

func TestM83WorkspaceNamespaceFailureStillRefusesStartup(t *testing.T) {
	for _, path := range []string{"boards", "boards/invalid-name.json"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			damaged := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(damaged), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(damaged, []byte("workspace namespace damage"), 0o600); err != nil {
				t.Fatal(err)
			}
			if app, err := openStore(root); err == nil {
				_ = app.close()
				t.Fatal("startup accepted a damaged workspace namespace")
			}
			if err := os.Remove(damaged); err != nil {
				t.Fatal(err)
			}
			app, err := openStore(root)
			if err != nil {
				t.Fatalf("failed startup did not release the writer lease: %v", err)
			}
			if err := app.close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
