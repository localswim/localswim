//go:build !windows || !amd64

package admin

import "io"

func consoleInput(io.Reader) bool { return false }
