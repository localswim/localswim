//go:build nativecompilerqualification

package archive

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

// This is actual TS7 declaration checking, not browser/domain qualification.
func TestNativeDeclarations(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" || runtime.Version() != "go1.27.3" {
		t.Fatal("requires qualified Windows/amd64 Go 1.27.3")
	}
	node, tsc, work := os.Getenv("C03_NODE"), os.Getenv("C03_TSC"), os.Getenv("C03_WORK")
	if !filepath.IsAbs(node) || !filepath.IsAbs(tsc) || !filepath.IsAbs(work) {
		t.Fatal("absolute C03_NODE/C03_TSC/private C03_WORK required")
	}
	if _, e := os.Stat(work); !os.IsNotExist(e) {
		t.Fatal("C03_WORK must be new")
	}
	if e := os.MkdirAll(work, 0700); e != nil {
		t.Fatal(e)
	}
	run := func(args ...string) []byte {
		t.Helper()
		c := exec.Command(node, args...)
		c.Dir = work
		b, e := c.CombinedOutput()
		if e != nil {
			os.WriteFile(filepath.Join(work, "failure.log"), b, 0600)
			t.Fatalf("native tool failed: %v; details private", e)
		}
		return b
	}
	if strings.TrimSpace(string(run("--version"))) != "v24.20.0" || !strings.Contains(string(run(tsc, "--version")), "7.0.2") {
		t.Fatal("native pins")
	}
	first, e := contract.GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	second, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	for _, files := range []map[string][]byte{first, second} {
		for p, b := range files {
			if strings.HasPrefix(p, "web/") {
				target := filepath.Join(work, filepath.FromSlash(strings.TrimPrefix(p, "web/")))
				if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(target, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	var source strings.Builder
	source.WriteString("import type * as A from './generated/archive/contracts';\n")
	for _, item := range []struct{ Name, Type, Path string }{{"manifest", "Manifest", "package/manifest.json"}, {"board", "Board", "package/board.json"}, {"history", "History", "package/history.json"}, {"activity", "Activity", "package/activity.json"}, {"outcomes", "Outcomes", "package/outcomes.json"}, {"preview", "Preview", "preview-clear.json"}, {"receipt", "Receipt", "restore-receipt.json"}, {"status", "Status", "status-unknown.json"}} {
		source.WriteString("const " + item.Name + " = " + strings.TrimSuffix(string(read(t, item.Path)), "\n") + " satisfies A." + item.Type + ";\n")
	}
	source.WriteString("// @ts-expect-error required incarnation\nconst missing: A.Identity = {boardId: 'synthetic'};\n// @ts-expect-error version is the literal one\nconst version: A.Status = {apiVersion: 2,scope:'restore',outcome:'unknown',result:{sourceIdentity:null,sourceRevision:null,destination:null}};\n// @ts-expect-error complete required nullable field\nconst nullField: A.FactRef = {kind:'attachment',id:'synthetic'};\ndeclare const frozen: A.IntentGrantRevoke;\n// @ts-expect-error unknown retained intent key\nconst secret: A.IntentGrantRevoke = {...frozen, bearer:'secret'};\n")
	if e = os.WriteFile(filepath.Join(work, "check.ts"), []byte(source.String()), 0600); e != nil {
		t.Fatal(e)
	}
	config := `{"compilerOptions":{"strict":true,"exactOptionalPropertyTypes":true,"noEmitOnError":true,"target":"ES2022","module":"CommonJS"},"files":["check.ts"]}` + "\n"
	if e = os.WriteFile(filepath.Join(work, "tsconfig.json"), []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	log := run(tsc, "-p", filepath.Join(work, "tsconfig.json"), "--noEmit")
	if e = os.WriteFile(filepath.Join(work, "check.log"), log, 0600); e != nil {
		t.Fatal(e)
	}
}
