//go:build windows && amd64 && k02_native

package evidence

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"golang.org/x/sys/windows"
)

func nativeStore(t *testing.T) *WindowsStore {
	t.Helper()
	base := os.Getenv("S01_TEST_ROOT")
	if !filepath.IsAbs(base) || os.Getenv("K02_NATIVE_OVERLAY") != "1" {
		t.Fatal("explicit private synthetic root required")
	}
	root, e := os.MkdirTemp(base, "k02-evidence-")
	if e != nil {
		t.Fatal("fixture unavailable")
	}
	s, e := NewWindowsStore(filepath.Join(root, "evidence"))
	if e != nil {
		t.Fatal("native provider unavailable")
	}
	return s
}
func numbered(t *testing.T, n int, description string) Record {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"opId": fmt.Sprintf("50000000-0000-0000-0000-%012d", n), "incarnation": incarnation, "target": nil, "expectedRevision": 5, "expectedCardRevision": nil, "input": map[string]string{"title": "Synthetic", "description": description}})
	selectedBoard := board
	if n%2 == 0 {
		selectedBoard = "00000000-0000-4000-8000-000000000099"
	}
	r, e := Freeze(Context{installation, "https://synthetic.localhost:47443"}, contract.ResolvedRoute{Command: "card.create", BoardID: selectedBoard}, contract.AuthenticatedActor{ActorID: actor}, b)
	if e != nil {
		t.Fatal("record unavailable")
	}
	return r
}
func terminalFor(t *testing.T, r Record) T.Terminal {
	t.Helper()
	v, e := T.Decode[T.Terminal](fixture(t, "command-card-create.json"))
	if e != nil {
		t.Fatal("terminal unavailable")
	}
	v.OpID = A.UUID(r.Identity().OpID)
	v.Fingerprint = A.Digest(r.Fingerprint())
	return v
}

