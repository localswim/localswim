//go:build windows && amd64

package local

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func workspaceNativeError(e error) error {
	if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
		return ErrWorkspaceMissing
	}
	if errors.Is(e, windows.ERROR_SHARING_VIOLATION) || errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
		return ErrWorkspaceBusy
	}
	return ErrWorkspaceFiles
}

// workspacePath extends the existing native guard vocabulary with physical
// identity and retryable close. It reuses safeDir, private ACL checking and the
// account-private creator; no lexical-only path grants access to a member.
func workspacePath(path string, create, private, exclusiveCreate bool) (*WorkspacePath, error) {
	return workspacePathWithAncestor(path, create, private, exclusiveCreate, nil)
}
func workspacePathWithAncestor(path string, create, private, exclusiveCreate bool, ancestor *WorkspacePath) (*WorkspacePath, error) {
	if !workspaceSafeDir(path) {
		return nil, ErrWorkspaceFiles
	}
	var handles []windows.Handle
	g := &WorkspacePath{}
	g.close = func() error {
		var failed []windows.Handle
		for i := len(handles) - 1; i >= 0; i-- {
			if windows.CloseHandle(handles[i]) != nil {
				failed = append(failed, handles[i])
			}
		}
		handles = failed
		if len(failed) > 0 {
			return ErrWorkspaceFiles
		}
		return nil
	}
	fail := func(e error) (*WorkspacePath, error) {
		if g.Close() != nil {
			return g, e
		}
		return nil, e
	}
	cur := path[:3]
	parts := strings.Split(path[3:], `\`)
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			cur = filepath.Join(cur, parts[i])
		}
		p, e := pathName(cur)
		if e != nil {
			return fail(ErrWorkspaceFiles)
		}
		if i == len(parts)-1 && create {
			sa, e := attributes()
			if e != nil {
				return fail(ErrWorkspaceFiles)
			}
			e = windows.CreateDirectory(p, sa)
			if e != nil && (exclusiveCreate || !errors.Is(e, windows.ERROR_ALREADY_EXISTS)) {
				return fail(workspaceNativeError(e))
			}
		}
		h, e := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			return fail(workspaceNativeError(e))
		}
		handles = append(handles, h)
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return fail(ErrWorkspaceFiles)
		}
		g.chain = append(g.chain, fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow))
		// Compare the actual pinned project ancestor before proceeding toward
		// the member leaf, particularly before any requested leaf creation.
		if ancestor != nil && len(g.chain) == len(ancestor.chain) && g.chain[len(g.chain)-1] != ancestor.key {
			return fail(ErrWorkspaceFiles)
		}
		if i == len(parts)-1 {
			if private && checkPrivate(h, true) != nil {
				return fail(ErrWorkspaceFiles)
			}
			var fs [32]uint16
			if windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))) != nil || windows.UTF16ToString(fs[:]) != "NTFS" || windows.GetDriveType(windows.StringToUTF16Ptr(path[:3])) != windows.DRIVE_FIXED {
				return fail(ErrWorkspaceFiles)
			}
			var final [512]uint16
			// Zero requests FILE_NAME_NORMALIZED | VOLUME_NAME_DOS.
			n, e := windows.GetFinalPathNameByHandle(h, &final[0], uint32(len(final)), 0)
			if e != nil || n == 0 || n >= uint32(len(final)) {
				return fail(ErrWorkspaceFiles)
			}
			canonical := strings.TrimPrefix(windows.UTF16ToString(final[:n]), `\\?\`)
			if !safeDir(canonical) || !strings.EqualFold(path, canonical) {
				return fail(ErrWorkspaceFiles)
			}
			g.path = canonical
			g.key = fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
		}
	}
	return g, nil
}

func workspaceOpen(path string, access, disposition, share uint32) (*os.File, error) {
	sa, e := attributes()
	if e != nil {
		return nil, ErrWorkspaceFiles
	}
	p, e := pathName(path)
	if e != nil {
		return nil, ErrWorkspaceFiles
	}
	h, e := windows.CreateFile(p, access|windows.READ_CONTROL, share, sa, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, workspaceNativeError(e)
	}
	if checkPrivate(h, false) != nil {
		if windows.CloseHandle(h) != nil {
			return nil, workspaceRetain(ErrWorkspaceFiles, func() error { return windows.CloseHandle(h) })
		}
		return nil, ErrWorkspaceFiles
	}
	return os.NewFile(uintptr(h), path), nil
}
func workspaceLock(root string) (*os.File, error) {
	return workspaceOpen(filepath.Join(root, "workspace.lock"), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_ALWAYS, 0)
}
func workspaceExistingLock(root string) (*os.File, error) {
	return workspaceOpen(filepath.Join(root, "local.lock"), windows.GENERIC_READ, windows.OPEN_EXISTING, 0)
}
func workspaceRead(path string, limit int64) (b []byte, err error) {
	f, e := workspaceOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_SHARE_READ)
	if errors.Is(e, ErrWorkspaceMissing) {
		return nil, os.ErrNotExist
	}
	if e != nil {
		return nil, e
	}
	defer func() {
		if f.Close() != nil {
			b = nil
			err = workspaceRetain(ErrWorkspaceFiles, f.Close)
		}
	}()
	b, e = io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, ErrWorkspaceFiles
	}
	return b, nil
}
func workspaceIdentity(root string) (*os.File, []byte, error) {
	f, e := workspaceOpen(filepath.Join(root, "identity.json"), windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_SHARE_READ)
	if e != nil {
		return nil, nil, e
	}
	b, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil || len(b) > 16384 {
		if f.Close() != nil {
			return nil, nil, workspaceRetain(ErrWorkspaceFiles, f.Close)
		}
		return nil, nil, ErrWorkspaceFiles
	}
	return f, b, nil
}
func workspacePublish(path string, b []byte, replace bool) error {
	temp := path + "." + uuid() + ".pending"
	f, e := workspaceOpen(temp, windows.GENERIC_WRITE, windows.CREATE_NEW, 0)
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
	ce := f.Close()
	if ce != nil {
		return workspaceRetain(ErrWorkspaceFiles, func() error {
			if f.Close() != nil {
				return ErrWorkspaceFiles
			}
			return workspaceRemove(temp)
		})
	}
	if e != nil {
		_ = workspaceRemove(temp)
		return ErrWorkspaceFiles
	}
	a, _ := pathName(temp)
	z, _ := pathName(path)
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	if windows.MoveFileEx(a, z, flags) != nil {
		_ = workspaceRemove(temp)
		return ErrWorkspaceFiles
	}
	return nil
}
func workspaceRemove(path string) error {
	p, e := pathName(path)
	if e != nil || windows.DeleteFile(p) != nil {
		return ErrWorkspaceFiles
	}
	return nil
}
