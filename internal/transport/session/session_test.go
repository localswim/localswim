package session

import (
	"encoding/base64"
	"github.com/localswim/localswim/internal/board/grants"
	"testing"
	"time"
)

func TestCanonicalSecretHash(t *testing.T) {
	for _, n := range []int{16, 32} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i + 1)
		}
		encoded := base64.RawURLEncoding.EncodeToString(b)
		h, e := digest(encoded, n)
		if e != nil || h == [32]byte{} {
			t.Fatal("canonical secret refused")
		}
		for _, bad := range []string{"", encoded + "=", encoded + "A", encoded[:len(encoded)-1], " " + encoded} {
			if _, e = digest(bad, n); e == nil {
				t.Fatal("noncanonical secret accepted")
			}
		}
	}
	// Last sextet must have zero unused trailing bits.
	if _, e := digest("AAAAAAAAAAAAAAAAAAAAAB", 16); e == nil {
		t.Fatal("noncanonical trailing bits")
	}
	if _, e := digest("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB", 32); e == nil {
		t.Fatal("noncanonical trailing bits")
	}
}
func TestExpiryPruningAndClose(t *testing.T) {
	now := time.Now()
	h1 := [32]byte{1}
	h2 := [32]byte{2}
	h3 := [32]byte{3}
	s := &Service{codes: map[[32]byte]code{h1: {expires: now}, h2: {expires: now.Add(time.Second)}}, sessions: map[[32]byte]grants.Credential{h1: {ExpiresAt: now, IdleExpiresAt: now.Add(time.Hour)}, h2: {ExpiresAt: now.Add(time.Hour), IdleExpiresAt: now}, h3: {ExpiresAt: now.Add(time.Hour), IdleExpiresAt: now.Add(time.Minute)}}}
	s.prune(now)
	if len(s.codes) != 1 || len(s.sessions) != 1 {
		t.Fatal("expiry boundary")
	}
	s.Close()
	if !s.closed || len(s.codes) != 0 || len(s.sessions) != 0 {
		t.Fatal("close retained authentication")
	}
}
func TestAbsentProvidersFailClosed(t *testing.T) {
	if _, e := New(nil, nil); e == nil {
		t.Fatal("missing providers accepted")
	}
	var s *Service
	s.Close()
	if _, e := s.Exchange(nil, ""); e == nil {
		t.Fatal("nil exchange")
	}
	if _, e := s.Current(nil, "browser", ""); e == nil {
		t.Fatal("nil current")
	}
}
