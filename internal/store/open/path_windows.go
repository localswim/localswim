//go:build windows && amd64

package open

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pathGuard pins every existing ancestor against rename/delete and rejects
// reparse traversal. The OS lock handle provides exclusion; its file's existence
// conveys no ownership and requires no PID/sentinel recovery protocol.
type pathGuard struct {
	path    string
	handles []windows.Handle
}

func (g *pathGuard) close() error {
	var errs []error
	for i := len(g.handles) - 1; i >= 0; i-- {
		errs = append(errs, windows.CloseHandle(g.handles[i]))
	}
	g.handles = nil
	return errors.Join(errs...)
}

func acquirePath(dir string, create bool) (*pathGuard, error) {
	return acquirePathMode(dir, create, false, false)
}

func acquireInspectionPath(dir string) (*pathGuard, error) {
	return acquirePathMode(dir, false, true, false)
}

func acquireUpgradePath(dir string) (*pathGuard, error) {
	return acquirePathMode(dir, false, false, true)
}

func acquirePathMode(dir string, create, inspect, upgrade bool) (*pathGuard, error) {
	if !safePath(dir) {
		return nil, errors.New("unsupported storage path")
	}
	g := &pathGuard{path: filepath.Join(dir, "board.sqlite")}
	ok := false
	defer func() {
		if !ok {
			g.close()
		}
	}()
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, errors.New("storage account unavailable")
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		return nil, errors.New("storage account unavailable")
	}
	sid := u.User.Sid
	sd, err := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return nil, errors.New("storage ACL unavailable")
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	parts := strings.Split(dir[3:], `\`)
	cur := dir[:3]
	for j := -1; j < len(parts); j++ {
		if j >= 0 {
			cur = filepath.Join(cur, parts[j])
		}
		h, e := openHandle(cur, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.OPEN_EXISTING, nil, true)
		if e != nil {
			return nil, errors.New("storage path lease unavailable")
		}
		g.handles = append(g.handles, h)
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return nil, errors.New("unsupported storage directory")
		}
	}
	root := g.handles[len(g.handles)-1]
	var fs [32]uint16
	if windows.GetVolumeInformationByHandle(root, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))) != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		return nil, errors.New("storage requires local NTFS")
	}
	if windows.GetDriveType(windows.StringToUTF16Ptr(dir[:3])) != windows.DRIVE_FIXED {
		return nil, errors.New("storage requires fixed local drive")
	}
	if err = privateACL(root, sid, true); err != nil {
		return nil, err
	}
	if err = inheritableDirectoryACL(root); err != nil {
		return nil, err
	}
	// Reject unfamiliar contents before touching SQLite: sidecars can otherwise
	// cause implicit journal/WAL recovery. Recovery is a separately scoped action.
	entries, err := directoryNames(dir)
	if err != nil {
		return nil, errors.New("storage inventory unavailable")
	}
	for _, name := range entries {
		if name != "board.sqlite" && name != "board.lock" {
			return nil, errors.New("storage requires recovery or has unknown files")
		}
	}
	lockAccess, lockDisposition := uint32(windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL), uint32(windows.OPEN_ALWAYS)
	dbAccess := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE | windows.READ_CONTROL)
	if upgrade {
		lockDisposition = windows.OPEN_EXISTING
		sa = nil
	}
	if inspect {
		// Existing-only inspection never creates an exclusion file. Real data
		// access (not attribute-only access) makes omitted delete sharing bind.
		lockAccess, lockDisposition = windows.GENERIC_READ|windows.READ_CONTROL, windows.OPEN_EXISTING
		dbAccess = windows.GENERIC_READ | windows.READ_CONTROL
		sa = nil
	}
	lock, e := openHandle(filepath.Join(dir, "board.lock"), lockAccess, 0, lockDisposition, sa, false)
	if e != nil {
		return nil, errors.New("storage exclusion unavailable")
	}
	g.handles = append(g.handles, lock)
	if err = regularPrivate(lock, sid); err != nil {
		return nil, err
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if create {
		disposition = windows.CREATE_NEW
	}
	h, e := openHandle(g.path, dbAccess, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, disposition, sa, false)
	if e != nil {
		return nil, errors.New("storage database unavailable or already exists")
	}
	g.handles = append(g.handles, h)
	if err = regularPrivate(h, sid); err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil || info.NumberOfLinks != 1 {
		return nil, errors.New("storage file aliases unsupported")
	}
	if !create {
		// Refuse malformed or WAL headers before the SQLite binding can perform
		// any implicit recovery or sidecar creation. Full validation follows.
		f, e := os.Open(g.path)
		if e != nil {
			return nil, errors.New("storage header unavailable")
		}
		var header [100]byte
		_, e = io.ReadFull(f, header[:])
		f.Close()
		if e != nil || string(header[:16]) != "SQLite format 3\x00" || header[18] != 1 || header[19] != 1 {
			return nil, errors.New("unsupported storage header or recovery required")
		}
	}
	ok = true
	return g, nil
}

func openHandle(path string, access, share, disposition uint32, sa *windows.SecurityAttributes, dir bool) (windows.Handle, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return windows.InvalidHandle, e
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if dir {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	return windows.CreateFile(p, access, share, sa, disposition, flags, 0)
}

func regularPrivate(h windows.Handle, sid *windows.SID) error {
	var i windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &i) != nil || i.NumberOfLinks != 1 || i.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return errors.New("unsupported storage file")
	}
	return privateACL(h, sid, true)
}

func inheritableDirectoryACL(h windows.Handle) error {
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return errors.New("storage directory inheritance unavailable")
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil {
		return errors.New("storage directory inheritance unavailable")
	}
	for n := uint32(0); n < uint32(acl.AceCount); n++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, n, &ace) != nil || ace.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			return errors.New("storage directory must inherit private ACL")
		}
	}
	return nil
}

func privateACL(h windows.Handle, sid *windows.SID, protected bool) error {
	return verifyACL(h, sid, protected, false)
}
func verifyACL(h windows.Handle, sid *windows.SID, protected, inherited bool) error {
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return errors.New("storage ACL unavailable")
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !owner.Equals(sid) {
		return errors.New("storage owner mismatch")
	}
	control, _, e := sd.Control()
	if e != nil || (protected && control&windows.SE_DACL_PROTECTED == 0) {
		return errors.New("storage DACL must be protected")
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		return errors.New("storage DACL mismatch")
	}
	system, e := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if e != nil {
		return errors.New("storage system identity unavailable")
	}
	seenUser, seenSystem := false, false
	for j := uint32(0); j < uint32(acl.AceCount); j++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, j, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x1f01ff || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || (!inherited && ace.Header.AceFlags&windows.INHERITED_ACE != 0) {
			return errors.New("storage ACE mismatch")
		}
		as := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if as.Equals(sid) {
			seenUser = true
		} else if as.Equals(system) {
			seenSystem = true
		} else {
			return errors.New("storage ACL principal mismatch")
		}
	}
	if !seenUser || !seenSystem {
		return errors.New("storage ACL principals missing")
	}
	return nil
}

// A deliberately narrow native path vocabulary avoids drive-relative, extended,
// UNC, alternate-stream, DOS device, 8.3 and trailing-dot/space aliases. Same-user
// hostile file replacement is outside the ACL boundary; pinned handles still
// prevent accidental ancestor redirection and main-file replacement.
func safePath(p string) bool {
	if len(p) < 4 || len(p) > 220 || p[1] != ':' || p[2] != '\\' || !(p[0] >= 'A' && p[0] <= 'Z' || p[0] >= 'a' && p[0] <= 'z') || filepath.Clean(p) != p {
		return false
	}
	for _, v := range strings.Split(p[3:], `\`) {
		if v == "" || v == "." || v == ".." || strings.HasSuffix(v, " ") || strings.HasSuffix(v, ".") || strings.ContainsAny(v, `:/*?"<>|~`) {
			return false
		}
		base := strings.ToUpper(strings.SplitN(v, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
			return false
		}
	}
	return true
}
