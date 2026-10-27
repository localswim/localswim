package core

import (
	"context"
	"net/http"
	"testing"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

func TestLocalConnectionCannotSelectRemoteOrNativeAuthority(t *testing.T) {
	metadata := CredentialMetadata{Selected: selected, ActorID: testActor, ExpiresAt: time.Now().Add(time.Hour)}
	for _, origin := range []string{"http://localhost:8792", "http://0.0.0.0:8792", "https://synthetic.localhost:8792", "http://127.0.0.1:8792/", "http://127.0.0.1:08792"} {
		if c, err := NewLocalBoardConnection(evidence.Context{InstallationID: testInstallation, Origin: origin}, metadata, T.SecretToken("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")); err == nil || c != nil {
			t.Fatal("invalid transport accepted")
		}
	}
	closed := 0
	c, err := NewLocalBoardConnection(evidence.Context{InstallationID: testInstallation, Origin: "http://127.0.0.1:8792"}, metadata, T.SecretToken("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), func() { closed++ })
	if err != nil || c.native {
		t.Fatal("ordinary local connection unavailable")
	}
	c.close()
	c.close()
	if closed != 1 {
		t.Fatal("local connection lifetime closed more than once")
	}
}

func TestLocalClientRefusesHumanSessionEvenWithMatchingSelectors(t *testing.T) {
	// The fixture has the matching actor/grant IDs but is explicitly human.
	// Local CLI composition must reject it rather than relying on request labels.
	c := connection(t, func(*http.Request) (int, []byte, error) { t.Fatal("mutation dispatched"); return 0, nil, nil })
	c.local = true
	c.context.Origin = "http://127.0.0.1:8792"
	closed := false
	c.close = func() { closed = true }
	client, err := Open(context.Background(), &provider{conn: c}, selected)
	if client != nil || err == nil || !closed {
		t.Fatal("local CLI accepted human authority")
	}
}
