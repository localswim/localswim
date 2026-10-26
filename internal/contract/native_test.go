//go:build nativecompilerqualification

package contract

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNativeTypeScriptCanonical executes actual TS7 output under pinned Node.
// The independent reference lives only in testdata and is not shipped to clients.
func TestNativeTypeScriptCanonical(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" || runtime.Version() != "go1.27.3" {
		t.Fatal("requires qualified Windows/amd64 Go 1.27.3")
	}
	node, tsc, work := os.Getenv("C01_NODE"), os.Getenv("C01_TSC"), os.Getenv("C01_WORK")
	if !filepath.IsAbs(node) || !filepath.IsAbs(tsc) || !filepath.IsAbs(work) {
		t.Fatal("absolute C01_NODE, C01_TSC and private C01_WORK required")
	}
	if e := os.MkdirAll(work, 0700); e != nil {
		t.Fatal(e)
	}
	run := func(name string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = work
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("native tool failed: %v (details in private work)", e)
		}
		return b
	}
	if strings.TrimSpace(string(run(node, "--version"))) != "v24.20.0" {
		t.Fatal("Node pin mismatch")
	}
	if !strings.Contains(string(run(node, tsc, "--version")), "7.0.2") {
		t.Fatal("TypeScript pin mismatch")
	}
	for source, target := range map[string]string{"testdata/canonical.ts": "canonical.ts", "../../web/generated/contracts.d.ts": "contracts.d.ts"} {
		b, e := os.ReadFile(source)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(work, target), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	config := []byte(`{"compilerOptions":{"strict":true,"exactOptionalPropertyTypes":true,"noEmitOnError":true,"target":"ES2022","module":"CommonJS","outDir":"assets"},"files":["canonical.ts","contracts.d.ts"]}` + "\n")
	if e := os.WriteFile(filepath.Join(work, "tsconfig.json"), config, 0600); e != nil {
		t.Fatal(e)
	}
	check := run(node, tsc, "-p", filepath.Join(work, "tsconfig.json"), "--noEmit")
	_ = os.WriteFile(filepath.Join(work, "check.log"), check, 0600)
	if _, e := os.Stat(filepath.Join(work, "assets")); !os.IsNotExist(e) {
		t.Fatal("--noEmit created output or work directory was reused")
	}
	build := run(node, tsc, "-p", filepath.Join(work, "tsconfig.json"))
	_ = os.WriteFile(filepath.Join(work, "build.log"), build, 0600)
	type vector struct {
		InputHex     string  `json:"inputHex"`
		CanonicalHex *string `json:"canonicalHex,omitempty"`
		SHA256       string  `json:"sha256,omitempty"`
	}
	vectors := []vector{}
	add := func(raw []byte) {
		t.Helper()
		v, e := DecodeJSON(raw)
		if e != nil {
			vectors = append(vectors, vector{InputHex: hex.EncodeToString(raw)})
			return
		}
		b, e := Canonical(v)
		if e != nil {
			vectors = append(vectors, vector{InputHex: hex.EncodeToString(raw)})
			return
		}
		h, e := Hash(v)
		if e != nil {
			t.Fatal(e)
		}
		s := hex.EncodeToString(b)
		vectors = append(vectors, vector{hex.EncodeToString(raw), &s, h})
	}
	for _, d := range Commands() {
		for _, target := range []string{cardC, cardD} {
			routeTarget := target
			if d.Target == "none" {
				routeTarget = ""
			}
			i, e := Freeze(sample(d.Command, routeTarget, samples()[d.Command]), ResolvedRoute{d.Command, board, routeTarget}, AuthenticatedActor{actor})
			if e != nil {
				t.Fatal(e)
			}
			add(i.CanonicalBytes())
		}
	}
	// Reversed owners/peers and distinct grant/link identifiers are already full
	// semantic intents above; this additional reverse peer changes both endpoints.
	reverse := strings.Replace(samples()["link.add"], cardD, cardC, 1)
	i, e := Freeze(sample("link.add", cardD, reverse), ResolvedRoute{"link.add", board, cardD}, AuthenticatedActor{actor})
	if e != nil {
		t.Fatal(e)
	}
	add(i.CanonicalBytes())
	for _, raw := range []string{`null`, `0`, `9007199254740991`, `{"a":null,"b":[]}`, `"<>&\u2028\u2029雪é😀\u0000\r\n\t"`, `"\ud83d\ude00"`, `{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `"\ud800"`, `"\udfff"`, `"\ud800\u0041"`, `9007199254740992`, `1e0`, `1.0`, `-0`, `-1`, `01`, `{} {}`, "\xef\xbb\xbf{}", "\"\xff\"", strings.Repeat("[", 33) + strings.Repeat("]", 33)} {
		add([]byte(raw))
	}
	data, e := json.Marshal(vectors)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(work, "vectors.json"), data, 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(node, filepath.Join(work, "assets", "canonical.js"))
	cmd.Dir = work
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(work, "node.log"), out, 0600)
	if e != nil {
		t.Fatal("Node canonical corpus failed; inspect private node.log")
	}
	var result struct {
		Passed int `json:"passed"`
	}
	if e = json.Unmarshal(out, &result); e != nil || result.Passed != len(vectors) {
		t.Fatal("missing native cases")
	}
	t.Logf("actual TS7 strict/noEmit and emitted Node canonical bytes/hashes/rejections: %d cases", result.Passed)
}
