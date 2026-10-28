//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"
)

func workspaceFilesTest(t *testing.T) (*WorkspaceFiles, string) {
	t.Helper()
	root := filepath.Join(testRoot(t), "ws")
	f, e := OpenWorkspaceFiles(root, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := f.Close(); e != nil {
			t.Error(e)
		}
	})
	return f, root
}

func TestWorkspaceFilesOwnershipBoundedCAS(t *testing.T) {
	f, root := workspaceFilesTest(t)
	if other, e := OpenWorkspaceFiles(root, false); !errors.Is(e, ErrWorkspaceBusy) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("second owner: %v", e)
	}
	if _, e := f.Read("workspace.json", 1<<20); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("missing config must remain absent")
	}
	if e := f.CompareAndSwap("workspace.json", nil, []byte("initial"), 1<<20); e != nil {
		t.Fatal(e)
	}
	if e := f.CompareAndSwap("workspace.json", nil, []byte("overwrite"), 1<<20); !errors.Is(e, ErrWorkspaceChanged) {
		t.Fatal("create CAS overwrote")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := f.CompareAndSwap("workspace.json", []byte("initial"), []byte("winner"), 1<<20)
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, ErrWorkspaceChanged) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("CAS winners %d", wins.Load())
	}
	if b, e := ReadWorkspaceFile(root, "workspace.json", 1<<20); e != nil || string(b) != "winner" {
		t.Fatal("live private read failed", e)
	}
	if e := f.CompareAndSwap("workspace-preferences.json", nil, bytes.Repeat([]byte{'p'}, 64<<10+1), 64<<10); e == nil {
		t.Fatal("oversize publication accepted")
	}
	if e := f.CompareAndSwap("../escape.json", nil, []byte("bad"), 1<<20); e == nil {
		t.Fatal("escape accepted")
	}
	if e := f.CompareAndSwap("workspace.json", []byte("winner"), bytes.Repeat([]byte{'c'}, 1<<20), 1<<20); e != nil {
		t.Fatal(e)
	}
	if _, e := f.Read("workspace.json", 1<<20-1); e == nil {
		t.Fatal("oversize read accepted")
	}
	if b, e := f.Read("workspace.json", 1<<20); e != nil || len(b) != 1<<20 {
		t.Fatal("boundary read failed", e)
	}
	if e := os.Rename(root, root+"-moved"); e == nil {
		t.Fatal("owned root renamed")
	}
	if e := f.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := f.Read("workspace.json", 1<<20); e == nil {
		t.Fatal("closed owner read")
	}
	other, e := OpenWorkspaceFiles(root, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = other.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestWorkspaceFilesAtomicFailureAndEpochRemoval(t *testing.T) {
	f, root := workspaceFilesTest(t)
	old := []byte("old epoch")
	if e := f.CompareAndSwap("workspace-service.json", nil, old, 64<<10); e != nil {
		t.Fatal(e)
	}
	if e := f.RemoveIf("workspace-service.json", []byte("foreign epoch"), 64<<10); !errors.Is(e, ErrWorkspaceChanged) {
		t.Fatal("foreign epoch removed")
	}
	block, e := workspaceOpen(filepath.Join(root, "workspace-service.json"), windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_SHARE_READ)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.CompareAndSwap("workspace-service.json", old, []byte("new epoch"), 64<<10); e == nil {
		t.Fatal("delete-sharing publication unexpectedly succeeded")
	}
	if e = block.Close(); e != nil {
		t.Fatal(e)
	}
	if b, e := f.Read("workspace-service.json", 64<<10); e != nil || !bytes.Equal(b, old) {
		t.Fatal("failed publication replaced old bytes")
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".pending") {
			t.Fatal("failed publication left pending bytes")
		}
	}
	if e = f.RemoveIf("workspace-service.json", old, 64<<10); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Read("workspace-service.json", 64<<10); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("own epoch not removed")
	}
}

