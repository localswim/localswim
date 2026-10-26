//go:build nativecompilerqualification

package transport

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestSessionCodeNativeDeclarations(t *testing.T) {
	node, tsc, workRoot := os.Getenv("C02_NODE"), os.Getenv("C02_TSC"), os.Getenv("C02_WORK")
	if !filepath.IsAbs(node) || !filepath.IsAbs(tsc) || !filepath.IsAbs(workRoot) {
		t.Fatal("absolute pinned C02_NODE/C02_TSC/new private C02_WORK required")
	}
	work := workRoot + "-session-code"
	if _, e := os.Stat(work); !os.IsNotExist(e) {
		t.Fatal("session code declaration work must be new")
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
			t.Fatal("native declaration check failed; diagnostics private")
		}
		return b
	}
	if strings.TrimSpace(string(run("--version"))) != "v24.20.0" || !strings.Contains(string(run(tsc, "--version")), "7.0.2") {
		t.Fatal("native compiler pins")
	}
	for _, generate := range []func() (map[string][]byte, error){contract.GeneratedFiles, A.GeneratedFiles, GeneratedFiles} {
		files, e := generate()
		if e != nil {
			t.Fatal(e)
		}
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
	source.WriteString("import type * as T from './generated/transport/contracts';\n")
	for i, name := range []string{"presented", "unknown", "unavailable"} {
		fmt.Fprintf(&source, "const fixture%d = %s satisfies T.SessionCodeCLI;\n", i, strings.TrimSpace(string(sessionCodeFixture(t, name))))
	}
	literals, e := os.ReadFile("testdata/session-code-cli/literal-types.ts.txt")
	if e != nil {
		t.Fatal(e)
	}
	source.Write(literals)
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
