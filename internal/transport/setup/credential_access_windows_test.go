//go:build windows && amd64 && k03credentialqualification

package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"golang.org/x/sys/windows"
)

const credentialBoard = "11111111-1111-4111-8111-111111111111"
const credentialInc = "22222222-2222-4222-8222-222222222222"
const credentialGrant = "33333333-3333-4333-8333-333333333333"

func credentialFixture(t *testing.T) (*Manager, CredentialSelection, T.CredentialSecret) {
	t.Helper()
	// This symbol exists only in a root-only private test overlay. Merely setting
	// an environment variable can never redirect production nativeRoot.
	if K03CredentialQualificationRoot() != os.Getenv("K03_CREDENTIAL_TEST_ROOT") {
		t.Fatal("compiled synthetic-only overlay required")
	}
	m, now, _ := fixture(t)
	good(t, m.Init(nil))
	c, e := m.ReadContext()
	if e != nil {
		t.Fatal("context unavailable")
	}
	s := CredentialSelection{c.InstallationID, credentialBoard, credentialInc, credentialGrant}
	secret := T.CredentialSecret{APIVersion: 1, InstallationID: T.UUIDv4(c.InstallationID), BoardID: A.UUID(s.BoardID), Incarnation: A.UUID(s.Incarnation), GrantID: A.UUID(s.GrantID), ActorID: A.UUID("44444444-4444-4444-8444-444444444444"), Kind: "human", Rights: T.Rights{"read"}, CredentialID: "55555555-5555-4555-8555-555555555555", IssuedAt: A.Timestamp(stamp(*now)), ExpiresAt: A.Timestamp(stamp(now.Add(30 * day))), Token: T.SecretToken(strings.Repeat("A", 43))}
	if T.Validate(secret) != nil {
		t.Fatal("invalid synthetic transport fixture")
	}
	return m, s, secret
}
func credentialIssue(t *testing.T, m *Manager, s CredentialSelection, secret T.CredentialSecret) {
	t.Helper()
	l, e := m.PrepareCredential(s)
	if e != nil {
		t.Fatal("prepare", e)
	}
	defer l.Close()
	if e = l.Disable(); e != nil {
		t.Fatal("disable", e)
	}
	if _, e = l.Publish(secret); e != nil {
		t.Fatal("publish", e)
	}
}
func credentialWant(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || failure(e).Code != code {
		t.Fatalf("wanted %s; got fixed category %v", code, e)
	}
}

func TestCredentialNativePublicationAndServerLease(t *testing.T) {
	m, s, secret := credentialFixture(t)
	server, e := m.AcquireServer()
	if e != nil {
		t.Fatal("server lease")
	}
	defer server.Close()
	credentialIssue(t, m, s, secret)
	mat, e := m.OpenCredential(s)
	if e != nil {
		t.Fatal("ordinary read while server holds root", e)
	}
	if mat.Metadata().Selected != s || mat.Metadata().ActorID != string(secret.ActorID) {
		t.Fatal("metadata binding")
	}
	if _, e := json.Marshal(mat); e == nil {
		t.Fatal("material serialized")
	}
	if strings.Contains(fmt.Sprintf("%#v", mat), string(secret.Token)) {
		t.Fatal("format leaked secret")
	}
	called := false
	if e = mat.WithToken(func(token T.SecretToken) error { called = token == secret.Token; return nil }); e != nil || !called {
		t.Fatal("guarded token callback")
	}
	if _, e = m.PrepareCredential(s); e == nil {
		t.Fatal("per-credential exclusion absent")
	}
	dir := m.fs.path(filepath.Join("cli", s.InstallationID, s.BoardID, s.Incarnation))
	if e = windows.MoveFileEx(ptr(dir), ptr(dir+".moved"), 0); e == nil {
		t.Fatal("credential path was not pinned for material lifetime")
	}
	mat.Close()
	credentialWant(t, mat.WithToken(func(T.SecretToken) error { return nil }), "CREDENTIAL_UNAVAILABLE")
	b, e := m.fs.read(filepath.Join("cli", s.InstallationID, s.BoardID, s.Incarnation, s.GrantID+".json"))
	if e != nil {
		t.Fatal("private file")
	}
	if bytes.Contains(b, []byte("apiVersion")) || !bytes.Contains(b, []byte("schemaVersion")) {
		t.Fatal("disk schema")
	}
}