func TestNativeExclusiveFlushReadbackAndSourcePreservation(t *testing.T) {
	for _, mode := range []string{"normal", "flush", "readback"} {
		t.Run(mode, func(t *testing.T) {
			s := nativeStore(t)
			r := record(t)
			if mode == "flush" {
				s.fs.flushFile = func(*os.File) error { return errors.New("injected flush failure") }
			}
			if mode == "readback" {
				s.fs.readback = func(string) ([]byte, error) { return []byte("corrupt"), nil }
			}
			path, e := s.Save(r)
			if mode != "normal" {
				if e == nil {
					t.Fatal("failed storage accepted")
				}
				if _, e = os.Stat(s.fs.path(r.Identity().OpID + ".json.pending")); e != nil {
					t.Fatal("failure destroyed source")
				}
				if !r.Valid() {
					t.Fatal("failure changed caller source")
				}
				return
			}
			x, e := s.Load(path)
			if e != nil || !bytes.Equal(x.Bytes(), r.Bytes()) {
				t.Fatal("native readback")
			}
			if _, e = s.Save(r); e == nil {
				t.Fatal("overwrote immutable evidence")
			}
			x, e = s.Load(path)
			if e != nil || !bytes.Equal(x.Bytes(), r.Bytes()) {
				t.Fatal("exclusive refusal changed original")
			}
			done, e := s.lock(false)
			if e != nil {
				t.Fatal("lock unavailable")
			}
			other, e := NewWindowsStore(s.fs.root)
			if e != nil {
				t.Fatal("second provider")
			}
			if _, e = other.Save(numbered(t, 2, "")); e == nil {
				t.Fatal("native cap lock bypass")
			}
			done()
		})
	}
}
func TestNativeACLHardlinkAndAncestorJunctionRefusals(t *testing.T) {
	s := nativeStore(t)
	r := record(t)
	path, e := s.Save(r)
	if e != nil {
		t.Fatal("save")
	}
	if e := os.Link(path, filepath.Join(filepath.Dir(s.fs.root), "alias.json")); e != nil {
		var errno syscall.Errno
		if errors.As(e, &errno) {
			t.Fatalf("hardlink fixture error %d", uint32(errno))
		}
		t.Fatal("hardlink fixture")
	}
	if _, e = s.Load(path); e == nil {
		t.Fatal("hardlinked evidence accepted")
	}
	other := nativeStore(t)
	p, e := other.Save(r)
	if e != nil {
		t.Fatal("save")
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if e != nil {
		t.Fatal("ACL fixture")
	}
	dacl, _, _ := sd.DACL()
	if windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil) != nil {
		t.Fatal("ACL mutation fixture")
	}
	if _, e = other.Load(p); e == nil {
		t.Fatal("broad ACL accepted")
	}
	j := filepath.Join(filepath.Dir(s.fs.root), "junction")
	if windows.CreateDirectory(ptr(j), s.fs.attrs()) != nil {
		t.Fatal("junction directory")
	}
	h, e := windows.CreateFile(ptr(j), windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal("junction handle")
	}
	sub := utf16.Encode([]rune(`\??\` + s.fs.root))
	display := utf16.Encode([]rune(s.fs.root))
	names := append(append(append(sub, 0), display...), 0)
	buf := make([]byte, 16+len(names)*2)
	binary.LittleEndian.PutUint32(buf, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:], uint16(len(sub)*2))
	binary.LittleEndian.PutUint16(buf[12:], uint16((len(sub)+1)*2))
	binary.LittleEndian.PutUint16(buf[14:], uint16(len(display)*2))
	for n, v := range names {
		binary.LittleEndian.PutUint16(buf[16+n*2:], v)
	}
	var returned uint32
	e = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
	windows.CloseHandle(h)
	if e != nil {
		t.Fatal("junction fixture")
	}
	alias, e := NewWindowsStore(filepath.Join(j, "child"))
	if e != nil {
		t.Fatal("lexical alias fixture")
	}
	if _, e = alias.Save(numbered(t, 3, "")); e == nil {
		t.Fatal("reparse ancestor accepted")
	}
}
func TestNativeCrossBoardCapsAndReservedResolution(t *testing.T) {
	s := nativeStore(t)
	var admitted []Record
	for n := 1; n <= 100; n++ {
		r := numbered(t, n, "")
		if _, e := s.Save(r); e != nil {
			t.Fatal("unresolved capacity reduced")
		}
		admitted = append(admitted, r)
	}
	if _, e := s.Save(numbered(t, 101, "")); e == nil {
		t.Fatal("unresolved cap bypass")
	}
	tm := terminalFor(t, admitted[0])
	if s.Resolve(admitted[0], tm) != nil {
		t.Fatal("at-cap resolution lacked reservation")
	}
	if _, e := s.Save(numbered(t, 101, "")); e != nil {
		t.Fatal("resolved evidence counted unresolved")
	}
	full := nativeStore(t)
	var first Record
	for n := 1; n <= 100; n++ {
		r := numbered(t, n, strings.Repeat("\n", 65536))
		if _, e := full.Save(r); e != nil {
			if n == 1 {
				t.Fatal("large record rejected")
			}
			break
		}
		if n == 1 {
			first = r
		}
	}
	if !first.Valid() || full.Resolve(first, terminalFor(t, first)) != nil {
		t.Fatal("byte-cap resolution failed")
	}
	// Concurrent instances contend on the same native OS lock; none may admit
	// beyond a shared limit. A loser fails closed and does not retry silently.
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for n := 102; n < 110; n++ {
		wg.Add(1)
		r := numbered(t, n, "")
		go func(r Record) {
			defer wg.Done()
			other, e := NewWindowsStore(s.fs.root)
			if e == nil {
				_, e = other.Save(r)
			}
			if e == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(r)
	}
	wg.Wait()
	if success != 0 {
		t.Fatal("concurrent cap bypass")
	}
}
func TestNativeResolvedRetentionAndUnresolvedPreservation(t *testing.T) {
	s := nativeStore(t)
	r := numbered(t, 1, "")
	path, e := s.Save(r)
	if e != nil || s.Resolve(r, terminalFor(t, r)) != nil {
		t.Fatal("resolution")
	}
	unresolved := numbered(t, 2, "")
	unresolvedPath, e := s.Save(unresolved)
	if e != nil {
		t.Fatal("unresolved")
	}
	if s.ExpireResolved(time.Now().Add(6*24*time.Hour)) != nil {
		t.Fatal("retention before boundary")
	}
	if _, e = s.Load(path); e != nil {
		t.Fatal("premature expiry")
	}
	if s.ExpireResolved(time.Now().Add(8*24*time.Hour)) != nil {
		t.Fatal("resolved expiry")
	}
	if _, e = s.Load(path); e == nil {
		t.Fatal("resolved evidence retained after requested expiry")
	}
	if _, e = s.Load(unresolvedPath); e != nil {
		t.Fatal("unresolved silently expired")
	}
}
