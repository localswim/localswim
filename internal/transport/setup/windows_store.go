//go:build windows && amd64

package setup

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NewWithWindowsCurrentUserRoot explicitly composes the native setup manager
// with the writable local physical CurrentUser Root provider. Construction opens
// no certificate store and enrolls nothing. New(nil) remains store-unavailable.
// Only Manager.ApplyTrust can reach this private provider's mutation methods in
// production; adapters must first display PlanTrust and obtain its confirmation.
// This cooperative current-account boundary is not hostile-process isolation,
// proof of meaningful human review, or qualification of browser trust adoption.
func NewWithWindowsCurrentUserRoot() (*Manager, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	calls := nativeWindowsStoreCalls()
	account, err := calls.account()
	if err != nil {
		return nil, fail("TRUST_STORE_UNAVAILABLE", 3)
	}
	m, err := New(&windowsRootStore{account: account, calls: calls})
	if err != nil {
		return nil, err
	}
	if m.fs.sid.String() != account.sid {
		return nil, fail("TRUST_STORE_UNAVAILABLE", 3)
	}
	return m, nil
}

type windowsStoreAccount struct {
	pid int
	sid string
}

// No caller-selected account, location, profile, name, flags or provider exists.
// PHYSICAL_W selects one sibling, whereas SYSTEM_W would merge inherited stores.
// Microsoft: /windows/win32/api/wincrypt/nf-wincrypt-certopenstore and
// /windows/win32/seccrypto/system-store-locations (learn.microsoft.com).
type windowsStoreSelector struct {
	provider uintptr
	encoding uint32
	crypt    uintptr
	flags    uint32
	name     string
}

func rootStoreSelector(write bool) windowsStoreSelector {
	// Include archived entries: an exact stored copy must not disappear from
	// membership/removal merely because a context property hides it by default.
	flags := uint32(windows.CERT_SYSTEM_STORE_CURRENT_USER | windows.CERT_STORE_OPEN_EXISTING_FLAG | windows.CERT_STORE_ENUM_ARCHIVED_FLAG)
	if !write {
		flags |= windows.CERT_STORE_READONLY_FLAG
	}
	return windowsStoreSelector{windows.CERT_STORE_PROV_PHYSICAL_W, 0, 0, flags, `Root\.Default`}
}

// The call layer is package-private and per-instance. Tests replace only this
// layer with synthetic calls/nonpersistent MEMORY stores, never a live root.
type windowsStoreCalls struct {
	account   func() (windowsStoreAccount, error)
	open      func(windowsStoreSelector) (windows.Handle, error)
	close     func(windows.Handle, uint32) error
	enum      func(windows.Handle, *windows.CertContext) (*windows.CertContext, error)
	duplicate func(*windows.CertContext) *windows.CertContext
	free      func(*windows.CertContext) error
	create    func(uint32, *byte, uint32) (*windows.CertContext, error)
	add       func(windows.Handle, *windows.CertContext, uint32, **windows.CertContext) error
	remove    func(*windows.CertContext) error
}

func nativeWindowsStoreCalls() windowsStoreCalls {
	return windowsStoreCalls{
		account: currentStoreAccount,
		open: func(s windowsStoreSelector) (windows.Handle, error) {
			name, err := windows.UTF16PtrFromString(s.name)
			if err != nil {
				return 0, err
			}
			h, err := windows.CertOpenStore(s.provider, s.encoding, s.crypt, s.flags, uintptr(unsafe.Pointer(name)))
			runtime.KeepAlive(name)
			return h, err
		},
		close: windows.CertCloseStore, enum: windows.CertEnumCertificatesInStore,
		duplicate: windows.CertDuplicateCertificateContext, free: windows.CertFreeCertificateContext,
		create: windows.CertCreateCertificateContext, add: windows.CertAddCertificateContextToStore,
		remove: windows.CertDeleteCertificateFromStore,
	}
}

func currentStoreAccount() (windowsStoreAccount, error) {
	// The caller pins this goroutine to its OS thread through the entire store
	// operation. Refuse every impersonation token, even for the same SID; do not
	// revert impersonation or change HKCU/profile mappings to make an open work.
	thread, err := windows.GetCurrentThread()
	if err != nil {
		return windowsStoreAccount{}, err
	}
	var token windows.Token
	err = windows.OpenThreadToken(thread, windows.TOKEN_QUERY, true, &token)
	if err == nil {
		token.Close()
		return windowsStoreAccount{}, fail("TRUST_STORE_UNAVAILABLE", 3)
	}
	if !errors.Is(err, windows.ERROR_NO_TOKEN) {
		return windowsStoreAccount{}, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return windowsStoreAccount{}, fail("TRUST_STORE_UNAVAILABLE", 3)
	}
	return windowsStoreAccount{os.Getpid(), user.User.Sid.String()}, nil
}

type windowsRootStore struct {
	account windowsStoreAccount
	calls   windowsStoreCalls
}

func (s *windowsRootStore) bound() bool {
	account, err := s.calls.account()
	return err == nil && s.account.pid != 0 && s.account.sid != "" && account == s.account
}

