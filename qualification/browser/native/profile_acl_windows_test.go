//go:build windows && amd64 && v02qualification

package main

import (
	"golang.org/x/sys/windows"
	"testing"
)

func profileTestDescriptor(root, directory bool) profileDescriptor {
	flags := byte(0x10)
	if directory {
		flags = 0x13
	}
	if root {
		flags = 0x03
	}
	return profileDescriptor{root: root, directory: directory, ownerCurrent: true, protected: root, daclPresent: true, diskObject: true, singleLink: true, aces: []profileACE{{0, flags, 0x001f01ff, profileCurrentUser}, {0, flags, 0x001f01ff, profileSystem}}}
}
func copyProfileDescriptor(d profileDescriptor) profileDescriptor {
	d.aces = append([]profileACE(nil), d.aces...)
	return d
}
func TestProfileSemanticExpectedForms(t *testing.T) {
	root := profileTestDescriptor(true, true)
	file := profileTestDescriptor(false, false)
	directory := profileTestDescriptor(false, true)
	mapped := copyProfileDescriptor(directory)
	mapped.networkScope = true
	mapped.aces = append(mapped.aces, profileACE{0, 0, 0x001301bf, profileNetwork}, profileACE{0, 0x0b, 0xe0010000, profileNetwork})
	inherited := copyProfileDescriptor(mapped)
	inherited.aces[2].flags = 0x10
	inherited.aces[3].flags = 0x1b
	capabilityFile := copyProfileDescriptor(file)
	capabilityFile.networkScope = true
	capabilityFile.aces = append(capabilityFile.aces, profileACE{0, 0x10, 0x001301bf, profileNetwork})
	deny := copyProfileDescriptor(file)
	deny.aces = append([]profileACE{{1, 0, 0x20, profileEveryone}}, deny.aces...)
	for name, d := range map[string]profileDescriptor{"root": root, "ordinary-file": file, "ordinary-directory": directory, "explicit-mapped-pair": mapped, "inherited-mapped-pair": inherited, "capability-file": capabilityFile, "file-execute-deny": deny} {
		t.Run(name, func(t *testing.T) {
			if !classifyProfileDescriptor(d) {
				t.Fatal("reviewed profile ACL form refused")
			}
		})
	}
	for _, path := range []string{`Default\Network`, `Default\Cache\Cache_Data\data_0`, `Default\Shared Dictionary\cache`} {
		if !profileNetworkScope(path) {
			t.Fatal("reviewed network scope refused")
		}
	}
	for _, path := range []string{`.`, `Default`, `Default\IndexedDB`, `Default\NetworkOther`, `Default\Cache\..\IndexedDB`, `..\Default\Network`} {
		if profileNetworkScope(path) {
			t.Fatal("unreviewed capability scope accepted")
		}
	}
}
func TestProfileSemanticRefusals(t *testing.T) {
	file := profileTestDescriptor(false, false)
	directory := profileTestDescriptor(false, true)
	capability := copyProfileDescriptor(file)
	capability.networkScope = true
	capability.aces = append(capability.aces, profileACE{0, 0x10, 0x001301bf, profileNetwork})
	tests := map[string]profileDescriptor{}
	d := copyProfileDescriptor(capability)
	d.aces[2].principal = profileOther
	tests["wrong-capability"] = d
	d = copyProfileDescriptor(file)
	d.aces = append(d.aces, profileACE{0, 0x10, 0x001f01ff, profileOther})
	tests["broad-account-allow"] = d
	d = copyProfileDescriptor(capability)
	d.aces[2].mask |= 0x00040000
	tests["capability-write-dac"] = d
	d = copyProfileDescriptor(capability)
	d.aces[2].mask |= 0x00080000
	tests["capability-write-owner"] = d
	d = copyProfileDescriptor(capability)
	d.aces[2].flags = 0x13
	tests["effective-inherit-flags"] = d
	d = copyProfileDescriptor(capability)
	d.networkScope = false
	tests["capability-on-recovery-idb"] = d
	d = copyProfileDescriptor(capability)
	d.root = true
	d.directory = true
	d.protected = true
	tests["capability-on-root"] = d
	d = copyProfileDescriptor(file)
	d.aces = d.aces[:1]
	tests["missing-system-grant"] = d
	d = copyProfileDescriptor(file)
	d.aces = d.aces[1:]
	tests["missing-user-grant"] = d
	d = copyProfileDescriptor(file)
	d.ownerCurrent = false
	tests["wrong-owner"] = d
	d = copyProfileDescriptor(file)
	d.reparse = true
	tests["reparse"] = d
	d = copyProfileDescriptor(file)
	d.diskObject = false
	tests["nonfilesystem-object"] = d
	d = copyProfileDescriptor(file)
	d.singleLink = false
	tests["hardlinked-file"] = d
	d = copyProfileDescriptor(file)
	d.daclPresent = false
	tests["missing-dacl"] = d
	d = copyProfileDescriptor(file)
	d.aces[0].kind = 0x09
	tests["callback-ace"] = d
	d = copyProfileDescriptor(file)
	d.aces = append(d.aces, profileACE{0, 0, 0x20, profileEveryone})
	tests["everyone-allow"] = d
	d = copyProfileDescriptor(directory)
	d.aces = append([]profileACE{{1, 0, 0x20, profileEveryone}}, d.aces...)
	tests["deny-on-directory"] = d
	d = copyProfileDescriptor(file)
	d.aces = append([]profileACE{{1, 0, 0x22, profileEveryone}}, d.aces...)
	tests["deny-read-write"] = d
	d = copyProfileDescriptor(file)
	d.aces = append(d.aces, profileACE{1, 0, 0x20, profileEveryone})
	tests["deny-after-allow"] = d
	d = copyProfileDescriptor(directory)
	d.networkScope = true
	d.aces = append(d.aces, profileACE{0, 0x10, 0x001301bf, profileNetwork})
	tests["missing-inherit-pair"] = d
	d = copyProfileDescriptor(directory)
	d.networkScope = true
	d.aces = append(d.aces, profileACE{0, 0x0b, 0xe0010000, profileNetwork})
	tests["missing-effective-pair"] = d
	for name, d := range tests {
		t.Run(name, func(t *testing.T) {
			if classifyProfileDescriptor(d) {
				t.Fatal("unreviewed profile ACL form accepted")
			}
		})
	}
}
func TestProfileCapabilityNativeDerivation(t *testing.T) {
	sid, e := deriveProfileNetworkSID()
	if e != nil || sid == nil || !sid.IsValid() || sid.IsWellKnown(windows.WinWorldSid) {
		t.Fatal("exact named native capability derivation unavailable")
	}
}
