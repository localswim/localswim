//go:build windows && amd64

package local

import (
	"errors"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

func attributes() (*windows.SecurityAttributes, error) {
	t, e := windows.OpenCurrentProcessToken()
	if e != nil {
		return nil, errLocal
	}
	defer t.Close()
	u, e := t.GetTokenUser()
	if e != nil {
		return nil, errLocal
	}
	sid := u.User.Sid.String()
	sd, e := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)")
	if e != nil {
		return nil, errLocal
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}
func pathName(p string) (*uint16, error) { return windows.UTF16PtrFromString(p) }
func checkPrivate(h windows.Handle, dir bool) error {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != dir || !dir && info.NumberOfLinks != 1 {
		return errLocal
	}
	t, e := windows.OpenCurrentProcessToken()
	if e != nil {
		return errLocal
	}
	defer t.Close()
	u, e := t.GetTokenUser()
	if e != nil {
		return errLocal
	}
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return errLocal
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !owner.Equals(u.User.Sid) {
		return errLocal
	}
	if dir {
		control, _, e := sd.Control()
		if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
			return errLocal
		}
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		return errLocal
	}
	sys, e := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if e != nil {
		return errLocal
	}
	userSeen, sysSeen := false, false
	for j := uint32(0); j < uint32(acl.AceCount); j++ {
		var a *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, j, &a) != nil || a.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || a.Mask != 0x1f01ff || a.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			return errLocal
		}
		if dir && a.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			return errLocal
		}
		sid := (*windows.SID)(unsafe.Pointer(&a.SidStart))
		if sid.Equals(u.User.Sid) {
			userSeen = true
		} else if sid.Equals(sys) {
			sysSeen = true
		} else {
			return errLocal
		}
	}
	if !userSeen || !sysSeen {
		return errLocal
	}
	return nil
}
func safeDir(p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || len(p) < 4 || len(p) > 180 || p[1] != ':' || p[2] != '\\' {
		return false
	}
	for _, part := range strings.Split(p[3:], `\`) {
		if part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:/*?"<>|~`) {
			return false
		}
	}
	return true
}

// pinRoot refuses reparse ancestors and keeps them pinned until release.
func pinRoot(root string, create bool) (func(), error) {
	if !safeDir(root) {
		return nil, errLocal
	}
	var hs []windows.Handle
	release := func() {
		for j := len(hs) - 1; j >= 0; j-- {
			_ = windows.CloseHandle(hs[j])
		}
	}
	cur := root[:3]
	parts := strings.Split(root[3:], `\`)
	for j := -1; j < len(parts); j++ {
		if j >= 0 {
			cur = filepath.Join(cur, parts[j])
		}
		p, e := pathName(cur)
		if e != nil {
			release()
			return nil, errLocal
		}
		if j == len(parts)-1 && create {
			sa, e := attributes()
			if e != nil {
				release()
				return nil, e
			}
			e = windows.CreateDirectory(p, sa)
			if e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
				release()
				return nil, errLocal
			}
		}
		h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			release()
			return nil, errLocal
		}
		hs = append(hs, h)
		var i windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &i) != nil || i.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || i.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			release()
			return nil, errLocal
		}
		if j == len(parts)-1 && checkPrivate(h, true) != nil {
			release()
			return nil, errLocal
		}
	}
	return release, nil
}
func privateDir(p string) error {
	sa, e := attributes()
	if e != nil {
		return e
	}
	ptr, e := pathName(p)
	if e != nil {
		return errLocal
	}
	if e = windows.CreateDirectory(ptr, sa); e != nil {
		return errLocal
	}
	return nil
}
func openPrivate(p string, access, disposition, share uint32) (*os.File, error) {
	sa, e := attributes()
	if e != nil {
		return nil, e
	}
	ptr, e := pathName(p)
	if e != nil {
		return nil, errLocal
	}
	h, e := windows.CreateFile(ptr, access|windows.READ_CONTROL, share, sa, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, errLocal
	}
	if e = checkPrivate(h, false); e != nil {
		windows.CloseHandle(h)
		return nil, e
	}
	return os.NewFile(uintptr(h), p), nil
}
func lockRoot(root string) (func(), error) {
	f, e := openPrivate(filepath.Join(root, "local.lock"), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_ALWAYS, 0)
	if e != nil {
		return nil, e
	}
	return func() { _ = f.Close() }, nil
}
func readPrivate(p string) ([]byte, error) {
	f, e := openPrivate(p, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_SHARE_READ)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil || len(b) > 16384 {
		return nil, errLocal
	}
	return b, nil
}
func publish(p string, b []byte, replace bool) error {
	temp := p + "." + uuid() + ".pending"
	f, e := openPrivate(temp, windows.GENERIC_WRITE, windows.CREATE_NEW, 0)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return errLocal
	}
	a, _ := pathName(temp)
	z, _ := pathName(p)
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	if windows.MoveFileEx(a, z, flags) != nil {
		return errLocal
	}
	return nil
}

// Status must never create a missing exclusion file.
func lockExistingRoot(root string) (func(), error) {
	f, e := openPrivate(filepath.Join(root, "local.lock"), windows.GENERIC_READ, windows.OPEN_EXISTING, 0)
	if e != nil {
		return nil, e
	}
	return func() { _ = f.Close() }, nil
}
func readPrivateBounded(p string, limit int64) ([]byte, error) {
	f, e := openPrivate(p, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_SHARE_READ)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, errLocal
	}
	return b, nil
}
