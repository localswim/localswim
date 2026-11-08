//go:build windows && amd64

package installation

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeTrustFixture struct {
	steps    [trustErrorMaxSteps]uint32
	cert     providerCertContext
	chain    providerCertificate
	provider providerPrefix
	signer   signerPrefix
	der      []byte
	pins     runtime.Pinner
}

func syntheticTrustFixture(t *testing.T) *nativeTrustFixture {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Unix(0, 0), NotAfter: time.Unix(100, 0), Subject: pkix.Name{CommonName: "OpenAI OpCo, LLC", Organization: []string{"OpenAI OpCo, LLC"}, Country: []string{"US"}, Province: []string{"California"}, Locality: []string{"San Francisco"}}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	f := &nativeTrustFixture{der: der}
	f.pins.Pin(f)
	f.pins.Pin(&der[0])
	t.Cleanup(f.pins.Unpin)
	f.cert = providerCertContext{Encoding: windows.X509_ASN_ENCODING, Encoded: uintptr(unsafe.Pointer(&f.der[0])), Length: uint32(len(f.der))}
	f.chain = providerCertificate{Size: uint32(unsafe.Sizeof(f.chain)), Certificate: uintptr(unsafe.Pointer(&f.cert))}
	f.provider = providerPrefix{Size: uint32(unsafe.Sizeof(f.provider)), SignerCount: 1, ErrorCount: trustErrorMaxSteps, Errors: uintptr(unsafe.Pointer(&f.steps))}
	f.signer = signerPrefix{Size: uint32(unsafe.Sizeof(f.signer)), Count: 1, Certificates: uintptr(unsafe.Pointer(&f.chain))}
	if code := primaryPublisher(&f.provider, &f.signer); code != "" {
		t.Fatal("synthetic primary fixture invalid", code)
	}
	return f
}
func writeNativeTestValue[T any](t *testing.T, address uintptr, value *T) {
	t.Helper()
	var written uintptr
	size := unsafe.Sizeof(*value)
	if err := windows.WriteProcessMemory(windows.CurrentProcess(), address, (*byte)(unsafe.Pointer(value)), size, &written); err != nil || written != size {
		t.Fatal("owned native-memory fixture write failed", err)
	}
}
func nativeGuardPages(t *testing.T) (uintptr, uintptr) {
	t.Helper()
	page := uintptr(os.Getpagesize())
	address, err := windows.VirtualAlloc(0, 2*page, windows.MEM_RESERVE|windows.MEM_COMMIT, windows.PAGE_READWRITE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.VirtualFree(address, 0, windows.MEM_RELEASE); err != nil {
			t.Error("owned native memory release failed", err)
		}
	})
	var previous uint32
	if err = windows.VirtualProtect(address+page, page, windows.PAGE_NOACCESS, &previous); err != nil {
		t.Fatal(err)
	}
	return address, address + page
}
func TestProviderNativePointerFailuresAreContained(t *testing.T) {
	for _, level := range []string{"provider-unreadable", "provider-partial", "signer-unreadable", "signer-partial", "steps-unreadable", "steps-partial", "chain-unreadable", "chain-partial", "chain-small", "context-unreadable", "context-partial", "DER-unreadable", "DER-partial", "DER-overflow", "DER-oversize", "DER-malformed", "encoding-invalid"} {
		t.Run(level, func(t *testing.T) {
			f := syntheticTrustFixture(t)
			_, blocked := nativeGuardPages(t)
			switch level {
			case "provider-unreadable":
				if _, valid := readProviderState(blocked); valid {
					t.Fatal("unreadable provider accepted")
				}
				return
			case "provider-partial":
				size := uint32(unsafe.Sizeof(providerPrefix{}))
				writeNativeTestValue(t, blocked-8, &size)
				if _, valid := readProviderState(blocked - 8); valid {
					t.Fatal("partial provider accepted")
				}
				return
			case "signer-unreadable":
				if _, valid := readProviderValue[signerPrefix](blocked); valid {
					t.Fatal("unreadable signer accepted")
				}
				return
			case "signer-partial":
				if _, valid := readProviderValue[signerPrefix](blocked - 8); valid {
					t.Fatal("partial signer accepted")
				}
				return
			case "steps-unreadable":
				f.provider.Errors = blocked
			case "steps-partial":
				f.provider.Errors = blocked - 4
			case "chain-unreadable":
				f.signer.Certificates = blocked
			case "chain-partial":
				size := uint32(unsafe.Sizeof(providerCertificate{}))
				writeNativeTestValue(t, blocked-8, &size)
				f.signer.Certificates = blocked - 8
			case "chain-small":
				f.chain.Size = uint32(unsafe.Sizeof(f.chain)) - 1
			case "context-unreadable":
				f.chain.Certificate = blocked
			case "context-partial":
				f.chain.Certificate = blocked - 8
			case "DER-unreadable":
				f.cert.Encoded = blocked
			case "DER-partial":
				f.cert.Encoded = blocked - 4
			case "DER-overflow":
				f.cert.Encoded = ^uintptr(0) - 3
			case "DER-oversize":
				f.cert.Length = maxProviderCertificateDER + 1
			case "DER-malformed":
				f.cert.Length = 3
			case "encoding-invalid":
				f.cert.Encoding = 0
			}
			runtime.GC()
			if code := primaryPublisher(&f.provider, &f.signer); code != "INSPECTION_UNKNOWN" {
				t.Fatal("unavailable native evidence was misclassified", code)
			}
			runtime.GC()
		})
	}
}

func TestProviderDERCopyInclusiveBound(t *testing.T) {
	bytes := make([]byte, maxProviderCertificateDER)
	bytes[0] = 1
	bytes[len(bytes)-1] = 2
	var pin runtime.Pinner
	pin.Pin(&bytes[0])
	defer pin.Unpin()
	copied, valid := readProviderDER(uintptr(unsafe.Pointer(&bytes[0])), uint32(len(bytes)))
	if !valid || len(copied) != maxProviderCertificateDER || copied[0] != 1 || copied[len(copied)-1] != 2 {
		t.Fatal("exact DER copy bound refused or truncated")
	}
	if _, valid = readProviderDER(uintptr(unsafe.Pointer(&bytes[0])), maxProviderCertificateDER+1); valid {
		t.Fatal("oversize DER copy admitted")
	}
}
