// assetstage verifies local CLI output and stages only declared public assets.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

var production = []string{
	"app", "api/boundary", "api/client", "api/local-session", "api/schema-data", "api/w03-session", "archive-ui/view", "board/view", "recovery/indexeddb", "recovery/local-readiness", "recovery/store", "recovery/types", "recovery-ui/admin", "recovery-ui/coordination", "recovery-ui/view", "session-ui/local-view", "session-ui/view", "session-ui/workspace",
}
var static = []string{"index.html", "styles/app.css"}
var bindings = []string{
	"web/tsconfig.json", "web/package.json", "web/package-lock.json", "web/generated/contracts.d.ts", "web/generated/archive/contracts.d.ts", "web/generated/transport/contracts.d.ts", "cmd/assetstage/main.go", "internal/assets/assets.go",
	"cmd/contractgen/main.go", "internal/contract/descriptor.go", "internal/contract/generate.go", "internal/contract/archive/generate.go", "internal/contract/transport/generate.go", "internal/contract/transport/session_code_generate.go",
	"go.mod", "go.sum", "schemas/mutation-requests.schema.json", "schemas/archive/contracts.schema.json", "schemas/transport/contracts.schema.json",
	"internal/contract/input.go", "internal/contract/intent.go", "internal/contract/json.go", "internal/contract/result.go",
	"internal/contract/archive/types.go", "internal/contract/archive/semantic.go", "internal/contract/archive/references.go", "internal/contract/archive/decode.go",
	"internal/contract/transport/types.go", "internal/contract/transport/mutation.go", "internal/contract/transport/encode.go", "internal/contract/transport/decode.go", "internal/contract/transport/references.go", "internal/contract/transport/session_code_cli.go",
}

type manifest struct {
	Version  int               `json:"version"`
	Compiler string            `json:"compiler"`
	Inputs   map[string]string `json:"inputs"`
	Files    map[string]string `json:"files"`
}

