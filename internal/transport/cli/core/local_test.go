package core

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

func TestWorkspaceLocalConnectionDerivesScopedSessionAndRefusesWrongBoard(t *testing.T) {
	metadata := CredentialMetadata{Selected: selected, ActorID: testActor, ExpiresAt: time.Now().Add(time.Hour)}
	connectionContext := evidence.Context{InstallationID: testInstallation, Origin: "http://127.0.0.1:8792"}
	for _, workspaceID := range []string{"", "not-a-uuid", testBoard + "/session", "00000000-0000-1000-8000-000000000020"} {
		if c, err := NewLocalWorkspaceBoardConnection(connectionContext, metadata, T.SecretToken("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), workspaceID); c != nil || err == nil {
			t.Fatal("invalid workspace identity accepted")
		}
	}
	for _, wrongBoard := range []bool{false, true} {
		c, err := NewLocalWorkspaceBoardConnection(connectionContext, metadata, T.SecretToken("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), testInstallation)
		if err != nil {
			t.Fatal("workspace connection unavailable")
		}
		calls := 0
		c.do = func(q *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				if q.Method != "GET" || q.URL.Path != "/api/v1/runtime-mode" || q.URL.RawQuery != "" {
					t.Fatal("workspace context was not independently verified")
				}
				return response(200, []byte(`{"apiVersion":1,"mode":"workspace","workspaceId":"`+testInstallation+`"}`)), nil
			}
			if q.Method != "GET" || q.URL.Path != "/api/v1/boards/"+selected.BoardID+"/session" || q.URL.RawQuery != "" || q.URL.Host != "127.0.0.1:8792" || q.Header.Get("Authorization") != c.authorization || q.GetBody != nil {
				t.Fatal("session context escaped selected board")
			}
			var session T.SessionRead
			if json.Unmarshal(fixture(t, "session-read.json"), &session) != nil {
				t.Fatal("session fixture unavailable")
			}
			session.Kind = "automation"
			if wrongBoard {
				session.Grant.BoardID = "00000000-0000-4000-8000-000000000099"
			}
			body, err := T.Readable(session)
			if err != nil {
				t.Fatal("session fixture invalid")
			}
			return response(200, body), nil
		}
		client, err := Open(context.Background(), &provider{conn: c}, selected)
		if calls != 2 || (wrongBoard && (err == nil || client != nil)) || (!wrongBoard && (err != nil || client == nil)) {
			t.Fatal("live selected-session verification failed")
		}
		if client != nil {
			client.Close()
		}
	}
}

func TestWorkspaceConnectionRefusesUnverifiedHostBeforeBoardDispatch(t *testing.T) {
	for _, body := range []string{
		`{"apiVersion":1,"mode":"standalone"}`,
		`{"apiVersion":1,"mode":"workspace","workspaceId":"00000000-0000-4000-8000-000000000099"}`,
		`{"apiVersion":1,"mode":"workspace","workspaceId":"` + testInstallation + `","extra":true}`,
		`{"apiVersion":1,"mode":"workspace","workspaceId":"` + testInstallation + `","workspaceId":"` + testInstallation + `"}`,
	} {
		metadata := CredentialMetadata{Selected: selected, ActorID: testActor, ExpiresAt: time.Now().Add(time.Hour)}
		c, err := NewLocalWorkspaceBoardConnection(evidence.Context{InstallationID: testInstallation, Origin: "http://127.0.0.1:8792"}, metadata, T.SecretToken("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), testInstallation)
		if err != nil {
			t.Fatal("connection unavailable")
		}
		calls, closed := 0, false
		c.close = func() { closed = true }
		c.do = func(q *http.Request) (*http.Response, error) {
			calls++
			if calls != 1 || q.URL.Path != "/api/v1/runtime-mode" {
				t.Fatal("board session requested from unverified host")
			}
			return response(200, []byte(body)), nil
		}
		if client, err := Open(context.Background(), &provider{conn: c}, selected); client != nil || err == nil || calls != 1 || !closed {
			t.Fatal("unverified workspace host accepted")
		}
	}
}

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
