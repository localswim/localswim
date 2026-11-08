//go:build windows && amd64

package nativecustody

import (
	"context"
	"time"

	"golang.org/x/sys/windows"
)

// VersionResult is private native evidence for the installation engine. It is
// never a transport response. Capture has one shared 64 KiB bound.
type VersionResult struct {
	Code           string
	Joined         bool
	ExitCode       uint32
	Stdout, Stderr []byte
	ObservedBytes  uint64
}

// ExecuteVersion is the restricted production bridge. The installation engine
// supplies held, freshly qualified image and private-work-root custody and a
// live authority/file check. Arguments, environment and the strict process
// profile are fixed here; there is no exported hook or alternate profile.
// It returns only after actual native join, even when the caller is cancelled.
// Its caller must retain board/runtime custody while this call is outstanding.
func ExecuteVersion(ctx context.Context, executable, workRoot string, check func(context.Context) error) VersionResult {
	system, err := windows.GetWindowsDirectory()
	if err != nil || check == nil {
		return VersionResult{Code: "CREATE_FAILED", Joined: true}
	}
	deadline := time.Now().Add(commandLimit)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	o := &owner{}
	p, err := o.start(ctx, spec{executable: executable, root: workRoot, systemRoot: system, check: func() error { return check(ctx) }, deadline: deadline})
	if err != nil {
		return VersionResult{Code: "CREATE_FAILED", Joined: true}
	}
	<-p.done
	r := p.snapshot(true)
	return VersionResult{r.Code, r.Joined, r.ExitCode, r.Stdout, r.Stderr, r.ObservedBytes}
}
