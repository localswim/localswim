package v2

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const workspaceBoardA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const workspaceBoardB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func workspacePinFields(ids ...string) map[string]json.RawMessage {
	data, _ := json.Marshal(ids)
	return map[string]json.RawMessage{"pinned_board_ids": data}
}

func TestWorkspacePinsPersistCanonicalOrder(t *testing.T) {
	root := t.TempDir()
	store := newWorkspace(root)
	initial, err := store.read()
	if err != nil || initial.Revision != 1 || initial.PinnedBoardIDs == nil || len(initial.PinnedBoardIDs) != 0 {
		t.Fatalf("initial workspace: %+v, %v", initial, err)
	}
	visible := func(id string) bool { return id == workspaceBoardA || id == workspaceBoardB }
	fields := workspacePinFields(strings.ToUpper(workspaceBoardB), workspaceBoardA)
	saved, err := store.patch(initial.ETag, fields, visible)
	if err != nil || saved.Revision != 2 || saved.ETag == initial.ETag || !slices.Equal(saved.PinnedBoardIDs, []string{workspaceBoardB, workspaceBoardA}) {
		t.Fatalf("saved workspace: %+v, %v", saved, err)
	}
	reopened := newWorkspace(root)
	read, err := reopened.read()
	if err != nil || read.ETag != saved.ETag || !slices.Equal(read.PinnedBoardIDs, saved.PinnedBoardIDs) {
		t.Fatalf("reopened workspace: %+v, %v", read, err)
	}
	noop, err := reopened.patch(`"unrelated", `+saved.ETag, fields, visible)
	if err != nil || noop.ETag != saved.ETag {
		t.Fatalf("no-op workspace: %+v, %v", noop, err)
	}
	if _, err := reopened.patch(initial.ETag, fields, visible); !errors.Is(err, errStale) {
		t.Fatalf("stale patch: %v", err)
	}
	for _, fields := range []map[string]json.RawMessage{
		workspacePinFields(workspaceBoardA, strings.ToUpper(workspaceBoardA)),
		workspacePinFields("cccccccc-cccc-4ccc-8ccc-cccccccccccc"),
		workspacePinFields(workspaceBoardA, "cccccccc-cccc-4ccc-8ccc-cccccccccccc"),
		{"pinned_board_ids": json.RawMessage("null")},
	} {
		if _, err := reopened.patch(`"unrelated", `+saved.ETag, fields, visible); err == nil {
			t.Fatal("accepted invalid pin list")
		}
	}
	read, err = reopened.read()
	if err != nil || read.ETag != saved.ETag || !slices.Equal(read.PinnedBoardIDs, saved.PinnedBoardIDs) {
		t.Fatalf("invalid patch changed workspace: %+v, %v", read, err)
	}
}

func TestWorkspaceRefusesToReplaceInvalidPreferences(t *testing.T) {
	root := t.TempDir()
	store := newWorkspace(root)
	initial, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, workspaceFileName)
	for _, invalid := range []string{
		"{",
		`{"revision":1,"revision":2,"pinned_board_ids":[]}`,
		`{"revision":1,"pinned_board_ids":null}`,
		`{"revision":1,"pinnedBoardIds":[]}`,
		`{"revision":true,"pinned_board_ids":[]}`,
		`{"revision":9007199254740992,"pinned_board_ids":[]}`,
		`{"revision":1,"pinned_board_ids":[],"publishing":null}`,
		`{"revision":1,"pinned_board_ids":[],"publishing":{"enabled":true,"pending":true}}`,
		`{"revision":1,"pinned_board_ids":[],"publishing":{"enabled":true,"repository":"other"}}`,
	} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.read(); !errors.Is(err, errWorkspaceUnavailable) {
			t.Fatalf("invalid read: %v", err)
		}
		if _, err := store.patch(initial.ETag, workspacePinFields(workspaceBoardA), func(string) bool { return true }); !errors.Is(err, errWorkspaceUnavailable) {
			t.Fatalf("patch over invalid preferences: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != invalid {
			t.Fatalf("invalid preferences changed: %v", err)
		}
	}
}

func TestWorkspaceRevisionLimit(t *testing.T) {
	store := newWorkspace(t.TempDir())
	data, err := json.Marshal(workspacePreferences{Revision: maxSnapshotInteger - 1, PinnedBoardIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	initial, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	visible := func(string) bool { return true }
	last, err := store.patch(initial.ETag, workspacePinFields(workspaceBoardA), visible)
	if err != nil || last.Revision != maxSnapshotInteger {
		t.Fatalf("last revision: %+v, %v", last, err)
	}
	if _, err := store.patch(last.ETag, workspacePinFields(workspaceBoardB), visible); !errors.Is(err, errWorkspaceUnavailable) {
		t.Fatalf("revision overflow accepted: %v", err)
	}
	read, err := store.read()
	if err != nil || read.Revision != last.Revision || !slices.Equal(read.PinnedBoardIDs, last.PinnedBoardIDs) {
		t.Fatalf("overflow changed workspace: %+v, %v", read, err)
	}
	if _, err := store.patch(last.ETag, workspacePinFields(workspaceBoardA), visible); err != nil {
		t.Fatalf("no-op at maximum revision refused: %v", err)
	}
}

func TestWorkspaceConcurrentPatchesUseOneRevision(t *testing.T) {
	store := newWorkspace(t.TempDir())
	initial, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{workspaceBoardA, workspaceBoardB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.patch(initial.ETag, workspacePinFields(id), func(string) bool { return true })
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var saved, stale int
	for err := range results {
		switch {
		case err == nil:
			saved++
		case errors.Is(err, errStale):
			stale++
		default:
			t.Fatal(err)
		}
	}
	if saved != 1 || stale != 1 {
		t.Fatalf("saved %d, stale %d", saved, stale)
	}
}
