package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/localswim/localswim/internal/contract/archive"
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

func TestArchiveBundleAndNonwritingCheck(t *testing.T) {
	root := t.TempDir()
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	want, err := archive.GeneratedFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"schemas/archive/contracts.schema.json", "web/generated/archive/contracts.d.ts"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(name))
			got, err := os.ReadFile(path)
			if err != nil || len(want[name]) == 0 || !bytes.Equal(got, want[name]) {
				t.Fatal("aggregate archive output missing or different")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := run(root, true); err == nil {
				t.Fatal("missing output passed check")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("check recreated missing output")
			}
			if err := os.WriteFile(path, []byte("drift\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := run(root, true); err == nil {
				t.Fatal("archive drift passed check")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "drift\n" {
				t.Fatal("check rewrote archive output")
			}
			if err := run(root, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
