//go:build windows && amd64

package installation

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"golang.org/x/sys/windows"
	"math/big"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

func TestVerifiedPrimaryPublisherBinding(t *testing.T) {
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	makeCert := func(cn string) []byte {
		c := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Unix(0, 0), NotAfter: time.Unix(100, 0), Subject: pkix.Name{CommonName: cn, Organization: []string{"OpenAI OpCo, LLC"}, Country: []string{"US"}, Province: []string{"California"}, Locality: []string{"San Francisco"}}}
		b, e := x509.CreateCertificate(rand.Reader, c, c, key.Public(), key)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	good, bad := makeCert("OpenAI OpCo, LLC"), makeCert("Other publisher")
	for _, kind := range []string{"selected-primary", "missing-provider", "provider-error", "multiple-primary", "missing-signer", "signer-error", "missing-chain", "empty-certificate", "unrelated-openai-counter", "wrong-primary", "step-error", "final-error", "missing-steps"} {
		t.Run(kind, func(t *testing.T) {
			cert := providerCertContext{Encoding: windows.X509_ASN_ENCODING, Encoded: uintptr(unsafe.Pointer(&good[0])), Length: uint32(len(good))}
			chain := providerCertificate{Size: uint32(unsafe.Sizeof(providerCertificate{})), Certificate: uintptr(unsafe.Pointer(&cert))}
			steps := new([trustErrorMaxSteps]uint32)
			var stepPin runtime.Pinner
			stepPin.Pin(steps)
			stepPin.Pin(&cert)
			stepPin.Pin(&chain)
			stepPin.Pin(&good[0])
			stepPin.Pin(&bad[0])
			defer stepPin.Unpin()
			p := &providerPrefix{Size: uint32(unsafe.Sizeof(providerPrefix{})), SignerCount: 1, ErrorCount: trustErrorMaxSteps, Errors: uintptr(unsafe.Pointer(steps))}
			s := &signerPrefix{Size: uint32(unsafe.Sizeof(signerPrefix{})), Count: 1, Certificates: uintptr(unsafe.Pointer(&chain))}
			want := "INSPECTION_UNKNOWN"
			switch kind {
			case "selected-primary":
				want = ""
			case "step-error":
				steps[14] = 0x800b010c
			case "final-error":
				p.FinalError = 0x800b010c
			case "missing-steps":
				p.ErrorCount = 0
				p.Errors = 0
			case "missing-provider":
				p = nil
			case "provider-error":
				p.Error = 1
			case "multiple-primary":
				p.SignerCount = 2
			case "missing-signer":
				s = nil
			case "signer-error":
				s.Error = 1
			case "missing-chain":
				s.Certificates = 0
			case "empty-certificate":
				cert.Length = 0
			case "unrelated-openai-counter", "wrong-primary":
				cert.Encoded = uintptr(unsafe.Pointer(&bad[0]))
				cert.Length = uint32(len(bad))
				s.CounterCount = 1
				s.Counters = uintptr(unsafe.Pointer(&good[0]))
				want = "PUBLISHER_UNQUALIFIED"
			}
			if got := primaryPublisher(p, s); got != want {
				t.Fatal("publisher binding", got, want)
			}
		})
	}
}

func TestProviderErrorPrefixWindowsAMD64ABI(t *testing.T) {
	var p providerPrefix
	if unsafe.Sizeof(p) != 208 || unsafe.Offsetof(p.ErrorCount) != 72 || unsafe.Offsetof(p.Errors) != 80 || unsafe.Offsetof(p.SignerCount) != 120 || unsafe.Offsetof(p.Signers) != 128 || unsafe.Offsetof(p.FinalError) != 204 {
		t.Fatal("provider prefix differs from SDK pack(8) ABI")
	}
	var cert providerCertContext
	var chain providerCertificate
	if unsafe.Sizeof(cert) != 40 || unsafe.Offsetof(cert.Encoded) != 8 || unsafe.Offsetof(cert.Length) != 16 || unsafe.Sizeof(chain) != 16 || unsafe.Offsetof(chain.Certificate) != 8 {
		t.Fatal("certificate prefix differs from SDK pack(8) ABI")
	}
}
func TestProviderTrustStepErrorsAreCompleteAndBounded(t *testing.T) {
	steps := new([trustErrorMaxSteps]uint32)
	var stepPin runtime.Pinner
	stepPin.Pin(steps)
	defer stepPin.Unpin()
	good := providerPrefix{Size: uint32(unsafe.Sizeof(providerPrefix{})), ErrorCount: trustErrorMaxSteps, Errors: uintptr(unsafe.Pointer(steps))}
	if !providerErrorsClear(&good) {
		t.Fatal("complete zero-error state refused")
	}
	for slot := range steps {
		steps[slot] = 0x800b010c
		if providerErrorsClear(&good) {
			t.Fatal("nonzero native trust step admitted", slot)
		}
		steps[slot] = 0
	}
	for _, name := range []string{"short-prefix", "provider-error", "final-error", "missing-count", "missing-array", "short-array", "oversize-array", "huge-count", "unaligned", "unreadable", "overflow"} {
		t.Run(name, func(t *testing.T) {
			p := good
			switch name {
			case "short-prefix":
				p.Size = 207
			case "provider-error":
				p.Error = 1
			case "final-error":
				p.FinalError = 1
			case "missing-count":
				p.ErrorCount = 0
			case "missing-array":
				p.Errors = 0
			case "short-array":
				p.ErrorCount = 37
			case "oversize-array":
				p.ErrorCount = 39
			case "huge-count":
				p.ErrorCount = ^uint32(0)
			case "unaligned":
				p.Errors = 3
			case "unreadable":
				p.Errors = 4
			case "overflow":
				p.Errors = ^uintptr(0) - 3
			}
			if providerErrorsClear(&p) {
				t.Fatal("inconsistent/unavailable native error evidence admitted")
			}
		})
	}
	if providerErrorsClear(nil) {
		t.Fatal("absent provider error state admitted")
	}
}
