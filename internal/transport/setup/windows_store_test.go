//go:build windows && amd64

package setup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Every native certificate operation in this file targets only a nonpersistent
// MEMORY store. The production PHYSICAL open function is NEVER invoked by tests.
type memoryRootFixture struct {
	store       *windowsRootStore
	handle      windows.Handle
	refs        int
	opens       []windowsStoreSelector
	adds, drops int
	closes      int
}

func memoryRoot(t *testing.T) *memoryRootFixture {
	t.Helper()
	h, err := windows.CertOpenStore(windows.CERT_STORE_PROV_MEMORY, 0, 0, windows.CERT_STORE_CREATE_NEW_FLAG|windows.CERT_STORE_ENUM_ARCHIVED_FLAG, 0)
	if err != nil {
		t.Fatal("nonpersistent memory store unavailable")
	}
	runtime.LockOSThread()
	account, err := currentStoreAccount()
	runtime.UnlockOSThread()
	if err != nil {
		t.Fatal("current process account unavailable")
	}
	f := &memoryRootFixture{handle: h}
	calls := nativeWindowsStoreCalls()
	duplicateStore := windows.NewLazySystemDLL("crypt32.dll").NewProc("CertDuplicateStore")
	calls.open = func(s windowsStoreSelector) (windows.Handle, error) {
		f.opens = append(f.opens, s)
		// This guard fails BEFORE any live open could occur, including a wrong
		// selector. No fallback is delegated to the production open function.
		if s != rootStoreSelector(s.flags&windows.CERT_STORE_READONLY_FLAG == 0) {
			return 0, windows.ERROR_INVALID_PARAMETER
		}
		v, _, _ := duplicateStore.Call(uintptr(h))
		if v == 0 {
			return 0, windows.ERROR_INVALID_HANDLE
		}
		return windows.Handle(v), nil
	}
	calls.close = func(h windows.Handle, flags uint32) error {
		f.closes++
		if flags != windows.CERT_CLOSE_STORE_CHECK_FLAG {
			t.Error("store close must check outstanding contexts")
		}
		// The fixture deliberately retains another store reference between calls.
		// CHECK on a duplicate reports pending closure until that reference closes.
		// Check all context ownership here, close this duplicate normally, and use
		// native CHECK on the final owning MEMORY handle in cleanup below.
		if f.refs != 0 {
			t.Error("contexts retained at operation close")
		}
		return windows.CertCloseStore(h, 0)
	}
	calls.enum = func(h windows.Handle, prev *windows.CertContext) (*windows.CertContext, error) {
		if prev != nil {
			f.refs--
		}
		c, e := windows.CertEnumCertificatesInStore(h, prev)
		if c != nil {
			f.refs++
		}
		return c, e
	}
	calls.duplicate = func(c *windows.CertContext) *windows.CertContext {
		dup := windows.CertDuplicateCertificateContext(c)
		if dup != nil {
			f.refs++
		}
		return dup
	}
	calls.free = func(c *windows.CertContext) error {
		f.refs--
		return windows.CertFreeCertificateContext(c)
	}
	calls.create = func(encoding uint32, der *byte, size uint32) (*windows.CertContext, error) {
		c, e := windows.CertCreateCertificateContext(encoding, der, size)
		if c != nil {
			f.refs++
		}
		return c, e
	}
	calls.add = func(h windows.Handle, c *windows.CertContext, disposition uint32, out **windows.CertContext) error {
		f.adds++
		if disposition != windows.CERT_STORE_ADD_NEW || out != nil {
			t.Fatal("unsafe add disposition or unowned output context")
		}
		return windows.CertAddCertificateContextToStore(h, c, disposition, out)
	}
	calls.remove = func(c *windows.CertContext) error {
		f.drops++
		f.refs-- // The native delete always consumes this reference.
		return windows.CertDeleteCertificateFromStore(c)
	}
	f.store = &windowsRootStore{account: account, calls: calls}
	t.Cleanup(func() {
		if f.refs != 0 {
			t.Errorf("context references outstanding: %d", f.refs)
		}
		if err := windows.CertCloseStore(h, windows.CERT_CLOSE_STORE_CHECK_FLAG); err != nil {
			t.Error("memory store did not close cleanly")
		}
	})
	return f
}

