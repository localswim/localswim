//go:build windows && amd64

package local

import (
	"bytes"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

type recipientFaultFile struct {
	*os.File
	fault  string
	closed bool
}

func (f *recipientFaultFile) Write(b []byte) (int, error) {
	if f.fault == "short-write" {
		return f.File.Write(b[:len(b)/2])
	}
	return f.File.Write(b)
}
func (f *recipientFaultFile) Sync() error {
	if f.fault == "sync" {
		return ErrWorkspaceFiles
	}
	return f.File.Sync()
}
func (f *recipientFaultFile) Close() error {
	if (f.fault == "close" || f.fault == "close-cleanup") && !f.closed {
		f.closed = true
		return ErrWorkspaceFiles
	}
	return f.File.Close()
}
func TestRecipientNativePublicationFaults(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Fatal("requires explicit private native fixture parent")
	}
	for _, fault := range []string{"short-write", "sync", "close", "rename", "cleanup", "close-cleanup"} {
		t.Run(fault, func(t *testing.T) {
			parent := testRoot(t)
			defer os.RemoveAll(parent)
			root := filepath.Join(parent, "private")
			if e := privateDir(root); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(root, recipientStateName)
			removeCalls := 0
			ops := recipientPublishOps{open: func(p string) (recipientFile, error) {
				f, e := workspaceOpen(p, windows.GENERIC_WRITE, windows.CREATE_NEW, 0)
				if e != nil {
					return nil, e
				}
				return &recipientFaultFile{File: f, fault: fault}, nil
			}, move: func(a, z string) error {
				if fault == "rename" || fault == "cleanup" {
					return ErrWorkspaceFiles
				}
				ap, _ := pathName(a)
				zp, _ := pathName(z)
				return windows.MoveFileEx(ap, zp, windows.MOVEFILE_WRITE_THROUGH)
			}, remove: func(p string) error {
				removeCalls++
				if (fault == "cleanup" || fault == "close-cleanup") && removeCalls == 1 {
					return ErrWorkspaceFiles
				}
				return workspaceRemove(p)
			}}
			e := recipientPublishUsing(path, []byte("private bounded record"), ops)
			if e == nil {
				t.Fatal("injected failure accepted")
			}
			var cleanup WorkspaceCleanup
			if errors.As(e, &cleanup) {
				if fault != "close" && fault != "cleanup" && fault != "close-cleanup" {
					t.Fatal("unexpected retained cleanup")
				}
				if fault == "close-cleanup" && cleanup.Close() == nil {
					t.Fatal("failed cleanup falsely joined")
				}
				if e = cleanup.Close(); e != nil {
					t.Fatal(e)
				}
			} else if fault == "close" || fault == "cleanup" || fault == "close-cleanup" {
				t.Fatal("lost native cleanup ownership")
			}
			if _, e = os.Stat(path); !os.IsNotExist(e) {
				t.Fatal("fault published final")
			}
			entries, e := os.ReadDir(root)
			if e != nil || len(entries) != 0 {
				t.Fatal("unjoined native stage")
			}
		})
	}
	t.Run("create-once", func(t *testing.T) {
		parent := testRoot(t)
		defer os.RemoveAll(parent)
		root := filepath.Join(parent, "private")
		if e := privateDir(root); e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(root, recipientStateName)
		original := []byte("original")
		if e := recipientPublish(path, original); e != nil {
			t.Fatal(e)
		}
		if e := recipientPublish(path, []byte("replacement")); e == nil {
			t.Fatal("replaced")
		}
		b, e := workspaceRead(path, 4096)
		if e != nil || !bytes.Equal(b, original) {
			t.Fatal("changed fixed record")
		}
	})
}