func validStoreIssuer(der []byte) bool {
	c, err := x509.ParseCertificate(der)
	return err == nil && bytes.Equal(c.Raw, der) && c.IsCA && c.BasicConstraintsValid &&
		bytes.Equal(c.RawIssuer, c.RawSubject) && c.CheckSignatureFrom(c) == nil
}

// exact scans the selected physical store, comparing BOTH SHA-256 and exact DER.
// retain duplicates only for removal, after enumeration is finished. Enumeration
// consumes prev even on failure. Deletion consumes its input even on failure.
// See Microsoft's CertEnumCertificatesInStore/CertDeleteCertificateFromStore docs.
func (s *windowsRootStore) exact(h windows.Handle, der []byte, retain bool) (present bool, held []*windows.CertContext, err error) {
	want := sha256.Sum256(der)
	var prev *windows.CertContext
	defer func() {
		if prev != nil {
			if e := s.calls.free(prev); e != nil {
				err = e
			}
		}
		if err != nil {
			for _, c := range held {
				s.calls.free(c)
			}
			held = nil
			present = false
		}
	}()
	for {
		c, e := s.calls.enum(h, prev)
		prev = c // The preceding context has been consumed, including on error.
		if e != nil {
			if c == nil && errors.Is(e, syscall.Errno(windows.CRYPT_E_NOT_FOUND)) {
				return present, held, nil
			}
			return false, held, e
		}
		if c == nil || c.Store != h || c.EncodingType&windows.X509_ASN_ENCODING == 0 || c.EncodedCert == nil || c.Length == 0 || c.Length > 1<<20 {
			return false, held, fail("TRUST_STORE_UNAVAILABLE", 3)
		}
		encoded := unsafe.Slice(c.EncodedCert, int(c.Length))
		if sha256.Sum256(encoded) != want || !bytes.Equal(encoded, der) {
			continue
		}
		present = true
		if retain {
			copy := s.calls.duplicate(c)
			if copy == nil {
				return false, held, fail("TRUST_STORE_UNAVAILABLE", 3)
			}
			held = append(held, copy)
		}
	}
}

func (s *windowsRootStore) Membership(der []byte) (bool, error) {
	return s.perform(der, "")
}
func (s *windowsRootStore) Enroll(der []byte) error {
	_, err := s.perform(der, "enroll")
	return err
}
func (s *windowsRootStore) Remove(der []byte) error {
	_, err := s.perform(der, "remove")
	return err
}

func (s *windowsRootStore) perform(input []byte, operation string) (present bool, err error) {
	// Isolate the exact reviewed bytes from cooperative caller reuse.
	der := append([]byte(nil), input...)
	if !validStoreIssuer(der) {
		return false, fail("IDENTITY_MISMATCH", 2)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !s.bound() {
		return false, fail("TRUST_STORE_UNAVAILABLE", 3)
	}
	h, e := s.calls.open(rootStoreSelector(operation != ""))
	if e != nil || h == 0 {
		return false, fail("TRUST_STORE_UNAVAILABLE", 3)
	}
	attempted := false
	defer func() {
		if e := s.calls.close(h, windows.CERT_CLOSE_STORE_CHECK_FLAG); e != nil {
			err = e
		}
		if err != nil {
			present = false
			if attempted {
				err = fail("SETUP_OUTCOME_UNKNOWN", 4)
			} else {
				err = fail("TRUST_STORE_UNAVAILABLE", 3)
			}
		}
	}()
	present, held, e := s.exact(h, der, operation == "remove")
	if e != nil {
		return false, e
	}
	defer func() {
		for _, c := range held {
			if c != nil {
				if e := s.calls.free(c); e != nil {
					err = e
				}
			}
		}
	}()
	if !s.bound() {
		return false, fail("TRUST_STORE_UNAVAILABLE", 3)
	}
	if operation == "enroll" && !present {
		// A fresh encoded-only context carries no private key or extended property.
		c, e := s.calls.create(windows.X509_ASN_ENCODING, &der[0], uint32(len(der)))
		runtime.KeepAlive(der)
		if e != nil || c == nil {
			return false, fail("TRUST_STORE_UNAVAILABLE", 3)
		}
		defer func() {
			if e := s.calls.free(c); e != nil {
				err = e
			}
		}()
		// ADD_NEW cannot replace a different same-name/issuer-serial certificate.
		attempted = true
		if e = s.calls.add(h, c, windows.CERT_STORE_ADD_NEW, nil); e != nil {
			return false, e
		}
	} else if operation == "remove" {
		for i, c := range held {
			held[i] = nil // Delete consumes this reference even if it fails.
			attempted = true
			if e = s.calls.remove(c); e != nil {
				return false, e
			}
		}
	}
	if !s.bound() {
		return false, fail("TRUST_STORE_UNAVAILABLE", 3)
	}
	if operation != "" {
		present, _, e = s.exact(h, der, false)
		if e != nil || present != (operation == "enroll") {
			return false, fail("SETUP_OUTCOME_UNKNOWN", 4)
		}
	}
	return present, nil
}
