//go:build windows && amd64

package admin

import (
	"errors"
	"io"
	"sync"
	"unicode/utf16"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"golang.org/x/sys/windows"
)

type nativeCodeConsole struct {
	mu           sync.Mutex
	h            windows.Handle
	used, closed bool
}

func openCodeConsole(stdin io.Reader) (codeConsole, error) {
	if !consoleInput(stdin) {
		return nil, &core.Failure{Code: "CONFIRMATION_REQUIRED", Exit: 2}
	}
	name, _ := windows.UTF16PtrFromString("CONOUT$")
	h, e := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if e != nil {
		return nil, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
	}
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		windows.CloseHandle(h)
		return nil, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
	}
	return &nativeCodeConsole{h: h}, nil
}
func (n *nativeCodeConsole) Present(code T.SecretCode) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.used || n.closed {
		return errors.New("private presentation unavailable")
	}
	n.used = true
	// Native console characters are deliberately not routed through os.Stdout,
	// os.Stderr, fmt, a filesystem path, clipboard or an ordinary logging writer.
	text := utf16.Encode([]rune("Browser login code: " + string(code) + "\r\n"))
	defer clear(text)
	var count uint32
	if e := windows.WriteConsole(n.h, &text[0], uint32(len(text)), &count, nil); e != nil || count != uint32(len(text)) {
		return errors.New("private presentation unavailable")
	}
	return nil
}
func (n *nativeCodeConsole) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil
	}
	n.closed = true
	return windows.CloseHandle(n.h)
}
