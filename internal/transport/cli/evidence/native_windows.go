//go:build windows && amd64

package evidence

import (
	"errors"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

func fail(_ string, _ int) error { return ErrUnavailable }

type nativeFS struct {
	root      string
	sid       *windows.SID
	sd        *windows.SECURITY_DESCRIPTOR
	flushFile func(*os.File) error
	readback  func(string) ([]byte, error)
}

func newNativeFS(root string) (*nativeFS, error) {
	// No UNC, device namespace, relative path, ADS, dot aliases or trailing names.
	if !filepath.IsAbs(root) || len(root) < 3 || root[1] != ':' || filepath.Clean(root) != root || strings.Contains(root[2:], ":") || strings.ContainsAny(root, "/") {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	for _, s := range strings.Split(root[3:], `\`) {
		if s == "" || strings.TrimRight(s, " .") != s {
			return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
	}
	v, _ := windows.UTF16PtrFromString(root[:3])
	var fs [32]uint16
	if windows.GetDriveType(v) != windows.DRIVE_FIXED || windows.GetVolumeInformation(v, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))) != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		return nil, fail("PLATFORM_UNQUALIFIED", 3)
	}
	token := windows.GetCurrentProcessToken()
	user, e := token.GetTokenUser()
	if e != nil {
		return nil, fail("PLATFORM_UNQUALIFIED", 3)
	}
	sid, e := user.User.Sid.Copy()
	if e != nil {
		return nil, e
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		return nil, e
	}
	return &nativeFS{root: root, sid: sid, sd: sd}, nil
}
func (n *nativeFS) attrs() *windows.SecurityAttributes {
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: n.sd}
}
func ptr(p string) *uint16                 { x, _ := windows.UTF16PtrFromString(p); return x }
func (n *nativeFS) path(rel string) string { return filepath.Join(n.root, rel) }
func (n *nativeFS) checkHandle(h windows.Handle, directory, private bool) error {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil {
		return fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	if !safeFileInfo(info, directory) {
		return fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	if !private {
		return nil
	}
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	owner, _, e := sd.Owner()
	if e != nil || !owner.Equals(n.sid) {
		return fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	control, _, e := sd.Control()
	if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		return fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	seenUser, seenSystem := false, false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x001f01ff || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			return fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(n.sid):
			seenUser = true
		case sid.IsWellKnown(windows.WinLocalSystemSid):
			seenSystem = true
		default:
			return fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
	}
	if !seenUser || !seenSystem {
		return fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return nil
}
func safeFileInfo(info windows.ByHandleFileInformation, directory bool) bool {
	return info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 && (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) == directory && (directory || info.NumberOfLinks == 1)
}

// pinChain holds non-delete-shared directory handles until the operation ends.
// Each ancestor is opened as the reparse object itself before descending.
func (n *nativeFS) pinChain(p string, create bool) (func(), error) {
	var held []windows.Handle
	closeAll := func() {
		for i := len(held) - 1; i >= 0; i-- {
			windows.CloseHandle(held[i])
		}
	}
	volume := filepath.VolumeName(p) + `\`
	current := volume
	parts := strings.Split(strings.TrimPrefix(p, volume), `\`)
	for _, part := range parts {
		current = filepath.Join(current, part)
		private := strings.EqualFold(current, n.root) || strings.HasPrefix(strings.ToLower(current), strings.ToLower(n.root)+`\`)
		h, e := windows.CreateFile(ptr(current), windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if (errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND)) && create && (private || strings.EqualFold(current, filepath.Dir(n.root))) {
			e = windows.CreateDirectory(ptr(current), n.attrs())
			runtime.KeepAlive(n.sd)
			if e == nil {
				h, e = windows.CreateFile(ptr(current), windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			}
		}
		if e != nil {
			closeAll()
			return nil, e
		}
		if e = n.checkHandle(h, true, private); e != nil {
			windows.CloseHandle(h)
			closeAll()
			return nil, e
		}
		var normalized [32768]uint16
		count, ne := windows.GetFinalPathNameByHandle(h, &normalized[0], uint32(len(normalized)), 0)
		actual := strings.TrimPrefix(windows.UTF16ToString(normalized[:]), `\\?\`)
		if ne != nil || count >= uint32(len(normalized)) || !strings.EqualFold(actual, current) {
			windows.CloseHandle(h)
			closeAll()
			return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
		held = append(held, h)
	}
	return closeAll, nil
}
func (n *nativeFS) rootPin(create bool) (func(), error) { return n.pinChain(n.root, create) }
func (n *nativeFS) validateFile(rel string) error {
	h, e := windows.CreateFile(ptr(n.path(rel)), windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	return n.checkHandle(h, false, true)
}
func (n *nativeFS) mkdir(rel string) error {
	c, e := n.pinChain(n.path(rel), true)
	if e != nil {
		return e
	}
	c()
	return nil
}
func (n *nativeFS) open(rel string, access, disposition uint32) (*os.File, error) {
	p := n.path(rel)
	closeDirs, e := n.pinChain(filepath.Dir(p), false)
	if e != nil {
		return nil, e
	}
	defer closeDirs()
	var sa *windows.SecurityAttributes
	if disposition == windows.CREATE_NEW {
		sa = n.attrs()
	}
	share := uint32(windows.FILE_SHARE_READ)
	if access&windows.GENERIC_WRITE == 0 {
		share |= windows.FILE_SHARE_DELETE
	}
	h, e := windows.CreateFile(ptr(p), access|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, share, sa, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(n.sd)
	if e != nil {
		return nil, e
	}
	if e = n.checkHandle(h, false, true); e != nil {
		windows.CloseHandle(h)
		return nil, e
	}
	return os.NewFile(uintptr(h), "private setup material"), nil
}
func (n *nativeFS) read(rel string) ([]byte, error) {
	f, e := n.open(rel, windows.GENERIC_READ, windows.OPEN_EXISTING)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, MaxRecord+1))
	if e != nil || len(b) > MaxRecord {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return b, nil
}
func (n *nativeFS) write(rel string, b []byte) error {
	f, e := n.open(rel, windows.GENERIC_WRITE, windows.CREATE_NEW)
	if e != nil {
		return e
	}
	written, err := f.Write(b)
	e = err
	if e == nil && written != len(b) {
		e = io.ErrShortWrite
	}
	if e == nil {
		if n.flushFile != nil {
			e = n.flushFile(f)
		} else {
			e = f.Sync()
		}
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	var read []byte
	if n.readback != nil {
		read, e = n.readback(rel)
	} else {
		read, e = n.read(rel)
	}
	if e != nil || string(read) != string(b) {
		return fail("INVOCATION_OR_IO", 1)
	}
	return nil
}
