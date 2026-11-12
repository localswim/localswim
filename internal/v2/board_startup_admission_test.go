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
			raw, headers = human.call("PATCH", "/workspace", pins, headers.Get("ETag"), 200)
			humanPins := turnOneDecode[workspace](t, raw)
			humanTag := headers.Get("ETag")
			if !slices.Equal(humanPins.PinnedBoardIDs, []string{failed.ID, healthy.ID}) {
				t.Fatal("a diagnostic row prevented pin ordering")
			}
			raw, headers = bot.call("GET", "/workspace", "", "", 200)
			botPins := turnOneDecode[workspace](t, raw)
			botTag := headers.Get("ETag")
			if botPins.Revision != humanPins.Revision || !slices.Equal(botPins.PinnedBoardIDs, []string{healthy.ID}) ||
				botTag == "" || botTag == humanTag || !strings.Contains(headers.Get("Vary"), "Authorization") {
				t.Fatal("Bot did not receive a revision-bound filtered workspace view")
			}
			const emptyPins = `{"pinned_board_ids":[]}`
			bot.call("PATCH", "/workspace", emptyPins, humanTag, 412)
			bot.call("PATCH", "/workspace", pins, botTag, 400)
			raw, headers = bot.call("PATCH", "/workspace", emptyPins, botTag, 200)
			botPins = turnOneDecode[workspace](t, raw)
			if botPins.PinnedBoardIDs == nil || len(botPins.PinnedBoardIDs) != 0 || botPins.Revision != humanPins.Revision+1 ||
				headers.Get("ETag") == botTag {
				t.Fatal("Bot pin update exposed hidden IDs or lost revision protection")
			}
			bot.call("PATCH", "/workspace", emptyPins, botTag, 412)
			botTag = headers.Get("ETag")
			raw, headers = bot.call("PATCH", "/workspace", emptyPins, botTag, 200)
			if got := turnOneDecode[workspace](t, raw); got.Revision != botPins.Revision ||
				got.PinnedBoardIDs == nil || len(got.PinnedBoardIDs) != 0 || headers.Get("ETag") != botTag {
				t.Fatal("Bot pin no-op exposed hidden IDs or advanced the revision")
			}
			raw, _ = human.call("GET", "/workspace", "", "", 200)
			if got := turnOneDecode[workspace](t, raw); got.Revision != botPins.Revision ||
				!slices.Equal(got.PinnedBoardIDs, []string{failed.ID}) {
				t.Fatal("Bot pin update removed the Human diagnostic pin")
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

func TestM83MissingRecoveryActorsQuarantineOnlyDependents(t *testing.T) {
	healthy, principals := m01Fixture()
	at := healthy.Board.CreatedAt
	firstActor := user{ID: "10000000-0000-4000-8000-000000000004", Revision: 1, CreatedAt: at,
		Label: "First historical actor", Kind: "human", Color: "#112233"}
	secondActor := user{ID: "10000000-0000-4000-8000-000000000005", Revision: 1, CreatedAt: at,
		Label: "Second historical actor", Kind: "human", Color: "#223344"}
	newBoard := func(id string, actors ...user) snapshot {
		members := append(slices.Clone(principals), actors...)
		value := freshSnapshot(id, "Recovery actor fixture", defaultWorkflow(id, at, members), m01HumanID, at)
		value.Board.DefaultOwnerID = m01BotID
		return value
	}
	provider := newBoard("20000000-0000-4000-8000-000000000001", firstActor)
	middle := newBoard("30000000-0000-4000-8000-000000000001", firstActor, secondActor)
	dependent := newBoard("40000000-0000-4000-8000-000000000001", secondActor)
	root := t.TempDir()
	initial, err := openStore(root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer initial.close()
	for _, value := range []snapshot{provider, middle, dependent, healthy} {
		if err := initial.create(value); err != nil {
			t.Fatal(err)
		}
	}
	recoverWithoutActor := func(value snapshot, actor string) snapshot {
		source := cloneSnapshot(value)
		source.Users = slices.DeleteFunc(source.Users, func(member user) bool { return member.ID == actor })
		source.Board.UserIDs = slices.DeleteFunc(source.Board.UserIDs, func(id string) bool { return id == actor })
		source.Policy.CreateGrants = slices.DeleteFunc(source.Policy.CreateGrants, func(g createGrant) bool { return g.UserID == actor })
		source.Policy.MoveGrants = slices.DeleteFunc(source.Policy.MoveGrants, func(g moveGrant) bool { return g.UserID == actor })
		source.Policy.ResourceGrants = slices.DeleteFunc(source.Policy.ResourceGrants, func(g resourceGrant) bool { return g.UserID == actor })
		recovered, err := initial.recoverSnapshot(value.Board.ID, actor, newOperationID(),
			matchingSnapshotTag(snapshotRepresentationTag(value)), source, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return recovered
	}
	middle = recoverWithoutActor(middle, firstActor.ID)
	dependent = recoverWithoutActor(dependent, secondActor.ID)
	if err := initial.close(); err != nil {
		t.Fatal(err)
	}
	providerPath := initial.snapshotPath(provider.Board.ID)
	if err := os.Remove(providerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(initial.recoveryReferencePath(provider.Board.ID)); err != nil {
		t.Fatal(err)
	}
	if reopened, err := openStore(root, principals...); !errors.Is(err, errIdentityConflict) {
		if reopened != nil {
			_ = reopened.close()
		}
		t.Fatalf("startup accepted orphan evidence without a failed saved snapshot: %v", err)
	}
	if err := writeAtomic(providerPath, []byte("{")); err != nil {
		t.Fatal(err)
	}
	isolated, err := openStore(root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.close()
	if len(isolated.failedBoards) != 3 || len(isolated.list()) != 1 {
		t.Fatal("startup did not isolate the provider and both dependents")
	}
	for _, value := range []snapshot{provider, middle, dependent} {
		if _, admitted := isolated.accepted(value.Board.ID); admitted || !isolated.isFailedBoard(value.Board.ID) {
			t.Fatal("startup admitted a board without trusted recovery actor evidence")
		}
	}
	if _, err := isolated.update(healthy.Board.ID, healthy.Board.Revision, m01Rename); err != nil {
		t.Fatalf("independent board could not commit an edit: %v", err)
	}
	saved, err := isolated.durable(healthy.Board.ID)
	if err != nil || saved.Board.Name != m01UpdatedName || saved.Board.Revision != healthy.Board.Revision+1 {
		t.Fatalf("independent board edit was not durable: %v", err)
	}
	if err := isolated.close(); err != nil {
		t.Fatal(err)
	}

	// The middle board still provides a trusted definition during the
	// conflict check, even though it will later need quarantine.
	event := &dependent.Activity[len(dependent.Activity)-1]
	evidence, ok := recoveryEventEvidence(*event)
	if !ok {
		t.Fatal("test fixture lacks recovery evidence")
	}
	evidence.ActorKind = "bot"
	data, err := marshalCanonical(evidence)
	if err != nil {
		t.Fatal(err)
	}
	after := string(data)
	event.After = &after
	data = marshalVerificationFixture(t, dependent)
	if _, err := parseSnapshot(data, dependent.Board.ID); err != nil {
		t.Fatalf("contradictory evidence must pass local validation: %v", err)
	}
	if err := writeAtomic(isolated.snapshotPath(dependent.Board.ID), data); err != nil {
		t.Fatal(err)
	}
	if reopened, err := openStore(root, principals...); !errors.Is(err, errIdentityConflict) {
		if reopened != nil {
			_ = reopened.close()
		}
		t.Fatalf("a local failure masked contradictory recovery actor evidence: %v", err)
	}
}
