//go:build windows && amd64

package core

import (
	"errors"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// The explicit private input is read once under pinned ordinary NTFS ancestry.
// No service-side runtime data, path search, stdin or environment fallback exists.
func readRecipientInput(path string) (out []byte, err error) {
	bad := errors.New("private recipient input unavailable")
	if path == "-" || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) < 4 || len(path) > 260 || path[1] != ':' || path[2] != '\\' {
		return nil, bad
	}
	for _, part := range strings.Split(path[3:], `\`) {
		if part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:/*?"<>|~`) {
			return nil, bad
		}
	}
	var owned []windows.Handle
	defer func() {
		for i := len(owned) - 1; i >= 0; i-- {
			if windows.CloseHandle(owned[i]) != nil {
				out = nil
				err = bad
			}
		}
	}()
	drive, _ := windows.UTF16PtrFromString(path[:3])
	var fs [32]uint16
	if windows.GetDriveType(drive) != windows.DRIVE_FIXED || windows.GetVolumeInformation(drive, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))) != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		return nil, bad
	}
	current := path[:3]
	parts := strings.Split(filepath.Dir(path)[3:], `\`)
	if filepath.Dir(path) == path[:3] {
		parts = nil
	}
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			current = filepath.Join(current, parts[i])
		}
		p, _ := windows.UTF16PtrFromString(current)
		h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			return nil, bad
		}
		owned = append(owned, h)
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return nil, bad
		}
		if !recipientCanonical(h, current) {
			return nil, bad
		}
	}
	p, _ := windows.UTF16PtrFromString(path)
	h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if e != nil {
		return nil, bad
	}
	f := os.NewFile(uintptr(h), "private-recipient-input")
	if f == nil {
		_ = windows.CloseHandle(h)
		return nil, bad
	}
	defer func() {
		if f.Close() != nil {
			out = nil
			err = bad
		}
	}()
	var info windows.ByHandleFileInformation
	kind, ke := windows.GetFileType(h)
	if ke != nil || kind != windows.FILE_TYPE_DISK || windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 || info.NumberOfLinks != 1 || info.FileSizeHigh != 0 || info.FileSizeLow > 1024 || !recipientCanonical(h, path) || !recipientPrivate(h) {
		return nil, bad
	}
	b, e := io.ReadAll(io.LimitReader(f, 1025))
	if e != nil || len(b) != int(info.FileSizeLow) || len(b) > 1024 {
		return nil, bad
	}
	return b, nil
}
func recipientCanonical(h windows.Handle, path string) bool {
	var final [32768]uint16
	n, e := windows.GetFinalPathNameByHandle(h, &final[0], uint32(len(final)), 0)
	return e == nil && n > 0 && n < uint32(len(final)) && strings.EqualFold(strings.TrimPrefix(windows.UTF16ToString(final[:n]), `\\?\`), path)
}
func recipientPrivate(h windows.Handle) bool {
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		return false
	}
	defer token.Close()
	user, e := token.GetTokenUser()
	if e != nil {
		return false
	}
	sys, e := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if e != nil {
		return false
	}
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return false
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return false
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		return false
	}
	u, s := false, false
	for j := uint32(0); j < uint32(acl.AceCount); j++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, j, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x1f01ff || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(user.User.Sid) {
			u = true
		} else if sid.Equals(sys) {
			s = true
		} else {
			return false
		}
	}
	return u && s
}
