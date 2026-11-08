//go:build windows && amd64 && nativecustodyqualification

package nativecustody

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestProductionVersionBridgeWithOwnedHelper(t *testing.T) {
	exe, root := helper(t, "normal")
	var checks atomic.Int32
	result := ExecuteVersion(context.Background(), exe, root, func(ctx context.Context) error { checks.Add(1); return ctx.Err() })
	if result.Code != "OK" || !result.Joined || result.ExitCode != 0 || string(result.Stdout) != "codex-cli 1.2.3\n" || len(result.Stderr) != 0 {
		t.Fatal("restricted bridge did not join exact owned-helper output", result.Code, result.Joined, result.ExitCode)
	}
	if checks.Load() < 3 {
		t.Fatal("native phase checks missing", checks.Load())
	}
}
