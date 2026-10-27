//go:build nativequalification && (!windows || !amd64)

package nativequalification

import "testing"

func TestNativeQualification(t *testing.T) {
	t.Fatal("native qualification requires actual Windows amd64 NTFS; no fallback")
}