func TestWorkspaceFilesCloseFailureRetainsExclusion(t *testing.T) {
	f, root := workspaceFilesTest(t)
	original := f.root.close
	first := true
	f.root.close = func() error {
		if first {
			first = false
			return ErrWorkspaceFiles
		}
		return original()
	}
	if e := f.Close(); e == nil {
		t.Fatal("failed close claimed release")
	}
	if f.closed || f.lock == nil {
		t.Fatal("failed close lost owner")
	}
	if other, e := OpenWorkspaceFiles(root, false); !errors.Is(e, ErrWorkspaceBusy) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("failed cleanup released exclusion")
	}
	if e := f.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestWorkspaceFilesPathAndPhysicalAliasGuards(t *testing.T) {
	base := testRoot(t)
	project := filepath.Join(base, "project")
	p, e := OpenProjectRoot(project, true)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	member := filepath.Join(project, "member")
	m, e := OpenMemberRoot(project, member, true)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	alias, e := OpenMemberRoot(strings.ToUpper(project), strings.ToUpper(member), false)
	if e != nil {
		t.Fatal(e)
	}
	if alias.PhysicalKey() != m.PhysicalKey() || alias.Path() != m.Path() {
		t.Fatal("case aliases did not canonicalize")
	}
	_ = alias.Close()
	if existing, e := OpenMemberRoot(project, member, true); e == nil {
		_ = existing.Close()
		t.Fatal("create adopted existing root")
	}
	for _, path := range []string{project, project + "-sibling", filepath.Join(base, "outside"), project + `\..\escape`, project + `\NUL`, project + `\COM1.txt`, project + `\LPT¹`, project + `\data:stream`, `\\server\share\data`, `\\?\C:\data`, project + `\short~1`, project + `\trailing.`} {
		if bad, e := OpenMemberRoot(project, path, true); e == nil {
			_ = bad.Close()
			t.Errorf("unsupported path accepted: %q", path)
		}
	}
	if _, e := os.Lstat(filepath.Join(base, "outside")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("invalid root created")
	}
	missing := filepath.Join(project, "missing")
	if g, e := OpenMemberRoot(project, missing, false); !errors.Is(e, ErrWorkspaceMissing) {
		if g != nil {
			_ = g.Close()
		}
		t.Fatal("missing root classification", e)
	}
	if _, e := os.Lstat(missing); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("existing-only check created root")
	}
}

func TestWorkspaceFilesCaseSensitiveAncestorPhysicalBoundary(t *testing.T) {
	base := testRoot(t)
	if e := exec.Command("fsutil.exe", "file", "setCaseSensitiveInfo", base, "enable").Run(); e != nil {
		t.Fatal("case-sensitive native fixture unavailable", e)
	}
	project := filepath.Join(base, "project")
	p, e := OpenProjectRoot(project, true)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	other := filepath.Join(base, "PROJECT")
	q, e := OpenProjectRoot(other, true)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if p.PhysicalKey() == q.PhysicalKey() {
		t.Fatal("case-sensitive fixture did not create distinct physical roots")
	}
	outside := filepath.Join(other, "board")
	member, e := OpenMemberRoot(other, outside, true)
	if e != nil {
		t.Fatal(e)
	}
	defer member.Close()
	if escaped, e := OpenMemberRoot(project, outside, false); e == nil {
		_ = escaped.Close()
		t.Fatal("case-sensitive sibling accepted as physical descendant")
	}
	missing := filepath.Join(other, "missing")
	if escaped, e := OpenMemberRoot(project, missing, true); e == nil {
		_ = escaped.Close()
		t.Fatal("case-sensitive sibling accepted for creation")
	}
	if _, e := os.Lstat(missing); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("rejected physical escape created a leaf")
	}
}

type workspaceCloseOnce struct {
	calls  int
	closer io.Closer
}

