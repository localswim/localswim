package core

import (
	"strconv"
	"testing"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/setup"
)

func TestPinnedConnectionRejectsPortAndAuthorityAliases(t *testing.T) {
	for _, tc := range []struct {
		port              int
		authority, origin string
	}{{0, "synthetic.localhost:0", "https://synthetic.localhost:0"}, {1023, "synthetic.localhost:1023", "https://synthetic.localhost:1023"}, {65536, "synthetic.localhost:65536", "https://synthetic.localhost:65536"}, {48443, "synthetic.localhost:048443", "https://synthetic.localhost:048443"}, {48443, "synthetic.localhost:47443", "https://synthetic.localhost:47443"}, {48443, "synthetic.localhost:48443", "https://synthetic.localhost:48443/"}, {48443, "synthetic.localhost:48443", "http://synthetic.localhost:48443"}} {
		c := setup.ConnectionContext{InstallationID: testInstallation, Hostname: "synthetic.localhost", Port: tc.port, Authority: tc.authority, Origin: tc.origin}
		_, err := NewBoardConnection(c, CredentialMetadata{Selected: selected, ActorID: testActor, ExpiresAt: time.Now().Add(time.Hour)}, T.SecretToken("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"))
		if f, ok := err.(*Failure); !ok || f.Code != "CREDENTIAL_CONTEXT_MISMATCH" {
			t.Fatal("port or authority alias accepted: " + strconv.Itoa(tc.port))
		}
	}
}
