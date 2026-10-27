//go:build nativequalification && windows && amd64

package transaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

//go:embed providers.json
var v01Providers []byte

//go:embed child.go.txt
var v01Child []byte

//go:embed root-overlay.txt
var v01RootOverlay string

//go:embed writer-overlay.txt
var v01WriterOverlay []byte

//go:embed evidence-overlay.txt
var v01EvidenceOverlay []byte

type v01Hash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func v01Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestV01RemainingNativeMatrix(t *testing.T) {
	if runtime.Version() != "go1.27.3" || os.Getenv("CGO_ENABLED") != "0" || os.Getenv("GOTOOLCHAIN") != "local" || os.Getenv("GOFLAGS") != "-mod=readonly" {
		t.Fatal("exact qualified toolchain configuration required")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal("source unavailable")
	}
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	artifacts := os.Getenv("V01_ARTIFACT_ROOT")
	if !filepath.IsAbs(artifacts) || filepath.Clean(artifacts) != artifacts || strings.HasPrefix(strings.ToLower(artifacts)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("canonical private artifact root required")
	}
	attempt, err := os.MkdirTemp(artifacts, "v01-cont-")
	if err != nil {
		t.Fatal("unique private attempt unavailable")
	}
	write := func(p string, b []byte) {
		t.Helper()
		if os.MkdirAll(filepath.Dir(p), 0700) != nil {
			t.Fatal("private inventory unavailable")
		}
		f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			t.Fatal("immutable evidence creation failed")
		}
		n, e := f.Write(b)
		ce := f.Close()
		if e != nil || ce != nil || n != len(b) {
			t.Fatal("evidence write failed")
		}
	}
	marshal := func(v any) []byte {
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			t.Fatal("inventory encoding failed")
		}
		return append(b, '\n')
	}
	for _, d := range []string{"source-before", "runtime", "logs", "results", "module", "overlays"} {
		if os.Mkdir(filepath.Join(attempt, d), 0700) != nil {
			t.Fatal("unique attempt inventory failed")
		}
	}
	var providers []v01Hash
	if json.Unmarshal(v01Providers, &providers) != nil || len(providers) == 0 {
		t.Fatal("provider inventory required")
	}
	verify := func(root string) {
		t.Helper()
		var paths []string
		e := filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				paths = append(paths, filepath.ToSlash(rel))
			}
			return nil
		})
		sort.Strings(paths)
		if e != nil || len(paths) != len(providers) {
			t.Fatal("provider inventory differs")
		}
		for i, row := range providers {
			if row.Path != paths[i] || (i > 0 && providers[i-1].Path >= row.Path) {
				t.Fatal("provider path set differs or is not unique sorted")
			}
			b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(row.Path)))
			if e != nil || v01Digest(b) != row.SHA256 {
				t.Fatal("frozen provider bytes differ")
			}
		}
	}
	verify(repo)
	defer verify(repo)
	module := filepath.Join(attempt, "module")
	var binding []v01Hash
	copyOne := func(rel string) {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(repo, rel))
		if e != nil {
			t.Fatal("frozen source unavailable")
		}
		write(filepath.Join(module, rel), b)
		write(filepath.Join(attempt, "source-before", rel), b)
		binding = append(binding, v01Hash{rel, v01Digest(b)})
	}
	for _, row := range providers {
		copyOne(row.Path)
	}
	for rel, want := range map[string]string{"go.mod": "e4884af04b03465b486bccd0f2e8c031d575b5d3ddbf20a6db64ab7fb84f3629", "go.sum": "73d8f9e5776a31a8d5eb60d1cec809f56ba60ffdc13933ae5db6b12fd5c9e8e8"} {
		b, e := os.ReadFile(filepath.Join(repo, rel))
		if e != nil || v01Digest(b) != want {
			t.Fatal("qualified module inputs differ")
		}
		copyOne(rel)
	}
	for _, rel := range []string{"native_windows_test.go", "continuation_windows_test.go", "child.go.txt", "root-overlay.txt", "writer-overlay.txt", "evidence-overlay.txt", "providers.json"} {
		b, e := os.ReadFile(filepath.Join(wd, rel))
		if e != nil {
			t.Fatal("qualification source unavailable")
		}
		write(filepath.Join(attempt, "source-before", "qualification", rel), b)
		binding = append(binding, v01Hash{"qualification/" + rel, v01Digest(b)})
	}
	goexe := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	b, e := os.ReadFile(goexe)
	if e != nil || v01Digest(b) != "83b7ba66215e696414fa49462d0db2da4f4cf15c7bebd42f0b189fa04866999d" {
		t.Fatal("qualified Go executable differs")
	}
	// Root allocation is native private ACL construction, before any child runs.
	anchor := privateDir(t)
	cases := []string{"TestV01PrecommitTermination", "TestV01AutomaticRollbackFence", "TestV01AutomaticReadbackFence", "TestV01CLIFlushRefusal", "TestV01CLIReadbackRefusal", "TestV01CLILostResponseRestartLookup", "TestV01RootRefusal"}
	roots := map[string]string{}
	var literals, declarations strings.Builder
	declarations.WriteString("package transaction\nvar v01CaseRoots = map[string]string{\n")
	for n, name := range cases {
		root := filepath.Join(anchor, fmt.Sprintf("n1-%02d-installation", n))
		if len(root) > 220 {
			t.Fatal("fixture path bound exceeded")
		}
		roots[name] = root
		fmt.Fprintf(&literals, "case %q: return p, nil\n", root)
		fmt.Fprintf(&declarations, "%q:%q,\n", name, root)
	}
	declarations.WriteString("}\n")
	childdir := filepath.Join(module, "qualification", "v01child")
	base, e := os.ReadFile(filepath.Join(wd, "native_windows_test.go"))
	if e != nil {
		t.Fatal("frozen subset unavailable")
	}
	write(filepath.Join(childdir, "base_test.go"), base)
	write(filepath.Join(childdir, "remainder_test.go"), v01Child)
	write(filepath.Join(childdir, "roots_test.go"), []byte(declarations.String()))
	old := "func nativeRoot() (string, error) {\n\tp, e := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)\n\tif e != nil {\n\t\treturn \"\", fail(\"PLATFORM_UNQUALIFIED\", 3)\n\t}\n\treturn filepath.Join(p, \"localswim\", \"slice01\"), nil\n}"
	setupRel := filepath.Join("internal", "transport", "setup", "native_windows.go")
	b, e = os.ReadFile(filepath.Join(module, setupRel))
	if e != nil || bytes.Count(b, []byte(old)) != 1 {
		t.Fatal("exact reviewed setup patch unavailable")
	}
	replacement := strings.Replace(v01RootOverlay, "V01_ALLOWED_CASES", literals.String(), 1)
	setupBytes := []byte(strings.Replace(string(b), old, replacement, 1))
	overlays := map[string]string{}
	var overlayHashes []map[string]string
	for _, patch := range []struct {
		rel         string
		appended    []byte
		replacement []byte
	}{
		{setupRel, nil, setupBytes},
		{filepath.Join("internal", "store", "writer", "writer.go"), v01WriterOverlay, nil},
		{filepath.Join("internal", "transport", "cli", "evidence", "store_windows.go"), v01EvidenceOverlay, nil},
	} {
		original, e := os.ReadFile(filepath.Join(module, patch.rel))
		if e != nil {
			t.Fatal("overlay original unavailable")
		}
		changed := append(append([]byte{}, original...), patch.appended...)
		if patch.replacement != nil {
			changed = patch.replacement
		}
		dest := filepath.Join(attempt, "overlays", fmt.Sprintf("%d.go", len(overlays)))
		write(dest, changed)
		overlays[filepath.Join(module, patch.rel)] = dest
		overlayHashes = append(overlayHashes, map[string]string{"path": patch.rel, "original": v01Digest(original), "replacement": v01Digest(changed)})
	}
	overlayfile := filepath.Join(attempt, "runtime", "overlay.json")
	write(overlayfile, marshal(map[string]any{"Replace": overlays}))
	write(filepath.Join(attempt, "runtime", "binding.json"), marshal(map[string]any{"source": binding, "overlays": overlayHashes, "overlaySHA256": v01Digest(marshal(map[string]any{"Replace": overlays})), "child": v01Digest(v01Child), "base": v01Digest(base), "roots": roots, "goSHA256": v01Digest(mustV01Read(t, goexe))}))
	// The identical child must fail specifically on all three missing symbols.
	// An uninstrumented executable is never run, even if compilation succeeds.
	run := func(log string, args ...string) error {
		c, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()
		cmd := exec.CommandContext(c, goexe, args...)
		cmd.Dir = module
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly")
		out, e := cmd.CombinedOutput()
		write(filepath.Join(attempt, "logs", log), out)
		return e
	}
	negative := run("missing-overlay.log", "test", "-c", "-tags=nativequalification", "-o", filepath.Join(attempt, "runtime", "forbidden.exe"), "./qualification/v01child")
	neg := mustV01Read(t, filepath.Join(attempt, "logs", "missing-overlay.log"))
	if negative == nil || !bytes.Contains(neg, []byte("undefined: setup.V01QualificationRoot")) || !bytes.Contains(neg, []byte("V01QualificationFault")) || !bytes.Contains(neg, []byte("V01QualificationCallbacks")) {
		t.Fatal("specific missing-overlay compile guard failed")
	}
	verify(module)
	childexe := filepath.Join(attempt, "runtime", "v01-child.exe")
	if run("build.log", "test", "-c", "-tags=nativequalification", "-overlay", overlayfile, "-o", childexe, "./qualification/v01child") != nil {
		t.Fatal("instrumented child build failed; private attempt retained")
	}
	verify(module)
	write(filepath.Join(attempt, "runtime", "executable.json"), marshal(v01Hash{"v01-child.exe", v01Digest(mustV01Read(t, childexe))}))
	// Recheck the immutable overlay bytes immediately before child execution.
	for _, row := range overlayHashes {
		if v01Digest(mustV01Read(t, overlays[filepath.Join(module, row["path"])])) != row["replacement"] {
			t.Fatal("runtime overlay changed")
		}
	}
	c, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, childexe, "-test.v", "-test.timeout=150s", "-test.run=^TestV01")
	cmd.Dir = childdir
	cmd.Env = append(os.Environ(), "V01_NATIVE_ROOT="+anchor, "V01_CASE_ROOT=", "V01_CHILD_MODE=", "V01_CHILD_BOARD=", "V01_CHILD_RESULTS="+filepath.Join(attempt, "results"))
	out, childErr := cmd.CombinedOutput()
	write(filepath.Join(attempt, "logs", "native-child.log"), out)
	passed := 0
	for _, name := range cases {
		if bytes.Count(out, []byte("--- PASS: "+name+" (")) == 1 {
			passed++
		}
	}
	write(filepath.Join(attempt, "results", "summary.json"), marshal(map[string]any{"childPassed": passed, "required": len(cases), "childExitOK": childErr == nil, "skips": bytes.Count(out, []byte("--- SKIP:")), "providersStable": true, "noPowerLossClaim": true}))
	if childErr != nil || passed != len(cases) || bytes.Contains(out, []byte("--- SKIP:")) {
		t.Fatal("mandatory native child matrix failed; immutable attempt retained")
	}
	verify(module)
	t.Logf("seven mandatory continuation cases passed; three private overlays and %d complete provider hashes verified", len(providers))
}

func mustV01Read(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal("bound evidence unavailable")
	}
	return b
}
