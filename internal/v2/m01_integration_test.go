package v2

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const (
	m01BoardID = "10000000-0000-4000-8000-000000000001"
	m01HumanID = "10000000-0000-4000-8000-000000000002"
	m01BotID   = "10000000-0000-4000-8000-000000000003"

	m01InitialName  = "Original board"
	m01UpdatedName  = "Renamed board"
	m01ProbeRootEnv = "LOCALSWIM_M01_PROBE_ROOT"
	m01ProbeETagEnv = "LOCALSWIM_M01_PROBE_ETAG"
)

func m01Fixture() (snapshot, []user) {
	now := time.Date(2026, time.November, 10, 0, 0, 0, 0, time.UTC)
	principals := []user{
		{ID: m01HumanID, Revision: 1, CreatedAt: now, Label: "Human", Kind: "human", Color: "#225588"},
		{ID: m01BotID, Revision: 1, CreatedAt: now, Label: "Bot", Kind: "bot", Color: "#885522"},
	}
	wf := defaultWorkflow(m01BoardID, now, principals)
	value := freshSnapshot(m01BoardID, m01InitialName, wf, m01HumanID, now)
	value.Board.DefaultOwnerID = m01BotID
	return value, principals
}

func m01CreateBoard(t *testing.T, root string) (*store, snapshot) {
	t.Helper()
	initial, principals := m01Fixture()
	s, err := openStore(root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.close(); err != nil {
			t.Error(err)
		}
	})
	if err := s.create(initial); err != nil {
		t.Fatal(err)
	}
	return s, initial
}

func m01Rename(value *snapshot) error {
	field, before, after := "name", value.Board.Name, m01UpdatedName
	value.Board.Name = after
	value.Activity = append(value.Activity, activity{
		ID: newOperationID(), BoardID: m01BoardID, Revision: value.Board.Revision + 1,
		At: canonicalTime(time.Now()), ActorID: m01HumanID, Operation: "patch",
		ResourceType: "board", ResourceID: m01BoardID,
		Field: &field, Before: &before, After: &after,
	})
	return nil
}

func TestM01ExclusiveWriterAndRestart(t *testing.T) {
	root := t.TempDir()
	s, initial := m01CreateBoard(t, root)
	next, err := s.update(m01BoardID, initial.Board.Revision, m01Rename)
	if err != nil {
		t.Fatal(err)
	}
	if next.Board.entityTag() == "" || next.Board.entityTag() == initial.Board.entityTag() {
		t.Fatal("committed update did not advance the board version")
	}
	m01ProbeWriter(t, root, "")
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	m01ProbeWriter(t, root, next.Board.entityTag())
}

func m01ProbeWriter(t *testing.T, root, wantETag string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestM01WriterProbeProcess$")
	cmd.Env = append(os.Environ(), m01ProbeRootEnv+"="+root, m01ProbeETagEnv+"="+wantETag)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("writer probe failed: %v\n%s", err, output)
	}
}

func TestM01WriterProbeProcess(t *testing.T) {
	root := os.Getenv(m01ProbeRootEnv)
	if root == "" {
		t.Skip("run by the competing-writer test")
	}
	_, principals := m01Fixture()
	s, err := openStore(root, principals...)
	if s != nil {
		defer func() {
			if err := s.close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wantETag := os.Getenv(m01ProbeETagEnv)
	if wantETag == "" {
		if s != nil || !errors.Is(err, errStoreBusy) {
			t.Fatalf("expected writer conflict, got %v", err)
		}
		return
	}
	if err != nil || s == nil {
		t.Fatalf("reopen after writer release: %v", err)
	}
	got, ok := s.accepted(m01BoardID)
	if !ok || got.Board.Name != m01UpdatedName || got.Board.entityTag() != wantETag {
		t.Fatal("restarted writer did not recover the committed board and version")
	}
	if len(got.Activity) != 2 || got.Activity[1].Operation != "patch" {
		t.Fatal("restarted writer did not recover the committed activity")
	}
}

func TestM01FailedReplacementPreservesSnapshot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires a Windows destination handle without delete sharing")
	}
	root := t.TempDir()
	s, initial := m01CreateBoard(t, root)
	path := filepath.Join(root, "boards", m01BoardID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Go opens ordinary Windows file handles without FILE_SHARE_DELETE.
	// Keep the destination open to force replacement to fail after staging.
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := s.update(m01BoardID, initial.Board.Revision, m01Rename); err == nil ||
		errors.Is(err, errWriteUncertain) {
		t.Fatalf("expected a known replacement refusal, got %v", err)
	}
	got, ok := s.accepted(m01BoardID)
	if !ok || got.Board.Name != initial.Board.Name || got.Board.entityTag() != initial.Board.entityTag() ||
		len(got.Activity) != len(initial.Activity) {
		t.Fatal("failed replacement changed the accepted board or version")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed replacement changed the durable snapshot bytes")
	}
	if pending, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".pending-*.tmp")); err != nil || len(pending) != 0 {
		t.Fatalf("failed replacement left pending snapshots: count %d, error %v", len(pending), err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	// Once the known refusal is verified, the same precondition must still work.
	next, err := s.update(m01BoardID, initial.Board.Revision, m01Rename)
	if err != nil || next.Board.Name != m01UpdatedName || next.Board.entityTag() == initial.Board.entityTag() {
		t.Fatalf("update after releasing the handle did not commit: %v", err)
	}
}
