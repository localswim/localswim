//go:build windows && amd64 && nativequalification

package installation

import (
	"context"
	"testing"
	"time"
)

func TestActualInstalledMetadataOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, cleanup := inspectNative(ctx)
	if cleanup != nil {
		if e := cleanup(); e != nil {
			t.Fatal("metadata handles did not close")
		}
	}
	if v != (Observation{"candidate", "CANDIDATE_PRESENT"}) {
		t.Fatalf("metadata qualification: %s/%s", v.State, v.Code)
	}
	t.Log("Native installed metadata qualified; no executable was invoked.")
}
