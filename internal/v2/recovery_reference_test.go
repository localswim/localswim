package v2

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestM95RestartVerificationRecoveryPoint(t *testing.T) {
	for _, tc := range []struct {
		name            string
		snapshotMissing bool
		alterReference  string
		wantMode        string
	}{
		{name: "damaged snapshot", wantMode: "same_id"},
		{name: "missing snapshot", snapshotMissing: true, wantMode: "same_id"},
		{name: "missing reference", alterReference: "missing", wantMode: "fresh_only"},
		{name: "corrupt reference", alterReference: "digest", wantMode: "fresh_only"},
		{name: "reference for another board", alterReference: "identity", wantMode: "fresh_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial, human := attachmentHTTPServer(t)
			raw, headers := human.call("POST", "/boards", `{"name":"Private name before damage"}`, "", 201)
			b := turnOneDecode[board](t, raw)
			raw, _ = human.call("PATCH", "/boards/"+b.ID, `{"name":"Private last committed name"}`, headers.Get("ETag"), 200)
			b = turnOneDecode[board](t, raw)
			raw, healthyHeaders := human.call("POST", "/boards", `{"name":"Healthy"}`, "", 201)
			healthy := turnOneDecode[board](t, raw)
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			damaged := []byte(`{"board":{"name":"Untrusted replacement name"}}`)
			path := initial.store.snapshotPath(b.ID)
			if tc.snapshotMissing {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := writeAtomic(path, damaged); err != nil {
				t.Fatal(err)
			}
			referencePath := initial.store.recoveryReferencePath(b.ID)
			refBefore, err := os.ReadFile(referencePath)
			if err != nil {
				t.Fatal(err)
			}
			switch tc.alterReference {
			case "missing":
				refBefore = nil
				if err := os.Remove(referencePath); err != nil {
					t.Fatal(err)
				}
			case "digest":
				refBefore = bytes.Replace(refBefore, []byte(`"revision_floor":2`), []byte(`"revision_floor":1`), 1)
				if err := writeAtomic(referencePath, refBefore); err != nil {
					t.Fatal(err)
				}
			case "identity":
				refBefore, err = os.ReadFile(initial.store.recoveryReferencePath(healthy.ID))
				if err != nil {
					t.Fatal(err)
				}
				if err := writeAtomic(referencePath, refBefore); err != nil {
					t.Fatal(err)
				}
			}

			bot := human
			bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
			app, err := Open(initial.DataRoot(), human.token, bot.token)
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			server := httptest.NewUnstartedServer(nil)
			server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
			server.Start()
			defer server.Close()
			human.url, bot.url = server.URL, server.URL
			raw, _ = human.call("GET", "/boards", "", "", 200)
			rows := turnOnePage[boardSummary](t, raw, "boards").Values
			if len(rows) != 2 || !slices.ContainsFunc(rows, func(row boardSummary) bool {
				return row.ID == b.ID && row.Availability == "unhealthy" && row.Name == nil && row.Revision == nil
			}) {
				t.Fatal("catalog did not retain the sanitized unavailable identity")
			}
			bot.call("GET", "/verifications/"+b.ID, "", "", 404)
			human.call("GET", "/snapshots/"+b.ID, "", "", 404)
			human.call("POST", "/boards", fmt.Sprintf(`{"id":%q,"name":"Attempted reuse"}`, b.ID), "", 409)
			raw, _ = human.call("GET", "/verifications/"+b.ID, "", "", 200)
			report := turnOneDecode[verification](t, raw)
			wantIssue := "integrity_failure"
			if tc.snapshotMissing {
				wantIssue = "required"
			}
			if report.BoardID != b.ID || report.Valid || report.Revision != nil || len(report.Issues) != 1 ||
				report.Issues[0].Code != wantIssue || report.Recovery == nil || report.Recovery.Mode != tc.wantMode {
				t.Fatalf("unexpected restart verification: %+v", report)
			}
			floor := report.Recovery.RevisionFloor
			if tc.wantMode == "same_id" && (floor == nil || *floor != b.Revision) ||
				tc.wantMode == "fresh_only" && floor != nil {
				t.Fatalf("unexpected recovery revision: %v", floor)
			}
			for _, forbidden := range []string{"Private", "Untrusted", "restore_actors", "snapshot_sha2_256", humanID, botID} {
				if bytes.Contains(raw, []byte(forbidden)) {
					t.Fatal("diagnostic leaked board content or authority")
				}
			}
			human.call("PATCH", "/boards/"+healthy.ID, `{"name":"Healthy edit after restart"}`, healthyHeaders.Get("ETag"), 200)

			saved, err := os.ReadFile(path)
			if tc.snapshotMissing && !errors.Is(err, os.ErrNotExist) ||
				!tc.snapshotMissing && (err != nil || !bytes.Equal(saved, damaged)) {
				t.Fatal("diagnostic or refused mutation changed the snapshot")
			}
			current, err := os.ReadFile(referencePath)
			if refBefore == nil && !errors.Is(err, os.ErrNotExist) ||
				refBefore != nil && (err != nil || !bytes.Equal(current, refBefore)) {
				t.Fatal("diagnostic or refused mutation changed the reference")
			}
			if tc.wantMode == "same_id" {
				if err := os.Remove(referencePath); err != nil {
					t.Fatal(err)
				}
				raw, _ = human.call("GET", "/verifications/"+b.ID, "", "", 200)
				report = turnOneDecode[verification](t, raw)
				if report.Recovery == nil || report.Recovery.Mode != "fresh_only" || report.Recovery.RevisionFloor != nil {
					t.Fatal("a reference removed after startup still conferred continuity")
				}
			}
		})
	}
}

