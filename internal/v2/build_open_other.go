//go:build !unix

package v2

import "os"

func openBuildFile(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
