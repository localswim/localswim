//go:build windows && amd64

package setup

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

type fakeStore struct {
	entries               map[string]bool
	inspectErr, mutateErr error
	calls                 int
}

func (s *fakeStore) Membership(der []byte) (bool, error) { return s.entries[hash(der)], s.inspectErr }
func (s *fakeStore) Enroll(der []byte) error {
	s.calls++
	s.entries[hash(der)] = true
	return s.mutateErr
}
func (s *fakeStore) Remove(der []byte) error {
	s.calls++
	delete(s.entries, hash(der))
	return s.mutateErr
}
func fixture(t *testing.T) (*Manager, *time.Time, *fakeStore) {
	t.Helper()
	base := os.Getenv("N01_TEST_ROOT")
	if base == "" {
		base = t.TempDir()
	}
	root, e := os.MkdirTemp(base, "synthetic-")
	if e != nil {
		t.Fatal("create synthetic container")
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	s := &fakeStore{entries: map[string]bool{}}
	m, e := managerAt(filepath.Join(root, "installation"), s)
	if e != nil {
		t.Fatal("native private fixture unavailable", failure(e).Code)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.bind = func(int) error { return nil }
	return m, &now, s
}
func good(t *testing.T, r Response) {
	t.Helper()
	if r.Exit != 0 {
		t.Fatalf("wanted confirmed, got %s/%d", r.Envelope.Outcome, r.Exit)
	}
}
func wantCode(t *testing.T, r Response, code string, exit int) {
	t.Helper()
	if r.Exit != exit || r.Envelope.Error == nil || r.Envelope.Error.Code != code {
		t.Fatalf("wanted %s/%d; got outcome %s exit %d diagnostic %v", code, exit, r.Envelope.Outcome, r.Exit, r.Envelope.Error)
	}
}
func active(t *testing.T, m *Manager) *material {
	t.Helper()
	x, e := m.coherent(false)
	if e != nil || x == nil {
		t.Fatal("valid synthetic material unavailable")
	}
	return x
}
func TestNativeInitialRepeatAndRedaction(t *testing.T) {
	m, now, _ := fixture(t)
	r := m.Status()
	good(t, r)
	if r.Envelope.Result.State != "absent" {
		t.Fatal("absent observation")
	}
	if _, e := os.Stat(m.fs.root); !os.IsNotExist(e) {
		t.Fatal("status created root")
	}
	p := 48123
	r = m.Init(&p)
	good(t, r)
	x := active(t, m)
	if r.Envelope.Result.Port.Value == nil || *r.Envelope.Result.Port.Value != p || !r.Envelope.Result.Changed || r.Envelope.Result.CurrentUserRoot != "absent" {
		t.Fatal("initial publication fields")
	}
	if len(x.manifest.Hostname) != 42 || x.manifest.InstallationID == x.manifest.OwnerActorID || !x.issuer.NotAfter.Equal(now.Add(3650*day)) {
		t.Fatal("identity/lifetime")
	}
	before, _ := m.fs.read("active.json")
	r = m.Init(nil)
	good(t, r)
	after, _ := m.fs.read("active.json")
	if r.Envelope.Result.Changed || !bytes.Equal(before, after) {
		t.Fatal("repeat changed material")
	}
	other := p + 1
	wantCode(t, m.Init(&other), "CONFIG_CONFLICT", 2)
	owner, e := m.LoadNativeOwner()
	if e != nil || !owner.ActiveTokenMatches(x.owner.Token, x.manifest.InstallationID, *now) || owner.ActiveTokenMatches(x.owner.Token, "wrong", *now) || owner.ActiveTokenMatches(x.owner.Token, x.manifest.InstallationID, now.Add(31*day)) {
		t.Fatal("owner provider binding")
	}
	if _, e = json.Marshal(owner); e == nil {
		t.Fatal("owner credential serialized")
	}
	b, _ := json.Marshal(m.Status().Envelope)
	for _, secret := range []string{x.owner.Token, x.manifest.OwnerTokenSHA256, "PRIVATE KEY", m.fs.root} {
		if strings.Contains(string(b), secret) {
			t.Fatal("secret/private path leaked")
		}
	}
	ctx, e := m.ReadContext()
	if e != nil || ctx.Origin != origin(x.manifest) {
		t.Fatal("source connection context")
	}
	cfg, e := ctx.TLSConfig()
	if e != nil || cfg.InsecureSkipVerify || cfg.MinVersion != tls.VersionTLS12 || cfg.ServerName != x.manifest.Hostname {
		t.Fatal("pinned TLS config")
	}
	if _, e = x.leaf.Verify(x509.VerifyOptions{Roots: cfg.RootCAs, DNSName: ctx.Hostname, CurrentTime: *now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e != nil {
		t.Fatal("real x509 pinned validation")
	}
	if _, e = x.leaf.Verify(x509.VerifyOptions{Roots: cfg.RootCAs, DNSName: "wrong.localhost", CurrentTime: *now}); e == nil {
		t.Fatal("wrong name accepted")
	}
	ctx.IssuerSHA256 = strings.Repeat("0", 64)
	if _, e = ctx.TLSConfig(); e == nil {
		t.Fatal("wrong pin accepted")
	}
}
func TestNativeExclusionAndRunningExpiry(t *testing.T) {
	m, now, _ := fixture(t)
	good(t, m.Init(nil))
	x := active(t, m)
	lease, e := m.AcquireServer()
	if e != nil {
		t.Fatal("server lease")
	}
	defer lease.Close()
	good(t, m.Init(nil))
	good(t, m.Status())
	if m.Status().Envelope.Result.ExclusiveLock != "busy" {
		t.Fatal("held native lock not busy")
	}
	wantCode(t, m.RenewCertificate(x.manifest.InstallationID, x.manifest.IssuerSHA256), "INSTALLATION_BUSY", 3)
	if _, e = lease.TLSConfig().GetCertificate(&tls.ClientHelloInfo{}); e != nil {
		t.Fatal("initial handshake material")
	}
	*now = now.Add(31 * day)
	if _, e = lease.TLSConfig().GetCertificate(&tls.ClientHelloInfo{}); e == nil {
		t.Fatal("expired running handshake accepted")
	}
	lease.Close()
}
func TestStoppedRenewalAndOwnerConfirmation(t *testing.T) {
	m, now, _ := fixture(t)
	good(t, m.Init(nil))
	old := active(t, m)
	id, pin := old.manifest.InstallationID, old.manifest.IssuerSHA256
	wantCode(t, m.RenewOwner(id, pin, Confirmation{}), "CONFIRMATION_REQUIRED", 2)
	wantCode(t, m.RenewOwner(id, pin, Confirmation{true, "wrong"}), "CONFIRMATION_REFUSED", 2)
	*now = now.Add(31 * day)
	good(t, m.Status())
	if m.Status().Envelope.Result.NextAction != "renew_leaf" {
		t.Fatal("expiry priority")
	}
	if _, e := m.LoadNativeOwner(); e == nil {
		t.Fatal("expired credential loaded")
	}
	good(t, m.RenewCertificate(id, pin))
	leaf := active(t, m)
	if leaf.manifest.InstallationID != id || leaf.manifest.Hostname != old.manifest.Hostname || leaf.manifest.OwnerActorID != old.manifest.OwnerActorID || !bytes.Equal(leaf.bytes["issuer-key.pem"], old.bytes["issuer-key.pem"]) || !bytes.Equal(leaf.bytes["owner.json"], old.bytes["owner.json"]) || bytes.Equal(leaf.bytes["server-key.pem"], old.bytes["server-key.pem"]) {
		t.Fatal("leaf renewal identity/secret preservation")
	}
	if m.Status().Envelope.Result.NextAction != "renew_owner" {
		t.Fatal("owner expiry status")
	}
	good(t, m.RenewOwner(id, pin, Confirmation{true, "renew-owner " + id + " " + pin}))
	renewed := active(t, m)
	owner, e := m.LoadNativeOwner()
	if e != nil || owner.ActiveTokenMatches(old.owner.Token, id, *now) || !owner.ActiveTokenMatches(renewed.owner.Token, id, *now) || renewed.manifest.OwnerActorID != old.manifest.OwnerActorID || !bytes.Equal(renewed.bytes["server.pem"], leaf.bytes["server.pem"]) {
		t.Fatal("same actor owner rotation")
	}
	*now = old.issuer.NotAfter.Add(-time.Hour)
	wantCode(t, m.RenewCertificate(id, pin), "ISSUER_REPLACEMENT_REQUIRED", 2)
	if m.Status().Envelope.Result.NextAction != "issuer_replacement_decision" {
		t.Fatal("issuer replacement stop")
	}
}
func TestInterruptedExactCandidateReconciliation(t *testing.T) {
	for _, point := range []string{"transaction", "file:issuer.pem", "complete", "before-selector", "after-selector", "cleanup"} {
		t.Run(point, func(t *testing.T) {
			m, _, _ := fixture(t)
			m.checkpoint = func(p string) error {
				if p == point {
					return errors.New("injected interruption")
				}
				return nil
			}
			r := m.Init(nil)
			if r.Exit == 0 {
				t.Fatal("injected interruption confirmed")
			}
			tx, e := m.pending()
			if e != nil || tx == nil {
				t.Fatal("pending evidence lost")
			}
			var candidate *material
			if tx.ManifestSHA256 != nil {
				candidate, _ = m.readGeneration(filepath.Join("pending", tx.TransactionID, tx.CandidateGeneration), tx.CandidateGeneration, *tx.ManifestSHA256, false)
				if candidate == nil {
					candidate, _ = m.readGeneration(filepath.Join("generations", tx.CandidateGeneration), tx.CandidateGeneration, *tx.ManifestSHA256, false)
				}
			}
			m.checkpoint = func(string) error { return nil }
			r = m.Init(nil)
			if point == "transaction" || point == "file:issuer.pem" {
				wantCode(t, r, "SETUP_RECOVERY_REQUIRED", 4)
				return
			}
			good(t, r)
			after := active(t, m)
			if candidate == nil || after.manifest.Generation != candidate.manifest.Generation || after.owner.Token != candidate.owner.Token {
				t.Fatal("candidate replaced during reconciliation")
			}
			if point == "after-selector" || point == "cleanup" {
				if r.Envelope.Result.Changed {
					t.Fatal("cleanup claimed second renewal")
				}
			}
			if tx, e = m.pending(); e != nil || tx != nil {
				t.Fatal("pending cleanup")
			}
		})
	}
}
func TestPortFailureRetainsExactCandidate(t *testing.T) {
	m, _, _ := fixture(t)
	m.bind = func(int) error { return fail("PORT_UNAVAILABLE", 3) }
	wantCode(t, m.Init(nil), "PORT_UNAVAILABLE", 3)
	tx, e := m.pending()
	if e != nil || tx == nil || tx.ManifestSHA256 == nil {
		t.Fatal("complete candidate not retained")
	}
	if s, _, _ := m.readSelector(); s != nil {
		t.Fatal("port failure published")
	}
	c, e := m.readGeneration(filepath.Join("pending", tx.TransactionID, tx.CandidateGeneration), tx.CandidateGeneration, *tx.ManifestSHA256, false)
	if e != nil {
		t.Fatal("candidate invalid")
	}
	m.bind = func(int) error { return nil }
	good(t, m.Init(nil))
	if active(t, m).owner.Token != c.owner.Token {
		t.Fatal("port retry replaced bearer")
	}
}
func TestUnpublishedCustomPortRequiresMatchingRetry(t *testing.T) {
	m, _, _ := fixture(t)
	m.bind = func(int) error { return fail("PORT_UNAVAILABLE", 3) }
	p := 48123
	wantCode(t, m.Init(&p), "PORT_UNAVAILABLE", 3)
	m.bind = func(int) error { return nil }
	wantCode(t, m.Init(nil), "CONFIG_CONFLICT", 2)
	good(t, m.Init(&p))
	good(t, m.Init(nil))
}
func TestNativeDualExclusiveBinds(t *testing.T) {
	// Native socket probe only; no TLS/HTTP service, browser or fixed setup port.
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal("native IPv4 unavailable")
	}
	p := l.Addr().(*net.TCPAddr).Port
	if e = bindBoth(p); failure(e).Code != "PORT_UNAVAILABLE" {
		t.Fatal("IPv4 conflict accepted")
	}
	l.Close()
	if e = bindBoth(p); e != nil {
		t.Fatal("native dual loopback bind unavailable")
	}
	l, e = net.Listen("tcp6", net.JoinHostPort("::1", fmtPort(p)))
	if e != nil {
		t.Fatal("native IPv6 unavailable")
	}
	defer l.Close()
	if e = bindBoth(p); failure(e).Code != "PORT_UNAVAILABLE" {
		t.Fatal("IPv6 conflict accepted")
	}
}
func TestMaterialAndACLRefusal(t *testing.T) {
	for _, which := range []string{"missing-owner", "issuer-key-mismatch", "leaf-file-change", "manifest-change", "unknown-generation-file", "broad-ACL", "null-DACL"} {
		t.Run(which, func(t *testing.T) {
			m, _, _ := fixture(t)
			good(t, m.Init(nil))
			x := active(t, m)
			base := filepath.Join("generations", x.manifest.Generation)
			switch which {
			case "missing-owner":
				windows.DeleteFile(ptr(m.fs.path(filepath.Join(base, "owner.json"))))
			case "issuer-key-mismatch":
				if e := m.fs.replace(filepath.Join(base, "issuer-key.pem"), x.bytes["server-key.pem"]); e != nil {
					t.Fatal("fixture mutation")
				}
			case "leaf-file-change":
				if e := m.fs.replace(filepath.Join(base, "server.pem"), append(x.bytes["server.pem"], '\n')); e != nil {
					t.Fatal("fixture mutation")
				}
			case "manifest-change":
				if e := m.fs.replace(filepath.Join(base, "manifest.json"), append(x.bytes["manifest.json"], '\n')); e != nil {
					t.Fatal("fixture mutation")
				}
			case "unknown-generation-file":
				if e := m.fs.write(filepath.Join(base, "foreign"), []byte("synthetic\n")); e != nil {
					t.Fatal("fixture mutation")
				}
			case "broad-ACL", "null-DACL":
				var acl *windows.ACL
				if which == "broad-ACL" {
					sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
					if e != nil {
						t.Fatal("fixture descriptor")
					}
					acl, _, _ = sd.DACL()
				}
				if e := windows.SetNamedSecurityInfo(m.fs.path(base), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
					t.Fatal("fixture ACL mutation")
				}
			}
			if m.Init(nil).Exit == 0 {
				t.Fatal("unsafe material accepted")
			}
			s := m.Status()
			if s.Envelope.Result.State != "invalid" || s.Envelope.Result.InstallationID != nil {
				t.Fatal("partially validated identity exposed")
			}
		})
	}
}
func TestInjectedReparseAndHardlinkMetadata(t *testing.T) {
	// The host denied the native hardlink creation attempt (Win32 error 5).
	// These are injected metadata guard vectors, not native alias qualification.
	for _, x := range []windows.ByHandleFileInformation{{NumberOfLinks: 2}, {NumberOfLinks: 1, FileAttributes: windows.FILE_ATTRIBUTE_REPARSE_POINT}} {
		if safeFileInfo(x, false) {
			t.Fatal("aliased/reparse file metadata accepted")
		}
	}
	if safeFileInfo(windows.ByHandleFileInformation{FileAttributes: windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_DIRECTORY}, true) {
		t.Fatal("reparse directory metadata accepted")
	}
}
func TestStrictLocalFormats(t *testing.T) {
	for _, b := range [][]byte{[]byte("{\"schemaVersion\":1,\"schemaVersion\":1,\"generation\":\"x\",\"manifestSha256\":\"x\"}\n"), []byte("{\"schemaVersion\":1,\"generation\":\"x\",\"manifestSha256\":\"x\",\"unknown\":1}\n"), []byte("{\"schemaVersion\":1,\"generation\":\"x\"}\n"), []byte("{\"schemaVersion\":1,\"generation\":\"x\",\"manifestSha256\":\"x\"}\r\n"), []byte("{\"schemaVersion\":1,\"generation\":\"\\ud800\",\"manifestSha256\":\"x\"}\n")} {
		var x selector
		if jsonDecode(b, &x) == nil {
			t.Fatal("malformed local format accepted")
		}
	}
}
func TestTrustPlanFakeStoreAndPublicOnlyRemoval(t *testing.T) {
	m, now, store := fixture(t)
	good(t, m.Init(nil))
	x := active(t, m)
	id, pin := x.manifest.InstallationID, x.manifest.IssuerSHA256
	p, e := m.PlanTrust("enroll", id, pin)
	if e != nil || p.StoreScope == "" || p.Consequence == "" {
		t.Fatal("trust plan")
	}
	wantCode(t, m.ApplyTrust(p, Confirmation{}), "CONFIRMATION_REQUIRED", 2)
	if store.calls != 0 {
		t.Fatal("unconfirmed store mutation")
	}
	wantCode(t, m.ApplyTrust(p, Confirmation{true, "wrong"}), "CONFIRMATION_REFUSED", 2)
	good(t, m.ApplyTrust(p, Confirmation{true, p.ConfirmationLine}))
	good(t, m.ApplyTrust(p, Confirmation{true, p.ConfirmationLine}))
	if store.calls != 1 {
		t.Fatal("repeat enrollment not idempotent")
	}
	if _, e = m.PlanTrust("enroll", id, strings.Repeat("0", 64)); e == nil {
		t.Fatal("mismatching public pin")
	}
	if e = windows.DeleteFile(ptr(m.fs.path(filepath.Join("generations", x.manifest.Generation, "issuer-key.pem")))); e != nil {
		t.Fatal("synthetic private key loss")
	}
	*now = x.issuer.NotAfter.Add(day)
	p, e = m.PlanTrust("remove", id, pin)
	if e != nil {
		t.Fatal("intact public trust cannot retire after key loss/expiry")
	}
	r := m.ApplyTrust(p, Confirmation{true, p.ConfirmationLine})
	good(t, r)
	if r.Envelope.Result != nil {
		t.Fatal("public-only removal implied complete material validation")
	}
	if store.calls != 2 {
		t.Fatal("exact fake-store removal")
	}
}
func TestAmbiguousAndIncompleteStops(t *testing.T) {
	m, _, _ := fixture(t)
	m.random = bytes.NewReader(nil)
	r := m.Init(nil)
	if r.Exit != 1 {
		t.Fatal("random failure outcome")
	}
	m.random = productionRandom
	if m.Init(nil).Exit == 0 {
		t.Fatal("incomplete root regenerated identity")
	}
	m2, _, _ := fixture(t)
	m2.checkpoint = func(p string) error {
		if p == "complete" {
			return errors.New("injected")
		}
		return nil
	}
	m2.Init(nil)
	tx, e := m2.pending()
	if e != nil || tx == nil {
		t.Fatal("pending fixture")
	}
	tx2, e := uuid(productionRandom)
	if e != nil {
		t.Fatal("synthetic id")
	}
	if e = m2.fs.mkdir(filepath.Join("pending", tx2)); e != nil {
		t.Fatal("ambiguous fixture")
	}
	m2.checkpoint = func(string) error { return nil }
	wantCode(t, m2.Init(nil), "SETUP_RECOVERY_REQUIRED", 4)
}

func TestNativeLockChild(t *testing.T) {
	root := os.Getenv("N01_LOCK_CHILD_ROOT")
	if root == "" {
		return
	}
	m, e := managerAt(root, nil)
	if e != nil {
		t.Fatal("child synthetic manager")
	}
	release, e := m.fs.lock(false)
	if release != nil {
		release()
		t.Fatal("child acquired held installation lock")
	}
	if failure(e).Code != "INSTALLATION_BUSY" {
		t.Fatal("child did not observe native exclusion")
	}
}
func TestNativeCrossProcessExclusion(t *testing.T) {
	m, _, _ := fixture(t)
	good(t, m.Init(nil))
	lease, e := m.AcquireServer()
	if e != nil {
		t.Fatal("parent lease")
	}
	defer lease.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestNativeLockChild$")
	child.Env = append(os.Environ(), "N01_LOCK_CHILD_ROOT="+m.fs.root)
	if e = child.Run(); e != nil {
		t.Fatal("native child exclusion failed")
	}
}
func TestStartupLeafRenewalAndClockRefusal(t *testing.T) {
	m, now, _ := fixture(t)
	good(t, m.Init(nil))
	old := active(t, m)
	*now = now.Add(24 * day)
	lease, e := m.AcquireServer()
	if e != nil {
		t.Fatal("stopped startup renewal")
	}
	lease.Close()
	renewed := active(t, m)
	if renewed.manifest.Generation == old.manifest.Generation || renewed.owner.Token != old.owner.Token || renewed.manifest.IssuerSHA256 != old.manifest.IssuerSHA256 {
		t.Fatal("startup did not use same renewal lifecycle")
	}
	*now = old.issuer.NotBefore.Add(-time.Second)
	if m.Status().Envelope.Result.NextAction != "repair_decision" {
		t.Fatal("clock skew not visible")
	}
	if _, e = m.ReadContext(); e == nil {
		t.Fatal("future certificate accepted")
	}
	if _, e = m.AcquireServer(); e == nil {
		t.Fatal("future certificate service lease")
	}
}
func TestScopeValidationUsesCertificatesNotOnlyHashes(t *testing.T) {
	for _, scope := range []string{"extra-name", "client-auth", "ip-address"} {
		t.Run(scope, func(t *testing.T) {
			m, now, _ := fixture(t)
			good(t, m.Init(nil))
			x := active(t, m)
			tpl := &x509.Certificate{SerialNumber: x.leaf.SerialNumber, Subject: pkix.Name{CommonName: x.manifest.Hostname}, NotBefore: x.leaf.NotBefore, NotAfter: x.leaf.NotAfter, SignatureAlgorithm: x509.ECDSAWithSHA256, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{x.manifest.Hostname}}
			switch scope {
			case "extra-name":
				tpl.DNSNames = append(tpl.DNSNames, "other.localhost")
			case "client-auth":
				tpl.ExtKeyUsage = append(tpl.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
			case "ip-address":
				tpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
			}
			leafKey := x.leafKey.(*ecdsa.PrivateKey)
			der, e := x509.CreateCertificate(rand.Reader, tpl, x.issuer, &leafKey.PublicKey, x.issuerKey)
			if e != nil {
				t.Fatal("synthetic disallowed scope certificate")
			}
			c, e := x509.ParseCertificate(der)
			if e != nil {
				t.Fatal("synthetic certificate parse")
			}
			x.bytes["server.pem"] = append(certificatePEM(c), certificatePEM(x.issuer)...)
			x.manifest.LeafSHA256 = hash(der)
			x.manifest.GenerationCreatedAt = stamp(*now)
			x.finish()
			if _, e = validateMaterial(x.bytes, x.manifest.Generation, hash(x.bytes["manifest.json"]), false); e == nil {
				t.Fatal("forbidden signing scope survived complete rehash")
			}
		})
	}
}
func TestUncertainSelectorFlushAndUnexpectedSelectorFence(t *testing.T) {
	m, _, _ := fixture(t)
	m.checkpoint = func(p string) error {
		if p == "selector-flushed" {
			return errors.New("injected selector interruption")
		}
		return nil
	}
	wantCode(t, m.Init(nil), "INVOCATION_OR_IO", 1)
	m.checkpoint = func(string) error { return nil }
	wantCode(t, m.Init(nil), "SETUP_RECOVERY_REQUIRED", 4)
	m2, _, _ := fixture(t)
	m2.checkpoint = func(p string) error {
		if p == "after-selector" {
			return errors.New("injected")
		}
		return nil
	}
	wantCode(t, m2.Init(nil), "SETUP_OUTCOME_UNKNOWN", 4)
	if e := m2.fs.replace("active.json", []byte("{\"bad\":true}\n")); e != nil {
		t.Fatal("synthetic malformed selector")
	}
	wantCode(t, m2.Init(nil), "SETUP_RECOVERY_REQUIRED", 4)
}
func TestInvalidPortAndPathAliasesHaveNoEffects(t *testing.T) {
	m, _, _ := fixture(t)
	p := 0
	wantCode(t, m.Init(&p), "INVOCATION_OR_IO", 1)
	if _, e := os.Stat(m.fs.root); !os.IsNotExist(e) {
		t.Fatal("invalid port created material")
	}
	for _, path := range []string{m.fs.root + ".", m.fs.root + " ", m.fs.root + ":stream", `\\?\` + m.fs.root, filepath.Dir(m.fs.root) + `\..\installation`} {
		if _, e := newNativeFS(path); e == nil {
			t.Fatal("aliased native root accepted")
		}
	}
}
func TestTrustUnavailableAndPossibleEffectRemainDistinct(t *testing.T) {
	m, _, store := fixture(t)
	good(t, m.Init(nil))
	x := active(t, m)
	p, e := m.PlanTrust("enroll", x.manifest.InstallationID, x.manifest.IssuerSHA256)
	if e != nil {
		t.Fatal("trust plan")
	}
	c := Confirmation{true, p.ConfirmationLine}
	store.inspectErr = errors.New("synthetic store unavailable")
	wantCode(t, m.ApplyTrust(p, c), "TRUST_STORE_UNAVAILABLE", 3)
	if store.calls != 0 {
		t.Fatal("unavailable store changed")
	}
	if m.Status().Envelope.Result.CurrentUserRoot != "unavailable" {
		t.Fatal("store unavailable reported absent")
	}
	store.inspectErr = nil
	store.mutateErr = errors.New("synthetic uncertain mutation")
	wantCode(t, m.ApplyTrust(p, c), "SETUP_OUTCOME_UNKNOWN", 4)
	store.mutateErr = nil
	good(t, m.ApplyTrust(p, c))
	if store.calls != 1 {
		t.Fatal("reconciliation repeated already observed exact effect")
	}
}

func TestNativeAliasRefusal(t *testing.T) {
	m, _, _ := fixture(t)
	good(t, m.Init(nil))
	x := active(t, m)
	// Native junction buffers use the documented MountPointReparseBuffer layout.
	// Both the redirect and target are synthetic private-test-container paths.
	junction := filepath.Join(filepath.Dir(m.fs.root), "junction")
	if e := windows.CreateDirectory(ptr(junction), m.fs.attrs()); e != nil {
		t.Fatal("synthetic junction directory")
	}
	h, e := windows.CreateFile(ptr(junction), windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal("synthetic junction handle")
	}
	sub := utf16.Encode([]rune(`\??\` + m.fs.root))
	printName := utf16.Encode([]rune(m.fs.root))
	names := append(append(append(sub, 0), printName...), 0)
	buf := make([]byte, 16+len(names)*2)
	binary.LittleEndian.PutUint32(buf, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:], uint16(len(sub)*2))
	binary.LittleEndian.PutUint16(buf[12:], uint16((len(sub)+1)*2))
	binary.LittleEndian.PutUint16(buf[14:], uint16(len(printName)*2))
	for i, v := range names {
		binary.LittleEndian.PutUint16(buf[16+i*2:], v)
	}
	var returned uint32
	e = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
	windows.CloseHandle(h)
	if e != nil {
		t.Fatal("native synthetic junction fixture denied")
	}
	redirected, e := managerAt(junction, nil)
	if e != nil {
		t.Fatal("synthetic alias lexical setup")
	}
	wantCode(t, redirected.Init(nil), "UNSAFE_LOCAL_MATERIAL", 2)
	if e = os.Link(m.fs.path(filepath.Join("generations", x.manifest.Generation, "owner.json")), filepath.Join(filepath.Dir(m.fs.root), "owner-alias.json")); e != nil {
		t.Fatal("native synthetic hardlink fixture denied")
	}
	if _, e = m.LoadNativeOwner(); failure(e).Code != "UNSAFE_LOCAL_MATERIAL" {
		t.Fatal("native hardlinked owner material accepted")
	}
}

func TestConcurrentReadDuringOwnerPublication(t *testing.T) {
	m, now, _ := fixture(t)
	good(t, m.Init(nil))
	before := active(t, m)
	*now = now.Add(time.Hour)
	started := make(chan struct{})
	done := make(chan string, 1)
	go func() {
		close(started)
		for i := 0; i < 20; i++ {
			mat, e := m.coherent(false)
			if e != nil {
				if failure(e).Code == "STATUS_UNAVAILABLE" {
					continue
				}
				done <- "coherent generation read failed"
				return
			}
			if mat == nil || mat.manifest.InstallationID != before.manifest.InstallationID || mat.manifest.OwnerActorID != before.manifest.OwnerActorID || mat.owner.InstallationID != mat.manifest.InstallationID || mat.owner.ActorID != mat.manifest.OwnerActorID {
				done <- "reader mixed installation material"
				return
			}
		}
		done <- ""
	}()
	<-started
	good(t, m.RenewOwner(before.manifest.InstallationID, before.manifest.IssuerSHA256, Confirmation{true, "renew-owner " + before.manifest.InstallationID + " " + before.manifest.IssuerSHA256}))
	if message := <-done; message != "" {
		t.Fatal(message)
	}
}
