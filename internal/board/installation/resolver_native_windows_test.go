//go:build windows && amd64 && nativequalification

package installation

import (
	"context"
	"encoding/binary"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func junctionFixture(t *testing.T, path, target string) {
	t.Helper()
	if e := os.MkdirAll(path, 0700); e != nil {
		t.Fatal(e)
	}
	name, _ := windows.UTF16PtrFromString(path)
	h, e := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(h)
	sub, _ := windows.UTF16FromString(`\??\` + target)
	print, _ := windows.UTF16FromString(target)
	b := make([]byte, 16+2*(len(sub)+len(print)))
	binary.LittleEndian.PutUint32(b, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(b[4:], uint16(len(b)-8))
	binary.LittleEndian.PutUint16(b[10:], uint16(2*(len(sub)-1)))
	binary.LittleEndian.PutUint16(b[12:], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(b[14:], uint16(2*(len(print)-1)))
	for j, v := range append(sub, print...) {
		binary.LittleEndian.PutUint16(b[16+j*2:], v)
	}
	var n uint32
	if e = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &b[0], uint32(len(b)), nil, 0, &n, nil); e != nil {
		t.Fatal(e)
	}
}
func TestNativeResolverRefusesPhysicalAndLayoutFaults(t *testing.T) {
	root := os.Getenv("M17_TRUSTED_FIXTURE_ROOT")
	if root == "" {
		t.Fatal("requires owned private M17_TRUSTED_FIXTURE_ROOT")
	}
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		t.Fatal(e)
	}
	defer token.Close()
	user, e := token.GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"missing", "plain-slot", "outside", "broken", "manifest", "multilink", "directory", "bin-junction", "unsigned"} {
		t.Run(kind, func(t *testing.T) {
			profile, e := os.MkdirTemp(root, "inspect-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(profile)
			base := filepath.Join(profile, ".codex", "packages", "standalone")
			target := filepath.Join(base, "releases", "0.1.0-x86_64-pc-windows-msvc")
			bin := filepath.Join(target, "bin")
			if e = os.MkdirAll(bin, 0700); e != nil {
				t.Fatal(e)
			}
			slot := filepath.Join(base, "current")
			if kind == "plain-slot" {
				if e = os.Mkdir(slot, 0700); e != nil {
					t.Fatal(e)
				}
			} else if kind != "missing" {
				destination := target
				if kind == "outside" {
					destination = profile
				}
				if kind == "broken" {
					destination = filepath.Join(base, "releases", "absent")
				}
				junctionFixture(t, slot, destination)
			}
			manifest := []byte(`{"layoutVersion":1,"version":"0.1.0","target":"x86_64-pc-windows-msvc","variant":"codex","entrypoint":"bin/codex.exe","resourcesDir":"codex-resources","pathDir":"codex-path"}`)
			if kind == "manifest" {
				manifest = []byte(`{"layoutVersion":1,"version":"wrong"}`)
			}
			if e = os.WriteFile(filepath.Join(target, "codex-package.json"), manifest, 0600); e != nil {
				t.Fatal(e)
			}
			exe := filepath.Join(bin, "codex.exe")
			data := make([]byte, 512)
			copy(data, "MZ")
			binary.LittleEndian.PutUint32(data[60:], 64)
			copy(data[64:], "PE\x00\x00")
			binary.LittleEndian.PutUint16(data[68:], 0x8664)
			binary.LittleEndian.PutUint16(data[86:], 2)
			binary.LittleEndian.PutUint16(data[88:], 0x20b)
			if kind == "directory" {
				if e = os.Mkdir(exe, 0700); e != nil {
					t.Fatal(e)
				}
			} else if e = os.WriteFile(exe, data, 0600); e != nil {
				t.Fatal(e)
			}
			if kind == "multilink" {
				a, _ := windows.UTF16PtrFromString(filepath.Join(bin, "second.exe"))
				b, _ := windows.UTF16PtrFromString(exe)
				if e = windows.CreateHardLink(a, b, 0); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "bin-junction" {
				if e = os.Rename(bin, filepath.Join(target, "other")); e != nil {
					t.Fatal(e)
				}
				junctionFixture(t, bin, filepath.Join(target, "other"))
			}
			v, cleanup := inspectProfile(context.Background(), profile, user.User.Sid)
			if cleanup != nil {
				if e = cleanup(); e != nil {
					t.Fatal("cleanup", e)
				}
			}
			codes := map[string]string{"missing": "TOOL_MISSING", "plain-slot": "SOURCE_UNQUALIFIED", "outside": "SOURCE_UNQUALIFIED", "broken": "SOURCE_UNQUALIFIED", "manifest": "SOURCE_UNQUALIFIED", "multilink": "PATH_UNQUALIFIED", "directory": "PATH_UNQUALIFIED", "bin-junction": "PATH_UNQUALIFIED", "unsigned": "PUBLISHER_UNQUALIFIED"}
			want := "unqualified"
			if kind == "missing" {
				want = "missing"
			}
			t.Logf("controlled case %s: %s/%s", kind, v.State, v.Code)
			if v.State != want || v.Code != codes[kind] {
				t.Fatal("unexpected observation", v)
			}
			if v.State == "candidate" {
				t.Fatal("synthetic fixture was qualified")
			}
		})
	}
}

func TestHeldMetadataDetectsACLChange(t *testing.T) {
	root := os.Getenv("M17_TRUSTED_FIXTURE_ROOT")
	if root == "" {
		t.Fatal("requires private fixture")
	}
	path := filepath.Join(root, "acl-change.txt")
	if e := os.WriteFile(path, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		t.Fatal(e)
	}
	defer token.Close()
	user, e := token.GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sys, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	admin, _ := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	ti, _, _, _ := windows.LookupSID("", "NT SERVICE\\TrustedInstaller")
	p := &pins{user: user.User.Sid, system: sys, admin: admin, installer: ti}
	if _, e = p.open(path, false, false); e != nil {
		t.Fatal(e)
	}
	defer p.close()
	if !p.unchanged() {
		t.Fatal("initial metadata unstable")
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;GR;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	if p.unchanged() {
		t.Fatal("changed ACL accepted as same observation")
	}
}