func syntheticRootDER(t *testing.T, serial int64) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("synthetic key generation failed")
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "synthetic same-name issuer"},
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal("synthetic certificate generation failed")
	}
	return der
}

func seedMemory(t *testing.T, f *memoryRootFixture, der []byte) {
	t.Helper()
	c, err := windows.CertCreateCertificateContext(windows.X509_ASN_ENCODING, &der[0], uint32(len(der)))
	if err != nil {
		t.Fatal("synthetic context creation failed")
	}
	defer windows.CertFreeCertificateContext(c)
	if windows.CertAddCertificateContextToStore(f.handle, c, windows.CERT_STORE_ADD_ALWAYS, nil) != nil {
		t.Fatal("synthetic memory seed failed")
	}
}

func requireStoreError(t *testing.T, err error, code string, exit int) {
	t.Helper()
	if err == nil || failure(err).Code != code || failure(err).Exit != exit {
		t.Fatalf("expected %s/%d, got %v", code, exit, err)
	}
}

func TestWindowsRootSelectorsAndMemoryLifecycle(t *testing.T) {
	f := memoryRoot(t)
	der := syntheticRootDER(t, 1)
	other := syntheticRootDER(t, 2)
	seedMemory(t, f, other)
	if found, err := f.store.Membership(der); found || err != nil {
		t.Fatal("different same-name root membership failed", found, err)
	}
	for i := 0; i < 2; i++ {
		if err := f.store.Enroll(der); err != nil {
			t.Fatal("memory enrollment failed", err)
		}
	}
	if f.adds != 1 {
		t.Fatal("repeated enrollment was not idempotent")
	}
	if found, err := f.store.Membership(der); !found || err != nil {
		t.Fatal("exact enrollment not observed")
	}
	// Duplicates are possible in a store; remove every exact DER copy only.
	seedMemory(t, f, der)
	if err := f.store.Remove(der); err != nil {
		t.Fatal("memory removal failed", err)
	}
	if f.drops != 2 {
		t.Fatal("exact duplicate removal incomplete")
	}
	if err := f.store.Remove(der); err != nil || f.drops != 2 {
		t.Fatal("absent removal not idempotent")
	}
	if found, err := f.store.Membership(other); !found || err != nil {
		t.Fatal("unrelated same-name root changed")
	}
	for _, s := range f.opens {
		// Assert literal values independently of the production helper.
		flags := uint32(0x00010000 | 0x00004000 | 0x00000200)
		if s.flags&0x00008000 != 0 {
			flags |= 0x00008000
		}
		if s.provider != 14 || s.encoding != 0 || s.crypt != 0 || s.flags != flags || s.name != `Root\.Default` {
			t.Fatal("wrong physical current-user store selector")
		}
	}
	if len(f.opens) == 0 || f.closes != len(f.opens) || f.refs != 0 {
		t.Fatal("store/context ownership mismatch")
	}
}

func TestWindowsRootSameIssuerSerialCannotReplace(t *testing.T) {
	f := memoryRoot(t)
	a, b := syntheticRootDER(t, 5), syntheticRootDER(t, 5)
	seedMemory(t, f, a)
	// Same subject/issuer and serial, different DER/key: native matching rules
	// can permit both entries. Neither enrollment nor removal may replace a.
	if err := f.store.Enroll(b); err != nil {
		t.Fatal("distinct DER enrollment failed", err)
	}
	if found, err := f.store.Membership(a); !found || err != nil {
		t.Fatal("existing issuer-serial entry replaced")
	}
	if found, err := f.store.Membership(b); !found || err != nil {
		t.Fatal("new exact DER not present")
	}
	if err := f.store.Remove(b); err != nil {
		t.Fatal("new exact DER removal failed", err)
	}
	if found, err := f.store.Membership(a); !found || err != nil {
		t.Fatal("same issuer/serial removal changed unrelated DER")
	}
}

