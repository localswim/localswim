//go:build windows && amd64

package setup

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeFS struct {
	root string
	sid  *windows.SID
	sd   *windows.SECURITY_DESCRIPTOR
}

func nativeRoot() (string, error) {
	p, e := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if e != nil {
		return "", fail("PLATFORM_UNQUALIFIED", 3)
	}
	return filepath.Join(p, "localswim", "slice01"), nil
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
	return &nativeFS{root, sid, sd}, nil
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
	b, e := io.ReadAll(io.LimitReader(f, 128*1024+1))
	if e != nil || len(b) > 128*1024 {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return b, nil
}
func (n *nativeFS) write(rel string, b []byte) error {
	f, e := n.open(rel, windows.GENERIC_WRITE, windows.CREATE_NEW)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	read, e := n.read(rel)
	if e != nil || string(read) != string(b) {
		return fail("INVOCATION_OR_IO", 1)
	}
	return nil
}
func (n *nativeFS) move(from, to string, replace bool) error {
	a, e := n.pinChain(filepath.Dir(n.path(from)), false)
	if e != nil {
		return e
	}
	defer a()
	b, e := n.pinChain(filepath.Dir(n.path(to)), false)
	if e != nil {
		return e
	}
	defer b()
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(ptr(n.path(from)), ptr(n.path(to)), flags)
}
func (n *nativeFS) replace(rel string, b []byte) error {
	temp := rel + ".next"
	if e := n.write(temp, b); e != nil {
		return e
	}
	return n.move(temp, rel, true)
}
func (n *nativeFS) list(rel string) ([]os.DirEntry, error) {
	c, e := n.pinChain(n.path(rel), false)
	if e != nil {
		return nil, e
	}
	defer c()
	return os.ReadDir(n.path(rel))
}
func (n *nativeFS) lock(create bool) (func(), error) {
	if create {
		f, e := n.open("install.lock", windows.GENERIC_READ|windows.GENERIC_WRITE, windows.CREATE_NEW)
		if e == nil {
			f.Close()
		} else if !errors.Is(e, windows.ERROR_FILE_EXISTS) && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
			if errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
				return nil, fail("INSTALLATION_BUSY", 3)
			}
			return nil, e
		}
	}
	f, e := n.open("install.lock", windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_EXISTING)
	if errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
		return nil, fail("INSTALLATION_BUSY", 3)
	}
	if e != nil {
		return nil, e
	}
	// No sharing of WRITE plus the held handle provides cross-process exclusion.
	return func() { f.Close() }, nil
}
func (n *nativeFS) busy() string {
	f, e := n.lock(false)
	if e == nil {
		f()
		return "free"
	}
	if failure(e).Code == "INSTALLATION_BUSY" {
		return "busy"
	}
	return "unavailable"
}
func (n *nativeFS) cleanup(tx string) error {
	// Only transaction metadata is removed. Candidate generations are retained.
	rel := filepath.Join("pending", tx, "transaction.json")
	metadata, e := n.read(rel)
	if e != nil {
		return e
	}
	entries, e := n.list(filepath.Join("pending", tx))
	if e != nil || len(entries) != 1 || entries[0].Name() != "transaction.json" {
		return fail("SETUP_RECOVERY_REQUIRED", 4)
	}
	if e := windows.DeleteFile(ptr(n.path(filepath.Join("pending", tx, "transaction.json")))); e != nil {
		return e
	}
	if e = windows.RemoveDirectory(ptr(n.path(filepath.Join("pending", tx)))); e != nil {
		// Retain exact reconciliation evidence after a returned cleanup failure.
		// If restoration also fails, preserve the directory and stop unknown;
		// there is no claim every process/power-loss boundary can auto-recover.
		_ = n.write(rel, metadata)
		return e
	}
	return nil
}

// bindBoth uses explicit Windows socket options before either bind. It closes
// both sockets before returning; success is an observation, never reservation.
func bindBoth(port int) error {
	config := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
		var x error
		e := raw.Control(func(fd uintptr) {
			x = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, ^windows.SO_REUSEADDR, 1)
			if x == nil && network == "tcp6" {
				x = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, windows.IPV6_V6ONLY, 1)
			}
		})
		if e != nil {
			return e
		}
		return x
	}}
	a, e := config.Listen(context.Background(), "tcp4", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if e != nil {
		return fail("PORT_UNAVAILABLE", 3)
	}
	defer a.Close()
	b, e := config.Listen(context.Background(), "tcp6", net.JoinHostPort("::1", fmtPort(port)))
	if e != nil {
		return fail("PORT_UNAVAILABLE", 3)
	}
	return b.Close()
}
