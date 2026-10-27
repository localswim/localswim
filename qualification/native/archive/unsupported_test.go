//go:build nativequalification && (!windows || !amd64)

package archivequalification

import "testing"

func TestV03LocalArchive(t *testing.T) { t.Fatal("V03 requires actual Windows amd64 NTFS") }