func TestWindowsRootArchivedExactMembershipAndRemoval(t *testing.T) {
	f := memoryRoot(t)
	der := syntheticRootDER(t, 6)
	seedMemory(t, f, der)
	c, err := windows.CertEnumCertificatesInStore(f.handle, nil)
	if err != nil || c == nil {
		t.Fatal("synthetic archived fixture context unavailable")
	}
	setProperty := windows.NewLazySystemDLL("crypt32.dll").NewProc("CertSetCertificateContextProperty")
	emptyBlob := windows.CryptDataBlob{}
	// CERT_ARCHIVED_PROP_ID == 19 (Microsoft reference-source CryptoAPI constants).
	ok, _, _ := setProperty.Call(uintptr(unsafe.Pointer(c)), 19, 0, uintptr(unsafe.Pointer(&emptyBlob)))
	runtime.KeepAlive(emptyBlob)
	if err := windows.CertFreeCertificateContext(c); err != nil || ok == 0 {
		t.Fatal("native archived property was not set")
	}
	if found, err := f.store.Membership(der); !found || err != nil {
		t.Fatal("archived exact DER was hidden from membership", err)
	}
	if err := f.store.Remove(der); err != nil || f.drops != 1 {
		t.Fatal("archived exact DER was not removed", err)
	}
	if found, err := f.store.Membership(der); found || err != nil {
		t.Fatal("archived exact DER remained after removal")
	}
}

func TestWindowsRootConcurrentExactAdditionRefusesReplacement(t *testing.T) {
	f := memoryRoot(t)
	der := syntheticRootDER(t, 7)
	nativeAdd := f.store.calls.add
	f.store.calls.add = func(h windows.Handle, c *windows.CertContext, disposition uint32, out **windows.CertContext) error {
		seedMemory(t, f, der) // Simulate another writer after the absence scan.
		err := nativeAdd(h, c, disposition, out)
		if !errors.Is(err, syscall.Errno(windows.CRYPT_E_EXISTS)) {
			t.Fatal("native ADD_NEW duplicate refusal branch not reached")
		}
		return err
	}
	requireStoreError(t, f.store.Enroll(der), "SETUP_OUTCOME_UNKNOWN", 4)
	if err := f.store.Enroll(der); err != nil || f.adds != 1 {
		t.Fatal("explicit retry failed exact membership reconciliation")
	}
}

func TestWindowsRootInvalidIssuerBeforeOpen(t *testing.T) {
	f := memoryRoot(t)
	m, _, _ := fixture(t)
	good(t, m.Init(nil))
	leaf := active(t, m).leaf.Raw
	for _, der := range [][]byte{nil, []byte("invalid"), leaf, append(syntheticRootDER(t, 8), 0)} {
		requireStoreError(t, f.store.Enroll(der), "IDENTITY_MISMATCH", 2)
		requireStoreError(t, f.store.Remove(der), "IDENTITY_MISMATCH", 2)
		if found, err := f.store.Membership(der); found || err == nil {
			t.Fatal("invalid issuer membership was not refused")
		}
	}
	if len(f.opens) != 0 || f.adds != 0 || f.drops != 0 {
		t.Fatal("invalid material reached store")
	}
}

