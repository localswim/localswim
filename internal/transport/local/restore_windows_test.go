//go:build windows && amd64

package local

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func localRestoreZIP(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("../../..", "fixtures/contracts/archive-recovery/package")
	names := []string{"manifest.json", "board.json", "history.json", "activity.json", "outcomes.json"}
	blobs, e := filepath.Glob(filepath.Join(root, "blobs", "*"))
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range blobs {
		names = append(names, "blobs/"+filepath.Base(b))
	}
	names = append(names, "COMPLETE")
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, name := range names {
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if e != nil {
			t.Fatal(e)
		}
		w, e := z.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, ReaderVersion: 20, CreatorVersion: 20, CRC32: crc32.ChecksumIEEE(b), CompressedSize64: uint64(len(b)), UncompressedSize64: uint64(len(b))})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func restoreHashes(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			out[p] = sha256.Sum256(b)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestLocalRestoreUsableAndStatusReadOnly(t *testing.T) {
	root := filepath.Join(testRoot(t), "restore")
	if e := privateDir(root); e != nil {
		t.Fatal(e)
	}
	sourceDir := filepath.Join(root, "source")
	if e := privateDir(sourceDir); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(sourceDir, "archive.zip")
	if e := publish(source, localRestoreZIP(t), false); e != nil {
		t.Fatal(e)
	}
	data := filepath.Join(root, "destination")
	request := A.UUID(uuid())
	sourceBefore, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	r := Restore(context.Background(), RestoreOptions{SourceArchive: source, DataDir: data, RequestID: request, ProtectedRoots: []string{sourceDir}, Port: freePort(t)})
	if r.Exit != 0 || r.Status.Outcome != "confirmed" {
		t.Fatalf("restore: %s", r.Status.Outcome)
	}
	before := restoreHashes(t, data)
	s := RestoreStatus(context.Background(), data, request)
	if s.Exit != 0 || !reflect.DeepEqual(s.Status, r.Status) {
		t.Fatal("complete offline status unavailable")
	}
	if !reflect.DeepEqual(before, restoreHashes(t, data)) {
		t.Fatal("status changed files")
	}
	runtime, e := Start(context.Background(), Options{DataDir: data})
	if e != nil {
		t.Fatal("restored service", e)
	}
	if e = runtime.Close(); e != nil {
		t.Fatal(e)
	}
	if RestoreStatus(context.Background(), data, request).Exit != 0 {
		t.Fatal("status rejected activated restore")
	}

	for _, name := range []string{"identity.json", "local-restore.json", "restore-receipt.json"} {
		path := filepath.Join(data, name)
		original, e := readPrivateBounded(path, 16*1024*1024)
		if e != nil {
			t.Fatal(e)
		}
		if publish(path, []byte(`{}`), true) != nil {
			t.Fatal("tamper fixture")
		}
		before := restoreHashes(t, data)
		if RestoreStatus(context.Background(), data, request).Status.Outcome != "unknown" {
			t.Fatalf("tampered %s confirmed", name)
		}
		if !reflect.DeepEqual(before, restoreHashes(t, data)) {
			t.Fatal("tamper status changed files")
		}
		if _, e := Start(context.Background(), Options{DataDir: data}); e == nil {
			t.Fatalf("tampered %s served", name)
		}
		if publish(path, original, true) != nil {
			t.Fatal("restore synthetic metadata")
		}
	}
	store, e := S.Existing(context.Background(), filepath.Join(data, "data"), schema.Identity{BoardID: runtime.id.BoardID, Incarnation: runtime.id.Incarnation})
	if e != nil {
		t.Fatal(e)
	}
	lease, e := store.Writer(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	e = lease.Do(context.Background(), func(c *sql.Conn) error {
		_, e := c.ExecContext(context.Background(), "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", runtime.id.Incarnation, runtime.id.BotGrant)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = lease.Close(); e != nil {
		t.Fatal(e)
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	before = restoreHashes(t, data)
	if RestoreStatus(context.Background(), data, request).Status.Outcome != "unknown" {
		t.Fatal("disabled local grant confirmed")
	}
	if !reflect.DeepEqual(before, restoreHashes(t, data)) {
		t.Fatal("grant status changed files")
	}
	after, e := os.ReadFile(source)
	if e != nil || !bytes.Equal(sourceBefore, after) {
		t.Fatal("source changed")
	}
	if Restore(context.Background(), RestoreOptions{SourceArchive: source, DataDir: data, RequestID: A.UUID(uuid()), ProtectedRoots: []string{sourceDir}}).Status.Outcome != "unknown" {
		t.Fatal("existing destination adopted")
	}
	if e = os.Remove(filepath.Join(data, "local.lock")); e != nil {
		t.Fatal(e)
	}
	before = restoreHashes(t, data)
	if RestoreStatus(context.Background(), data, request).Status.Outcome != "unknown" {
		t.Fatal("missing lock confirmed")
	}
	if !reflect.DeepEqual(before, restoreHashes(t, data)) {
		t.Fatal("status recreated missing lock")
	}
}
func TestLocalRestoreRejectsUnmarkedPublication(t *testing.T) {
	root := filepath.Join(testRoot(t), "partial")
	if e := privateDir(root); e != nil {
		t.Fatal(e)
	}
	if e := privateDir(filepath.Join(root, "data")); e != nil {
		t.Fatal(e)
	}
	if _, e := Start(context.Background(), Options{DataDir: root}); e == nil {
		t.Fatal("adopted unmarked publication")
	}
	if Restore(context.Background(), RestoreOptions{DataDir: root, RequestID: A.UUID(uuid())}).Status.Outcome != "unknown" {
		t.Fatal("missing protected roots accepted")
	}
}