func (c *workspaceCloseOnce) Error() string { return "synthetic subordinate cleanup failure" }
func (c *workspaceCloseOnce) Close() error {
	c.calls++
	if c.calls == 1 {
		return ErrWorkspaceFiles
	}
	return c.closer.Close()
}
func TestWorkspaceFilesInspectCleanupCapabilityRetainsOwnership(t *testing.T) {
	root := filepath.Join(testRoot(t), "board")
	r, e := OpenEngine(context.Background(), EngineOptions{DataDir: root, WorkspaceID: uuid(), Origin: "http://127.0.0.1:18411"})
	if e != nil {
		t.Fatal(e)
	}
	want := r.Identity()
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	subordinate := &workspaceCloseOnce{}
	_, e = inspectWorkspaceBoard(context.Background(), root, &want, func(f *os.File) io.Closer {
		subordinate.closer = f
		return subordinate
	})
	var pending WorkspaceCleanup
	if !errors.As(e, &pending) {
		t.Fatal("cleanup capability lost")
	}
	for _, formatted := range []string{fmt.Sprint(e), fmt.Sprintf("%#v", e)} {
		if strings.Contains(formatted, root) || strings.Contains(formatted, "synthetic") {
			t.Fatal("cleanup formatting leaked private detail")
		}
	}
	if _, e := InspectBoard(context.Background(), root, &want); !errors.Is(e, ErrWorkspaceBusy) {
		t.Fatal("failed subordinate cleanup released root exclusion", e)
	}
	if e = pending.Close(); e != nil {
		t.Fatal(e)
	}
	if subordinate.calls != 2 {
		t.Fatal("subordinate close was not retried")
	}
	if _, e := InspectBoard(context.Background(), root, &want); e != nil {
		t.Fatal("retry did not release root", e)
	}
}

func TestWorkspaceFilesReparseAndHardlinkRefused(t *testing.T) {
	f, root := workspaceFilesTest(t)
	if e := f.CompareAndSwap("workspace.json", nil, []byte("config"), 1<<20); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "alias")
	if e := os.Link(filepath.Join(root, "workspace.json"), link); e != nil {
		t.Fatal(e)
	}
	if _, e := f.Read("workspace.json", 1<<20); e == nil {
		t.Fatal("hardlinked private file accepted")
	}
	if e := os.Remove(link); e != nil {
		t.Fatal(e)
	}
	project := filepath.Join(testRoot(t), "project")
	p, e := OpenProjectRoot(project, true)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	symlink := filepath.Join(project, "alias")
	if e := os.Symlink(root, symlink); e != nil {
		t.Skip("native symlink creation unavailable")
	}
	defer os.Remove(symlink)
	if g, e := OpenMemberRoot(project, symlink, false); e == nil {
		_ = g.Close()
		t.Fatal("reparse leaf accepted")
	}
	if g, e := OpenMemberRoot(project, filepath.Join(symlink, "nested"), true); e == nil {
		_ = g.Close()
		t.Fatal("reparse ancestor accepted")
	}
}

func workspaceInventory(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if d.IsDir() {
			out[rel] = [32]byte{}
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		out[rel] = sha256.Sum256(b)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestWorkspaceFilesInspectBoardReadOnlyIdentity(t *testing.T) {
	root := filepath.Join(testRoot(t), "board")
	r, e := OpenEngine(context.Background(), EngineOptions{DataDir: root, WorkspaceID: uuid(), Origin: "http://127.0.0.1:18411"})
	if e != nil {
		t.Fatal(e)
	}
	want := r.Identity()
	if _, e := InspectBoard(context.Background(), root, nil); !errors.Is(e, ErrWorkspaceBusy) {
		t.Fatal("busy board inspection", e)
	}
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	before := workspaceInventory(t, root)
	got, e := InspectBoard(context.Background(), root, &want)
	if e != nil || got != want {
		t.Fatal("existing initialized inspection", e)
	}
	after := workspaceInventory(t, root)
	if len(after) != len(before) {
		t.Fatal("inspection changed inventory")
	}
	for name, sum := range before {
		if after[name] != sum {
			t.Fatal("inspection changed bytes", name)
		}
	}
	wrong := want
	wrong.Incarnation = uuid()
	if _, e := InspectBoard(context.Background(), root, &wrong); !errors.Is(e, ErrWorkspaceIdentityConflict) {
		t.Fatal("wrong tuple accepted", e)
	}
	if e := os.Remove(filepath.Join(root, "local.lock")); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectBoard(context.Background(), root, nil); !errors.Is(e, ErrWorkspaceMissing) {
		t.Fatal("missing exclusion accepted", e)
	}
	if _, e := os.Lstat(filepath.Join(root, "local.lock")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("inspection initialized exclusion")
	}
	missing := filepath.Join(testRoot(t), "missing")
	if _, e := InspectBoard(context.Background(), missing, nil); !errors.Is(e, ErrWorkspaceMissing) {
		t.Fatal("missing inspection", e)
	}
	if _, e := os.Lstat(missing); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("inspection created missing root")
	}
}