func TestWindowsRootBoundAccountAndNoFallback(t *testing.T) {
	for _, scenario := range []string{"pid", "sid", "unknown", "open"} {
		t.Run(scenario, func(t *testing.T) {
			f := memoryRoot(t)
			der := syntheticRootDER(t, 9)
			account := f.store.account
			switch scenario {
			case "pid":
				account.pid++
			case "sid":
				account.sid = "synthetic-other-account"
			case "unknown":
				f.store.calls.account = func() (windowsStoreAccount, error) { return account, windows.ERROR_ACCESS_DENIED }
			case "open":
				f.store.calls.open = func(s windowsStoreSelector) (windows.Handle, error) {
					f.opens = append(f.opens, s)
					return 0, windows.ERROR_ACCESS_DENIED
				}
			}
			if scenario == "pid" || scenario == "sid" {
				f.store.calls.account = func() (windowsStoreAccount, error) { return account, nil }
			}
			requireStoreError(t, f.store.Enroll(der), "TRUST_STORE_UNAVAILABLE", 3)
			wantOpens := 0
			if scenario == "open" {
				wantOpens = 1
			}
			if len(f.opens) != wantOpens || f.adds != 0 {
				t.Fatal("refusal attempted mutation or fallback")
			}
		})
	}
}

func TestWindowsRootEnumerationFailureConsumesPrevious(t *testing.T) {
	f := memoryRoot(t)
	der := syntheticRootDER(t, 11)
	seedMemory(t, f, der)
	nativeEnum := f.store.calls.enum
	f.store.calls.enum = func(h windows.Handle, prev *windows.CertContext) (*windows.CertContext, error) {
		if prev != nil {
			if err := f.store.calls.free(prev); err != nil {
				t.Fatal("failed to consume previous context")
			}
			return nil, windows.ERROR_ACCESS_DENIED
		}
		return nativeEnum(h, prev)
	}
	if found, err := f.store.Membership(der); found || err == nil {
		t.Fatal("partial enumeration produced successful membership")
	}
	requireStoreError(t, f.store.Remove(der), "TRUST_STORE_UNAVAILABLE", 3)
	if f.refs != 0 || f.drops != 0 {
		t.Fatal("enumeration failure leaked or deleted retained context")
	}
}

func TestWindowsRootDuplicateFailureAndWrongStore(t *testing.T) {
	for _, scenario := range []string{"duplicate", "wrong-store"} {
		t.Run(scenario, func(t *testing.T) {
			f := memoryRoot(t)
			der := syntheticRootDER(t, 13)
			seedMemory(t, f, der)
			if scenario == "duplicate" {
				f.store.calls.duplicate = func(*windows.CertContext) *windows.CertContext { return nil }
			} else {
				nativeEnum := f.store.calls.enum
				f.store.calls.enum = func(h windows.Handle, prev *windows.CertContext) (*windows.CertContext, error) {
					c, err := nativeEnum(h, prev)
					if c != nil {
						// Return a separate real detached context (Store == 0);
						// never alter the native read-only CERT_CONTEXT structure.
						f.store.calls.free(c)
						c, err = f.store.calls.create(windows.X509_ASN_ENCODING, &der[0], uint32(len(der)))
					}
					return c, err
				}
			}
			requireStoreError(t, f.store.Remove(der), "TRUST_STORE_UNAVAILABLE", 3)
			if f.drops != 0 || f.refs != 0 {
				t.Fatal("invalid context mutated or leaked")
			}
		})
	}
}

func TestWindowsRootDeleteFailureConsumesInputAndFreesRest(t *testing.T) {
	f := memoryRoot(t)
	der := syntheticRootDER(t, 15)
	seedMemory(t, f, der)
	seedMemory(t, f, der)
	f.store.calls.remove = func(c *windows.CertContext) error {
		f.drops++
		// Reproduce the documented consuming failure, without live mutation.
		if err := f.store.calls.free(c); err != nil {
			t.Fatal("failure context was not consumed")
		}
		return windows.ERROR_ACCESS_DENIED
	}
	requireStoreError(t, f.store.Remove(der), "SETUP_OUTCOME_UNKNOWN", 4)
	if f.drops != 1 || f.refs != 0 {
		t.Fatal("delete failure retried or freed a consumed reference")
	}
}

