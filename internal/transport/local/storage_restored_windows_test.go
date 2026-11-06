//go:build windows && amd64

package local

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
)

func TestStorageRestoredPublicationLockIsValidatedAndPreserved(t *testing.T) {
	root := filepath.Join(testRoot(t), "restored-storage")
	if err := privateDir(root); err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Join(root, "source")
	if err := privateDir(sourceDir); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "archive.zip")
	if err := publish(source, localRestoreZIP(t), false); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination")
	result := Restore(context.Background(), RestoreOptions{SourceArchive: source, DataDir: destination, RequestID: A.UUID(uuid()), ProtectedRoots: []string{sourceDir}})
	if result.Exit != 0 {
		t.Fatal("restore fixture unavailable")
	}
	raw, err := readPrivate(filepath.Join(destination, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var id identity
	if decode(raw, &id) != nil {
		t.Fatal("identity")
	}
	expected := schema.Identity{BoardID: id.BoardID, Incarnation: id.Incarnation}
	before := restoreHashes(t, destination)
	inspect, changed, err := Storage(context.Background(), destination, expected, false)
	if err != nil || changed || inspect.PhysicalVersion != schema.Version {
		t.Fatal("restored inspect", err)
	}
	if !reflect.DeepEqual(before, restoreHashes(t, destination)) {
		t.Fatal("inspect changed restored root")
	}
	lock, err := workspaceExistingRestoreLock(destination)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = Storage(context.Background(), destination, expected, false); err == nil {
		t.Fatal("held publication exclusion admitted")
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(destination, "restore.lock")
	if err = os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Storage(context.Background(), destination, expected, false); err == nil {
		t.Fatal("missing publication lock admitted")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing lock created")
	}
	if err = os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Storage(context.Background(), destination, expected, false); err == nil {
		t.Fatal("nonempty publication lock admitted")
	}
}
