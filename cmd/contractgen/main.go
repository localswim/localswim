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

	"github.com/localswim/localswim/internal/contract"
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