func TestWindowsRootPostEffectFailuresAreUnknown(t *testing.T) {
	for _, scenario := range []string{"add-failure", "readback", "wrong-result", "account-change", "close"} {
		t.Run(scenario, func(t *testing.T) {
			f := memoryRoot(t)
			der := syntheticRootDER(t, 17)
			nativeAdd, nativeEnum, nativeClose := f.store.calls.add, f.store.calls.enum, f.store.calls.close
			if scenario == "add-failure" || scenario == "wrong-result" {
				f.store.calls.add = func(h windows.Handle, c *windows.CertContext, d uint32, out **windows.CertContext) error {
					if scenario == "wrong-result" {
						f.adds++
						return nil
					}
					if err := nativeAdd(h, c, d, out); err != nil {
						t.Fatal("intended successful effect unavailable")
					}
					return windows.ERROR_ACCESS_DENIED
				}
			}
			if scenario == "readback" {
				f.store.calls.enum = func(h windows.Handle, prev *windows.CertContext) (*windows.CertContext, error) {
					if f.adds > 0 {
						return nil, windows.ERROR_ACCESS_DENIED
					}
					return nativeEnum(h, prev)
				}
			}
			if scenario == "account-change" {
				f.store.calls.account = func() (windowsStoreAccount, error) {
					a := f.store.account
					if f.adds > 0 {
						a.pid++
					}
					return a, nil
				}
			}
			if scenario == "close" {
				f.store.calls.close = func(h windows.Handle, flags uint32) error {
					if err := nativeClose(h, flags); err != nil {
						t.Fatal("native memory close failed")
					}
					return windows.ERROR_ACCESS_DENIED
				}
			}
			requireStoreError(t, f.store.Enroll(der), "SETUP_OUTCOME_UNKNOWN", 4)
			if f.adds != 1 || f.refs != 0 || f.closes != 1 {
				t.Fatal("intended post-effect branch not reached or resources leaked")
			}
		})
	}
}

func TestWindowsRootManagerGateWithMemoryProvider(t *testing.T) {
	f := memoryRoot(t)
	m, _, _ := fixture(t)
	m.store = f.store
	good(t, m.Init(nil))
	mat := active(t, m)
	plan, err := m.PlanTrust("enroll", mat.manifest.InstallationID, mat.manifest.IssuerSHA256)
	if err != nil {
		t.Fatal("synthetic plan failed")
	}
	if f.adds != 0 || f.drops != 0 {
		t.Fatal("initialization or planning implicitly changed trust")
	}
	wantCode(t, m.ApplyTrust(plan, Confirmation{}), "CONFIRMATION_REQUIRED", 2)
	wantCode(t, m.ApplyTrust(plan, Confirmation{true, "wrong"}), "CONFIRMATION_REFUSED", 2)
	tampered := plan
	tampered.Authority = "wrong.localhost:47443"
	wantCode(t, m.ApplyTrust(tampered, Confirmation{true, plan.ConfirmationLine}), "IDENTITY_MISMATCH", 2)
	if f.adds != 0 {
		t.Fatal("unconfirmed or changed plan reached add")
	}
	lease, err := m.AcquireServer()
	if err != nil {
		t.Fatal("synthetic server exclusion unavailable")
	}
	wantCode(t, m.ApplyTrust(plan, Confirmation{true, plan.ConfirmationLine}), "INSTALLATION_BUSY", 3)
	lease.Close()
	good(t, m.ApplyTrust(plan, Confirmation{true, plan.ConfirmationLine}))
	good(t, m.ApplyTrust(plan, Confirmation{true, plan.ConfirmationLine}))
	if f.adds != 1 {
		t.Fatal("manager exact confirmed enrollment not idempotent")
	}
	remove, err := m.PlanTrust("remove", mat.manifest.InstallationID, mat.manifest.IssuerSHA256)
	if err != nil {
		t.Fatal("synthetic removal plan unavailable")
	}
	good(t, m.ApplyTrust(remove, Confirmation{true, remove.ConfirmationLine}))
	if f.drops != 1 {
		t.Fatal("manager exact confirmed removal missing")
	}
}

