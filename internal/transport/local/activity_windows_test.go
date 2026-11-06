//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/boardapi"
)

func assertActivityRefusal(t *testing.T, status int, body []byte, expected int, code string) {
	t.Helper()
	refusal, err := T.Decode[T.NonterminalRefusal](body)
	if status != expected || err != nil || refusal.Error.Code != code {
		t.Fatalf("activity refusal status=%d decode=%v code=%s; want %d %s", status, err, refusal.Error.Code, expected, code)
	}
}

func TestActivityNativeOrdinaryHTTP(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Skip("requires explicit private native test parent")
	}
	r, root := startTest(t)
	other, otherRoot := startTest(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	otherInfo, err := ReadClient(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	cookie := bootstrapTest(t, r)
	makeCard := func(rev int, title string) []byte {
		return mutationWire(t, r, "", rev, 0, map[string]any{"title": title, "description": "PRIVATE-DESCRIPTION"})
	}
	create := func(b []byte) response {
		return request(t, r, "POST", r.boardPath()+"cards", b, info.Token, nil, nil)
	}
	for rev := range 21 {
		wire := makeCard(rev, fmt.Sprintf("PRIVATE-CARD-%d", rev))
		term(t, create(wire), "committed")
		if rev == 20 {
			term(t, create(wire), "committed")
		}
	}
	if conflict := term(t, create(makeCard(0, "PRIVATE-CONFLICT")), "not_committed"); conflict.Code != "REVISION_CONFLICT" {
		t.Fatal("expected a retained revision conflict")
	}
	path := r.boardPath() + "activity?incarnation=" + info.Incarnation
	activityPage := func(res response) T.ActivityView {
		t.Helper()
		p, err := T.Decode[T.ActivityRead](res.b)
		if res.status != 200 || err != nil || string(p.Result.Identity.Incarnation) != info.Incarnation || string(p.Result.Identity.BoardID) != info.BoardID || bytes.Contains(res.b, []byte("PRIVATE")) {
			t.Fatalf("activity page status=%d decode=%v", res.status, err)
		}
		return p.Result
	}
	first := activityPage(request(t, r, "GET", path, nil, info.Token, nil, nil))
	if first.NextCursor == nil || len(first.Events) != 20 || first.Revision != 21 || first.LocalEventUpper != 21 {
		t.Fatal("committed activity differs from stored outcomes")
	}
	continuation := "&cursor=" + url.QueryEscape(*first.NextCursor)
	second := activityPage(request(t, r, "GET", path+continuation, nil, info.Token, nil, nil))
	if second.NextCursor != nil || len(second.Events) != 1 || second.Events[0].EventID != 1 || second.Events[0].ActorKind != "automation" {
		t.Fatal("replay or refused operation added an activity event")
	}
	for _, tc := range []struct {
		res    response
		status int
		code   string
	}{
		{request(t, r, "GET", r.boardPath()+"activity?incarnation="+otherInfo.Incarnation+continuation, nil, info.Token, nil, nil), 403, "FORBIDDEN"},
		{request(t, r, "GET", other.boardPath()+"activity?incarnation="+otherInfo.Incarnation+continuation, nil, info.Token, nil, nil), 404, "NOT_FOUND"},
		{request(t, other, "GET", other.boardPath()+"activity?incarnation="+otherInfo.Incarnation+continuation, nil, otherInfo.Token, nil, nil), 409, "CURSOR_STALE"},
		{request(t, r, "GET", path+continuation, nil, "", cookie, nil), 409, "CURSOR_STALE"},
		{request(t, r, "GET", path+continuation, nil, info.Token, cookie, nil), 401, "UNAUTHENTICATED"},
		{request(t, r, "GET", path+continuation, nil, "", cookie, func(q *http.Request) { q.Header.Set("Origin", other.Origin()) }), 403, "FORBIDDEN"},
		{request(t, r, "GET", path+continuation, []byte("x"), info.Token, nil, nil), 400, "INVALID_INPUT"},
	} {
		assertActivityRefusal(t, tc.res.status, tc.res.b, tc.status, tc.code)
	}
	q := httptest.NewRequest("GET", path, nil)
	q.Host = strings.TrimPrefix(r.Origin(), "http://")
	q.AddCookie(cookie)
	q.Header.Set("X-Localswim-Client", "browser")
	q.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	activityPage(response{status: w.Code, b: w.Body.Bytes()})
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatal("ordinary CSP or cache policy changed")
	}
	term(t, create(makeCard(21, "PRIVATE-LATE")), "committed")
	stale := request(t, r, "GET", path+continuation, nil, info.Token, nil, nil)
	assertActivityRefusal(t, stale.status, stale.b, 409, "CURSOR_STALE")
	if fresh := activityPage(request(t, r, "GET", path, nil, info.Token, nil, nil)); fresh.Revision != 22 || fresh.Events[0].EventID != 22 {
		t.Fatal("fresh read missed the later commit")
	}
	configuredGrantSQL(t, r, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", r.id.Incarnation, r.id.BotGrant)
	denied := request(t, r, "GET", path+continuation, nil, info.Token, nil, nil)
	assertActivityRefusal(t, denied.status, denied.b, 403, "FORBIDDEN")
}

type activityTestAccess struct {
	*authority
	admit func(mode string, token T.SecretToken) (*grants.Service, error)
}

func (a activityTestAccess) Admission(mode string, token T.SecretToken) (*grants.Service, error) {
	return a.admit(mode, token)
}

func TestActivityNativeRechecksAfterOuterAuthority(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Skip("requires explicit private native test parent")
	}
	r, root := startTest(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	var cli grants.Credential
	for _, c := range configuredCredentials(r) {
		if c.Mode == "cli" {
			cli = c
		}
	}
	replacementGrant := uuid()
	configuredGrantSQL(t, r, "INSERT INTO grant_fact(incarnation,grant_id) VALUES(?,?)", r.id.Incarnation, replacementGrant)
	configuredGrantSQL(t, r, "INSERT INTO grant_record(incarnation,grant_id,actor_id,enabled) VALUES(?,?,?,1)", r.id.Incarnation, replacementGrant, r.id.BotID)
	configuredGrantSQL(t, r, "INSERT INTO grant_right(incarnation,grant_id,right_name) VALUES(?,?,'read')", r.id.Incarnation, replacementGrant)
	for _, tc := range []struct {
		name   string
		admit  func(*testing.T, string, T.SecretToken) (*grants.Service, error)
		status int
		code   string
	}{
		{"replacement", func(*testing.T, string, T.SecretToken) (*grants.Service, error) {
			return grants.New(cli.InstallationID, cli.Origin, func(context.Context, writer.Read, time.Time) (grants.Credential, error) {
				replacement := cli
				replacement.GrantID = replacementGrant
				return replacement, nil
			})
		}, 403, "FORBIDDEN"},
		{"revocation", func(t *testing.T, mode string, token T.SecretToken) (*grants.Service, error) {
			configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.BotGrant)
			t.Cleanup(func() {
				configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=1 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.BotGrant)
			})
			return r.auth.Admission(mode, token)
		}, 403, "FORBIDDEN"},
		{"credential query failure", func(t *testing.T, _ string, _ T.SecretToken) (*grants.Service, error) {
			return grants.New(cli.InstallationID, cli.Origin, func(_ context.Context, read writer.Read, _ time.Time) (grants.Credential, error) {
				var value string
				if err := read.Scan("SELECT value FROM missing_controlled_credential_table", nil, &value); err == nil {
					t.Fatal("credential failure fixture unexpectedly succeeded")
				}
				return grants.Credential{}, grants.ErrDenied
			})
		}, 503, "UNAVAILABLE"},
		{"expired", func(*testing.T, string, T.SecretToken) (*grants.Service, error) {
			return grants.New(cli.InstallationID, cli.Origin, func(context.Context, writer.Read, time.Time) (grants.Credential, error) {
				expired := cli
				expired.IssuedAt = time.Now().Add(-time.Hour)
				expired.ExpiresAt = time.Now().Add(-time.Minute)
				return expired, nil
			})
		}, 401, "UNAUTHENTICATED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Even a well-formed cursor with a bad signature must wait for the
			// current ordinary admission decision.
			h, err := boardapi.NewAuthenticated(r.store, activityTestAccess{r.auth, func(mode string, token T.SecretToken) (*grants.Service, error) {
				return tc.admit(t, mode, token)
			}}, r)
			if err != nil {
				t.Fatal(err)
			}
			q := httptest.NewRequest("GET", r.boardPath()+"activity?incarnation="+info.Incarnation+"&cursor=e30.YQ", nil)
			q.Host = strings.TrimPrefix(r.Origin(), "http://")
			q.Header.Set("Authorization", "Bearer "+string(info.Token))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, q)
			assertActivityRefusal(t, w.Code, w.Body.Bytes(), tc.status, tc.code)
		})
	}
}
