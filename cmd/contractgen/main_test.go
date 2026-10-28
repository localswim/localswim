package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/contract/transport"
)

func TestBrowserSchemasAreGeneratedAndReferencesResolve(t *testing.T) {
	root := t.TempDir()
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "web/src/api/schema-data.ts")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, content, found := strings.Cut(string(body), " = ")
	var definitions map[string]any
	if !found || json.Unmarshal([]byte(strings.TrimSuffix(content, ";\n")), &definitions) != nil {
		t.Fatal("browser schema map missing")
	}
	for _, name := range []string{"transport:RuntimeMode", "transport:BoardBootstrapRequest", "transport:LocalBootstrap", "transport:WorkspaceBootstrap", "archive:Identity", "mutation:RequestCardCreate"} {
		if definitions[name] == nil {
			t.Fatal("browser boundary omitted: " + name)
		}
	}
	var check func(any)
	check = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, child := range value {
				check(child)
			}
		case map[string]any:
			for name, child := range value {
				if name == "$ref" {
					ref, ok := child.(string)
					if !ok || definitions[ref] == nil {
						t.Fatal("unresolved browser schema reference")
					}
				} else {
					check(child)
				}
			}
		}
	}
	check(definitions)
	if err := os.WriteFile(path, []byte("stale browser schema\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err == nil {
		t.Fatal("browser schema drift accepted")
	}
	if got, _ := os.ReadFile(path); string(got) != "stale browser schema\n" {
		t.Fatal("check overwrote browser schema")
	}
}

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

func TestProviderBundlesAndNonwritingCheck(t *testing.T) {
	root := t.TempDir()
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	want, err := archive.GeneratedFiles()
	if err != nil {
		t.Fatal(err)
	}
	transportFiles, err := transport.GeneratedFiles()
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range transportFiles {
		want[name] = data
	}
	for _, name := range []string{"schemas/archive/contracts.schema.json", "web/generated/archive/contracts.d.ts", "schemas/transport/contracts.schema.json", "web/generated/transport/contracts.d.ts"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(name))
			got, err := os.ReadFile(path)
			if err != nil || len(want[name]) == 0 || !bytes.Equal(got, want[name]) {
				t.Fatal("aggregate provider output missing or different")
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
				t.Fatal("provider drift passed check")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "drift\n" {
				t.Fatal("check rewrote provider output")
			}
			if err := run(root, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
