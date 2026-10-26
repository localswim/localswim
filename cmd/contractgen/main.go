// contractgen writes Go-owned boundary artifacts, or verifies drift without
// modifying any output when -check is supplied. Run from the module root.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/localswim/localswim/internal/contract"
	"github.com/localswim/localswim/internal/contract/archive"
)

func main() {
	check := flag.Bool("check", false, "fail on generated drift without writing outputs")
	root := flag.String("root", ".", "repository output root")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	if err := run(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(root string, check bool) error {
	files, e := contract.GeneratedFiles()
	if e != nil {
		return e
	}
	archiveFiles, e := archive.GeneratedFiles()
	if e != nil {
		return e
	}
	for name, data := range archiveFiles {
		if filepath.IsAbs(name) || filepath.ToSlash(filepath.Clean(name)) != name ||
			strings.Contains(name, "\\") ||
			!(strings.HasPrefix(name, "schemas/archive/") || strings.HasPrefix(name, "web/generated/archive/")) {
			return fmt.Errorf("invalid archive output: %s", name)
		}
		if _, exists := files[name]; exists {
			return fmt.Errorf("duplicate generated output: %s", name)
		}
		files[name] = data
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		want := files[name]
		if check {
			got, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(got, want) {
				return fmt.Errorf("generated drift: %s", name)
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			return e
		}
		if e = os.WriteFile(path, want, 0644); e != nil {
			return e
		}
	}
	return nil
}
