package evidence

import (
	"bytes"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

func TestLocalContextPreservesImmutableIntent(t *testing.T) {
	original := record(t)
	for _, origin := range []string{"http://127.0.0.1:1024", "http://127.0.0.1:8792", "http://127.0.0.1:65535"} {
		c := Context{installation, origin}
		x, err := Freeze(c, original.Route(), contract.AuthenticatedActor{ActorID: actor}, original.Wire())
		if err != nil || x.Context() != c || x.Fingerprint() != original.Fingerprint() || !bytes.Equal(x.Semantic(), original.Semantic()) {
			t.Fatal("local transport changed original intent")
		}
		decoded, err := Decode(x.Bytes())
		if err != nil || decoded.Context() != c || !bytes.Equal(decoded.Bytes(), x.Bytes()) {
			t.Fatal("local original evidence did not roundtrip")
		}
	}
}

func TestLocalContextRefusesNetworkAndOriginAliases(t *testing.T) {
	for _, origin := range []string{"http://localhost:8792", "http://127.1:8792", "http://0.0.0.0:8792", "http://192.168.1.1:8792", "http://[::1]:8792", "https://127.0.0.1:8792", "http://127.0.0.1:08792", "http://127.0.0.1:1023", "http://127.0.0.1:65536", "http://127.0.0.1:8792/", "http://127.0.0.1:8792?", "http://127.0.0.1:8792#", "http://user@127.0.0.1:8792", "http://127.0.0.1.evil:8792"} {
		if ValidLocalContext(Context{installation, origin}) {
			t.Fatal("unsafe local context accepted")
		}
	}
}
