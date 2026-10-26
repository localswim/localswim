package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	if e := run(root, false); e != nil {
		t.Fatal(e)
	}
	if e := run(root, true); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "web", "generated", "contracts.d.ts")
	if e := os.WriteFile(path, []byte("drift\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := run(root, true); e == nil {
		t.Fatal("drift passed")
	}
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "drift\n" {
		t.Fatal("check rewrote output")
	}
}