func TestCredentialOwnerExpiryIndependentExactSelection(t *testing.T) {
	m, s, secret := credentialFixture(t)
	base := m.time()
	m.now = func() time.Time { return base.Add(day) }
	secret.IssuedAt = A.Timestamp(stamp(m.time()))
	secret.ExpiresAt = A.Timestamp(stamp(m.time().Add(30 * day)))
	credentialIssue(t, m, s, secret)
	m.now = func() time.Time { return base.Add(29 * day) }
	c, e := m.ReadContext()
	if e != nil {
		t.Fatal("context")
	}
	good(t, m.RenewCertificate(c.InstallationID, c.IssuerSHA256))
	m.now = func() time.Time { return base.Add(30*day + time.Hour) }
	// The board credential and renewed leaf outlive the native owner.
	if _, e = m.LoadNativeOwner(); e == nil {
		t.Fatal("native owner unexpectedly active")
	}
	opened, e := m.OpenCredential(s)
	if e != nil {
		t.Fatal("owner expiry blocked ordinary credential", e)
	}
	opened.Close()
	wrong := s
	wrong.GrantID = "66666666-6666-4666-8666-666666666666"
	_, e = m.OpenCredential(wrong)
	credentialWant(t, e, "CREDENTIAL_UNAVAILABLE")
	wrong = s
	wrong.GrantID = "..\\other"
	_, e = m.OpenCredential(wrong)
	credentialWant(t, e, "CREDENTIAL_SELECTION_REQUIRED")
	wrong = s
	wrong.InstallationID = "77777777-7777-4777-8777-777777777777"
	_, e = m.PrepareCredential(wrong)
	credentialWant(t, e, "CREDENTIAL_CONTEXT_MISMATCH")
	m.now = func() time.Time { return base.Add(31*day + time.Hour) }
	_, e = m.OpenCredential(s)
	credentialWant(t, e, "CREDENTIAL_EXPIRED")
}

func TestCredentialRevokeMarkerAndUnknown(t *testing.T) {
	m, s, secret := credentialFixture(t)
	credentialIssue(t, m, s, secret)
	l, e := m.PrepareCredential(s)
	if e != nil {
		t.Fatal("prepare")
	}
	mat, e := l.LoadForRevocation()
	if e != nil {
		t.Fatal("revoke load")
	}
	// Capture the same loaded bearer for session binding while it is still
	// locally enabled; no new connection may be built from a disabled token.
	var captured T.SecretToken
	if e = mat.WithToken(func(token T.SecretToken) error {
		if token != secret.Token {
			return errors.New("wrong token")
		}
		captured = token
		return nil
	}); e != nil {
		t.Fatal("preflight connection capture")
	}
	if yes, e := l.state.(*nativeCredentialLease).marker(); e != nil || yes {
		t.Fatal("session binding must precede marker")
	}
	credentialWant(t, mat.WithToken(func(T.SecretToken) error { t.Fatal("second capture must not run"); return nil }), "CREDENTIAL_UNAVAILABLE")
	if e = l.Disable(); e != nil {
		t.Fatal("durable marker")
	}
	if captured != secret.Token {
		t.Fatal("captured revocation bearer changed")
	}
	credentialWant(t, mat.WithToken(func(T.SecretToken) error { t.Fatal("postmarker capture must not run"); return nil }), "CREDENTIAL_OUTCOME_UNKNOWN")
	mat.Close()
	l.Close()
	_, e = m.OpenCredential(s)
	credentialWant(t, e, "CREDENTIAL_DISABLED")
	blocked, e := m.PrepareCredential(s)
	if e != nil {
		t.Fatal("disabled lease")
	}
	_, e = blocked.LoadForRevocation()
	credentialWant(t, e, "CREDENTIAL_OUTCOME_UNKNOWN")
	blocked.Close()
	// Unknown revoke retains marker. Deliberate new issue is the only reuse.
	secret.CredentialID = "88888888-8888-4888-8888-888888888888"
	secret.Token = T.SecretToken(strings.Repeat("B", 42) + "A")
	credentialIssue(t, m, s, secret)
	l, e = m.PrepareCredential(s)
	if e != nil {
		t.Fatal("revoke prepare")
	}
	defer l.Close()
	mat, e = l.LoadForRevocation()
	if e != nil {
		t.Fatal("revoke material")
	}
	defer mat.Close()
	if e = l.Disable(); e != nil {
		t.Fatal("marker")
	}
	if e = l.Remove(); e != nil {
		t.Fatal("confirmed local removal")
	}
	if yes, e := l.state.(*nativeCredentialLease).marker(); e != nil || !yes {
		t.Fatal("removal must retain marker")
	}
	_, e = m.OpenCredential(s)
	credentialWant(t, e, "CREDENTIAL_DISABLED")
}

