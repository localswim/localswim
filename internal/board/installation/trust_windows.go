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

// Native SDK prefixes, through the fields read. Native address words remain
// uintptr, so copied provider bytes can never become Go pointer fields. The
// Windows/amd64 ABI inserts the SDK's required eight-byte alignment.
type providerPrefix struct {
	Size                    uint32
	Data                    uintptr
	Opened                  uint32
	Window                  windows.Handle
	Action                  uintptr
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
	PrivateCount            uint32
	PrivateData             uintptr
	SubjectChoice           uint32
	Subject                 uintptr
	UsageOID                uintptr
	Recall                  int32
	SystemTime              windows.Filetime
	CTLSignerUsageOID       uintptr
	Flags                   uint32
	FinalError              uint32
}
type signerPrefix struct {
	Size                uint32
	Verified            windows.Filetime
	Count               uint32
	Certificates        uintptr
	Type                uint32
	Signer              uintptr
	Error, CounterCount uint32
	Counters            uintptr
	Chain               uintptr
}
type providerCertificate struct {
	Size        uint32
	Certificate uintptr
}

// CERT_CONTEXT has no cbStruct. Mirror its fixed Windows/amd64 SDK layout using
// address words, never Go pointers populated by untrusted native memory.
type providerCertContext struct {
	Encoding uint32
	Encoded  uintptr
	Length   uint32
	Info     uintptr
	Store    uintptr
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
func publisherSubject(c *x509.Certificate) bool {
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
	p, valid := readProviderState(pd)
	if !valid || p.SignerCount != 1 {
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
	size := unsafe.Sizeof(out)
	if address == 0 || size == 0 || address%unsafe.Alignof(out) != 0 || address > ^uintptr(0)-(size-1) {
		return out, false
	}
	var n uintptr
	err := windows.ReadProcessMemory(windows.CurrentProcess(), address, (*byte)(unsafe.Pointer(&out)), size, &n)
	return out, err == nil && n == size
}
func readProviderState(address uintptr) (providerPrefix, bool) {
	size, valid := readProviderValue[uint32](address)
	if !valid || size < uint32(unsafe.Sizeof(providerPrefix{})) {
		return providerPrefix{}, false
	}
	p, valid := readProviderValue[providerPrefix](address)
	return p, valid && p.Size == size && providerErrorsClear(&p)
}

const maxProviderCertificateDER = 1 << 20

func readProviderDER(address uintptr, length uint32) ([]byte, bool) {
	if address == 0 || length == 0 || length > maxProviderCertificateDER || address > ^uintptr(0)-(uintptr(length)-1) {
		return nil, false
	}
	out := make([]byte, int(length))
	var n uintptr
	err := windows.ReadProcessMemory(windows.CurrentProcess(), address, &out[0], uintptr(length), &n)
	if err != nil || n != uintptr(length) {
		return nil, false
	}
	return out, true
}

// TRUSTERROR_MAX_STEPS is the complete SDK error-slot inventory, including final
// provider steps through slot 37. Microsoft WinTrust.h uses pack(8); the amd64
// provider prefix through dwFinalError is 208 bytes (dwFinalError offset 204).
// https://github.com/microsoft/win32metadata/blob/main/generation/WinSDK/RecompiledIdlHeaders/um/WinTrust.h
// https://learn.microsoft.com/windows/win32/api/wintrust/ns-wintrust-crypt_provider_data
const trustErrorMaxSteps = 38

func providerErrorsClear(p *providerPrefix) bool {
	if p == nil || p.Size < uint32(unsafe.Sizeof(*p)) || p.Error != 0 || p.FinalError != 0 || p.ErrorCount != trustErrorMaxSteps || p.Errors == 0 {
		return false
	}
	var steps [trustErrorMaxSteps]uint32
	size := unsafe.Sizeof(steps)
	if p.Errors%unsafe.Alignof(steps[0]) != 0 || p.Errors > ^uintptr(0)-(size-1) {
		return false
	}
	var read uintptr
	// ReadProcessMemory validates the entire bounded native range. Never cast an
	// untrusted native address into a Go slice or accept a partial readable prefix.
	err := windows.ReadProcessMemory(windows.CurrentProcess(), p.Errors, (*byte)(unsafe.Pointer(&steps[0])), size, &read)
	if err != nil || read != size {
		return false
	}
	for _, step := range steps {
		if step != 0 {
			return false
		}
	}
	return true
}

// primaryPublisher consumes only the primary signer obtained from the successful
// pinned-file verification state; countersigner/independent certificates are absent.
func primaryPublisher(p *providerPrefix, signer *signerPrefix) string {
	if !providerErrorsClear(p) || p.SignerCount != 1 || signer == nil || signer.Size < uint32(unsafe.Sizeof(*signer)) || signer.Error != 0 || signer.Count == 0 || signer.Certificates == 0 {
		return "INSPECTION_UNKNOWN"
	}
	size, valid := readProviderValue[uint32](signer.Certificates)
	if !valid || size < uint32(unsafe.Sizeof(providerCertificate{})) {
		return "INSPECTION_UNKNOWN"
	}
	chain, valid := readProviderValue[providerCertificate](signer.Certificates)
	if !valid || chain.Size != size || chain.Certificate == 0 {
		return "INSPECTION_UNKNOWN"
	}
	cert, valid := readProviderValue[providerCertContext](chain.Certificate)
	if !valid || cert.Encoding&windows.X509_ASN_ENCODING == 0 || cert.Encoding&^(uint32(windows.X509_ASN_ENCODING)|uint32(windows.PKCS_7_ASN_ENCODING)) != 0 {
		return "INSPECTION_UNKNOWN"
	}
	der, valid := readProviderDER(cert.Encoded, cert.Length)
	if !valid {
		return "INSPECTION_UNKNOWN"
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return "INSPECTION_UNKNOWN"
	}
	if !publisherSubject(parsed) {
		return "PUBLISHER_UNQUALIFIED"
	}
	return ""
}
