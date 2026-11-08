//go:build windows && amd64

package installation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	A "github.com/localswim/localswim/internal/contract/archive"
	"golang.org/x/sys/windows"
)

const maxExecutable = 536870912

var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func validVersion(s string) bool {
	if len(s) > 96 || !versionRE.MatchString(s) {
		return false
	}
	pre, ok := strings.CutPrefix(strings.SplitN(s, "+", 2)[0], strings.SplitN(s, "-", 2)[0]+"-")
	if ok {
		for _, part := range strings.Split(pre, ".") {
			numeric := true
			for _, c := range part {
				numeric = numeric && c >= '0' && c <= '9'
			}
			if numeric && len(part) > 1 && part[0] == '0' {
				return false
			}
		}
	}
	return true
}

type packageManifest struct {
	LayoutVersion A.Version `json:"layoutVersion"`
	Version       string    `json:"version"`
	Target        string    `json:"target"`
	Variant       string    `json:"variant"`
	Entrypoint    string    `json:"entrypoint"`
	ResourcesDir  string    `json:"resourcesDir"`
	PathDir       string    `json:"pathDir"`
}

func manifestValid(b []byte, release string) bool {
	m, e := A.Decode[packageManifest](b)
	return e == nil && len(b) <= 4096 && m.LayoutVersion == 1 && validVersion(m.Version) && release == m.Version+"-x86_64-pc-windows-msvc" && m.Target == "x86_64-pc-windows-msvc" && m.Variant == "codex" && m.Entrypoint == "bin/codex.exe" && m.ResourcesDir == "codex-resources" && m.PathDir == "codex-path"
}

type pin struct {
	h        windows.Handle
	path     string
	info     windows.ByHandleFileInformation
	security string
	digest   [32]byte
}
type pins struct {
	items                          []*pin
	user, system, admin, installer *windows.SID
	trust                          *trustState
	closeFailed                    bool
}

