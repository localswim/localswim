//go:build windows && amd64

package publication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"

	R "github.com/localswim/localswim/internal/archive/restore"
	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
	"golang.org/x/sys/windows"
)

var errNative = errors.New("offline restore native validation failed")

const receiptName = "restore-receipt.json"

type native struct {
	sid   *windows.SID
	sd    *windows.SECURITY_DESCRIPTOR
	fault func(string) error
}

func newNative() (*native, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, errNative
	}
	sid, e := u.User.Sid.Copy()
	if e != nil {
		return nil, errNative
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		return nil, errNative
	}
	return &native{sid: sid, sd: sd}, nil
}

func (n *native) inject(stage string) error {
	if n.fault != nil {
		return n.fault(stage)
	}
	return nil
}
func (n *native) attrs() *windows.SecurityAttributes {
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: n.sd}
}
func pointer(p string) *uint16 { x, _ := windows.UTF16PtrFromString(p); return x }

func safePath(p string) bool {
	if !utf8.ValidString(p) || strings.ContainsRune(p, 0) || len(p) < 4 || len(p) > 220 || p[1] != ':' || p[2] != '\\' || !(p[0] >= 'A' && p[0] <= 'Z' || p[0] >= 'a' && p[0] <= 'z') || filepath.Clean(p) != p {
		return false
	}
	for _, s := range strings.Split(p[3:], `\`) {
		if s == "" || s == "." || s == ".." || strings.TrimRight(s, " .") != s || strings.ContainsAny(s, `:/*?"<>|~`) {
			return false
		}
		base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
			return false
		}
	}
	return true
}
func overlap(a, b string) bool {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	return a == b || strings.HasPrefix(a, b+`\`) || strings.HasPrefix(b, a+`\`)
}

func (n *native) check(h windows.Handle, dir, private bool) error {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != dir || (!dir && info.NumberOfLinks != 1) {
		return errNative
	}
	if !private {
		return nil
	}
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return errNative
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !owner.Equals(n.sid) {
		return errNative
	}
	control, _, e := sd.Control()
	if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errNative
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		return errNative
	}
	user, system := false, false
	for j := uint32(0); j < uint32(acl.AceCount); j++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, j, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x1f01ff || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			return errNative
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(n.sid) {
			user = true
		} else if sid.IsWellKnown(windows.WinLocalSystemSid) {
			system = true
		} else {
			return errNative
		}
	}
	if !user || !system {
		return errNative
	}
	return nil
}

// pin walks and opens the reparse object itself. Non-delete-shared ancestor
// handles remain held for the whole operation, preventing path redirection.
func (n *native) pin(dir string, private bool) (func(), error) {
	if !safePath(dir) {
		return nil, errNative
	}
	if windows.GetDriveType(pointer(dir[:3])) != windows.DRIVE_FIXED {
		return nil, errNative
	}
	var held []windows.Handle
	closeAll := func() {
		for j := len(held) - 1; j >= 0; j-- {
			windows.CloseHandle(held[j])
		}
	}
	cur := dir[:3]
	parts := append([]string{""}, strings.Split(dir[3:], `\`)...)
	for _, part := range parts {
		if part != "" {
			cur = filepath.Join(cur, part)
		}
		h, e := windows.CreateFile(pointer(cur), windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			closeAll()
			return nil, errNative
		}
		held = append(held, h)
		if e = n.check(h, true, private && strings.EqualFold(cur, dir)); e != nil {
			closeAll()
			return nil, e
		}
		var normalized [32768]uint16
		count, e := windows.GetFinalPathNameByHandle(h, &normalized[0], uint32(len(normalized)), 0)
		actual := strings.TrimPrefix(windows.UTF16ToString(normalized[:]), `\\?\`)
		if e != nil || count >= uint32(len(normalized)) || !strings.EqualFold(strings.TrimRight(actual, `\`), strings.TrimRight(cur, `\`)) {
			closeAll()
			return nil, errNative
		}
	}
	var fs [32]uint16
	if windows.GetVolumeInformationByHandle(held[len(held)-1], nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))) != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		closeAll()
		return nil, errNative
	}
	return closeAll, nil
}
func (n *native) mkdir(path string) error {
	if !safePath(path) {
		return errNative
	}
	if e := windows.CreateDirectory(pointer(path), n.attrs()); e != nil {
		return errNative
	}
	runtime.KeepAlive(n.sd)
	close, e := n.pin(path, true)
	if e != nil {
		return e
	}
	close()
	return nil
}
func (n *native) file(path string, write, create, private, allowDelete bool) (*os.File, error) {
	access := uint32(windows.GENERIC_READ | windows.READ_CONTROL | windows.FILE_READ_ATTRIBUTES)
	if write {
		access |= windows.GENERIC_WRITE
	}
	share := uint32(windows.FILE_SHARE_READ)
	if allowDelete {
		share |= windows.FILE_SHARE_DELETE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	var attrs *windows.SecurityAttributes
	if create {
		disposition = windows.CREATE_NEW
		attrs = n.attrs()
	}
	h, e := windows.CreateFile(pointer(path), access, share, attrs, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(n.sd)
	if e != nil {
		return nil, e
	}
	if e = n.check(h, false, private); e != nil {
		windows.CloseHandle(h)
		return nil, e
	}
	return os.NewFile(uintptr(h), "offline restore material"), nil
}
func (n *native) write(path string, raw []byte) error {
	f, e := n.file(path, true, true, true, false)
	if e != nil {
		return errNative
	}
	nn, e := f.Write(raw)
	if nn != len(raw) && e == nil {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = n.inject("file-flush")
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return errNative
	}
	if n.inject("file-readback") != nil {
		return errNative
	}
	f, e = n.file(path, false, false, true, false)
	if e != nil {
		return errNative
	}
	got, e := io.ReadAll(io.LimitReader(f, int64(len(raw))+1))
	ce = f.Close()
	if e != nil || ce != nil || !bytes.Equal(raw, got) {
		return errNative
	}
	return nil
}
func (n *native) receipt(root string) (A.Receipt, error) {
	f, e := n.file(filepath.Join(root, receiptName), false, false, true, false)
	if e != nil {
		return A.Receipt{}, errNative
	}
	raw, e := io.ReadAll(io.LimitReader(f, V.MaxJSONBytes+1))
	ce := f.Close()
	if e != nil || ce != nil || len(raw) > V.MaxJSONBytes {
		return A.Receipt{}, errNative
	}
	return A.DecodeReadable[A.Receipt](raw)
}
func (n *native) update(root string, old, next A.Receipt) error {
	// Verify immutable fields and monotone phase before replacing our own receipt.
	a, b := old, next
	a.TargetInventory = nil
	b.TargetInventory = nil
	a.Phase = "preparing"
	b.Phase = "preparing"
	a.Result = "incomplete"
	b.Result = "incomplete"
	a.FailureCode = nil
	b.FailureCode = nil
	a.UpdatedAt = a.CreatedAt
	b.UpdatedAt = b.CreatedAt
	ax, _ := A.Canonical(a)
	bx, _ := A.Canonical(b)
	if !bytes.Equal(ax, bx) || old.Result != "incomplete" || next.UpdatedAt < old.UpdatedAt {
		return errNative
	}
	order := map[A.Phase]int{"preparing": 0, "prepared": 1, "published": 2}
	if order[next.Phase] < order[old.Phase] {
		return errNative
	}
	if old.TargetInventory != nil {
		x, _ := A.Canonical(old.TargetInventory)
		y, _ := A.Canonical(next.TargetInventory)
		if !bytes.Equal(x, y) {
			return errNative
		}
	}
	current, e := n.receipt(root)
	if e != nil {
		return e
	}
	x, _ := A.Canonical(current)
	y, _ := A.Canonical(old)
	if !bytes.Equal(x, y) {
		return errNative
	}
	raw, e := A.Readable(next)
	if e != nil {
		return e
	}
	temp := filepath.Join(root, receiptName+".next")
	if e = n.write(temp, raw); e != nil {
		return e
	}
	if e = windows.MoveFileEx(pointer(temp), pointer(filepath.Join(root, receiptName)), windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); e != nil {
		return errNative
	}
	current, e = n.receipt(root)
	if e != nil {
		return e
	}
	x, _ = A.Canonical(current)
	y, _ = A.Canonical(next)
	if !bytes.Equal(x, y) {
		return errNative
	}
	return nil
}
func stamp(now func() time.Time) A.Timestamp {
	return A.Timestamp(now().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z"))
}
func absent(path string) (bool, error) {
	_, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return true, nil
	}
	if e != nil {
		return false, errNative
	}
	return false, nil
}
func inventory(root string, allowMissingData bool) error {
	entries, e := os.ReadDir(root)
	if e != nil {
		return errNative
	}
	seen := map[string]bool{}
	for _, v := range entries {
		if v.Name() != receiptName && v.Name() != "data" && v.Name() != "restore.lock" {
			return errNative
		}
		seen[v.Name()] = true
	}
	if !seen[receiptName] || !seen["restore.lock"] || (!allowMissingData && !seen["data"]) {
		return errNative
	}
	return nil
}

func leasePath(destination string, request A.UUID) string {
	return filepath.Join(filepath.Dir(destination), ".restore-"+string(request)+".lock")
}
func leaseBinding(destination string, request A.UUID) []byte {
	// Fixed-format local binding contains no prose, credential or raw path. Case
	// folds only Windows path selection; archive/domain text is never normalized.
	x := sha256.Sum256([]byte(string(request) + "\n" + strings.ToLower(destination) + "\n" + strings.ToLower(destination+".partial-"+string(request)) + "\n"))
	return []byte(hex.EncodeToString(x[:]) + "\n")
}
func (n *native) lease(destination string, request A.UUID, create bool) (*os.File, error) {
	path := leasePath(destination, request)
	if !safePath(path) {
		return nil, errNative
	}
	f, e := n.file(path, true, create, true, false)
	if e != nil {
		return nil, errNative
	}
	want := leaseBinding(destination, request)
	if create {
		written, e := f.Write(want)
		if e != nil || written != len(want) {
			f.Close()
			return nil, errNative
		}
		if e = f.Sync(); e != nil {
			f.Close()
			return nil, errNative
		}
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		f.Close()
		return nil, errNative
	}
	got, e := io.ReadAll(io.LimitReader(f, 66))
	if e != nil || !bytes.Equal(want, got) {
		f.Close()
		return nil, errNative
	}
	return f, nil
}

type fileID struct{ volume, high, low uint32 }

func handleID(h windows.Handle) (fileID, error) {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil {
		return fileID{}, errNative
	}
	return fileID{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}, nil
}
func (n *native) directoryID(path string) (fileID, error) {
	h, e := windows.CreateFile(pointer(path), windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return fileID{}, errNative
	}
	defer windows.CloseHandle(h)
	if e = n.check(h, true, true); e != nil {
		return fileID{}, e
	}
	return handleID(h)
}

func nativeRestore(ctx context.Context, q Request, now func() time.Time) Response {
	return restoreWithFault(ctx, q, now, nil)
}

func restoreWithFault(ctx context.Context, q Request, now func() time.Time, fault func(string) error) Response {
	unknown := conclusion("unknown", nil)
	if ctx == nil || ctx.Err() != nil || A.Validate(q.RequestID) != nil || !safePath(q.SourceArchive) || !safePath(q.Destination) {
		return unknown
	}
	partial := q.Destination + ".partial-" + string(q.RequestID)
	if !safePath(partial) || overlap(q.Destination, filepath.Dir(q.SourceArchive)) {
		return unknown
	}
	for _, root := range q.ProtectedRoots {
		if !safePath(root) || overlap(q.Destination, root) || overlap(partial, root) {
			return unknown
		}
	}
	n, e := newNative()
	if e != nil {
		return unknown
	}
	n.fault = fault
	parents, e := n.pin(filepath.Dir(q.Destination), false)
	if e != nil {
		return unknown
	}
	defer parents()
	sourceParents, e := n.pin(filepath.Dir(q.SourceArchive), false)
	if e != nil {
		return unknown
	}
	defer sourceParents()
	for _, path := range []string{q.Destination, partial} {
		empty, e := absent(path)
		if e != nil || !empty {
			return unknown
		}
	}
	source, e := n.file(q.SourceArchive, false, false, false, false)
	if e != nil {
		return unknown
	}
	defer source.Close()
	validated, e := V.Read(ctx, source)
	if e != nil {
		if errors.Is(e, V.ErrInvalid) || errors.Is(e, V.ErrLimit) {
			return conclusion("failed", nil)
		}
		return unknown
	}
	prepared, e := R.Prepare(ctx, validated, stamp(now))
	if e != nil {
		if ctx.Err() == nil {
			return conclusion("failed", nil)
		}
		return unknown
	}
	r, e := prepared.Receipt(q.RequestID)
	if e != nil {
		return unknown
	}
	sibling, e := n.lease(q.Destination, q.RequestID, true)
	if e != nil {
		return unknown
	}
	defer sibling.Close()
	siblingID, e := handleID(windows.Handle(sibling.Fd()))
	if e != nil {
		return unknown
	}
	// No destination is created until whole-source validation and fresh identity.
	if e = n.mkdir(partial); e != nil {
		return unknown
	}
	lock, e := n.file(filepath.Join(partial, "restore.lock"), true, true, true, true)
	if e != nil {
		return unknown
	}
	defer lock.Close()
	raw, e := A.Readable(r)
	if e != nil || n.write(filepath.Join(partial, receiptName), raw) != nil {
		return unknown
	}
	if n.inject("after-preparing") != nil {
		return conclusion("unknown", &r)
	}
	rootPin, e := n.pin(partial, true)
	if e != nil {
		return unknown
	}
	e = n.inject("construction-error")
	if e == nil {
		e = n.mkdir(filepath.Join(partial, "data"))
	}
	if e == nil {
		e = prepared.Construct(ctx, filepath.Join(partial, "data"))
	}
	if e != nil {
		rootPin()
		if ctx.Err() != nil {
			return conclusion("unknown", &r)
		}
		failed := r
		failed.Result = "failed"
		code := "PREPARATION_FAILED"
		failed.FailureCode = &code
		failed.UpdatedAt = stamp(now)
		if inventory(partial, true) != nil || n.update(partial, r, failed) != nil {
			return conclusion("unknown", &r)
		}
		return conclusion("failed", &failed)
	}
	if n.inject("after-construct") != nil {
		rootPin()
		return conclusion("unknown", &r)
	}
	// Reproject after close/reopen. The expected transform must agree before the
	// immutable logical anchor is fixed in the prepared receipt.
	anchor, e := prepared.NativeInventory(ctx, filepath.Join(partial, "data"))
	if e != nil {
		rootPin()
		return conclusion("unknown", &r)
	}
	preparedReceipt := r
	preparedReceipt.Phase = "prepared"
	preparedReceipt.TargetInventory = &anchor
	preparedReceipt.UpdatedAt = stamp(now)
	if e = n.update(partial, r, preparedReceipt); e != nil {
		rootPin()
		return conclusion("unknown", &r)
	}
	r = preparedReceipt
	if n.inject("after-prepared") != nil {
		rootPin()
		return conclusion("unknown", &r)
	}
	if e = R.ValidateNative(ctx, filepath.Join(partial, "data"), r); e != nil {
		rootPin()
		return conclusion("unknown", &r)
	}
	if ctx.Err() != nil {
		rootPin()
		return conclusion("unknown", &r)
	}
	// Release only the candidate directory pin for the native rename. Parent
	// chains, source handles and the deletion-shared exclusive lock stay held.
	rootPin()
	if n.inject("before-rename") != nil {
		return conclusion("unknown", &r)
	}
	partialID, e := n.directoryID(partial)
	if e != nil {
		return conclusion("unknown", &r)
	}
	rootLockID, e := handleID(windows.Handle(lock.Fd()))
	if e != nil {
		return conclusion("unknown", &r)
	}
	// NTFS directory publication cannot keep a descendant file open. The sibling
	// lease remains exclusive and path-bound while the root lock is closed.
	if e = lock.Close(); e != nil {
		return conclusion("unknown", &r)
	}
	if e = windows.MoveFileEx(pointer(partial), pointer(q.Destination), windows.MOVEFILE_WRITE_THROUGH); e != nil {
		return conclusion("unknown", &r)
	}
	if n.inject("after-rename") != nil {
		return conclusion("unknown", &r)
	}
	finalID, e := n.directoryID(q.Destination)
	if e != nil || finalID != partialID {
		return conclusion("unknown", &r)
	}
	lock, e = n.file(filepath.Join(q.Destination, "restore.lock"), true, false, true, false)
	if e != nil {
		return conclusion("unknown", &r)
	}
	defer lock.Close()
	newRootLockID, e := handleID(windows.Handle(lock.Fd()))
	if e != nil || newRootLockID != rootLockID {
		return conclusion("unknown", &r)
	}
	newSiblingID, e := handleID(windows.Handle(sibling.Fd()))
	if e != nil || newSiblingID != siblingID {
		return conclusion("unknown", &r)
	}
	finalPin, e := n.pin(q.Destination, true)
	if e != nil {
		return conclusion("unknown", &r)
	}
	defer finalPin()
	if inventory(q.Destination, false) != nil || R.ValidateNative(ctx, filepath.Join(q.Destination, "data"), r) != nil {
		return conclusion("unknown", &r)
	}
	if n.check(windows.Handle(sibling.Fd()), false, true) != nil || n.check(windows.Handle(lock.Fd()), false, true) != nil || n.check(windows.Handle(source.Fd()), false, false) != nil {
		return conclusion("unknown", &r)
	}
	published := r
	published.Phase = "published"
	published.Result = "complete"
	published.UpdatedAt = stamp(now)
	if e = n.update(q.Destination, r, published); e != nil {
		return conclusion("unknown", &r)
	}
	// Complete evidence is never downgraded due to later output/delivery failure.
	return conclusion("confirmed", &published)
}

func nativeStatus(ctx context.Context, destination string, request A.UUID) Response {
	unknown := conclusion("unknown", nil)
	partial := destination + ".partial-" + string(request)
	if ctx == nil || ctx.Err() != nil || A.Validate(request) != nil || !safePath(destination) || !safePath(partial) {
		return unknown
	}
	n, e := newNative()
	if e != nil {
		return unknown
	}
	parent, e := n.pin(filepath.Dir(destination), false)
	if e != nil {
		return unknown
	}
	defer parent()
	sibling, e := n.lease(destination, request, false)
	if e != nil {
		return unknown
	}
	defer sibling.Close()
	finalAbsent, e := absent(destination)
	if e != nil {
		return unknown
	}
	partialAbsent, e := absent(partial)
	if e != nil || finalAbsent == partialAbsent {
		return unknown
	}
	root := destination
	if finalAbsent {
		root = partial
	}
	pin, e := n.pin(root, true)
	if e != nil {
		return unknown
	}
	defer pin()
	lock, e := n.file(filepath.Join(root, "restore.lock"), true, false, true, false)
	if e != nil {
		return unknown
	}
	defer lock.Close()
	r, e := n.receipt(root)
	if e != nil || r.RequestID != request {
		return unknown
	}
	if inventory(root, r.Phase == "preparing") != nil {
		return conclusion("unknown", &r)
	}
	if finalAbsent && r.Result == "failed" && r.Phase != "published" {
		return conclusion("failed", &r)
	}
	if !finalAbsent && r.Phase == "published" && r.Result == "complete" && R.ValidateNative(ctx, filepath.Join(root, "data"), r) == nil {
		return conclusion("confirmed", &r)
	}
	return conclusion("unknown", &r)
}
