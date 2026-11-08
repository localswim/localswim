//go:build windows && amd64

package installation

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"golang.org/x/sys/windows"
	"math/big"
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
	for _, kind := range []string{"selected-primary", "missing-provider", "provider-error", "multiple-primary", "missing-signer", "signer-error", "missing-chain", "empty-certificate", "unrelated-openai-counter", "wrong-primary"} {
		t.Run(kind, func(t *testing.T) {
			cert := windows.CertContext{EncodedCert: &good[0], Length: uint32(len(good))}
			chain := providerCertificate{Certificate: &cert}
			p := &providerPrefix{Size: uint32(unsafe.Sizeof(providerPrefix{})), SignerCount: 1}
			s := &signerPrefix{Size: uint32(unsafe.Sizeof(signerPrefix{})), Count: 1, Certificates: &chain}
			want := "INSPECTION_UNKNOWN"
			switch kind {
			case "selected-primary":
				want = ""
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
				s.Certificates = nil
			case "empty-certificate":
				cert.Length = 0
			case "unrelated-openai-counter", "wrong-primary":
				cert.EncodedCert = &bad[0]
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