func (p *pins) close() error {
	if p.closeFailed {
		return ErrIncomplete
	}
	if p.trust != nil {
		if e := p.trust.close(); e != nil {
			p.closeFailed = true
			return e
		}
		p.trust = nil
	}
	for len(p.items) > 0 {
		n := len(p.items) - 1
		if e := windows.CloseHandle(p.items[n].h); e != nil {
			p.closeFailed = true
			return e
		}
		p.items = p.items[:n]
	}
	return nil
}
func cleanLocal(path string) bool {
	if len(path) < 3 || len(path) > 4096 || path[1] != ':' || path[2] != '\\' || filepath.Clean(path) != path || strings.Count(path, `\`) > 32 {
		return false
	}
	for _, part := range strings.Split(path[3:], `\`) {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:/*?"<>|~`) {
			return false
		}
	}
	return true
}
func finalPath(h windows.Handle) (string, error) {
	b := make([]uint16, 4097)
	n, e := windows.GetFinalPathNameByHandle(h, &b[0], uint32(len(b)), 0)
	if e != nil || n == 0 || n >= uint32(len(b)) {
		return "", errors.New("path unavailable")
	}
	s := strings.TrimPrefix(windows.UTF16ToString(b[:n]), `\\?\`)
	if !cleanLocal(s) {
		return "", errors.New("path unqualified")
	}
	return s, nil
}
func (p *pins) trusted(sid *windows.SID, root bool) bool {
	return sid != nil && (sid.Equals(p.user) || sid.Equals(p.system) || sid.Equals(p.admin) || root && sid.Equals(p.installer))
}

// security reads the owner independently; a read-only DACL cannot legitimize a foreign owner.
func (p *pins) security(h windows.Handle, root bool) (string, bool, error) {
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return "", false, e
	}
	return p.validateSecurity(sd, root)
}
func (p *pins) validateSecurity(sd *windows.SECURITY_DESCRIPTOR, root bool) (string, bool, error) {
	if sd == nil {
		return "", false, errors.New("missing security descriptor")
	}
	owner, _, e := sd.Owner()
	if e != nil || !p.trusted(owner, root) {
		return "", false, errors.New("owner unqualified")
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount > 256 {
		return "", false, errors.New("acl unqualified")
	}
	control, _, e := sd.Control()
	if e != nil {
		return "", false, e
	}
	// Read, execute, synchronize and READ_CONTROL are nonmutating. Everything else fails closed.
	const readMask = uint32(0x001200a9)
	const genericReadExecute = uint32(0xa0000000)
	for j := uint32(0); j < uint32(acl.AceCount); j++ {
		var a *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, j, &a) != nil || a.Header.AceSize < 20 || a.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return "", false, errors.New("ace unqualified")
		}
		if a.Header.AceFlags & ^byte(0x1f) != 0 || uint32(a.Mask) & ^uint32(0xf01f01ff) != 0 {
			return "", false, errors.New("unknown ACE bits")
		}
		sid := (*windows.SID)(unsafe.Pointer(&a.SidStart))
		if !sid.IsValid() {
			return "", false, errors.New("sid unqualified")
		}
		if a.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if p.trusted(sid, root) {
			continue
		}
		mask := uint32(a.Mask)
		if mask&^(readMask|genericReadExecute) != 0 {
			if !(root && mask == 4) {
				return "", false, errors.New("mutation principal unqualified")
			}
		}
	}
	return sd.String(), control&windows.SE_DACL_PROTECTED != 0, nil
}
func (p *pins) open(path string, dir, alias bool) (*pin, error) {
	access := uint32(windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL)
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if dir {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	} else {
		access |= windows.GENERIC_READ
		share = windows.FILE_SHARE_READ
	}
	if alias {
		share = windows.FILE_SHARE_READ
	}
	ptr, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(ptr, access, share, nil, windows.OPEN_EXISTING, flags, 0)
	if e != nil {
		return nil, e
	}
	x := &pin{h: h, path: path}
	p.items = append(p.items, x)
	if windows.GetFileInformationByHandle(h, &x.info) != nil {
		return nil, errors.New("file information unavailable")
	}
	typ, e := windows.GetFileType(h)
	if e != nil || typ != windows.FILE_TYPE_DISK || ((x.info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY) != 0) != dir || !dir && x.info.NumberOfLinks != 1 {
		return nil, errors.New("file type unqualified")
	}
	if !alias && x.info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, errors.New("reparse unqualified")
	}
	security, _, e := p.security(h, len(path) == 3)
	if e != nil {
		return nil, e
	}
	x.security = security
	return x, nil
}
func (p *pins) chain(ctx context.Context, path string) (*pin, error) {
	if !cleanLocal(path) {
		return nil, errors.New("path unqualified")
	}
	cur := path[:3]
	parts := strings.Split(path[3:], `\`)
	var result *pin
	for j := -1; j < len(parts); j++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if j >= 0 {
			cur = filepath.Join(cur, parts[j])
		}
		x, e := p.open(cur, true, false)
		if e != nil {
			return nil, e
		}
		if j == 0 {
			_, protected, e := p.security(x.h, false)
			if e != nil || !protected {
				return nil, errors.New("unprotected root child")
			}
		}
		result = x
	}
	return result, nil
}
func (p *pins) unchanged() bool {
	for _, x := range p.items {
		var now windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(x.h, &now) != nil || now.VolumeSerialNumber != x.info.VolumeSerialNumber || now.FileIndexHigh != x.info.FileIndexHigh || now.FileIndexLow != x.info.FileIndexLow || now.FileAttributes != x.info.FileAttributes || now.CreationTime != x.info.CreationTime || now.NumberOfLinks != x.info.NumberOfLinks {
			return false
		}
		// Unrelated directory entries may change; only file payload metadata is stable.
		if now.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 && (now.FileSizeHigh != x.info.FileSizeHigh || now.FileSizeLow != x.info.FileSizeLow || now.LastWriteTime != x.info.LastWriteTime) {
			return false
		}
		s, _, e := p.security(x.h, len(x.path) == 3)
		if e != nil || s != x.security {
			return false
		}
	}
	return true
}
func readPinned(x *pin, limit int64) ([]byte, error) {
	// Sequential reads use the same pinned handle; no independent pathname is reopened.
	size := int64(x.info.FileSizeHigh)<<32 | int64(x.info.FileSizeLow)
	if size < 0 || size > limit {
		return nil, errors.New("file size unqualified")
	}
	if _, e := windows.Seek(x.h, 0, io.SeekStart); e != nil {
		return nil, e
	}
	b := make([]byte, size)
	var got uint32
	if len(b) > 0 {
		if e := windows.ReadFile(x.h, b, &got, nil); e != nil || int(got) != len(b) {
			return nil, errors.New("read unavailable")
		}
	}
	return b, nil
}
func hashPE(ctx context.Context, x *pin) bool {
	if _, e := windows.Seek(x.h, 0, io.SeekStart); e != nil {
		return false
	}
	size := int64(x.info.FileSizeHigh)<<32 | int64(x.info.FileSizeLow)
	if size < 256 || size > maxExecutable {
		return false
	}
	header := make([]byte, 256)
	var n uint32
	if windows.ReadFile(x.h, header, &n, nil) != nil || n != 256 || string(header[:2]) != "MZ" {
		return false
	}
	offset := int64(binary.LittleEndian.Uint32(header[60:64]))
	if offset < 64 || offset > size-256 {
		return false
	}
	if _, e := windows.Seek(x.h, offset, io.SeekStart); e != nil {
		return false
	}
	if windows.ReadFile(x.h, header, &n, nil) != nil || n != 256 || string(header[:4]) != "PE\x00\x00" || binary.LittleEndian.Uint16(header[4:6]) != 0x8664 || binary.LittleEndian.Uint16(header[24:26]) != 0x20b {
		return false
	}
	flags := binary.LittleEndian.Uint16(header[22:24])
	if flags&2 == 0 || flags&0x2000 != 0 || binary.LittleEndian.Uint64(header[24+112+14*8:24+112+15*8]) != 0 {
		return false
	}
	if _, e := windows.Seek(x.h, 0, io.SeekStart); e != nil {
		return false
	}
	hash := sha256.New()
	buf := make([]byte, 65536)
	var total int64
	for total < size {
		if ctx.Err() != nil {
			return false
		}
		want := int64(len(buf))
		if size-total < want {
			want = size - total
		}
		if windows.ReadFile(x.h, buf[:want], &n, nil) != nil || n == 0 {
			return false
		}
		_, _ = hash.Write(buf[:n])
		total += int64(n)
	}
	copy(x.digest[:], hash.Sum(nil))
	return total == size
}
func inspectNative(ctx context.Context) (Observation, func() error) {
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		return Unqualified("INSPECTION_UNKNOWN"), nil
	}
	closeToken := func() error { return token.Close() }
	user, e := token.GetTokenUser()
	if e != nil {
		return Unqualified("INSPECTION_UNKNOWN"), closeOnce(closeToken)
	}
	profile, e := token.GetUserProfileDirectory()
	if e != nil {
		return Unqualified("INSPECTION_UNKNOWN"), closeOnce(closeToken)
	}
	observation, cleanup := inspectProfile(ctx, profile, user.User.Sid)
	return observation, closeOnce(cleanup, closeToken)
}
func inspectProfile(ctx context.Context, profile string, user *windows.SID) (Observation, func() error) {
	sys, e := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if e != nil {
		return Unqualified("INSPECTION_UNKNOWN"), nil
	}
	admin, e := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if e != nil {
		return Unqualified("INSPECTION_UNKNOWN"), nil
	}
	installer, _, _, e := windows.LookupSID("", "NT SERVICE\\TrustedInstaller")
	if e != nil {
		return Unqualified("INSPECTION_UNKNOWN"), nil
	}
	p := &pins{user: user, system: sys, admin: admin, installer: installer}
	fail := func(code string) (Observation, func() error) { return Unqualified(code), p.close }
	base := filepath.Join(profile, ".codex", "packages", "standalone")
	if _, e = p.chain(ctx, base); e != nil {
		return fail("PATH_UNQUALIFIED")
	}
	slot := filepath.Join(base, "current")
	alias, e := p.open(slot, true, true)
	if e != nil {
		if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
			return Observation{"missing", "TOOL_MISSING"}, p.close
		}
		return fail("PATH_UNQUALIFIED")
	}
	if alias.info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		var tag struct{ Attributes, ReparseTag uint32 }
		if windows.GetFileInformationByHandleEx(alias.h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&tag)), uint32(unsafe.Sizeof(tag))) != nil || tag.ReparseTag != windows.IO_REPARSE_TAG_MOUNT_POINT {
			return fail("PATH_UNQUALIFIED")
		}
	}
	ptr, _ := windows.UTF16PtrFromString(slot)
	resolved, e := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if e != nil {
		return fail("SOURCE_UNQUALIFIED")
	}
	p.items = append(p.items, &pin{h: resolved, path: ""})
	target, e := finalPath(resolved)
	if e != nil {
		return fail("SOURCE_UNQUALIFIED")
	}
	releases := filepath.Join(base, "releases")
	if !strings.EqualFold(filepath.Dir(target), releases) {
		return fail("SOURCE_UNQUALIFIED")
	}
	release := filepath.Base(target)
	if _, e = p.chain(ctx, target); e != nil {
		return fail("PATH_UNQUALIFIED")
	}
	if _, e = p.chain(ctx, filepath.Join(target, "bin")); e != nil {
		return fail("PATH_UNQUALIFIED")
	}
	manifest, e := p.open(filepath.Join(target, "codex-package.json"), false, false)
	if e != nil {
		return fail("SOURCE_UNQUALIFIED")
	}
	b, e := readPinned(manifest, 4096)
	if e != nil || !manifestValid(b, release) {
		return fail("SOURCE_UNQUALIFIED")
	}
	executable, e := p.open(filepath.Join(target, "bin", "codex.exe"), false, false)
	if e != nil {
		if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
			return Observation{"missing", "TOOL_MISSING"}, p.close
		}
		return fail("PATH_UNQUALIFIED")
	}
	if !hashPE(ctx, executable) {
		return fail("PATH_UNQUALIFIED")
	}
	firstDigest := executable.digest
	state, code := verifyTrust(executable)
	p.trust = state
	if code != "" {
		return fail(code)
	}
	afterManifest, readErr := readPinned(manifest, 4096)
	if readErr != nil {
		return fail("INSPECTION_UNKNOWN")
	}
	if !bytes.Equal(b, afterManifest) || !hashPE(ctx, executable) || firstDigest != executable.digest {
		return fail("FILE_CHANGED")
	}
	// The resolved alias handle pins the target too; give it a complete snapshot for recheck.
	for _, x := range p.items {
		if x.path == "" {
			x.path = target
			if windows.GetFileInformationByHandle(x.h, &x.info) != nil {
				return fail("INSPECTION_UNKNOWN")
			}
			x.security, _, e = p.security(x.h, false)
			if e != nil {
				return fail("PATH_UNQUALIFIED")
			}
		}
	}
	if !p.unchanged() {
		return fail("FILE_CHANGED")
	}
	return Observation{"candidate", "CANDIDATE_PRESENT"}, p.close
}
