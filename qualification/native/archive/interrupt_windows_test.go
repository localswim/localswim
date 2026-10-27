//go:build nativequalification && windows && amd64

package archivequalification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	L "github.com/localswim/localswim/internal/transport/local"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testInterrupted(t *testing.T, ctx context.Context, attempt, source, protected string) {
	t.Helper()
	wd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	template := filepath.Join(wd, "publication-child.go.txt")
	raw, e := os.ReadFile(template)
	if e != nil {
		t.Fatal(e)
	}
	child := filepath.Join(attempt, "publication-child_test.go")
	save(t, child, raw)
	overlay := filepath.Join(attempt, "overlay.json")
	b, _ := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(repo, "internal/archive/publication/v03_qualification_test.go"): child}})
	save(t, overlay, b)
	goexe, e := exec.LookPath("go")
	if e != nil {
		t.Fatal("pinned Go unavailable")
	}
	// Missing overlay must not silently execute a zero-case command successfully.
	probe := exec.CommandContext(ctx, goexe, "test", "-list", "^TestV03PublicationInterrupt$", "./internal/archive/publication")
	probe.Dir = repo
	probeRaw, e := probe.CombinedOutput()
	save(t, filepath.Join(attempt, "missing-overlay.log"), probeRaw)
	if e != nil || bytes.Contains(probeRaw, []byte("TestV03PublicationInterrupt")) {
		t.Fatal("missing-overlay negative control invalid")
	}
	cmd := exec.CommandContext(ctx, goexe, "test", "-count=1", "-json", "-overlay="+overlay, "-run", "^TestV03PublicationInterrupt$", "./internal/archive/publication")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "V03_CHILD_ROOT="+attempt, "V03_CHILD_SOURCE="+source, "V03_CHILD_PROTECTED="+protected)
	output, e := cmd.CombinedOutput()
	save(t, filepath.Join(attempt, "interruption-child.jsonl"), output)
	if e != nil {
		t.Fatal("interruption child failed; inspect private evidence")
	}
	passed := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		var event struct{ Action, Test string }
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.Action == "skip" || event.Action == "fail" {
			t.Fatal("child skip or failure")
		}
		if event.Action == "pass" {
			passed[event.Test] = true
		}
	}
	for _, name := range []string{"TestV03PublicationInterrupt", "TestV03PublicationInterrupt/after-preparing", "TestV03PublicationInterrupt/before-rename", "TestV03PublicationInterrupt/after-rename"} {
		if !passed[name] {
			t.Fatal("required interruption case missing")
		}
	}
	before := hashTree(t, attempt)
	for j := 0; j < 3; j++ {
		request := A.UUID(fmt.Sprintf("10000000-0000-4000-8000-%012d", j+1))
		destination := filepath.Join(attempt, fmt.Sprint("int", j))
		for k := 0; k < 2; k++ {
			s := L.RestoreStatus(ctx, destination, request)
			if s.Exit != 4 || s.Status.Outcome != "unknown" {
				t.Fatal("normal local status incorrectly settled incomplete publication")
			}
		}
	}
	if !reflect.DeepEqual(before, hashTree(t, attempt)) {
		t.Fatal("normal local status changed interrupted files")
	}
	r, e := L.Start(ctx, L.Options{DataDir: filepath.Join(attempt, "int2"), Port: port(t)})
	if e == nil {
		r.Close()
		t.Fatal("normal startup adopted unmarked publication")
	}
}
