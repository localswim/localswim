package evidence

import (
	"bytes"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

func TestConfiguredOriginPortRoundtripWithoutSemanticChange(t *testing.T) {
	original := record(t)
	for _, origin := range []string{"https://synthetic.localhost:1024", "https://synthetic.localhost:47443", "https://synthetic.localhost:48443", "https://synthetic.localhost:65535"} {
		c := Context{installation, origin}
		x, e := Freeze(c, original.Route(), contract.AuthenticatedActor{ActorID: actor}, original.Wire())
		if e != nil || x.Context() != c || !bytes.Equal(x.Semantic(), original.Semantic()) || x.Fingerprint() != original.Fingerprint() {
			t.Fatal("configured origin changed semantic identity")
		}
		decoded, e := Decode(x.Bytes())
		if e != nil || decoded.Context() != c || !bytes.Equal(decoded.Bytes(), x.Bytes()) {
			t.Fatal("exact origin not retained")
		}
	}
}

func TestConfiguredOriginRejectsAliasesAndUnsafePorts(t *testing.T) {
	for _, origin := range []string{"https://synthetic.localhost", "https://synthetic.localhost:0", "https://synthetic.localhost:1023", "https://synthetic.localhost:65536", "https://synthetic.localhost:048443", "https://synthetic.localhost:+48443", "https://synthetic.localhost:48443/", "https://synthetic.localhost:48443?", "https://synthetic.localhost:48443#", "https://user@synthetic.localhost:48443", "http://synthetic.localhost:48443", "https://SYNTHETIC.localhost:48443", "https://synthetic.localhost.:48443", "https://synthetic.localhost.evil:48443", "https://.localhost:48443", "https://-bad.localhost:48443", "https://bad-.localhost:48443", "https://bad_name.localhost:48443", "https://localhost:48443", "https://127.0.0.1:48443", "https://[::1]:48443"} {
		if validContext(Context{installation, origin}) {
			t.Fatal("unsafe origin accepted")
		}
	}
}
