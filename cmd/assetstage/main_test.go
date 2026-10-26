package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDeclaredInventoryRefusesMissingExtraTamper(t *testing.T) {
	expected := map[string][]byte{"app.js": []byte("export {};\n")}
	for _, test := range []struct {
		name   string
		actual map[string][]byte
	}{
		{"missing", map[string][]byte{}}, {"private extra", map[string][]byte{"app.js": []byte("export {};\n"), "private.json": []byte("{}\n")}}, {"stale", map[string][]byte{"app.js": []byte("stale\n")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if equalInventory(test.actual, expected) == nil {
				t.Fatal("unsafe inventory accepted")
			}
		})
	}
}

func TestInputSymlinksAndText(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "public.ts")
	if err := os.WriteFile(target, []byte("export {};\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := read(target, true); err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{[]byte("x\r\n"), []byte("x\n\n"), []byte("x"), {0xff, 10}} {
		if err := os.WriteFile(target, b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := read(target, true); err == nil {
			t.Fatal("noncanonical text accepted")
		}
	}
}

func TestInputLinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "public.ts")
	if err := os.WriteFile(target, []byte("export {};\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.ts")
	if err := os.Symlink(target, link); err == nil {
		if _, err := read(link, true); err == nil {
			t.Fatal("symlink input accepted")
		}
		return
	}
	if runtime.GOOS != "windows" {
		t.Fatal("native symlink creation unavailable")
	}
	// Windows junctions exercise the same ancestor traversal without requiring
	// the optional symbolic-link creation privilege. Only a fresh temp link is made.
	directory := filepath.Join(root, "junction")
	if b, err := exec.Command("cmd", "/c", "mklink", "/J", directory, root).CombinedOutput(); err != nil {
		t.Fatalf("native junction creation failed: %s", b)
	}
	defer os.Remove(directory)
	if _, err := read(filepath.Join(directory, "public.ts"), true); err == nil {
		t.Fatal("junction ancestor accepted")
	}
	if _, err := inventory(directory); err == nil {
		t.Fatal("junction staging tree accepted")
	}
}
