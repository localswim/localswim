package v2

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const legacyBoardID = "c03181b1-7f3b-433a-9b26-53fde69ac68e"

func legacySnapshotFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/snapshot-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestVersionOneSnapshotReadNoopMutationAndRestart(t *testing.T) {
	original := legacySnapshotFixture(t)
	loaded, err := parseSnapshot(original, legacyBoardID)
	if err != nil || loaded.FormatVersion != 2 {
		t.Fatalf("legacy read: version %d, error %v", loaded.FormatVersion, err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "boards"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "boards", legacyBoardID+".json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(root, loaded.Users...)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	noop, err := store.update(legacyBoardID, loaded.Board.ETag, func(*snapshot) error { return nil })
	if err != nil || noop.Board.ETag != loaded.Board.ETag {
		t.Fatalf("legacy no-op: %v", err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(unchanged, original) {
		t.Fatal("legacy read or no-op rewrote the file")
	}
	changed, err := store.update(legacyBoardID, loaded.Board.ETag, func(candidate *snapshot) error {
		target := "018e7f0a-bcde-7abc-8def-0123456789ac"
		field := "notifications.target_thread_id"
		before := candidate.Board.Notifications.TargetThreadID
		candidate.Board.Notifications.TargetThreadID = &target
		candidate.Activity = append(candidate.Activity, activity{
			ID: newID(), BoardID: legacyBoardID, Revision: candidate.Board.Revision + 1,
			At: time.Now().UTC(), ActorID: candidate.Users[0].ID, Operation: "patch",
			ResourceType: "board", ResourceID: legacyBoardID, Field: &field, Before: before, After: &target,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(root, loaded.Users...)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	saved, ok := reopened.accepted(legacyBoardID)
	if !ok || saved.FormatVersion != 2 || saved.Board.ETag != changed.Board.ETag ||
		saved.Board.Revision != loaded.Board.Revision+1 ||
		*saved.Activity[1].Field != "notifications.targetThreadId" ||
		*saved.Activity[2].Field != "notifications.target_thread_id" {
		t.Fatal("version 2 save or audit preservation failed")
	}
}

func TestVersionOneSnapshotRefusesInvalidAliases(t *testing.T) {
	original := string(legacySnapshotFixture(t))
	for name, changed := range map[string]string{
		"duplicate":      strings.Replace(original, `"formatVersion": 1`, `"formatVersion": 1, "formatVersion": 1`, 1),
		"mixed":          strings.Replace(original, `"createdAt"`, `"created_at"`, 1),
		"collision":      strings.Replace(original, `"formatVersion": 1`, `"formatVersion": 1, "format_version": 1`, 1),
		"unknown":        strings.Replace(original, `"formatVersion": 1`, `"formatVersion": 1, "unknown": true`, 1),
		"new_audit_name": strings.Replace(original, "notifications.targetThreadId", "notifications.target_thread_id", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSnapshot([]byte(changed), legacyBoardID); err == nil {
				t.Fatal("accepted invalid legacy snapshot")
			}
		})
	}
}

func TestSnapshotRefusesDuplicateAuditAliases(t *testing.T) {
	value, err := parseSnapshot(legacySnapshotFixture(t), legacyBoardID)
	if err != nil {
		t.Fatal(err)
	}
	field, target := "notifications.target_thread_id", "018e7f0a-bcde-7abc-8def-0123456789ac"
	duplicate := value.Activity[1]
	duplicate.ID = newID()
	duplicate.Field = &field
	duplicate.Before = duplicate.After
	duplicate.After = &target
	value.Activity = append(value.Activity, duplicate)
	value.Board.Notifications.TargetThreadID = &target
	if err := validateSnapshot(value, legacyBoardID); err == nil {
		t.Fatal("accepted two spellings of the same audit field in one revision")
	}
}
