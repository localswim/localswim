//go:build windows && amd64

package local

import (
	"golang.org/x/sys/windows"
	"io"
	"os"
)

// Recipient publication is create-once and retains failed cleanup ownership.
// No final-path replacement or deletion is available through this operation.
type recipientFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}
type recipientPublishOps struct {
	open   func(string) (recipientFile, error)
	move   func(string, string) error
	remove func(string) error
}

func recipientPublish(path string, b []byte) error {
	return recipientPublishUsing(path, b, recipientPublishOps{
		open: func(p string) (recipientFile, error) {
			f, e := workspaceOpen(p, windows.GENERIC_WRITE, windows.CREATE_NEW, 0)
			return f, e
		},
		move: func(a, z string) error {
			ap, e := pathName(a)
			if e != nil {
				return e
			}
			zp, e := pathName(z)
			if e != nil {
				return e
			}
			return windows.MoveFileEx(ap, zp, windows.MOVEFILE_WRITE_THROUGH)
		}, remove: workspaceRemove})
}

var _ recipientFile = (*os.File)(nil)

// A per-call dependency seam permits controlled syscall failure qualification;
// production never accepts operations from a caller or mutable global hooks.
func recipientPublishUsing(path string, b []byte, ops recipientPublishOps) error {
	temp := path + "." + uuid() + ".pending"
	f, e := ops.open(temp)
	if e != nil {
		return e
	}
	n, e := f.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); ce != nil {
		joined := false
		return workspaceRetain(ErrWorkspaceFiles, func() error {
			if !joined {
				if f.Close() != nil {
					return ErrWorkspaceFiles
				}
				joined = true
			}
			return ops.remove(temp)
		})
	}
	cleanup := func() error {
		if e := ops.remove(temp); e != nil {
			return workspaceRetain(ErrWorkspaceFiles, func() error { return ops.remove(temp) })
		}
		return ErrWorkspaceFiles
	}
	if e != nil {
		return cleanup()
	}
	if ops.move(temp, path) != nil {
		return cleanup()
	}
	return nil
}
