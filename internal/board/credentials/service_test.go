package credentials

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	T "github.com/localswim/localswim/internal/contract/transport"
)

// These tests never construct N01 or resolve a native root. Real native service
// tests require the explicit g02_native qualification tag and private overlay.
func TestCanonicalTokenAndRedactedUnknown(t *testing.T) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal("entropy")
	}
	token := T.SecretToken(base64.RawURLEncoding.EncodeToString(b[:]))
	h, err := tokenHash(token)
	if err != nil || len(h) != 64 {
		t.Fatal("hash")
	}
	if _, err = hex.DecodeString(h); err != nil {
		t.Fatal("hex")
	}
	for _, bad := range []string{string(token) + "=", string(token) + "\n", "garbage", base64.RawURLEncoding.EncodeToString(b[:31])} {
		if _, err = tokenHash(T.SecretToken(bad)); err == nil {
			t.Fatal("noncanonical token accepted")
		}
	}
	r := IssueResult{Status: Unknown, secret: &T.CredentialSecret{Token: token}}
	encoded, err := json.Marshal(r)
	if err != nil || strings.Contains(string(encoded), string(token)) {
		t.Fatal("secret in public result")
	}
	if _, err = r.MarshalEphemeral(); err == nil {
		t.Fatal("unknown exposed secret")
	}
}
