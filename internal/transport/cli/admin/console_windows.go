//go:build windows && amd64

package admin

import (
	"golang.org/x/sys/windows"
	"io"
)

func consoleInput(r io.Reader) bool {
	f, ok := consoleFile(r)
	if !ok {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}