func TestWindowsRootClosedMemoryHandleUnavailable(t *testing.T) {
	h, err := windows.CertOpenStore(windows.CERT_STORE_PROV_MEMORY, 0, 0, windows.CERT_STORE_CREATE_NEW_FLAG, 0)
	if err != nil {
		t.Fatal("memory open unavailable")
	}
	// Exercise the injected invalid-handle branch rather than pass a freed native
	// pointer back into CryptoAPI, whose invalid-memory behavior is not specified.
	if err = windows.CertCloseStore(h, windows.CERT_CLOSE_STORE_CHECK_FLAG); err != nil {
		t.Fatal("memory close unavailable")
	}
	f := memoryRoot(t)
	f.store.calls.open = func(windowsStoreSelector) (windows.Handle, error) { return 0, windows.ERROR_INVALID_HANDLE }
	if found, err := f.store.Membership(syntheticRootDER(t, 19)); found || !errors.As(err, new(*Failure)) {
		t.Fatal("closed/unavailable store falsely reported absence")
	}
}

func TestWindowsRootNativeOwnedMemoryClose(t *testing.T) {
	calls := nativeWindowsStoreCalls()
	runtime.LockOSThread()
	account, err := calls.account()
	runtime.UnlockOSThread()
	if err != nil {
		t.Fatal("native account observation unavailable")
	}
	opens, closes := 0, 0
	calls.open = func(s windowsStoreSelector) (windows.Handle, error) {
		opens++
		if s != rootStoreSelector(true) {
			t.Fatal("unexpected production selection")
		}
		return windows.CertOpenStore(windows.CERT_STORE_PROV_MEMORY, 0, 0, windows.CERT_STORE_CREATE_NEW_FLAG, 0)
	}
	nativeClose := calls.close
	calls.close = func(h windows.Handle, flags uint32) error {
		closes++
		if flags != windows.CERT_CLOSE_STORE_CHECK_FLAG {
			t.Fatal("ownership check missing")
		}
		return nativeClose(h, flags)
	}
	s := &windowsRootStore{account: account, calls: calls}
	if err := s.Enroll(syntheticRootDER(t, 23)); err != nil || opens != 1 || closes != 1 {
		t.Fatal("independently owned native memory store did not close cleanly", err)
	}
}

func TestWindowsRootNativeImpersonationRefusal(t *testing.T) {
	f := memoryRoot(t)
	der := syntheticRootDER(t, 29)
	var process, impersonation windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &process); err != nil {
		t.Fatal("synthetic impersonation token source unavailable")
	}
	defer process.Close()
	if err := windows.DuplicateTokenEx(process, windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &impersonation); err != nil {
		t.Fatal("synthetic same-account token duplication unavailable")
	}
	defer impersonation.Close()
	runtime.LockOSThread()
	if err := windows.SetThreadToken(nil, impersonation); err != nil {
		runtime.UnlockOSThread()
		t.Fatal("synthetic thread impersonation unavailable")
	}
	defer func() {
		if err := windows.RevertToSelf(); err != nil {
			// Keep the thread locked on failure so Go destroys it as this test
			// goroutine exits rather than reusing an impersonating thread.
			t.Error("synthetic thread impersonation cleanup failed")
			return
		}
		runtime.UnlockOSThread()
	}()
	requireStoreError(t, f.store.Enroll(der), "TRUST_STORE_UNAVAILABLE", 3)
	if len(f.opens) != 0 || f.adds != 0 {
		t.Fatal("impersonating thread reached certificate store")
	}
}