func TestM95InterruptedSnapshotAndReferencePair(t *testing.T) {
	for _, tc := range []struct {
		name, snapshotState string
		settled             bool
		wantFloor           uint64
	}{
		{"before snapshot replace", "previous", false, 1},
		{"after snapshot replace", "next", false, 2},
		{"unknown commit and damaged snapshot", "damaged", false, 0},
		{"unknown commit and arbitrary valid replacement", "unrelated", false, 0},
		{"settled floor and rolled-back snapshot", "previous", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			initial, previous := m01CreateBoard(t, root)
			next, err := initial.update(previous.Board.ID, previous.Board.Revision, m01Rename)
			if err != nil {
				t.Fatal(err)
			}
			previousData := marshalVerificationFixture(t, previous)
			nextData := marshalVerificationFixture(t, next)
			if !tc.settled {
				current := recoveryContextFor(next, nextData)
				ref := recoveryReference{Committed: recoveryContextFor(previous, previousData), Pending: &current}
				if err := initial.writeRecoveryReference(ref); err != nil {
					t.Fatal(err)
				}
			}
			if err := initial.close(); err != nil {
				t.Fatal(err)
			}
			var data []byte
			switch tc.snapshotState {
			case "previous":
				data = previousData
			case "next":
				data = nextData
			case "damaged":
				data = []byte("{")
			case "unrelated":
				other := cloneSnapshot(next)
				other.Board.Name = "Different valid snapshot"
				last := &other.Activity[len(other.Activity)-1]
				last.After = &other.Board.Name
				data = marshalVerificationFixture(t, other)
				if _, err := parseSnapshot(data, other.Board.ID); err != nil {
					t.Fatalf("replacement fixture must be valid: %v", err)
				}
			}
			if err := writeAtomic(initial.snapshotPath(previous.Board.ID), data); err != nil {
				t.Fatal(err)
			}
			app, err := openStore(root, initial.principals...)
			if err != nil {
				t.Fatal(err)
			}
			defer app.close()
			accepted, healthy := app.accepted(previous.Board.ID)
			if tc.snapshotState == "damaged" || tc.snapshotState == "unrelated" || tc.settled {
				if healthy || !app.isFailedBoard(previous.Board.ID) {
					t.Fatal("startup admitted a snapshot without a matching commit")
				}
				report, _, _ := app.failedBoardVerification(previous.Board.ID)
				if tc.wantFloor == 0 && report.Recovery.Mode != "fresh_only" ||
					tc.wantFloor != 0 && (report.Recovery.RevisionFloor == nil || *report.Recovery.RevisionFloor != tc.wantFloor) {
					t.Fatalf("unexpected continuity after refusal: %+v", report.Recovery)
				}
				return
			}
			if !healthy || accepted.Board.Revision != tc.wantFloor {
				t.Fatal("startup did not resume the exact previous or next board")
			}
			ref, valid := app.readRecoveryReference(previous.Board.ID)
			if !valid || ref.Pending != nil || ref.Committed.RevisionFloor != tc.wantFloor {
				t.Fatal("startup did not settle the validated exact snapshot")
			}
		})
	}
}

func TestM95HealthyIdentityCollisionRefusesContinuity(t *testing.T) {
	healthyStore, healthy := m01CreateBoard(t, t.TempDir())
	id := healthy.Lanes[0].ID
	_, principals := m01Fixture()
	candidate := freshSnapshot(id, "Colliding board", defaultWorkflow(id, healthy.Board.CreatedAt, principals),
		m01HumanID, healthy.Board.CreatedAt)
	candidate.Board.DefaultOwnerID = m01BotID
	if err := healthyStore.create(candidate); !errors.Is(err, errIdentityConflict) {
		t.Fatalf("healthy lane identity was available as a board identity: %v", err)
	}
	if err := healthyStore.writeRecoveryReference(recoveryReference{Committed: recoveryContextFor(candidate,
		marshalVerificationFixture(t, candidate))}); err != nil {
		t.Fatal(err)
	}
	if err := healthyStore.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(healthyStore.root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if !reopened.isFailedBoard(id) {
		t.Fatal("colliding reference was not discoverable")
	}
	report, _, _ := reopened.failedBoardVerification(id)
	if report.Recovery.Mode != "fresh_only" || report.Recovery.RevisionFloor != nil {
		t.Fatal("colliding identity authorized same-ID recovery")
	}
	if _, err := reopened.update(healthy.Board.ID, healthy.Board.Revision, m01Rename); err != nil {
		t.Fatalf("collision interfered with a healthy board edit: %v", err)
	}
}

func TestM95MissingRestoreAuthorityRequiresFreshBoard(t *testing.T) {
	value, principals := m01Fixture()
	for index := range value.Policy.ResourceGrants {
		grant := &value.Policy.ResourceGrants[index]
		grant.Capabilities = slices.DeleteFunc(grant.Capabilities, func(capability string) bool {
			return capability == "restore"
		})
	}
	root := t.TempDir()
	initial, err := openStore(root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer initial.close()
	if err := initial.create(value); err != nil {
		t.Fatal(err)
	}
	if err := initial.close(); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(initial.snapshotPath(value.Board.ID), []byte("{")); err != nil {
		t.Fatal(err)
	}
	app, err := openStore(root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer app.close()
	if ref, valid := app.readRecoveryReference(value.Board.ID); !valid || len(ref.Committed.RestoreActors) != 0 {
		t.Fatal("fixture must retain valid identity and revision evidence without restore grants")
	}
	report, _, _ := app.failedBoardVerification(value.Board.ID)
	if report.Recovery.Mode != "fresh_only" || report.Recovery.RevisionFloor != nil {
		t.Fatal("the board identity and revision alone implied restore authority")
	}
}
