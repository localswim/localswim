//go:build windows && amd64

package archive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This is the same qualified owner+SYSTEM protected-DACL policy used by K02
// evidence and A04 publication. It applies only to newly saved CLI byte files.
// It neither repairs parent ACLs nor introduces a board permission decision.
type localFS struct {
	sid *windows.SID
	sd  *windows.SECURITY_DESCRIPTOR
}

func newLocalFS() (*localFS, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, errLocal
	}
	sid, e := u.User.Sid.Copy()
	if e != nil {
		return nil, errLocal
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		return nil, errLocal
	}
	return &localFS{sid, sd}, nil
}
func localPointer(p string) *uint16 { v, _ := windows.UTF16PtrFromString(p); return v }
func localPath(p string) bool {
	if !utf8.ValidString(p) || strings.ContainsRune(p, 0) || len(p) < 4 || len(p) > 220 || p[1] != ':' || p[2] != '\\' || !(p[0] >= 'A' && p[0] <= 'Z' || p[0] >= 'a' && p[0] <= 'z') || filepath.Clean(p) != p {
		return false
	}
	for _, s := range strings.Split(p[3:], `\`) {
		if s == "" || s == "." || s == ".." || strings.TrimRight(s, " .") != s || strings.ContainsAny(s, `:/*?"<>|~`) {
			return false
		}
		b := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
		if b == "CON" || b == "PRN" || b == "AUX" || b == "NUL" || len(b) == 4 && (strings.HasPrefix(b, "COM") || strings.HasPrefix(b, "LPT")) && b[3] >= '0' && b[3] <= '9' {
			return false
		}
	}
	return true
}
func localOverlap(a, b string) bool {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	return a == b || strings.HasPrefix(a, b+`\`) || strings.HasPrefix(b, a+`\`)
}
func (n *localFS) check(h windows.Handle, dir, private bool) error {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != dir || !dir && info.NumberOfLinks != 1 {
		return errLocal
	}
	if !private {
		return nil
	}
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return errLocal
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !owner.Equals(n.sid) {
		return errLocal
	}
	c, _, e := sd.Control()
	if e != nil || c&windows.SE_DACL_PROTECTED == 0 {
		return errLocal
	}
	a, _, e := sd.DACL()
	if e != nil || a == nil || a.AceCount != 2 {
		return errLocal
	}
	user, system := false, false
	for i := uint32(0); i < uint32(a.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(a, i, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x1f01ff || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			return errLocal
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(n.sid) {
			user = true
		} else if sid.IsWellKnown(windows.WinLocalSystemSid) {
			system = true
		} else {
			return errLocal
		}
	}
	if !user || !system {
		return errLocal
	}
	return nil
}
func (n *localFS) pin(dir string) (func(), error) {
	if !localPath(dir) || windows.GetDriveType(localPointer(dir[:3])) != windows.DRIVE_FIXED {
		return nil, errLocal
	}
	var held []windows.Handle
	closeAll := func() {
		for i := len(held) - 1; i >= 0; i-- {
			windows.CloseHandle(held[i])
		}
	}
	cur := dir[:3]
	parts := append([]string{""}, strings.Split(dir[3:], `\`)...)
	for _, p := range parts {
		if p != "" {
			cur = filepath.Join(cur, p)
		}
		h, e := windows.CreateFile(localPointer(cur), windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			closeAll()
			return nil, errLocal
		}
		held = append(held, h)
		if n.check(h, true, false) != nil {
			closeAll()
			return nil, errLocal
		}
		var norm [32768]uint16
		count, e := windows.GetFinalPathNameByHandle(h, &norm[0], uint32(len(norm)), 0)
		actual := strings.TrimPrefix(windows.UTF16ToString(norm[:]), `\\?\`)
		if e != nil || count >= uint32(len(norm)) || !strings.EqualFold(strings.TrimRight(actual, `\`), strings.TrimRight(cur, `\`)) {
			closeAll()
			return nil, errLocal
		}
	}
	var fs [32]uint16
	if windows.GetVolumeInformationByHandle(held[len(held)-1], nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))) != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		closeAll()
		return nil, errLocal
	}
	return closeAll, nil
}
func (n *localFS) file(path string, create, deleteShare, private bool) (*os.File, error) {
	access := uint32(windows.GENERIC_READ | windows.READ_CONTROL | windows.FILE_READ_ATTRIBUTES)
	share := uint32(0)
	disposition := uint32(windows.OPEN_EXISTING)
	var sa *windows.SecurityAttributes
	if create {
		access |= windows.GENERIC_WRITE
		disposition = windows.CREATE_NEW
		sa = &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: n.sd}
	}
	if deleteShare {
		share |= windows.FILE_SHARE_DELETE
	}
	h, e := windows.CreateFile(localPointer(path), access, share, sa, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(n.sd)
	if e != nil {
		return nil, errLocal
	}
	if n.check(h, false, private) != nil {
		windows.CloseHandle(h)
		return nil, errLocal
	}
	return os.NewFile(uintptr(h), "CLI local byte material"), nil
}
func nativeRead(ctx context.Context, path string, max int64) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || max < 0 || !localPath(path) {
		return nil, errLocal
	}
	n, e := newLocalFS()
	if e != nil {
		return nil, errLocal
	}
	release, e := n.pin(filepath.Dir(path))
	if e != nil {
		return nil, errLocal
	}
	defer release()
	f, e := n.file(path, false, false, false)
	if e != nil {
		return nil, errLocal
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max || ctx.Err() != nil || n.check(windows.Handle(f.Fd()), false, false) != nil {
		return nil, errLocal
	}
	return b, nil
}
func nativeSave(ctx context.Context, path string, raw []byte, roots []string, inject func(string) error) saveState {
	if ctx == nil || ctx.Err() != nil || !localPath(path) || roots == nil {
		return saveFailed
	}
	n, e := newLocalFS()
	if e != nil {
		return saveFailed
	}
	release, e := n.pin(filepath.Dir(path))
	if e != nil {
		return saveFailed
	}
	defer release()
	for _, root := range roots {
		if !localPath(root) || localOverlap(path, root) {
			return saveFailed
		}
	}
	// Candidate names are fresh per attempt. Neither an existing target nor an
	// existing partial is overwritten or removed after a failure.
	if _, e = os.Lstat(path); !os.IsNotExist(e) {
		return saveFailed
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return saveFailed
	}
	partial := path + ".partial-" + hex.EncodeToString(nonce[:])
	if !localPath(partial) {
		return saveFailed
	}
	f, e := n.file(partial, true, true, true)
	if e != nil {
		return saveFailed
	}
	defer f.Close()
	if nn, e := f.Write(raw); e != nil || nn != len(raw) {
		return saveFailed
	}
	if inject("file-flush") != nil || f.Sync() != nil {
		return saveFailed
	}
	if inject("file-readback") != nil {
		return saveFailed
	}
	if _, e = f.Seek(0, 0); e != nil {
		return saveFailed
	}
	got, e := io.ReadAll(io.LimitReader(f, int64(len(raw))+1))
	if e != nil || !bytes.Equal(raw, got) || n.check(windows.Handle(f.Fd()), false, true) != nil {
		return saveFailed
	}
	if ctx.Err() != nil || inject("before-rename") != nil {
		return saveFailed
	}
	// The original exclusive byte handle stays open while its name changes.
	// NTFS no-replace publication cannot race a target overwrite; read/write
	// access to the candidate remains denied throughout rename and validation.
	if windows.MoveFileEx(localPointer(partial), localPointer(path), windows.MOVEFILE_WRITE_THROUGH) != nil {
		return saveUnknown
	}
	if inject("after-rename") != nil || ctx.Err() != nil {
		return saveUnknown
	}
	var normalized [32768]uint16
	count, e := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &normalized[0], uint32(len(normalized)), 0)
	actual := strings.TrimPrefix(windows.UTF16ToString(normalized[:]), `\\?\`)
	if e != nil || count >= uint32(len(normalized)) || !strings.EqualFold(actual, path) || n.check(windows.Handle(f.Fd()), false, true) != nil {
		return saveUnknown
	}
	if inject("published-readback") != nil {
		return saveUnknown
	}
	if _, e = f.Seek(0, 0); e != nil {
		return saveUnknown
	}
	got, e = io.ReadAll(io.LimitReader(f, int64(len(raw))+1))
	if e != nil || !bytes.Equal(raw, got) {
		return saveUnknown
	}
	count, e = windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &normalized[0], uint32(len(normalized)), 0)
	actual = strings.TrimPrefix(windows.UTF16ToString(normalized[:]), `\\?\`)
	if e != nil || count >= uint32(len(normalized)) || !strings.EqualFold(actual, path) || n.check(windows.Handle(f.Fd()), false, true) != nil {
		return saveUnknown
	}
	if e = f.Close(); e != nil {
		return saveUnknown
	}
	return saveConfirmed
}
