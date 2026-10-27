//go:build nativequalification && (!windows || !amd64)

package transaction

import "testing"

func TestUnsupportedNativePlatform(t *testing.T) {
	t.Fatal("V01 requires native Windows/amd64 on fixed local NTFS; no fallback is qualified")
}
