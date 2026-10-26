package cards

import (
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestWorkflowSnapshotRejectsInvalidScope(t *testing.T) {
	if _, err := LoadSnapshot(nil, "", ""); err == nil {
		t.Fatal("nil read accepted")
	}
	if err := WriteSnapshot(nil, "", "", 0, A.SnapshotState{}); err == nil {
		t.Fatal("nil write accepted")
	}
}
