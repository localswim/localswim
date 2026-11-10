package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func nativeVerificationFixture() snapshot {
	at := time.Date(2026, time.November, 10, 16, 0, 0, 123456000, time.UTC)
	id := newID()
	member := user{entity: newEntity(humanID, at), Label: "Owner", Kind: "human"}
	w := defaultWorkflow(id, at, []user{member})
	return freshSnapshot(id, "Verification fixture", w, member.ID, at)
}

func TestNativeSnapshotRejectsWorkflowWithoutCreators(t *testing.T) {
	at := time.Date(2026, time.November, 10, 16, 0, 0, 123456000, time.UTC)
	id := newID()
	w := defaultWorkflow(id, at, []user{})
	value := freshSnapshot(id, "No implicit membership", w, humanID, at)
	if _, err := parseSnapshot(marshalVerificationFixture(t, value), id); err == nil {
		t.Fatal("accepted a workflow without any users able to create cards")
	}
}

func marshalVerificationFixture(t *testing.T, value snapshot) []byte {
	t.Helper()
	data, err := marshalCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNativeSnapshotClosedShape(t *testing.T) {
	value := nativeVerificationFixture()
	data := marshalVerificationFixture(t, value)
	if _, err := parseSnapshot(data, value.Board.ID); err != nil {
		t.Fatalf("valid native fixture refused: %v", err)
	}

	for _, tc := range []struct{ name, original, replacement string }{
		{"unsupported version", `"format_version":2`, `"format_version":1`},
		{"noncanonical member spelling", `"format_version":2`, `"formatVersion":2`},
		{"duplicate root field", `"format_version":2`, `"format_version":2,"format_version":2`},
		{"duplicate nested field", `"enabled":false`, `"enabled":false,"enabled":false`},
		{"unknown nested field", `"target_thread_id":null`, `"target_thread_id":null,"privateField":true`},
		{"case-variant member", `"target_thread_id":null`, `"TargetThreadId":null`},
		{"missing required nullable field", `"last_card_edit_at":null,`, ""},
		{"null required array", `"cards":[]`, `"cards":null`},
		{"null required boolean", `"enabled":false`, `"enabled":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			damaged := bytes.Replace(data, []byte(tc.original), []byte(tc.replacement), 1)
			if bytes.Equal(damaged, data) {
				t.Fatal("test did not change fixture")
			}
			if _, err := parseSnapshot(damaged, value.Board.ID); !errors.Is(err, errInvalidSnapshot) {
				t.Fatalf("damaged snapshot error = %v", err)
			}
		})
	}
}

func TestNativeSnapshotReferencesAndAudit(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*snapshot)
	}{
		{"wrong board etag", func(s *snapshot) { s.Board.ETag = etag(s.Board.ID, 7) }},
		{"missing lane reference", func(s *snapshot) { s.Board.LaneIDs[0] = newID() }},
		{"no creation permission", func(s *snapshot) { s.Policy.CreateGrants = []createGrant{} }},
		{"no completion route", func(s *snapshot) { s.Policy.MoveGrants = []moveGrant{} }},
		{"foreign grant actor", func(s *snapshot) { s.Policy.CreateGrants[0].UserID = newID() }},
		{"duplicate capability", func(s *snapshot) {
			g := &s.Policy.ResourceGrants[0]
			g.Capabilities = append(g.Capabilities, g.Capabilities[0])
		}},
		{"foreign audit actor", func(s *snapshot) { s.Activity[0].ActorID = newID() }},
		{"audit revision gap", func(s *snapshot) {
			s.Board.Revision++
			s.Board.ETag = etag(s.Board.ID, s.Board.Revision)
		}},
		{"derived attention mismatch", func(s *snapshot) { s.Board.Attention.Blocked++ }},
		{"invalid attachment", func(s *snapshot) { s.Attachments = append(s.Attachments, attachment{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := nativeVerificationFixture()
			tc.edit(&value)
			if err := validateSnapshot(value, value.Board.ID); !errors.Is(err, errInvalidSnapshot) {
				t.Fatalf("damaged snapshot error = %v", err)
			}
		})
	}
}

func TestNativeSnapshotNotificationAuditStartsAtCreationDefaults(t *testing.T) {
	value := nativeVerificationFixture()
	b := &value.Board
	target := newID()
	b.Notifications.TargetThreadID = &target
	if _, err := parseSnapshot(marshalVerificationFixture(t, value), b.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("unaudited target error = %v", err)
	}
	b.Notifications.Enabled = true
	if _, err := parseSnapshot(marshalVerificationFixture(t, value), b.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("unaudited enabled settings error = %v", err)
	}

	b.Revision++
	b.ETag = etag(b.ID, b.Revision)
	enabledField, targetField := "notifications.enabled", "notifications.target_thread_id"
	disabled, enabled := "false", "true"
	value.Activity = append(value.Activity,
		activity{ID: newID(), BoardID: b.ID, Revision: b.Revision, At: b.CreatedAt, ActorID: humanID,
			Operation: "patch", ResourceType: "board", ResourceID: b.ID, Field: &enabledField, Before: &disabled, After: &enabled},
		activity{ID: newID(), BoardID: b.ID, Revision: b.Revision, At: b.CreatedAt, ActorID: humanID,
			Operation: "patch", ResourceType: "board", ResourceID: b.ID, Field: &targetField, After: &target},
	)
	if err := validateSnapshot(value, b.ID); err != nil {
		t.Fatalf("valid notification audit refused: %v", err)
	}
	assertMissingAuditCapability(t, value, "manage_notifications")

	wrongTarget := newID()
	value.Activity[2].Before = &wrongTarget
	if err := validateSnapshot(value, b.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("wrong initial notification target error = %v", err)
	}
}

func TestNativeSnapshotRenameAuditRequiresManageBoard(t *testing.T) {
	value := nativeVerificationFixture()
	b := &value.Board
	field, previous := "name", b.Name
	b.Name = "Renamed"
	b.Revision++
	b.ETag = etag(b.ID, b.Revision)
	value.Activity = append(value.Activity, activity{
		ID: newID(), BoardID: b.ID, Revision: b.Revision, At: b.CreatedAt, ActorID: humanID,
		Operation: "patch", ResourceType: "board", ResourceID: b.ID, Field: &field, Before: &previous, After: &b.Name,
	})
	if err := validateSnapshot(value, b.ID); err != nil {
		t.Fatalf("valid rename audit refused: %v", err)
	}
	assertMissingAuditCapability(t, value, "manage_board")
	assertMissingAuditCapability(t, value, "read")
}

func assertMissingAuditCapability(t *testing.T, value snapshot, capability string) {
	t.Helper()
	value = cloneSnapshot(value)
	grant := &value.Policy.ResourceGrants[0]
	grant.Capabilities = slices.DeleteFunc(grant.Capabilities, func(candidate string) bool { return candidate == capability })
	if err := validateSnapshot(value, value.Board.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("audit without %s error = %v", capability, err)
	}
}

func TestNativeSnapshotParentAuditAndCycle(t *testing.T) {
	value := nativeVerificationFixture()
	b := &value.Board
	for i := range 2 {
		value.Cards = append(value.Cards, card{
			entity: newEntity(newID(), b.CreatedAt), BoardID: b.ID,
			Ticket: uint64(i + 1), Title: "Card", LaneID: value.Lanes[0].ID,
			PriorityID: value.Policy.DefaultPriorityID, ChildIDs: []string{},
		})
	}
	child, parent := &value.Cards[0], &value.Cards[1]
	child.ParentID = &parent.ID
	parent.ChildIDs = []string{child.ID}
	field := "parent_id"
	b.Revision++
	b.ETag = etag(b.ID, b.Revision)
	value.Activity = append(value.Activity, activity{
		ID: newID(), BoardID: b.ID, Revision: b.Revision, At: b.CreatedAt, ActorID: humanID,
		Operation: "patch", ResourceType: "card", ResourceID: child.ID, Field: &field, After: &parent.ID,
	})
	b.Attention, b.LastCardEditAt = deriveAttention(value)
	if err := validateSnapshot(value, b.ID); err != nil {
		t.Fatalf("valid audited parent refused: %v", err)
	}
	assertMissingAuditCapability(t, value, "manage_relationships")

	parent.ParentID = &child.ID
	child.ChildIDs = []string{parent.ID}
	if err := validateSnapshot(value, b.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("cycle error = %v", err)
	}
	parent.ParentID = nil
	child.ChildIDs = []string{}
	value.Activity[1].After = nil
	if err := validateSnapshot(value, b.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("bad parent audit error = %v", err)
	}
}

func TestOfflineVerificationReportsDamageWithoutRewriting(t *testing.T) {
	root := t.TempDir()
	value := nativeVerificationFixture()
	dir := filepath.Join(root, "boards")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, value.Board.ID+".json")
	data := marshalVerificationFixture(t, value)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	good, err := VerifyOffline(root, value.Board.ID)
	if err != nil || !good.Valid || good.Revision == nil || *good.Revision != 1 || len(good.Issues) != 0 {
		t.Fatalf("valid verification = %+v, %v", good, err)
	}
	if _, err := time.Parse(timestampLayout, good.At); err != nil {
		t.Fatalf("noncanonical verification timestamp: %v", err)
	}

	damaged := []byte("{\"private-user-data\":")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	bad, err := VerifyOffline(root, value.Board.ID)
	if err != nil || bad.Valid || bad.Revision != nil || len(bad.Issues) != 1 {
		t.Fatalf("damaged verification = %+v, %v", bad, err)
	}
	result, err := json.Marshal(bad)
	if err != nil || bytes.Contains(result, []byte("private-user-data")) || bytes.Contains(result, []byte("message")) {
		t.Fatalf("verification used unsanitized fields: %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, damaged) {
		t.Fatalf("verification changed the durable snapshot: %v", err)
	}

	if err := os.Truncate(path, maxSnapshotBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := readSnapshot(path, value.Board.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("oversized file error = %v", err)
	}
	if _, err := readSnapshot(dir, value.Board.ID); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("directory error = %v", err)
	}
}
