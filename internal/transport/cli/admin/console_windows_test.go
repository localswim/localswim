//go:build windows && amd64

package admin

import (
	"os"
	"testing"
)

func TestNativePipeCannotAssertTerminalConsent(t *testing.T) {
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	defer w.Close()
	if consoleInput(r) || consoleInput(w) {
		t.Fatal("native pipe mistaken for console")
	}
	if sink, e := openCodeConsole(r); e == nil || sink != nil {
		if sink != nil {
			sink.Close()
		}
		t.Fatal("native redirected input armed private code console")
	}
}
