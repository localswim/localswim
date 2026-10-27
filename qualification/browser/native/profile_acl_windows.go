//go:build windows && amd64 && v02qualification

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/store/schema"
	"golang.org/x/sys/windows"
)

// This narrow browser-profile child policy is independent of the unchanged
// exact-two-ACE profile root, native ROOT, board and evidence requirements.
const profileNetworkCapability = "lpacChromeNetworkSandbox"
const profilePinnedBrowserVersion = "156.0.8078.4"
const profilePinnedBrowserSHA256 = "7f08161bbeb2853ffe1780a9ded4985087f52efb9725d7d8207aabdd42f7cfb9"
const profilePinnedBrowserArchiveSHA256 = "220021ecfdad08c116fc71a153e65e3861ba841c531afc4f7acc7bd5d44b41db"

type profilePrincipal uint8

const (
	profileOther profilePrincipal = iota
	profileCurrentUser
	profileSystem
	profileNetwork
	profileEveryone
)

type profileACE struct {
	kind, flags byte
	mask        uint32
	principal   profilePrincipal
}
type profileDescriptor struct {
	root, directory, ownerCurrent, protected, daclPresent, reparse, diskObject, singleLink, networkScope bool
	aces                                                                                                 []profileACE
}

func profileNetworkScope(relative string) bool {
	if relative == "." || filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		return false
	}
	for _, root := range []string{`Default\Network`, `Default\Cache`, `Default\Shared Dictionary`} {
		if relative == root || strings.HasPrefix(relative, root+`\`) {
			return true
		}
	}
	return false
}
func classifyProfileDescriptor(d profileDescriptor) bool {
	if !d.ownerCurrent || !d.daclPresent || d.reparse || !d.diskObject || (!d.directory && !d.singleLink) || d.protected != d.root {
		return false
	}
	if len(d.aces) < 2 || len(d.aces) > 4 {
		return false
	}
	users, systems, effective, generic, denies := 0, 0, 0, 0, 0
	for i, a := range d.aces {
		switch a.principal {
		case profileCurrentUser, profileSystem:
			flags := byte(windows.INHERITED_ACE)
			if d.directory {
				flags |= windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
			}
			if d.root {
				flags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
			}
			if a.kind != windows.ACCESS_ALLOWED_ACE_TYPE || a.mask != 0x001f01ff || a.flags != flags {
				return false
			}
			if a.principal == profileCurrentUser {
				users++
			} else {
				systems++
			}
		case profileNetwork:
			if d.root || !d.networkScope || a.kind != windows.ACCESS_ALLOWED_ACE_TYPE {
				return false
			}
			if a.mask == 0x001301bf && (a.flags == windows.INHERITED_ACE || d.directory && a.flags == 0) {
				effective++
			} else if d.directory && a.mask == 0xe0010000 && (a.flags == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE|windows.INHERIT_ONLY_ACE || a.flags == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE|windows.INHERIT_ONLY_ACE|windows.INHERITED_ACE) {
				generic++
			} else {
				return false
			}
		case profileEveryone:
			// This is Chromium's restrictive file execute-mapping protection, not allow.
			if d.root || d.directory || a.kind != windows.ACCESS_DENIED_ACE_TYPE || a.mask != 0x20 || a.flags != 0 || i != 0 {
				return false
			}
			denies++
		default:
			return false
		}
	}
	if users != 1 || systems != 1 || effective > 1 || generic > 1 || denies > 1 || denies > 0 && (effective > 0 || generic > 0) {
		return false
	}
	if d.root {
		return len(d.aces) == 2
	}
	if d.directory {
		return effective == generic
	}
	return generic == 0
}

// Derive only the reviewed named capability through the native Windows API.
// No caller-selected name, SID prefix or alternate capability is accepted.
// https://learn.microsoft.com/en-us/windows/win32/api/securitybaseapi/nf-securitybaseapi-derivecapabilitysidsfromname
func deriveProfileNetworkSID() (*windows.SID, error) {
	proc := windows.NewLazySystemDLL("kernelbase.dll").NewProc("DeriveCapabilitySidsFromName")
	if proc.Find() != nil {
		return nil, unavailable
	}
	name, e := windows.UTF16PtrFromString(profileNetworkCapability)
	if e != nil {
		return nil, unavailable
	}
	var groups, capabilities **windows.SID
	var groupCount, capabilityCount uint32
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&groups)), uintptr(unsafe.Pointer(&groupCount)), uintptr(unsafe.Pointer(&capabilities)), uintptr(unsafe.Pointer(&capabilityCount)))
	runtime.KeepAlive(name)
	release := func(array **windows.SID, count uint32) {
		if array == nil {
			return
		}
		if count <= 16 {
			for _, sid := range unsafe.Slice(array, int(count)) {
				if sid != nil {
					_, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(sid))))
				}
			}
		}
		_, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(array))))
	}
	defer release(groups, groupCount)
	defer release(capabilities, capabilityCount)
	if ok == 0 || groupCount != 1 || capabilityCount != 1 || capabilities == nil {
		return nil, unavailable
	}
	sid := unsafe.Slice(capabilities, 1)[0]
	if sid == nil || !sid.IsValid() {
		return nil, unavailable
	}
	return sid.Copy()
}
func profileBrowserPinValid(f manifest) bool {
	if f.BrowserVersion != profilePinnedBrowserVersion || f.ChromeSHA256 != profilePinnedBrowserSHA256 || f.BrowserArchiveSHA256 != profilePinnedBrowserArchiveSHA256 || f.PlaywrightVersion != "1.64.1" || !filepath.IsAbs(f.ChromeExecutable) || filepath.Clean(f.ChromeExecutable) != f.ChromeExecutable {
		return false
	}
	file, e := os.Open(f.ChromeExecutable)
	if e != nil {
		return false
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return false
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, file); e != nil {
		return false
	}
	return hex.EncodeToString(hash.Sum(nil)) == profilePinnedBrowserSHA256
}
func inspectBrowserProfile(path, relative string, entry os.DirEntry, user, system, capability, everyone *windows.SID) (bool, int, int) {
	h, e := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return false, 0, 0
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil {
		return false, 0, 0
	}
	typ, e := windows.GetFileType(h)
	if e != nil {
		return false, 0, 0
	}
	directory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if directory != entry.IsDir() {
		return false, 0, 0
	}
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return false, 0, 0
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil {
		return false, 0, 0
	}
	control, _, e := sd.Control()
	if e != nil {
		return false, 0, 0
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount > 4 {
		return false, 0, 0
	}
	d := profileDescriptor{root: relative == ".", directory: directory, ownerCurrent: owner.Equals(user), protected: control&windows.SE_DACL_PROTECTED != 0, daclPresent: control&windows.SE_DACL_PRESENT != 0, reparse: info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, diskObject: typ == windows.FILE_TYPE_DISK, singleLink: info.NumberOfLinks == 1, networkScope: profileNetworkScope(relative)}
	capCount, denyCount := 0, 0
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var native *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &native) != nil || native == nil {
			return false, 0, 0
		}
		if native.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && native.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE || native.Header.AceSize < uint16(unsafe.Offsetof(native.SidStart))+8 {
			return false, 0, 0
		}
		sid := (*windows.SID)(unsafe.Pointer(&native.SidStart))
		if sid.Len() < 8 || uint32(native.Header.AceSize) < uint32(unsafe.Offsetof(native.SidStart))+uint32(sid.Len()) || !sid.IsValid() {
			return false, 0, 0
		}
		principal := profileOther
		switch {
		case sid.Equals(user):
			principal = profileCurrentUser
		case sid.Equals(system):
			principal = profileSystem
		case sid.Equals(capability):
			principal = profileNetwork
			capCount++
		case sid.Equals(everyone):
			principal = profileEveryone
			denyCount++
		}
		d.aces = append(d.aces, profileACE{native.Header.AceType, native.Header.AceFlags, uint32(native.Mask), principal})
	}
	return classifyProfileDescriptor(d), capCount, denyCount
}
func auditBrowserProfile(f manifest) any {
	result := map[string]any{"ok": false, "profileId": f.ProfileID, "nativeACLVerified": false, "policy": "pinned-browser-account-profile-private-v1"}
	root, e := pinned()
	if e != nil || f.Profile != filepath.Join(filepath.Dir(root), "profile") || !schema.ValidUUID(f.ProfileID) || verifyPrivateDirectory(f.Profile) != nil || !profileBrowserPinValid(f) {
		return result
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return result
	}
	system, e := windows.StringToSid("S-1-5-18")
	if e != nil {
		return result
	}
	everyone, e := windows.StringToSid("S-1-1-0")
	if e != nil {
		return result
	}
	capability, e := deriveProfileNetworkSID()
	if e != nil {
		return result
	}
	files, dirs, capCount, denyCount := 0, 0, 0, 0
	deadline := time.Now().Add(15 * time.Second)
	e = filepath.WalkDir(f.Profile, func(path string, entry os.DirEntry, e error) error {
		if e != nil || time.Now().After(deadline) {
			return unavailable
		}
		relative, e := filepath.Rel(f.Profile, path)
		if e != nil {
			return unavailable
		}
		ok, c, d := inspectBrowserProfile(path, relative, entry, user.User.Sid, system, capability, everyone)
		if !ok {
			return unavailable
		}
		capCount += c
		denyCount += d
		if entry.IsDir() {
			dirs++
		} else {
			files++
		}
		return nil
	})
	result["ok"] = e == nil
	result["nativeACLVerified"] = e == nil
	result["protectedRootDACL"] = true
	result["fileCount"] = files
	result["directoryCount"] = dirs
	result["namedCapability"] = profileNetworkCapability
	result["namedCapabilityACECount"] = capCount
	result["restrictiveFileExecuteDenyCount"] = denyCount
	result["ordinaryAllowPrincipals"] = []string{"current-user", "SYSTEM"}
	return result
}
