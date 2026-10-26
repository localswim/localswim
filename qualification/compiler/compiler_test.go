//go:build nativecompilerqualification

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const vectors = `[{"label":"雪é😀","count":0},{"label":"x","count":9007199254740991,"note":null},{"label":"","count":1,"note":""},{"label":"x","count":1,"note":4},{"label":"x","count":1.5},{"label":"x","count":9007199254740992},{"label":null,"count":0},{"label":"x"},{"label":"x","count":-1},{"label":"x","count":0,"extra":true},null,[],{"label":"x","count":0,"note":null}]`

var expected = []bool{true, true, true, false, false, false, false, false, false, false, false, false, true}

const embeddedProgram = `package main
import("embed";"crypto/sha256";"encoding/hex";"encoding/json";"fmt";"os")
//go:embed assets/accepted.js assets/sample.schema.json assets/sample.d.ts
var assets embed.FS
//go:embed manifest.json
var manifest []byte
func main(){var hashes map[string]string;if json.Unmarshal(manifest,&hashes)!=nil{os.Exit(2)};if len(hashes)!=3{os.Exit(3)};for name,want:=range hashes{b,e:=assets.ReadFile("assets/"+name);if e!=nil{os.Exit(4)};h:=sha256.Sum256(b);if hex.EncodeToString(h[:])!=want{os.Exit(5)}};fmt.Println("verified synthetic assets")}
`

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func TestNativeCompilerHarness(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" || runtime.Version() != "go1.27.3" {
		t.Fatal("requires native Windows/amd64 Go1.27.3")
	}
	for k, w := range map[string]string{"GOTOOLCHAIN": "local", "CGO_ENABLED": "0", "GOOS": "windows", "GOARCH": "amd64", "GOFLAGS": "-mod=readonly"} {
		if os.Getenv(k) != w {
			t.Fatalf("wrong %s", k)
		}
	}
	need := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			t.Fatalf("%s required; mandatory suite cannot skip", k)
		}
		return v
	}
	node, npm, tsc, work := need("Q03_NODE"), need("Q03_NPM_CLI"), need("Q03_TSC"), need("Q03_WORK")
	if e := os.MkdirAll(work, 0700); e != nil {
		t.Fatal(e)
	}
	records := []map[string]any{}
	run := func(dir string, fail bool, bin string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		out, e := cmd.CombinedOutput()
		records = append(records, map[string]any{"dir": dir, "bin": bin, "args": args, "output": string(out), "failed": e != nil})
		if fail && filepath.Base(bin) == "stale.exe" {
			exit, ok := e.(*exec.ExitError)
			if !ok || exit.ExitCode() != 5 {
				t.Fatalf("stale asset did not fail with hash mismatch exit 5: %v", e)
			}
		}
		if fail {
			if e == nil {
				t.Fatalf("negative command unexpectedly succeeded: %s", bin)
			}
		} else if e != nil {
			t.Fatalf("command failed: %s: %v\n%s", bin, e, out)
		}
		return out
	}
	defer func() {
		b, _ := json.MarshalIndent(records, "", "  ")
		_ = os.WriteFile(filepath.Join(work, "commands.json"), append(b, '\n'), 0600)
	}()
	goBin, e := exec.LookPath("go")
	if e != nil {
		t.Fatal(e)
	}
	if strings.TrimSpace(string(run(work, false, node, "--version"))) != "v24.20.0" {
		t.Fatal("wrong Node")
	}
	if strings.TrimSpace(string(run(work, false, node, npm, "--version"))) != "12.0.2" {
		t.Fatal("wrong npm")
	}
	if strings.TrimSpace(string(run(work, false, node, tsc, "--version"))) != "Version 7.0.2" {
		t.Fatal("wrong TypeScript")
	}
	files, e := generate()
	if e != nil {
		t.Fatal(e)
	}
	again, e := generate()
	if e != nil {
		t.Fatal(e)
	}
	for name, b := range files {
		golden, err := os.ReadFile(filepath.Join("expected", name))
		if err != nil || !bytes.Equal(b, golden) {
			t.Fatalf("missing/stale generated fixture %s", name)
		}
		if !bytes.Equal(b, again[name]) {
			t.Fatal("generator nondeterminism")
		}
	}
	var schema map[string]any
	if json.Unmarshal(files["sample.schema.json"], &schema) != nil {
		t.Fatal("schema JSON")
	}
	if schema["additionalProperties"] != false {
		t.Fatal("schema admits unknowns")
	}
	props := schema["properties"].(map[string]any)
	count := props["count"].(map[string]any)
	if count["maximum"] != float64(9007199254740991) || count["minimum"] != float64(0) {
		t.Fatal("schema integer bounds")
	}
	if string(files["sample.d.ts"]) != "// Generated synthetic sample; no application contract.\nexport interface Sample {\n  label: string;\n  count: number;\n  note?: string | null;\n}\n" {
		t.Fatal("declaration drift")
	}
	var raw []json.RawMessage
	if e = json.Unmarshal([]byte(vectors), &raw); e != nil {
		t.Fatal(e)
	}
	for i, b := range raw {
		if validate(b) != expected[i] {
			t.Fatalf("Go vector %d mismatch", i)
		}
	}
	sample, e := os.ReadFile("sample.ts.txt")
	if e != nil {
		t.Fatal(e)
	}
	rejected, e := os.ReadFile("rejected.ts.txt")
	if e != nil {
		t.Fatal(e)
	}
	var builds []map[string]string
	for i := 0; i < 2; i++ {
		dir, e := os.MkdirTemp(work, fmt.Sprintf("clean-%d-", i))
		if e != nil {
			t.Fatal(e)
		}
		write := func(name string, b []byte) {
			t.Helper()
			if e := os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
		for name, b := range files {
			write(name, b)
		}
		write("accepted.ts", bytes.ReplaceAll(sample, []byte("VECTORS"), []byte(vectors)))
		config := `{"compilerOptions":{"strict":true,"exactOptionalPropertyTypes":true,"noEmitOnError":true,"target":"ES2022","module":"ES2022","moduleResolution":"bundler","outDir":"assets","skipLibCheck":false},"files":["accepted.ts","sample.d.ts"]}`
		write("tsconfig.json", []byte(config))
		write("package.json", []byte(`{"type":"module"}`))
		run(dir, false, node, tsc, "-p", "tsconfig.json", "--noEmit")
		if _, e := os.Stat(filepath.Join(dir, "assets")); !os.IsNotExist(e) {
			t.Fatal("check emitted assets")
		}
		run(dir, false, node, tsc, "-p", "tsconfig.json")
		output := run(dir, false, node, filepath.Join(dir, "assets", "accepted.js"))
		want, _ := json.Marshal(struct {
			Label   string `json:"label"`
			Results []bool `json:"results"`
		}{"雪é😀", expected})
		if strings.TrimSpace(string(output)) != string(want) {
			t.Fatal("TS runtime vector mismatch")
		}
		write("rejected.ts", rejected)
		write("negative.json", []byte(strings.Replace(config, `["accepted.ts","sample.d.ts"]`, `["rejected.ts","sample.d.ts"]`, 1)))
		out := run(dir, true, node, tsc, "-p", "negative.json")
		for _, code := range []string{"TS2322", "TS2741", "TS2375", "TS2353"} {
			if !bytes.Contains(out, []byte(code)) {
				t.Fatalf("missing expected diagnostic %s", code)
			}
		}
		if _, e := os.Stat(filepath.Join(dir, "assets", "rejected.js")); !os.IsNotExist(e) {
			t.Fatal("noEmitOnError violated")
		}
		hashes := map[string]string{}
		for name, b := range files {
			if e = os.WriteFile(filepath.Join(dir, "assets", name), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
		for _, name := range []string{"accepted.js", "sample.schema.json", "sample.d.ts"} {
			b, e := os.ReadFile(filepath.Join(dir, "assets", name))
			if e != nil {
				t.Fatal(e)
			}
			hashes[name] = hash(b)
		}
		manifest, _ := json.Marshal(hashes)
		write("manifest.json", manifest)
		write("main.go", []byte(embeddedProgram))
		// A temporary standalone module is private; no nested module is committed.
		write("go.mod", []byte("module synthetic.invalid/q03\n\ngo 1.27.3\n"))
		run(dir, false, goBin, "build", "-trimpath", "-buildvcs=false", "-o", "sample.exe", ".")
		if strings.TrimSpace(string(run(dir, false, filepath.Join(dir, "sample.exe")))) != "verified synthetic assets" {
			t.Fatal("embed execution mismatch")
		}
		exe, e := os.ReadFile(filepath.Join(dir, "sample.exe"))
		if e != nil {
			t.Fatal(e)
		}
		hashes["sample.exe"] = hash(exe)
		builds = append(builds, hashes)
		// Stale asset bytes compile but must fail the independent embedded hash check.
		if e = os.WriteFile(filepath.Join(dir, "assets", "accepted.js"), []byte("stale\n"), 0600); e != nil {
			t.Fatal(e)
		}
		run(dir, false, goBin, "build", "-trimpath", "-buildvcs=false", "-o", "stale.exe", ".")
		run(dir, true, filepath.Join(dir, "stale.exe"))
		if e = os.Remove(filepath.Join(dir, "assets", "accepted.js")); e != nil {
			t.Fatal(e)
		}
		missing := run(dir, true, goBin, "build", "-trimpath", "-buildvcs=false", "-o", "missing.exe", ".")
		if !bytes.Contains(missing, []byte("no matching files found")) {
			t.Fatal("missing-asset failure was unrelated")
		}
	}
	a, _ := json.Marshal(builds[0])
	b, _ := json.Marshal(builds[1])
	if !bytes.Equal(a, b) {
		t.Fatal("clean harness outputs differ")
	}
	result := map[string]any{"status": "PASS_ISOLATED_SAMPLE_ONLY", "builds": builds, "vectors": len(expected), "go": runtime.Version(), "node": "24.20.0", "npm": "12.0.2", "typescript": "7.0.2", "environment": map[string]string{"GOTOOLCHAIN": os.Getenv("GOTOOLCHAIN"), "GOFLAGS": os.Getenv("GOFLAGS"), "GOOS": os.Getenv("GOOS"), "GOARCH": os.Getenv("GOARCH"), "CGO_ENABLED": os.Getenv("CGO_ENABLED")}}
	toolHashes := map[string]string{}
	for _, path := range []string{goBin, node, npm, tsc, filepath.Join(filepath.Dir(tsc), "..", "..", "@typescript", "typescript-win32-x64", "lib", "tsc.exe")} {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		toolHashes[path] = hash(b)
	}
	result["tool_sha256"] = toolHashes
	b, e = json.MarshalIndent(result, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(work, "result.json"), append(b, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}