func TestCredentialOrdinaryMaterialRejectsMarkerChange(t *testing.T) {
	m, s, secret := credentialFixture(t)
	credentialIssue(t, m, s, secret)
	material, e := m.OpenCredential(s)
	if e != nil {
		t.Fatal("ordinary load")
	}
	defer material.Close()
	if e = material.WithToken(func(token T.SecretToken) error {
		if token != secret.Token {
			t.Fatal("ordinary binding")
		}
		return nil
	}); e != nil {
		t.Fatal("active ordinary callback")
	}
	// Same-account fixture injection changes the marker while the native lease
	// remains held. The normal material must recheck it before every callback.
	rel := filepath.Join("cli", s.InstallationID, s.BoardID, s.Incarnation, s.GrantID+".disabled")
	if e = m.fs.write(rel, credentialDisabled); e != nil {
		t.Fatal("marker fixture")
	}
	credentialWant(t, material.WithToken(func(T.SecretToken) error { t.Fatal("disabled ordinary callback ran"); return nil }), "CREDENTIAL_DISABLED")
}

func TestCredentialRevocationCaptureFailureSpent(t *testing.T) {
	m, s, secret := credentialFixture(t)
	credentialIssue(t, m, s, secret)
	l, e := m.PrepareCredential(s)
	if e != nil {
		t.Fatal("prepare")
	}
	material, e := l.LoadForRevocation()
	if e != nil {
		t.Fatal("revocation material")
	}
	called := 0
	constructionFailure := errors.New("synthetic construction failure")
	e = material.WithToken(func(T.SecretToken) error { called++; return constructionFailure })
	if !errors.Is(e, constructionFailure) || called != 1 {
		t.Fatal("construction failure was not returned")
	}
	credentialWant(t, material.WithToken(func(T.SecretToken) error { called++; return nil }), "CREDENTIAL_UNAVAILABLE")
	if called != 1 {
		t.Fatal("failed capture was retried")
	}
	m.checkpoint = func(point string) error {
		if point == "credential:disabled" {
			return errors.New("synthetic marker failure")
		}
		return nil
	}
	credentialWant(t, l.Disable(), "CREDENTIAL_UNAVAILABLE")
	_, e = l.LoadForRevocation()
	credentialWant(t, e, "CREDENTIAL_OUTCOME_UNKNOWN")
	credentialWant(t, material.WithToken(func(T.SecretToken) error { called++; return nil }), "CREDENTIAL_OUTCOME_UNKNOWN")
	if called != 1 {
		t.Fatal("marker failure enabled a new capture")
	}
	material.Close()
	l.Close()
	_, e = m.OpenCredential(s)
	credentialWant(t, e, "CREDENTIAL_DISABLED")
}

