//go:build nativequalification && windows && amd64

package archivequalification

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	L "github.com/localswim/localswim/internal/transport/local"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testRefusals(t *testing.T, ctx context.Context, attempt, archive, source string) {
	t.Helper()
	check := func(t *testing.T, src, dst, want string) {
		t.Helper()
		before := hashTree(t, attempt)
		q := L.RestoreOptions{SourceArchive: src, DataDir: dst, RequestID: id(), ProtectedRoots: []string{source}, Port: port(t)}
		got := L.Restore(ctx, q)
		wantExit := 4
		if want == "failed" {
			wantExit = 1
		}
		if got.Exit != wantExit || string(got.Status.Outcome) != want {
			t.Fatal("refusal did not take its required validation branch")
		}
		if !reflect.DeepEqual(before, hashTree(t, attempt)) {
			t.Fatal("refused preflight changed files")
		}
	}
	inputDir := filepath.Dir(archive)
	// These bad-input variants must reach source opening/ZIP validation, not
	// the earlier rule rejecting destinations inside the source's parent tree.
	badInput := func(t *testing.T, src, dst, want string) {
		t.Helper()
		parent := strings.ToLower(filepath.Dir(src))
		destination := strings.ToLower(dst)
		if parent == destination || strings.HasPrefix(destination, parent+`\`) || strings.HasPrefix(parent, destination+`\`) {
			t.Fatal("bad-input fixture overlaps source parent")
		}
		check(t, src, dst, want)
		proof, _ := json.Marshal(map[string]any{"sourceParentDisjoint": true, "outcome": want, "exit": map[string]int{"failed": 1, "unknown": 4}[want], "filesUnchanged": true})
		save(t, filepath.Join(attempt, filepath.Base(dst)+"-refusal.json"), proof)
	}
	t.Run("missing", func(t *testing.T) {
		missing := filepath.Join(inputDir, "absent.zip")
		if _, e := os.Stat(missing); !os.IsNotExist(e) {
			t.Fatal("missing-input fixture exists")
		}
		badInput(t, missing, filepath.Join(attempt, "missing"), "unknown")
	})
	t.Run("corrupt", func(t *testing.T) {
		p := filepath.Join(inputDir, "corrupt.zip")
		raw, e := os.ReadFile(archive)
		if e != nil {
			t.Fatal(e)
		}
		at := bytes.Index(raw, []byte{0, 255, 1, 'V', '0', '3', '\n'})
		if at < 0 {
			t.Fatal("exact BLOB payload unavailable for corruption")
		}
		raw[at] ^= 255
		save(t, p, raw)
		badInput(t, p, filepath.Join(attempt, "corrupt"), "failed")
	})
	t.Run("unsafe-member", func(t *testing.T) {
		raw, e := os.ReadFile(archive)
		if e != nil {
			t.Fatal(e)
		}
		original, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if e != nil {
			t.Fatal(e)
		}
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for _, f := range original.File {
			reader, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			data, e := io.ReadAll(reader)
			reader.Close()
			if e != nil {
				t.Fatal(e)
			}
			h := f.FileHeader
			if h.Name == "board.json" {
				h.Name = "../bad.bin"
			}
			w, e := z.CreateRaw(&h)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = w.Write(data); e != nil {
				t.Fatal(e)
			}
		}
		if e = z.Close(); e != nil {
			t.Fatal(e)
		}
		p := filepath.Join(inputDir, "unsafe.zip")
		save(t, p, b.Bytes())
		badInput(t, p, filepath.Join(attempt, "unsafe"), "failed")
	})
	t.Run("existing-directory", func(t *testing.T) {
		p := filepath.Join(attempt, "exists")
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
		save(t, filepath.Join(p, "keep"), []byte("preserve existing destination"))
		check(t, archive, p, "unknown")
	})
	t.Run("existing-file", func(t *testing.T) {
		p := filepath.Join(attempt, "exists-file")
		save(t, p, []byte("preserve file"))
		check(t, archive, p, "unknown")
	})
	t.Run("protected-source", func(t *testing.T) { check(t, archive, filepath.Join(source, "nested"), "unknown") })
	t.Run("unsafe-path", func(t *testing.T) { check(t, archive, filepath.Join(attempt, "reserved")+":stream", "unknown") })
}
