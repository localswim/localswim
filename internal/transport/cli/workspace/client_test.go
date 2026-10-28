package workspace

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	W "github.com/localswim/localswim/internal/workspace"
)

const workspaceID = "00000000-0000-4000-8000-000000000001"
const epochID = "00000000-0000-4000-8000-000000000002"

func testInfo() W.ClientInfo {
	return W.ClientInfo{WorkspaceID: workspaceID, Origin: "http://127.0.0.1:8792", Epoch: epochID, Token: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
}
func testClient() *Client {
	i := testInfo()
	return &Client{root: "private", info: i, lastRevision: 1, readClient: func(string) (W.ClientInfo, error) { return i, nil }, done: make(chan struct{})}
}
func response(status int, v any) *http.Response {
	b, _ := T.Readable(v)
	return rawResponse(status, string(b))
}
func rawResponse(status int, b string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(b)), ContentLength: -1}
}
func assertFailure(t *testing.T, e error, code string, exit int) {
	t.Helper()
	var f *core.Failure
	if !errors.As(e, &f) || f.Code != code || f.Exit != exit {
		t.Fatalf("wrong fixed failure: %v", e)
	}
}

func TestConnectionRejectsOriginAliasesAndDisablesAmbientProxy(t *testing.T) {
	for _, origin := range []string{"http://localhost:8792", "http://127.0.0.1:08792", "http://127.0.0.1:8792/", "https://127.0.0.1:8792", "http://127.0.0.1:1023", "http://[::1]:8792", "http://user@127.0.0.1:8792", "http://127.0.0.1:8792?x"} {
		i := testInfo()
		i.Origin = origin
		if tr, e := transport(i); tr != nil || e == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
	tr, e := transport(testInfo())
	if e != nil || tr.Proxy != nil || !tr.DisableKeepAlives || !tr.DisableCompression {
		t.Fatal("unsafe transport defaults")
	}
	if conn, e := tr.DialContext(context.Background(), "tcp", "localhost:8792"); conn != nil || e == nil {
		t.Fatal("alternate authority dialed")
	}
}
func TestAuthenticatedWorkspaceReadChecksUUIDAndPrivateEpoch(t *testing.T) {
	c := testClient()
	calls := 0
	c.do = func(q *http.Request) (*http.Response, error) {
		calls++
		if q.Header.Get("Authorization") != "Bearer "+string(c.info.Token) || q.GetBody != nil || q.Method != "GET" {
			t.Fatal("channel headers escaped")
		}
		return response(200, T.WorkspaceProjects{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: 1, Projects: []T.WorkspaceProject{}}), nil
	}
	if _, e := c.Projects(context.Background()); e != nil || calls != 1 {
		t.Fatal("authenticated read failed")
	}
	i := c.info
	i.Epoch = "00000000-0000-4000-8000-000000000099"
	c.readClient = func(string) (W.ClientInfo, error) { return i, nil }
	_, e := c.Projects(context.Background())
	assertFailure(t, e, "CREDENTIAL_CONTEXT_MISMATCH", 2)
	if calls != 1 {
		t.Fatal("stale epoch reached network")
	}
	c.readClient = func(string) (W.ClientInfo, error) { return c.info, nil }
	c.do = func(*http.Request) (*http.Response, error) {
		return response(200, T.WorkspaceProjects{APIVersion: 1, WorkspaceID: "00000000-0000-4000-8000-000000000099", ConfigRevision: 1, Projects: []T.WorkspaceProject{}}), nil
	}
	_, e = c.Projects(context.Background())
	assertFailure(t, e, "CREDENTIAL_CONTEXT_MISMATCH", 2)
}
func TestControlReadsRevisionOnceAndNeverReplays(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   any
		raw    string
		code   string
		exit   int
	}{
		{"registered", 200, T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: 8, Status: "registered", ProjectKey: "harbor"}, "", "", 0},
		{"later concurrent revision", 200, T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: 9, Status: "registered", ProjectKey: "harbor"}, "", "", 0},
		{"stale", 412, T.WorkspaceControlError{APIVersion: 1, Outcome: "refused", ConfigRevision: 9, Error: T.Error{Code: "CONFIG_CHANGED", Message: "refused"}}, "", "CONFIG_CHANGED", 2},
		{"refused", 503, T.WorkspaceControlError{APIVersion: 1, Outcome: "refused", ConfigRevision: 7, Error: T.Error{Code: "UNAVAILABLE", Message: "refused"}}, "", "UNAVAILABLE", 3},
		{"host refusal", 403, T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "FORBIDDEN", Message: "refused"}}, "", "FORBIDDEN", 2},
		{"malformed success", 200, nil, `{"private":"secret-path"}`, "OUTCOME_UNKNOWN", 4},
		{"redirect", 307, nil, `{}`, "OUTCOME_UNKNOWN", 4},
		{"wrong revision", 200, T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: 7, Status: "registered", ProjectKey: "harbor"}, "", "OUTCOME_UNKNOWN", 4},
		{"unknown tuple", 200, T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: 8, Status: "UNKNOWN", ProjectKey: "harbor", Identity: &T.WorkspaceIdentity{InstallationID: workspaceID, BoardID: "00000000-0000-4000-8000-000000000003", Incarnation: "00000000-0000-4000-8000-000000000004"}}, "", "OUTCOME_UNKNOWN", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient()
			gets, posts := 0, 0
			c.do = func(q *http.Request) (*http.Response, error) {
				if q.Method == "GET" {
					gets++
					return response(200, T.WorkspaceProjects{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: 7, Projects: []T.WorkspaceProject{}}), nil
				}
				posts++
				if q.GetBody != nil || q.Header.Get("Idempotency-Key") != "" {
					t.Fatal("POST replay support enabled")
				}
				b, _ := io.ReadAll(q.Body)
				v, e := T.Decode[T.WorkspaceProjectRequest](b)
				if e != nil || v.ExpectedConfigRevision != 7 {
					t.Fatal("revision not bound to one read")
				}
				if tc.raw != "" {
					return rawResponse(tc.status, tc.raw), nil
				}
				return response(tc.status, tc.body), nil
			}
			v, e := c.CreateProject(context.Background(), T.WorkspaceProjectRequest{Key: "harbor", Label: "Harbor", Root: `C:\synthetic`})
			if tc.code == "" {
				if e != nil || v.Status != "registered" {
					t.Fatal("registration refused")
				}
			} else {
				assertFailure(t, e, tc.code, tc.exit)
				if tc.exit == 4 && (v.Status != "UNKNOWN" || v.Identity != nil) {
					t.Fatal("ambiguous registration not unknown")
				}
			}
			if gets != 1 || posts != 1 {
				t.Fatal("revision reread or control replay")
			}
		})
	}
}
func TestOpenObservationUsedOnce(t *testing.T) {
	c := testClient()
	c.observedRevision = 7
	gets, posts := 0, 0
	c.do = func(q *http.Request) (*http.Response, error) {
		if q.Method == "GET" {
			gets++
			return nil, errors.New("should not reread")
		}
		posts++
		return response(200, T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: 8, Status: "registered", ProjectKey: "harbor"}), nil
	}
	if _, e := c.CreateProject(context.Background(), T.WorkspaceProjectRequest{Key: "harbor", Label: "Harbor", Root: `C:\synthetic`}); e != nil || gets != 0 || posts != 1 {
		t.Fatal("authenticated Open observation was reread")
	}
}

