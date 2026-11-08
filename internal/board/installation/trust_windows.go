//go:build windows && amd64

package installation

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

var trustDLL = windows.NewLazySystemDLL("wintrust.dll")
var trustVerify = trustDLL.NewProc("WinVerifyTrust")
var providerData = trustDLL.NewProc("WTHelperProvDataFromStateData")
var providerSigner = trustDLL.NewProc("WTHelperGetProvSignerFromChain")

// Native SDK prefixes, through the fields read. Pointer fields use uintptr/pointers
// so Go's Windows/amd64 ABI inserts the required eight-byte alignment.
type providerPrefix struct {
	Size                    uint32
	Data                    *windows.WinTrustData
	Opened                  uint32
	Window                  windows.Handle
	Action                  *windows.GUID
	Provider                windows.Handle
	Error, Security, Policy uint32
	Functions               uintptr
	ErrorCount              uint32
	Errors                  uintptr
	StoreCount              uint32
	Stores                  uintptr
	Encoding                uint32
	Message                 windows.Handle
	SignerCount             uint32
	Signers                 uintptr
}
type signerPrefix struct {
	Size                uint32
	Verified            windows.Filetime
	Count               uint32
	Certificates        *providerCertificate
	Type                uint32
	Signer              uintptr
	Error, CounterCount uint32
	Counters            uintptr
	Chain               uintptr
}
type providerCertificate struct {
	Size        uint32
	Certificate *windows.CertContext
}
type trustState struct {
	data        windows.WinTrustData
	file        windows.WinTrustFileInfo
	settings    windows.WinTrustSignatureSettings
	path        *uint16
	closeFailed bool
	callTest    func(uint32) int32
}

func (s *trustState) call(action uint32) int32 {
	s.data.StateAction = action
	if s.callTest != nil {
		return s.callTest(action)
	}
	r, _, _ := trustVerify.Call(0, uintptr(unsafe.Pointer(&windows.WINTRUST_ACTION_GENERIC_VERIFY_V2)), uintptr(unsafe.Pointer(&s.data)))
	runtime.KeepAlive(s)
	return int32(r)
}
func (s *trustState) close() error {
	if s.closeFailed {
		return ErrIncomplete
	}
	if s.data.StateData == 0 {
		return nil
	}
	if s.call(windows.WTD_STATEACTION_CLOSE) != 0 {
		s.closeFailed = true
		return ErrIncomplete
	}
	s.data.StateData = 0
	return nil
}
func publisherSubject(der []byte) bool {
	c, e := x509.ParseCertificate(der)
	if e != nil {
		return false
	}
	var names pkix.RDNSequence
	rest, e := asn1.Unmarshal(c.RawSubject, &names)
	if e != nil || len(rest) != 0 {
		return false
	}
	expected := map[string]string{"2.5.4.3": "OpenAI OpCo, LLC", "2.5.4.10": "OpenAI OpCo, LLC", "2.5.4.6": "US", "2.5.4.8": "California", "2.5.4.7": "San Francisco"}
	seen := map[string]bool{}
	for _, rdn := range names {
		for _, a := range rdn {
			k := a.Type.String()
			want, ok := expected[k]
			value, yes := a.Value.(string)
			if !ok || !yes || seen[k] || value != want {
				return false
			}
			seen[k] = true
		}
	}
	return len(seen) == len(expected)
}
func trustFailure(status int32) string {
	switch uint32(status) {
	case 0x800b010c, 0x800b0109, 0x800b0004, 0x800b0111, 0x800b0100, 0x80096010:
		return "PUBLISHER_UNQUALIFIED"
	default:
		return "INSPECTION_UNKNOWN"
	}
}
func verifyTrust(x *pin) (*trustState, string) {
	if trustVerify.Find() != nil || providerData.Find() != nil || providerSigner.Find() != nil {
		return nil, "INSPECTION_UNKNOWN"
	}
	path, e := windows.UTF16PtrFromString(x.path)
	if e != nil {
		return nil, "INSPECTION_UNKNOWN"
	}
	s := &trustState{path: path}
	s.file = windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(s.file)), FilePath: path, File: x.h}
	s.settings = windows.WinTrustSignatureSettings{Size: uint32(unsafe.Sizeof(s.settings)), Index: 0, Flags: 1}
	s.data = windows.WinTrustData{Size: uint32(unsafe.Sizeof(s.data)), UIChoice: windows.WTD_UI_NONE, UnionChoice: windows.WTD_CHOICE_FILE, FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&s.file), ProvFlags: windows.WTD_CACHE_ONLY_URL_RETRIEVAL | windows.WTD_REVOCATION_CHECK_CHAIN_EXCLUDE_ROOT, SignatureSettings: &s.settings}
	if status := s.call(windows.WTD_STATEACTION_VERIFY); status != 0 {
		return s, trustFailure(status)
	}
	if s.data.StateData == 0 || s.settings.VerifiedSigIndex != 0 {
		return s, "INSPECTION_UNKNOWN"
	}
	pd, _, _ := providerData.Call(uintptr(s.data.StateData))
	if pd == 0 {
		return s, "INSPECTION_UNKNOWN"
	}
	p, valid := readProviderValue[providerPrefix](pd)
	if !valid || p.Size < uint32(unsafe.Sizeof(p)) || p.Error != 0 || p.SignerCount != 1 {
		return s, "INSPECTION_UNKNOWN"
	}
	ps, _, _ := providerSigner.Call(pd, 0, 0, 0)
	if ps == 0 {
		return s, "INSPECTION_UNKNOWN"
	}
	signer, valid := readProviderValue[signerPrefix](ps)
	if !valid {
		return s, "INSPECTION_UNKNOWN"
	}
	code := primaryPublisher(&p, &signer)
	runtime.KeepAlive(s)
	return s, code
}

// Copy a bounded SDK prefix from this process's live verification state. The OS
// validates the native address; no Go uintptr-to-pointer lifetime is invented.
func readProviderValue[T any](address uintptr) (T, bool) {
	var out T
	var n uintptr
	err := windows.ReadProcessMemory(windows.CurrentProcess(), address, (*byte)(unsafe.Pointer(&out)), unsafe.Sizeof(out), &n)
	return out, err == nil && n == unsafe.Sizeof(out)
}

// primaryPublisher consumes only the primary signer obtained from the successful
// pinned-file verification state; countersigner/independent certificates are absent.
func primaryPublisher(p *providerPrefix, signer *signerPrefix) string {
	if p == nil || p.Size < uint32(unsafe.Sizeof(*p)) || p.Error != 0 || p.SignerCount != 1 || signer == nil || signer.Size < uint32(unsafe.Sizeof(*signer)) || signer.Error != 0 || signer.Count == 0 || signer.Certificates == nil {
		return "INSPECTION_UNKNOWN"
	}
	cert := signer.Certificates.Certificate
	if cert == nil || cert.EncodedCert == nil || cert.Length == 0 || cert.Length > 1<<20 {
		return "INSPECTION_UNKNOWN"
	}
	if !publisherSubject(unsafe.Slice(cert.EncodedCert, int(cert.Length))) {
		return "PUBLISHER_UNQUALIFIED"
	}
	return ""
}