func TestCredentialPublicationFailureBoundaries(t *testing.T) {
	for _, point := range []string{"credential:disabled", "credential:flushed", "credential:published", "credential:marker-removed"} {
		t.Run(point, func(t *testing.T) {
			m, s, secret := credentialFixture(t)
			l, e := m.PrepareCredential(s)
			if e != nil {
				t.Fatal("prepare")
			}
			reached := false
			m.checkpoint = func(p string) error {
				if p == point {
					reached = true
					return errors.New("injected")
				}
				return nil
			}
			e = l.Disable()
			if point == "credential:disabled" {
				if e == nil {
					t.Fatal("marker failure not reported")
				}
			} else {
				if e != nil {
					t.Fatal("marker")
				}
				_, e = l.Publish(secret)
				credentialWant(t, e, "CREDENTIAL_OUTCOME_UNKNOWN")
			}
			if !reached {
				t.Fatal("requested native boundary was not reached")
			}
			l.Close()
			_, e = m.OpenCredential(s)
			if e == nil {
				t.Fatal("ambiguous publication became ordinarily available")
			}
		})
	}
}

func TestCredentialMalformedAliasAndPrivateACL(t *testing.T) {
	m, s, secret := credentialFixture(t)
	credentialIssue(t, m, s, secret)
	rel := filepath.Join("cli", s.InstallationID, s.BoardID, s.Incarnation, s.GrantID+".json")
	if e := m.fs.validateFile(rel); e != nil {
		t.Fatal("private-at-create ACL validation")
	}
	b, e := m.fs.read(rel)
	if e != nil {
		t.Fatal("read")
	}
	for _, bad := range [][]byte{bytes.Replace(b, []byte("\"schemaVersion\":1"), []byte("\"schemaVersion\":1,\"schemaVersion\":1"), 1), bytes.Replace(b, []byte(string(secret.GrantID)), []byte("66666666-6666-4666-8666-666666666666"), 1), bytes.Replace(b, []byte("\"schemaVersion\":1"), []byte("\"schemaVersion\":1,\"apiVersion\":1"), 1)} {
		if e = m.fs.replace(rel, bad); e != nil {
			t.Fatal("synthetic replacement")
		}
		if _, e = m.OpenCredential(s); e == nil {
			t.Fatal("malformed/mismatch adopted")
		}
	}
	if e = m.fs.replace(rel, b); e != nil {
		t.Fatal("restore exact fixture")
	}
	link := m.fs.path(rel + ".hardlink")
	if e = windows.CreateHardLink(ptr(link), ptr(m.fs.path(rel)), 0); e != nil {
		t.Fatal("actual NTFS hardlink fixture unavailable")
	}
	_, e = m.OpenCredential(s)
	credentialWant(t, e, "UNSAFE_LOCAL_MATERIAL")
}

func TestCredentialCrossProcessExclusion(t *testing.T) {
	if os.Getenv("K03_CREDENTIAL_CHILD") == "1" {
		m, e := managerAt(os.Getenv("K03_CREDENTIAL_CHILD_ROOT"), nil)
		if e != nil {
			t.Fatal("child manager")
		}
		s := CredentialSelection{os.Getenv("K03_CREDENTIAL_CHILD_ID"), credentialBoard, credentialInc, credentialGrant}
		_, e = m.PrepareCredential(s)
		credentialWant(t, e, "CREDENTIAL_UNAVAILABLE")
		return
	}
	m, s, secret := credentialFixture(t)
	credentialIssue(t, m, s, secret)
	mat, e := m.OpenCredential(s)
	if e != nil {
		t.Fatal("parent material")
	}
	defer mat.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCredentialCrossProcessExclusion$")
	cmd.Env = append(os.Environ(), "K03_CREDENTIAL_CHILD=1", "K03_CREDENTIAL_CHILD_ROOT="+m.fs.root, "K03_CREDENTIAL_CHILD_ID="+s.InstallationID)
	if e = cmd.Run(); e != nil {
		t.Fatal("child did not confirm exclusion")
	}
}
