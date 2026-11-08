//go:build windows && amd64

package installation

import (
	"context"
	"golang.org/x/sys/windows"
	"testing"
)

func TestNativeOwnerAndPathPolicies(t *testing.T) {
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		t.Fatal(e)
	}
	defer token.Close()
	u, e := token.GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sys, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	admin, _ := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	ti, _, _, _ := windows.LookupSID("", "NT SERVICE\\TrustedInstaller")
	p := &pins{user: u.User.Sid, system: sys, admin: admin, installer: ti}
	foreign, _ := windows.CreateWellKnownSid(windows.WinWorldSid)
	if p.trusted(foreign, false) || p.trusted(nil, false) || p.trusted(ti, false) || !p.trusted(ti, true) || !p.trusted(u.User.Sid, false) {
		t.Fatal("owner scope")
	}
	for _, path := range []string{`relative`, `\\server\share`, `C:\a\..\b`, `C:\a:stream`, `C:\a.`, `C:\a~1`} {
		if cleanLocal(path) {
			t.Fatal("unsafe path", path)
		}
	}
	_ = context.Background()
}
func TestManifestBoundaries(t *testing.T) {
	good := []byte(`{"layoutVersion":1,"version":"0.154.0","target":"x86_64-pc-windows-msvc","variant":"codex","entrypoint":"bin/codex.exe","resourcesDir":"codex-resources","pathDir":"codex-path"}`)
	if !manifestValid(good, "0.154.0-x86_64-pc-windows-msvc") {
		t.Fatal("valid manifest")
	}
	if manifestValid(good, "other") || manifestValid(append(good, []byte(`{}`)...), "0.154.0-x86_64-pc-windows-msvc") {
		t.Fatal("bad manifest")
	}
	for _, s := range []string{"01.2.3", "1.2.3-01", "1.2.3;evil"} {
		if validVersion(s) {
			t.Fatal(s)
		}
	}
}
func TestTrustStatusNeverUsesHRESULTSuccess(t *testing.T) {
	for _, n := range []int32{1, -1, 0x7fffffff} {
		if trustFailure(n) == "" {
			t.Fatal("nonzero accepted")
		}
	}
}

func TestDescriptorRejectsOwnerAndMutationIndependently(t *testing.T) {
	u, _ := windows.StringToSid("S-1-5-21-1-2-3-1001")
	sys, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	admin, _ := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	ti, _, _, _ := windows.LookupSID("", "NT SERVICE\\TrustedInstaller")
	p := &pins{user: u, system: sys, admin: admin, installer: ti}
	for _, row := range []struct {
		name, owner, ace string
		root, valid      bool
	}{
		{"owned-read", u.String(), "(A;;GR;;;WD)", false, true},
		{"foreign-owner-read", "WD", "(A;;GR;;;WD)", false, false},
		{"foreign-write", u.String(), "(A;;GW;;;WD)", false, false},
		{"unknown-mask", u.String(), "(A;;0x200000;;;WD)", false, false},
		{"installer-root", ti.String(), "(A;;GR;;;WD)", true, true},
		{"installer-child", ti.String(), "(A;;GR;;;WD)", false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			sd, e := windows.SecurityDescriptorFromString("O:" + row.owner + "D:P" + row.ace)
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = p.validateSecurity(sd, row.root)
			if (e == nil) != row.valid {
				t.Fatal("unexpected ACL result", e)
			}
		})
	}
}

func TestProviderCloseCannotReplayAfterAmbiguousFailure(t *testing.T) {
	count := 0
	s := &trustState{callTest: func(action uint32) int32 {
		if action != windows.WTD_STATEACTION_CLOSE {
			t.Fatal(action)
		}
		count++
		return 1
	}}
	s.data.StateData = 123
	if s.close() != ErrIncomplete || s.close() != ErrIncomplete || count != 1 {
		t.Fatal("provider state replay")
	}
}

func TestFileClosePoisonDoesNotTouchReusedHandle(t *testing.T) {
	p := &pins{items: []*pin{{h: 0}}}
	if p.close() == nil {
		t.Fatal("invalid close accepted")
	}
	h, e := windows.CreateEvent(nil, 0, 0, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(h)
	p.items[0].h = h // Represents a numeric slot now belonging to another object.
	if p.close() != ErrIncomplete {
		t.Fatal("poison guard absent")
	}
	if _, err := windows.WaitForSingleObject(h, 0); err != nil {
		t.Fatal("reused object was closed")
	}
}
