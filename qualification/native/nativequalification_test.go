//go:build nativequalification && windows && amd64

package nativequalification

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestNativeQualification(t *testing.T) {
	if runtime.Version() != "go1.27.3" || os.Getenv("CGO_ENABLED") != "0" || os.Getenv("GOTOOLCHAIN") != "local" || os.Getenv("GOFLAGS") != "-mod=readonly" || os.Getenv("GOPROXY") != "off" || os.Getenv("GOSUMDB") != "off" {
		t.Fatal("pinned offline Go configuration required")
	}
	wd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	repo := filepath.Clean(filepath.Join(wd, "../.."))
	root := os.Getenv("V04_NATIVE_ROOT")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.HasPrefix(strings.ToLower(root)+`\`, strings.ToLower(repo)+`\`) || len(root) > 70 || strings.HasPrefix(root, `\\`) {
		t.Fatal("explicit short canonical private local V04_NATIVE_ROOT outside public repository required")
	}
	goexe := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	raw, e := os.ReadFile(goexe)
	if e != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != "83b7ba66215e696414fa49462d0db2da4f4cf15c7bebd42f0b189fa04866999d" {
		t.Fatal("qualified Go executable required")
	}
	var inv inventory
	if json.Unmarshal(inventoryBytes, &inv) != nil || len(inv.Transaction) != 8 || len(inv.TransactionChild) != 7 || len(inv.Archive) != 14 || len(inv.ArchiveChild) != 4 || len(inv.ArchiveRows) != 6 {
		t.Fatal("complete root inventory required")
	}
	// The parent is explicitly supplied. Every invocation gets a new private ACL
	// child; existing fixtures and evidence are never reused or cleaned up.
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal("account unavailable")
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal(e)
	}
	attempt := filepath.Join(root, fmt.Sprintf("r%x", time.Now().UnixNano()))
	if e = windows.CreateDirectory(windows.StringToUTF16Ptr(attempt), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); e != nil {
		t.Fatal("fresh private attempt unavailable")
	}
	write := func(name string, b []byte) {
		t.Helper()
		p := filepath.Join(attempt, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, e = f.Write(b)
		ce := f.Close()
		if e != nil || ce != nil {
			t.Fatal("evidence write failed")
		}
	}
	encode := func(v any) []byte {
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		return append(b, '\n')
	}
	snapshot := func(copy bool) map[string]string {
		t.Helper()
		binding := map[string]string{}
		for _, dir := range []string{"internal", "cmd", "qualification/native"} {
			e := filepath.WalkDir(filepath.Join(repo, dir), func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.IsDir() {
					return nil
				}
				b, e := os.ReadFile(p)
				if e != nil {
					return e
				}
				rel, e := filepath.Rel(repo, p)
				if e != nil {
					return e
				}
				binding[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(b))
				if copy {
					write(filepath.Join("source-before", rel), b)
				}
				return nil
			})
			if e != nil {
				t.Fatal("source inventory unavailable")
			}
		}
		for _, rel := range []string{"go.mod", "go.sum"} {
			b, e := os.ReadFile(filepath.Join(repo, rel))
			if e != nil {
				t.Fatal(e)
			}
			binding[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
			if copy {
				write(filepath.Join("source-before", rel), b)
			}
		}
		return binding
	}
	before := snapshot(true)
	write("binding.json", encode(before))
	write("inventory.json", inventoryBytes)

	v01 := filepath.Join(attempt, "n1")
	art := filepath.Join(attempt, "a1")
	v03 := filepath.Join(attempt, "n3")
	for _, p := range []string{v01, art, v03} {
		if e = windows.CreateDirectory(windows.StringToUTF16Ptr(p), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); e != nil {
			t.Fatal(e)
		}
	}
	env := append(os.Environ(), "V01_NATIVE_ROOT="+v01, "V01_ARTIFACT_ROOT="+art, "V03_NATIVE_ROOT="+v03, "V01_CHILD_MODE=", "V01_CHILD_BOARD=", "V01_PRECOMMIT_CHILD=", "V01_CASE_ROOT=")
	results := map[string]any{"noPowerLossClaim": true, "goSHA256": fmt.Sprintf("%x", sha256.Sum256(raw))}
	defer func() {
		after := snapshot(false)
		write("binding-after.json", encode(after))
		stable := reflect.DeepEqual(before, after)
		if !stable {
			t.Error("source changed during native execution")
		}
		results["sourceStable"] = stable
		results["complete"] = !t.Failed()
		write("results.json", encode(results))
	}()
	for _, suite := range []struct {
		name string
		ids  []string
	}{{"transaction", inv.Transaction}, {"archive", inv.Archive}} {
		ctx, cancel := context.WithTimeout(context.Background(), 270*time.Second)
		cmd := exec.CommandContext(ctx, goexe, "test", "-tags=nativequalification", "-count=1", "-json", "-timeout=4m", "./qualification/native/"+suite.name)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		cancel()
		write(suite.name+".jsonl", out)
		cov, parseErr := dispatch(func() ([]byte, error) { return out, err }, suite.ids)
		results[suite.name] = map[string]any{"coverage": cov, "exitOK": err == nil, "reportOK": parseErr == nil}
		if err != nil || parseErr != nil {
			t.Errorf("%s mandatory native execution failed; private attempt retained", suite.name)
			continue
		}
		pattern := filepath.Join(art, "v01-cont-*", "logs", "native-child.log")
		if suite.name == "archive" {
			pattern = filepath.Join(v03, "*", "interruption-child.jsonl")
		}
		logs, e := filepath.Glob(pattern)
		if e != nil || len(logs) != 1 {
			t.Errorf("%s unique nested report missing", suite.name)
			continue
		}
		child, e := os.ReadFile(logs[0])
		if e != nil {
			t.Fatal("nested report unavailable")
		}
		var nested coverage
		var ne error
		if suite.name == "transaction" {
			nested, ne = parseVerbose(child, inv.TransactionChild)
		} else {
			nested, ne = parseReport(child, inv.ArchiveChild)
		}
		results[suite.name+"Child"] = nested
		if ne != nil {
			t.Errorf("%s mandatory nested coverage failed", suite.name)
		}
	}
	t.Log("real V01 and V03 suites dispatched; immutable private attempt retained")
}