func main() {
	check := flag.Bool("check", false, "regenerate in temporary output and compare checked-in assets")
	node := flag.String("node", "node", "Node executable for the locked local TypeScript CLI")
	modules := flag.String("modules", "web/node_modules", "installed locked local modules directory")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "assetstage: unexpected arguments")
		os.Exit(1)
	}
	if err := run(".", *node, *modules, *check); err != nil {
		fmt.Fprintln(os.Stderr, "assetstage:", err)
		os.Exit(1)
	}
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Every ancestor is inspected: a declared filename must not escape through a
// symlink/junction, even when its final component is an ordinary file.
func regular(name string) error {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	for p := absolute; ; p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if s.Mode()&os.ModeSymlink != 0 || s.Mode()&os.ModeIrregular != 0 {
			return errors.New("symlink or irregular input")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	s, err := os.Stat(absolute)
	if err != nil {
		return err
	}
	if !s.Mode().IsRegular() {
		return errors.New("nonregular input")
	}
	return nil
}
func read(name string, text bool) ([]byte, error) {
	if err := regular(name); err != nil {
		return nil, err
	}
	s, err := os.Stat(name)
	if err != nil || s.Size() > 64*1024*1024 {
		return nil, errors.New("input missing or too large")
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if text && (!utf8.Valid(b) || bytes.Contains(b, []byte{13}) || bytes.HasPrefix(b, []byte{239, 187, 191}) || len(b) == 0 || b[len(b)-1] != 10 || len(b) > 1 && b[len(b)-2] == 10) {
		return nil, errors.New("public text must be UTF-8 LF with one final newline")
	}
	return b, nil
}
func inventory(root string) (map[string][]byte, error) {
	result := map[string][]byte{}
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in staging tree")
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		b, err := read(name, true)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(rel)] = b
		return nil
	})
	return result, err
}
func equalInventory(actual, expected map[string][]byte) error {
	if len(actual) != len(expected) {
		return errors.New("missing or extra staged files")
	}
	for n, b := range expected {
		a, ok := actual[n]
		if !ok || !bytes.Equal(a, b) {
			return fmt.Errorf("missing or stale asset %s", n)
		}
	}
	return nil
}
func compile(node, cli, config, output string) error {
	cmd := exec.Command(node, cli, "-p", config, "--outDir", output)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("local TypeScript CLI failed: %s", strings.TrimSpace(string(b)))
	}
	return nil
}
func run(root, node, modules string, check bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	modules, err = filepath.Abs(modules)
	if err != nil {
		return err
	}
	m := manifest{Version: 1, Compiler: "7.0.2", Inputs: map[string]string{}, Files: map[string]string{}}
	expected := map[string][]byte{}
	jsExpected := map[string][]byte{}
	for _, n := range bindings {
		b, e := read(filepath.Join(root, filepath.FromSlash(n)), true)
		if e != nil {
			return fmt.Errorf("declared input %s: %w", n, e)
		}
		m.Inputs[n] = hash(b)
	}
	for _, n := range production {
		p := "web/src/" + n + ".ts"
		b, e := read(filepath.Join(root, filepath.FromSlash(p)), true)
		if e != nil {
			return fmt.Errorf("declared source %s: %w", p, e)
		}
		m.Inputs[p] = hash(b)
	}
	for _, n := range static {
		b, e := read(filepath.Join(root, "web", filepath.FromSlash(n)), true)
		if e != nil {
			return e
		}
		expected[n] = b
		m.Inputs["web/"+n] = hash(b)
	}
	// Freeze the CLI, its native executable and library inputs; no compiler API.
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	for _, dir := range []string{"typescript", "@typescript/typescript-" + platform + "-" + arch()} {
		tree := filepath.Join(modules, filepath.FromSlash(dir))
		e := filepath.WalkDir(tree, func(n string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("compiler symlink")
			}
			if d.IsDir() {
				return nil
			}
			b, e := read(n, false)
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(modules, n)
			if e != nil {
				return e
			}
			m.Inputs["compiler/"+filepath.ToSlash(rel)] = hash(b)
			return nil
		})
		if e != nil {
			return fmt.Errorf("locked compiler unavailable: %w", e)
		}
	}
	cli := filepath.Join(modules, "typescript", "bin", "tsc")
	cmd := exec.Command(node, cli, "--version")
	version, e := cmd.CombinedOutput()
	if e != nil || strings.TrimSpace(string(version)) != "Version 7.0.2" {
		return errors.New("requires locked local TypeScript 7.0.2 CLI")
	}
	temporary, e := os.MkdirTemp("", "localswim-assetstage-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temporary)
	output := filepath.Join(temporary, "js")
	if e = compile(node, cli, filepath.Join(root, "web", "tsconfig.json"), output); e != nil {
		return e
	}
	fresh, e := inventory(output)
	if e != nil {
		return e
	}
	for _, n := range production {
		b, ok := fresh[n+".js"]
		if !ok {
			return fmt.Errorf("missing emitted module %s", n)
		}
		jsExpected[n+".js"] = b
		expected["js/"+n+".js"] = b
	}
	if e = equalInventory(fresh, jsExpected); e != nil {
		return fmt.Errorf("undeclared compiler output: %w", e)
	}
	staged, e := inventory(filepath.Join(root, "web", "dist", "js"))
	if e != nil {
		return fmt.Errorf("run local web build first: %w", e)
	}
	if e = equalInventory(staged, jsExpected); e != nil {
		return fmt.Errorf("web build: %w", e)
	}
	// Refuse changes observed while the compiler was running. The manifest must
	// describe the inputs used for this comparison, rather than an earlier read.
	for name, expectedHash := range m.Inputs {
		input := filepath.Join(root, filepath.FromSlash(name))
		compiler := strings.HasPrefix(name, "compiler/")
		if compiler {
			input = filepath.Join(modules, filepath.FromSlash(strings.TrimPrefix(name, "compiler/")))
		}
		b, e := read(input, !compiler)
		if e != nil || hash(b) != expectedHash {
			return fmt.Errorf("input changed during staging: %s", name)
		}
	}
	for n, b := range expected {
		m.Files[n] = hash(b)
	}
	encoded, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	expected["manifest.json"] = append(encoded, 10)
	destination := filepath.Join(root, "internal", "assets", "bundle")
	actual, e := inventory(destination)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if check {
		return equalInventory(actual, expected)
	}
	for n := range actual {
		if _, ok := expected[n]; !ok {
			return errors.New("unexpected file in embed staging tree")
		}
	}
	names := make([]string, 0, len(expected))
	for n := range expected {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := filepath.Join(destination, filepath.FromSlash(n))
		if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return e
		}
		if _, e = os.Lstat(p); e == nil {
			if e = regular(p); e != nil {
				return e
			}
		}
		if e = os.WriteFile(p, expected[n], 0644); e != nil {
			return e
		}
	}
	return nil
}
func arch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}
