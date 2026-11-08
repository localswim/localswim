//go:build windows && amd64

package nativecustody

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStrictPolicyBitsAndKnownProfileFailures(t *testing.T) {
	for _, flags := range []uint32{0, 2, 3, 4, 5, 7, 0xffffffff} {
		if validChildPolicy(flags) {
			t.Fatal("contradictory child policy admitted", flags)
		}
	}
	if !validChildPolicy(1) {
		t.Fatal("exact child restriction refused")
	}
	for _, err := range []error{windows.ERROR_NOT_SUPPORTED, windows.ERROR_INVALID_PARAMETER, windows.ERROR_CALL_NOT_IMPLEMENTED, windows.ERROR_PROC_NOT_FOUND} {
		if !errors.Is(classifyProfileError(err), errProfileUnsupported) {
			t.Fatal("missing policy not classified", err)
		}
	}
	err := windows.ERROR_NOT_ENOUGH_MEMORY
	if errors.Is(classifyProfileError(err), errProfileUnsupported) {
		t.Fatal("unavailable preparation falsely definitive")
	}
}
