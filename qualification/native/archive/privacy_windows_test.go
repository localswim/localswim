//go:build nativequalification && windows && amd64

package archivequalification

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"
)

func privateAttempt(t *testing.T, root string) string {
	t.Helper()
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal("native test identity unavailable")
	}
	sid := user.User.Sid
	check := func(path string) {
		t.Helper()
		sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if e != nil {
			t.Fatal("private root metadata unavailable")
		}
		owner, _, e := sd.Owner()
		if e != nil || owner == nil || !owner.Equals(sid) {
			t.Fatal("private root owner mismatch")
		}
		control, _, e := sd.Control()
		if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatal("protected private test root required")
		}
		acl, _, e := sd.DACL()
		if e != nil || acl == nil || acl.AceCount != 2 {
			t.Fatal("exact private test root ACL required")
		}
		seenUser, seenSystem := false, false
		for j := uint32(0); j < 2; j++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if windows.GetAce(acl, j, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x1f01ff || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
				t.Fatal("private test ACL mismatch")
			}
			who := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if who.Equals(sid) {
				seenUser = true
			} else if who.IsWellKnown(windows.WinLocalSystemSid) {
				seenSystem = true
			} else {
				t.Fatal("unexpected test root principal")
			}
		}
		if !seenUser || !seenSystem {
			t.Fatal("private test principals missing")
		}
	}
	check(root)
	sd, e := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal("private test security descriptor unavailable")
	}
	attrs := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	attempt := filepath.Join(root, "a3-"+string(id())[:8])
	name, e := windows.UTF16PtrFromString(attempt)
	if e != nil || windows.CreateDirectory(name, attrs) != nil {
		t.Fatal("private attempt CREATE_NEW failed")
	}
	runtime.KeepAlive(sd)
	check(attempt)
	return attempt
}
