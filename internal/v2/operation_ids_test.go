package v2

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperationIDFamilies(t *testing.T) {
	before := time.Now().UnixMilli()
	id := newOperationID()
	after := time.Now().UnixMilli()
	if !validSnapshotOperationID(id) || validSnapshotID(id) {
		t.Fatal("operation generator did not return a canonical UUIDv7")
	}
	data, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	created := int64(binary.BigEndian.Uint64(data[:8]) >> 16)
	if created < before || created > after {
		t.Fatal("operation UUID does not encode its generation time")
	}
	ordinary := newID()
	if !validSnapshotID(ordinary) || validSnapshotOperationID(ordinary) {
		t.Fatal("ordinary entity generator must remain UUIDv4")
	}
	for _, test := range []struct {
		id        string
		operation bool
	}{
		{id, true}, {ordinary, false},
	} {
		raw := json.RawMessage(fmt.Sprintf("%q", strings.ToUpper(test.id)))
		if test.operation {
			got, err := inputOperationID(raw, "id")
			if err != nil || got != test.id {
				t.Fatal("operation input did not normalize mixed case")
			}
			if _, err := inputID(raw, "id"); err == nil {
				t.Fatal("ordinary input accepted UUIDv7")
			}
		} else {
			got, err := inputID(raw, "id")
			if err != nil || got != test.id {
				t.Fatal("ordinary input did not normalize mixed case")
			}
			if _, err := inputOperationID(raw, "id"); err == nil {
				t.Fatal("operation input accepted UUIDv4")
			}
		}
	}
	for _, invalid := range []string{
		"00000000-0000-6000-8000-000000000001",
		"00000000-0000-7000-c000-000000000001",
		"00000000-0000-7000-8000-00000000000g", " " + id,
	} {
		if _, ok := normalizeOperationID(invalid); ok {
			t.Fatal("operation parser accepted an invalid UUID")
		}
	}
}

func TestOperationRoutesAndInputsRejectWrongVersion(t *testing.T) {
	_, client, state, _ := backupCaptureFixture(t)
	ordinary, operation := newID(), newOperationID()
	for _, route := range []string{"backups", "publications", "restorations", "approvals", "activations"} {
		client.call("GET", "/"+route+"/"+ordinary, "", "", 400)
		client.call("GET", "/"+route+"/"+strings.ToUpper(operation), "", "", 404)
	}
	for _, route := range []string{"boards", "cards", "comments", "attachments", "relationships", "builds", "snapshots"} {
		client.call("GET", "/"+route+"/"+operation, "", "", 400)
	}
	requests := map[string]string{
		"backups":      fmt.Sprintf(`{"id":%q,"board_id":%q,"expected_board_etag":%q}`, ordinary, state.Board.ID, state.Board.ETag),
		"publications": fmt.Sprintf(`{"id":%q,"board_id":%q,"backup_id":%q}`, ordinary, state.Board.ID, operation),
		"restorations": fmt.Sprintf(`{"id":%q,"board_id":%q,"backup_id":%q,"expected_board_etag":%q}`, ordinary, state.Board.ID, operation, state.Board.ETag),
		"approvals":    fmt.Sprintf(`{"id":%q}`, ordinary),
		"activations":  fmt.Sprintf(`{"id":%q}`, ordinary),
	}
	for route, body := range requests {
		client.call("POST", "/"+route, body, "", 400)
	}
}

func TestNativeSnapshotOperationIDValidationAndNormalization(t *testing.T) {
	state := nativeVerificationFixture()
	if err := validateSnapshot(state, state.Board.ID); err != nil {
		t.Fatal(err)
	}
	for _, event := range state.Activity {
		if !validSnapshotOperationID(event.ID) {
			t.Fatal("fresh audit contains an ordinary entity UUID")
		}
	}
	invalid := cloneSnapshot(state)
	invalid.Activity[0].ID = newID()
	if err := validateSnapshot(invalid, state.Board.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatal("native validation accepted a UUIDv4 audit event")
	}
	data := marshalVerificationFixture(t, state)
	upper := bytes.ReplaceAll(data, []byte(state.Activity[0].ID), []byte(strings.ToUpper(state.Activity[0].ID)))
	normalized, err := normalizeSnapshotInput(upper)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := parseSnapshot(normalized, state.Board.ID)
	if err != nil || loaded.Activity[0].ID != state.Activity[0].ID {
		t.Fatal("native input did not normalize its UUIDv7 audit identity")
	}
}

func TestBackupAndPublicationCreationTimeUsesOnlyID(t *testing.T) {
	app, client, state, _ := backupCaptureFixture(t)
	raw, _ := client.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`, state.Board.ID, state.Board.ETag), "", 201)
	capture := turnOneDecode[backup](t, raw)
	if !validSnapshotOperationID(capture.ID) || bytes.Contains(raw, []byte(`"created_at"`)) {
		t.Fatal("backup creation time contract is incorrect")
	}
	raw, _ = client.call("POST", "/publications", fmt.Sprintf(`{"board_id":%q,"backup_id":%q}`, state.Board.ID, capture.ID), "", 202)
	published := turnOneDecode[publication](t, raw)
	if !validSnapshotOperationID(published.ID) || bytes.Contains(raw, []byte(`"created_at"`)) || published.FinishedAt == nil {
		t.Fatal("publication creation or completion time contract is incorrect")
	}
	for _, record := range []struct {
		path string
		read func() error
	}{
		{filepath.Join(app.store.root, "backups", capture.ID, "backup.json"), func() error { _, err := readBackup(app.store.root, capture.ID); return err }},
		{filepath.Join(app.store.root, "publications", published.ID+".json"), func() error { _, err := readPublication(app.store.root, published.ID); return err }},
	} {
		data, err := os.ReadFile(record.path)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		value["created_at"] = json.RawMessage(`"2026-11-10 00:00:00+00:00"`)
		changed, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record.path, changed, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := record.read(); err == nil {
			t.Fatal("superseded stored creation timestamp was accepted")
		}
	}
}
