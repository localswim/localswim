//go:build windows && amd64 && nativecustodyqualification

package installation

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

func workspaceFixture(t *testing.T) (string, *windows.SID) {
	t.Helper()
	base := os.Getenv("NATIVECUSTODY_TEST_ROOT")
	if base == "" {
		t.Fatal("requires private NATIVECUSTODY_TEST_ROOT")
	}
	root, err := os.MkdirTemp(base, "workspace-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error("owned fixture cleanup", err)
		}
	})
	ptr, _ := windows.UTF16PtrFromString(root)
	h, e := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	root, e = finalPath(h)
	closeErr := windows.CloseHandle(h)
	if e != nil || closeErr != nil {
		t.Fatal(e, closeErr)
	}
	if err := os.MkdirAll(filepath.Join(root, "AppData", "Local"), 0700); err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// Copy the SID while the token is held; it has its own Go allocation.
	sid, err := windows.StringToSid(user.User.Sid.String())
	if err != nil {
		t.Fatal(err)
	}
	if err = token.Close(); err != nil {
		t.Fatal(err)
	}
	return root, sid
}
func TestPrivateWorkspaceExclusiveAndActualCleanup(t *testing.T) {
	profile, sid := workspaceFixture(t)
	runtime, probe := uuid.NewString(), uuid.NewString()
	w, cleanup, err := createNativeWorkspace(context.Background(), profile, sid, runtime, probe)
	if err != nil {
		t.Fatal("private root qualification", err)
	}
	if err = w.recheck(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "local", "roaming", "temp", "codex"} {
		if info, e := os.Stat(filepath.Join(w.root, name)); e != nil || !info.IsDir() {
			t.Fatal(name, e)
		}
	}
	_, collisionCleanup, err := createNativeWorkspace(context.Background(), profile, sid, runtime, uuid.NewString())
	if err == nil {
		t.Fatal("adopted existing runtime leaf")
	}
	if err = collisionCleanup(); err != nil {
		t.Fatal("collision acquired unowned cleanup", err)
	}
	if err = w.recheck(); err != nil {
		t.Fatal("collision damaged owned runtime", err)
	}
	runtimePath := w.runtime
	if err = cleanup(); err != nil {
		t.Fatal("actual cleanup", err)
	}
	if _, err = os.Stat(runtimePath); !os.IsNotExist(err) {
		t.Fatal("owned runtime survived", err)
	}
	if err = cleanup(); err != nil {
		t.Fatal("cleanup not idempotent", err)
	}
}
func workspaceJunction(t *testing.T, path, target string) {
	t.Helper()
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	ptr, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(ptr, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
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
	var count uint32
	err = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &b[0], uint32(len(b)), nil, 0, &count, nil)
	closeErr := windows.CloseHandle(h)
	if err != nil || closeErr != nil {
		t.Fatal("junction fixture", err, closeErr)
	}
}
func TestPrivateCleanupDoesNotTraverseChildReparseCanary(t *testing.T) {
	profile, sid := workspaceFixture(t)
	outside := t.TempDir()
	canary := filepath.Join(outside, "canary.txt")
	const content = "outside-owned-canary\n"
	if err := os.WriteFile(canary, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	w, cleanup, err := createNativeWorkspace(context.Background(), profile, sid, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	workspaceJunction(t, filepath.Join(w.root, "escape"), outside)
	if err = cleanup(); err != nil {
		t.Fatal("reparse-safe cleanup", err)
	}
	b, err := os.ReadFile(canary)
	if err != nil || string(b) != content {
		t.Fatal("external canary changed", err)
	}
	if _, err = os.Stat(w.runtime); !os.IsNotExist(err) {
		t.Fatal("run root survived", err)
	}
}
func TestPrivateWorkspaceRejectsUnsafeExistingBaseWithoutRepair(t *testing.T) {
	profile, sid := workspaceFixture(t)
	base := filepath.Join(profile, ".localswim-native-runtime", "private")
	if err := os.MkdirAll(filepath.Join(base, "releases"), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, "preserve.txt")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	// Inherited ambient ACL does not meet the exact protected private-root ACL.
	w, cleanup, err := createNativeWorkspace(context.Background(), profile, sid, uuid.NewString(), uuid.NewString())
	if err == nil {
		t.Fatal("repaired/adopted unsafe base", w.root)
	}
	if err = cleanup(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(b)) != "preserve" {
		t.Fatal("unowned content changed", err)
	}
}

func TestPrivateWorkspacePreservesExistingSharedApplicationDirectory(t *testing.T) {
	for _, unsafeAppLocal := range []bool{false, true} {
		name := "normal"
		if unsafeAppLocal {
			name = "unsafe-AppLocal"
		}
		t.Run(name, func(t *testing.T) {
			profile, sid := workspaceFixture(t)
			appLocal := filepath.Join(profile, "AppData", "Local")
			shared := filepath.Join(appLocal, "localswim")
			if err := os.Mkdir(shared, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(shared, "preserve.txt")
			const content = "existing application state\n"
			if err := os.WriteFile(marker, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if unsafeAppLocal {
				sd, err := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;GW;;;WD)")
				if err != nil {
					t.Fatal(err)
				}
				acl, _, err := sd.DACL()
				if err != nil {
					t.Fatal(err)
				}
				if err = windows.SetNamedSecurityInfo(appLocal, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
					t.Fatal(err)
				}
			}
			before, err := windows.GetNamedSecurityInfo(shared, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			appBefore, err := windows.GetNamedSecurityInfo(appLocal, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			w, cleanup, err := createNativeWorkspace(context.Background(), profile, sid, uuid.NewString(), uuid.NewString())
			if err != nil {
				t.Fatal("unselected application ancestry prevented dedicated private work", err)
			}
			if filepath.Dir(w.runtime) != filepath.Join(profile, ".localswim-native-runtime", "private") {
				t.Fatal("native workspace did not use fixed verified-profile child")
			}
			if err = cleanup(); err != nil {
				t.Fatal(err)
			}
			after, err := windows.GetNamedSecurityInfo(shared, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			if err != nil || after.String() != before.String() {
				t.Fatal("shared application ACL changed", err)
			}
			appAfter, err := windows.GetNamedSecurityInfo(appLocal, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			if err != nil || appAfter.String() != appBefore.String() {
				t.Fatal("unselected AppLocal ACL changed", err)
			}
			bytes, err := os.ReadFile(marker)
			if err != nil || string(bytes) != content {
				t.Fatal("shared application state changed", err)
			}
			if info, err := os.Stat(shared); err != nil || !info.IsDir() {
				t.Fatal("shared application directory removed", err)
			}
		})
	}
}

func TestPrivateWorkspacePreservesReadonlyNamespaceACL(t *testing.T) {
	profile, sid := workspaceFixture(t)
	namespace := filepath.Join(profile, ".localswim-native-runtime")
	if err := os.Mkdir(namespace, 0700); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;GRGX;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(namespace, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(namespace, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	w, cleanup, err := createNativeWorkspace(context.Background(), profile, sid, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal("read-only namespace refused", err)
	}
	private := filepath.Join(namespace, "private")
	ptr, _ := windows.UTF16PtrFromString(private)
	h, err := windows.CreateFile(ptr, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = strictPrivate(h, sid, w.policy.system); err != nil {
		t.Fatal("inner private ACL broadened", err)
	}
	if err = windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(w.runtime) != private {
		t.Fatal("runtime data placed outside inner private subtree")
	}
	if err = cleanup(); err != nil {
		t.Fatal(err)
	}
	after, err := windows.GetNamedSecurityInfo(namespace, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || before.String() != after.String() {
		t.Fatal("namespace ACL repaired/rewritten", err)
	}
	entries, err := os.ReadDir(namespace)
	if err != nil || len(entries) != 1 || entries[0].Name() != "private" {
		t.Fatal("data created in nondata namespace", err)
	}
}
func TestPrivateWorkspaceRejectsUnsafeNamespaceWithoutRepair(t *testing.T) {
	for _, kind := range []string{"mutation", "reparse"} {
		t.Run(kind, func(t *testing.T) {
			profile, sid := workspaceFixture(t)
			namespace := filepath.Join(profile, ".localswim-native-runtime")
			outside := t.TempDir()
			marker := filepath.Join(outside, "canary")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "reparse" {
				workspaceJunction(t, namespace, outside)
			} else {
				if err := os.Mkdir(namespace, 0700); err != nil {
					t.Fatal(err)
				}
				sd, err := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;GW;;;WD)")
				if err != nil {
					t.Fatal(err)
				}
				acl, _, err := sd.DACL()
				if err != nil {
					t.Fatal(err)
				}
				if err = windows.SetNamedSecurityInfo(namespace, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
					t.Fatal(err)
				}
			}
			_, cleanup, err := createNativeWorkspace(context.Background(), profile, sid, uuid.NewString(), uuid.NewString())
			if err == nil {
				t.Fatal("unsafe namespace adopted")
			}
			if err = cleanup(); err != nil {
				t.Fatal(err)
			}
			bytes, err := os.ReadFile(marker)
			if err != nil || string(bytes) != "preserve" {
				t.Fatal("external canary changed", err)
			}
			if _, err = os.Stat(filepath.Join(outside, "private")); !os.IsNotExist(err) {
				t.Fatal("unowned reparse target received private state", err)
			}
		})
	}
}
func TestNamespaceOwnerPolicyRemainsIndependentOfReadonlyACL(t *testing.T) {
	_, sid := workspaceFixture(t)
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	p := &pins{user: sid, system: system, admin: admin}
	sd, err := windows.SecurityDescriptorFromString("O:WDD:P(A;;GRGX;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.validateSecurity(sd, false); err == nil {
		t.Fatal("read-only grants legitimized a foreign namespace owner")
	}
}

func TestManagedSlotRequiresPhysicalScope(t *testing.T) {
	root, sid := workspaceFixture(t)
	for _, kind := range []string{"kelvin-escape", "ascii-case", "case-distinct-sibling"} {
		t.Run(kind, func(t *testing.T) {
			testRoot := filepath.Join(root, kind)
			owned := filepath.Join(testRoot, "K")
			sibling := filepath.Join(testRoot, "K")
			if kind == "case-distinct-sibling" {
				if err := os.Mkdir(testRoot, 0700); err != nil {
					t.Fatal(err)
				}
				enableCaseSensitiveDirectory(t, testRoot)
				sibling = filepath.Join(testRoot, "k")
			}
			base := filepath.Join(owned, ".codex", "packages", "standalone")
			chosen := owned
			if kind != "ascii-case" {
				chosen = sibling
			}
			target := filepath.Join(chosen, ".codex", "packages", "standalone", "releases", "1.2.3-x86_64-pc-windows-msvc")
			if err := os.MkdirAll(filepath.Join(target, "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(base, "releases"), 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "case-distinct-sibling" {
				expected := filepath.Join(base, "releases")
				actual := filepath.Dir(target)
				left, leftErr := os.Stat(expected)
				right, rightErr := os.Stat(actual)
				equal, compareErr := ordinalPathEqual(expected, actual)
				if leftErr != nil || rightErr != nil || compareErr != nil || !equal || os.SameFile(left, right) {
					t.Fatal("distinct physical scope control not established", leftErr, rightErr, compareErr)
				}
			}
			manifest := []byte(`{"layoutVersion":1,"version":"1.2.3","target":"x86_64-pc-windows-msvc","variant":"codex","entrypoint":"bin/codex.exe","resourcesDir":"codex-resources","pathDir":"codex-path"}`)
			if err := os.WriteFile(filepath.Join(target, "codex-package.json"), manifest, 0600); err != nil {
				t.Fatal(err)
			}
			pe := make([]byte, 512)
			copy(pe, "MZ")
			binary.LittleEndian.PutUint32(pe[60:], 64)
			copy(pe[64:], "PE\x00\x00")
			binary.LittleEndian.PutUint16(pe[68:], 0x8664)
			binary.LittleEndian.PutUint16(pe[86:], 2)
			binary.LittleEndian.PutUint16(pe[88:], 0x20b)
			if err := os.WriteFile(filepath.Join(target, "bin", "codex.exe"), pe, 0600); err != nil {
				t.Fatal(err)
			}
			junctionFixturePath := filepath.Join(base, "current")
			workspaceJunction(t, junctionFixturePath, target)
			requested := owned
			if kind == "ascii-case" {
				requested = strings.ToUpper(owned)
			}
			observation, cleanup := inspectProfile(context.Background(), requested, sid)
			if cleanup == nil {
				t.Fatal("missing resolver custody")
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			want := "SOURCE_UNQUALIFIED"
			if kind == "ascii-case" {
				want = "PUBLISHER_UNQUALIFIED"
			}
			if observation.Code != want {
				t.Fatal("incorrect ordinal source decision", observation)
			}
			bytes, err := os.ReadFile(filepath.Join(target, "codex-package.json"))
			if err != nil || string(bytes) != string(manifest) {
				t.Fatal("owned target canary changed", err)
			}
		})
	}
}

func enableCaseSensitiveDirectory(t *testing.T, path string) {
	t.Helper()
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(ptr, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	flag := uint32(1)
	// FileCaseSensitiveInfo is FILE_INFO_BY_HANDLE_CLASS 23.
	err = windows.SetFileInformationByHandle(h, 23, (*byte)(unsafe.Pointer(&flag)), 4)
	closeErr := windows.CloseHandle(h)
	if err != nil || closeErr != nil {
		t.Fatal("owned case-sensitive fixture setup failed", err, closeErr)
	}
}

func TestPrivateCleanupSeparatesCaseDistinctJunction(t *testing.T) {
	profile, sid := workspaceFixture(t)
	private := filepath.Join(profile, ".localswim-native-runtime", "private")
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(private, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	enableCaseSensitiveDirectory(t, private)
	w, cleanup, err := createNativeWorkspace(context.Background(), profile, sid, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	canary := filepath.Join(outside, "canary")
	if err := os.WriteFile(canary, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	ordinary := filepath.Join(w.root, "home")
	link := filepath.Join(w.root, "HOME")
	workspaceJunction(t, link, outside)
	names, err := w.names(w.root)
	if err != nil || !slices.Contains(names, "home") || !slices.Contains(names, "HOME") {
		t.Fatal("distinct ordinary and junction entries not established", err)
	}
	if err = w.remove(link, 1); err != nil {
		t.Fatal("exact junction detach failed", err)
	}
	if info, err := os.Stat(ordinary); err != nil || !info.IsDir() {
		t.Fatal("ordinary held directory was removed instead", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("junction remained", err)
	}
	if err = cleanup(); err != nil {
		t.Fatal("owned cleanup did not join", err)
	}
	if bytes, err := os.ReadFile(canary); err != nil || string(bytes) != "preserve" {
		t.Fatal("external canary changed", err)
	}
}