type pendingCleanup struct{ calls int }

func (*pendingCleanup) Error() string { return "private cleanup pending" }
func (p *pendingCleanup) Close() error {
	p.calls++
	if p.calls == 1 {
		return errors.New("pending")
	}
	return nil
}
func TestCleanupCustodyRetainedUntilSuccessfulRetry(t *testing.T) {
	c := testClient()
	p := &pendingCleanup{}
	c.retain(p)
	if c.Close() == nil {
		t.Fatal("failed close reported released")
	}
	select {
	case <-c.Done():
		t.Fatal("Done closed with custody")
	default:
	}
	if c.Close() != nil || p.calls != 2 {
		t.Fatal("cleanup not retryable")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done missing after release")
	}
	if c.Close() != nil || p.calls != 2 {
		t.Fatal("completed cleanup repeated")
	}
}
func TestStatusResultIdentityIsNotInvented(t *testing.T) {
	c := testClient()
	c.do = func(*http.Request) (*http.Response, error) {
		b := T.WorkspaceKey("berth")
		return response(200, T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: A.Positive(1), Status: "registered", ProjectKey: "harbor", BoardKey: &b}), nil
	}
	v, e := c.Status(context.Background(), T.WorkspaceStatusRequest{ProjectKey: "harbor", BoardKey: "berth"})
	assertFailure(t, e, "OUTCOME_UNKNOWN", 4)
	if v.Status != "UNKNOWN" || v.Identity != nil {
		t.Fatal("unverified identity accepted")
	}
}

func TestStatusRejectsDifferentExpectedIdentityAndOlderRevision(t *testing.T) {
	identity := T.WorkspaceIdentity{InstallationID: workspaceID, BoardID: "00000000-0000-4000-8000-000000000003", Incarnation: "00000000-0000-4000-8000-000000000004"}
	for _, wrongRevision := range []bool{false, true} {
		c := testClient()
		c.lastRevision = 7
		c.do = func(*http.Request) (*http.Response, error) {
			b := T.WorkspaceKey("berth")
			id := identity
			revision := A.Positive(7)
			if wrongRevision {
				revision = 6
			} else {
				id.BoardID = "00000000-0000-4000-8000-000000000005"
			}
			return response(200, T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: workspaceID, ConfigRevision: revision, Status: "registered", ProjectKey: "harbor", BoardKey: &b, Identity: &id}), nil
		}
		v, e := c.Status(context.Background(), T.WorkspaceStatusRequest{ProjectKey: "harbor", BoardKey: "berth", ExpectedIdentity: &identity})
		assertFailure(t, e, "OUTCOME_UNKNOWN", 4)
		if v.Status != "UNKNOWN" || v.Identity != nil {
			t.Fatal("unbound status facts published")
		}
	}
}
