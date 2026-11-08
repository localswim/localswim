//go:build windows && amd64

package installation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

type workspaceEntry struct {
	h        windows.Handle
	path     string
	info     windows.ByHandleFileInformation
	security string
}
type nativeWorkspace struct {
	root, runtime string
	policy        *pins
	entries       map[string]*workspaceEntry
	failed        bool
	// Retain a failed enumeration close without ever retrying its numeric handle.
	quarantinedFind windows.Handle
}

func strictPrivate(h windows.Handle, user, system *windows.SID) (string, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user) {
		return "", ErrUnavailable
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return "", ErrUnavailable
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 2 {
		return "", ErrUnavailable
	}
	seenUser, seenSystem := false, false
	for j := uint32(0); j < 2; j++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, j, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < 20 || ace.Mask != 0x1f01ff || ace.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			return "", ErrUnavailable
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			return "", ErrUnavailable
		}
		if sid.Equals(user) {
			seenUser = true
		} else if sid.Equals(system) {
			seenSystem = true
		} else {
			return "", ErrUnavailable
		}
	}
	if !seenUser || !seenSystem {
		return "", ErrUnavailable
	}
	return sd.String(), nil
}

func createNativeWorkspace(ctx context.Context, profile string, user *windows.SID, runtimeGeneration, probeGeneration string) (*nativeWorkspace, func() error, error) {
	w := &nativeWorkspace{entries: make(map[string]*workspaceEntry)}
	cleanup := closeOnce(w.cleanup)
	fail := func(err error) (*nativeWorkspace, func() error, error) { return w, cleanup, err }
	for _, s := range []string{runtimeGeneration, probeGeneration} {
		id, err := uuid.Parse(s)
		if err != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || id.String() != s {
			return fail(ErrStale)
		}
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fail(err)
	}
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fail(err)
	}
	installer, _, _, err := windows.LookupSID("", "NT SERVICE\\TrustedInstaller")
	if err != nil {
		return fail(err)
	}
	w.policy = &pins{user: user, system: system, admin: admin, installer: installer}
	base := profile
	if _, err = w.policy.chain(ctx, base); err != nil {
		return fail(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.String() + "D:P(A;OICI;FA;;;" + user.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return fail(err)
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	for _, name := range []string{".localswim-native-runtime"} {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		base = filepath.Join(base, name)
		ptr, _ := windows.UTF16PtrFromString(base)
		err = windows.CreateDirectory(ptr, sa)
		created := err == nil
		if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return fail(err)
		}
		p, e := w.policy.open(base, true, false)
		if e != nil {
			if created {
				w.failed = true
			}
			return fail(e)
		}
		if _, e = strictPrivate(p.h, user, system); e != nil {
			if created {
				w.failed = true
			}
			return fail(e)
		}
	}
	create := func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		if err = windows.CreateDirectory(ptr, sa); err != nil {
			return err
		} // Exclusive; never adopt a stale run.
		entry, err := w.open(path)
		if err != nil {
			w.failed = true
			return err
		}
		if _, err = strictPrivate(entry.h, user, system); err != nil {
			w.failed = true
			return err
		}
		return nil
	}
	runtime := filepath.Join(base, "runtime-"+runtimeGeneration)
	if err = create(runtime); err != nil {
		return fail(err)
	}
	w.runtime = runtime
	root := filepath.Join(runtime, "probe-"+probeGeneration)
	if err = create(root); err != nil {
		w.failed = true
		return fail(err)
	}
	w.root = root
	for _, name := range []string{"home", "local", "roaming", "temp", "codex"} {
		if err = create(filepath.Join(root, name)); err != nil {
			w.failed = true
			return fail(err)
		}
	}
	if err = w.recheck(); err != nil {
		return fail(err)
	}
	return w, cleanup, nil
}

// open uses OPEN_REPARSE_POINT and denies share-delete. Cleanup later marks the
// exact held object for deletion, rather than reopening a pathname to delete it.
func (w *nativeWorkspace) open(path string) (*workspaceEntry, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(ptr, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, err
	}
	e := &workspaceEntry{h: h, path: path}
	w.entries[strings.ToLower(path)] = e
	if windows.GetFileInformationByHandle(h, &e.info) != nil {
		return nil, ErrUnavailable
	}
	typ, err := windows.GetFileType(h)
	if err != nil || typ != windows.FILE_TYPE_DISK {
		return nil, ErrUnavailable
	}
	final, err := finalPath(h)
	if err != nil || !strings.EqualFold(final, path) {
		return nil, ErrUnavailable
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	e.security = sd.String()
	return e, nil
}
func (w *nativeWorkspace) recheck() error {
	if w.failed || w.policy == nil || !w.policy.unchanged() {
		return ErrIdentityChanged
	}
	for _, entry := range w.entries {
		var now windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(entry.h, &now) != nil || now.VolumeSerialNumber != entry.info.VolumeSerialNumber || now.FileIndexHigh != entry.info.FileIndexHigh || now.FileIndexLow != entry.info.FileIndexLow || now.FileAttributes != entry.info.FileAttributes || now.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return ErrIdentityChanged
		}
		security, err := strictPrivate(entry.h, w.policy.user, w.policy.system)
		if err != nil || security != entry.security {
			return ErrIdentityChanged
		}
	}
	return nil
}
func (w *nativeWorkspace) names(path string) ([]string, error) {
	ptr, err := windows.UTF16PtrFromString(filepath.Join(path, "*"))
	if err != nil {
		return nil, err
	}
	var data windows.Win32finddata
	h, err := windows.FindFirstFile(ptr, &data)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for {
		name := windows.UTF16ToString(data.FileName[:])
		if name != "." && name != ".." {
			if name == "" || strings.ContainsAny(name, `\/:`) || len(names) >= 4096 {
				err = ErrIncomplete
				break
			}
			names = append(names, name)
		}
		err = windows.FindNextFile(h, &data)
		if err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				err = nil
			}
			break
		}
	}
	if windows.FindClose(h) != nil {
		w.quarantinedFind = h
		return nil, ErrIncomplete
	}
	return names, err
}
func (w *nativeWorkspace) remove(path string, depth int) error {
	if depth > 32 {
		return ErrIncomplete
	}
	entry := w.entries[strings.ToLower(path)]
	if entry == nil {
		var err error
		entry, err = w.open(path)
		if err != nil {
			return err
		}
	}
	// Never enumerate a reparse object, including a child-created junction. Its
	// held link object alone is detached; a canary outside the root is untouched.
	if entry.info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 && entry.info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		names, err := w.names(path)
		if err != nil {
			return err
		}
		for _, name := range names {
			if err = w.remove(filepath.Join(path, name), depth+1); err != nil {
				return err
			}
		}
	}
	disposition := byte(1)
	if err := windows.SetFileInformationByHandle(entry.h, windows.FileDispositionInfo, &disposition, 1); err != nil {
		return err
	}
	if err := windows.CloseHandle(entry.h); err != nil {
		w.failed = true
		return ErrIncomplete
	}
	delete(w.entries, strings.ToLower(path))
	return nil
}
func (w *nativeWorkspace) cleanup() error {
	if w.failed || w.quarantinedFind != 0 {
		return ErrIncomplete
	}
	if w.runtime != "" {
		if err := w.recheck(); err != nil {
			return err
		}
		if err := w.remove(w.runtime, 0); err != nil {
			w.failed = true
			return ErrIncomplete
		}
	}
	if len(w.entries) != 0 {
		return ErrIncomplete
	}
	if w.policy != nil {
		return w.policy.close()
	}
	return nil
}
