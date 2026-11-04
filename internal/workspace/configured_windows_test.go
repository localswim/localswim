//go:build windows && amd64

package workspace

import (
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"testing"
)

func workspaceWorkflow(t *testing.T) A.Workflow {
	t.Helper()
	w, e := A.DecodeWorkflow([]byte(`{"workflowId":"10000000-0000-4000-8000-000000000010","revision":1,"lanes":[{"id":"10000000-0000-4000-8000-000000000001","label":"Queue","role":"initial"},{"id":"10000000-0000-4000-8000-000000000002","label":"Work","role":"in_progress"},{"id":"10000000-0000-4000-8000-000000000003","label":"Review","role":"review"},{"id":"10000000-0000-4000-8000-000000000004","label":"Done","role":"completed"}],"transitions":[{"from":"10000000-0000-4000-8000-000000000001","to":"10000000-0000-4000-8000-000000000002","actors":["human"]},{"from":"10000000-0000-4000-8000-000000000002","to":"10000000-0000-4000-8000-000000000003","actors":["human"]},{"from":"10000000-0000-4000-8000-000000000003","to":"10000000-0000-4000-8000-000000000004","actors":["human"]}],"actorRights":{"human":["approve","create","edit","read","read-own-outcome","review-handoff"],"automation":["read","read-own-outcome"]}}`))
	if e != nil {
		t.Fatal(e)
	}
	return *w
}

func TestConfiguredBrowserCreateStatusUsesOwnedSnapshot(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "workspace"), testPort(t))
	code, _ := testHTTP(t, r, "POST", "/api/v1/workspace/projects", projectRequest(filepath.Join(base, "project"), 1), nil)
	if code != 200 {
		t.Fatal("project setup")
	}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	defer browser.CloseIdleConnections()
	code, _ = testHTTP(t, r, "POST", "/api/v1/workspace/bootstrap", map[string]any{}, browser)
	if code != 200 {
		t.Fatal("bootstrap")
	}
	w := workspaceWorkflow(t)
	req := T.ConfiguredWorkspaceBoardRequest{APIVersion: 1, WorkspaceID: r.snapshot().WorkspaceID, ExpectedConfigRevision: 2, ProjectKey: "harbor", BoardKey: "custom", Label: "Custom", Workflow: w}
	code, raw := testHTTP(t, r, "POST", "/api/v1/workspace/boards/configured", req, browser)
	created, e := T.Decode[T.WorkspaceControlResult](raw)
	if code != 200 || e != nil || created.Status != "registered" {
		t.Fatalf("create %d: %v", code, e)
	}
	r.mu.Lock()
	state := r.states[memberKey("harbor", "custom")]
	engine := state.engine
	r.mu.Unlock()
	status := T.ConfiguredWorkspaceStatusRequest{APIVersion: 1, WorkspaceID: req.WorkspaceID, ProjectKey: req.ProjectKey, BoardKey: req.BoardKey, Workflow: w, ExpectedIdentity: created.Identity}
	code, raw = testHTTP(t, r, "POST", "/api/v1/workspace/boards/configured/status", status, browser)
	got, e := T.Decode[T.WorkspaceControlResult](raw)
	if code != 200 || e != nil || got.Status != "registered" {
		t.Fatalf("live status %d: %v", code, e)
	}
	r.mu.Lock()
	same := r.states[memberKey("harbor", "custom")].engine == engine
	r.mu.Unlock()
	if !same {
		t.Fatal("status replaced live engine")
	}
	status.Workflow.Lanes[0].Label = "Changed"
	_, raw = testHTTP(t, r, "POST", "/api/v1/workspace/boards/configured/status", status, browser)
	got, e = T.Decode[T.WorkspaceControlResult](raw)
	if e != nil || got.Status != "UNKNOWN" {
		t.Fatal("same UUID different definition reported success")
	}
	code, _ = testHTTP(t, r, "POST", "/api/v1/workspace/boards/configured", req, browser)
	if code != 412 {
		t.Fatal("duplicate create did not preserve CAS")
	}
	code, _ = testHTTP(t, r, "POST", "/api/v1/workspace/boards/configured", req, nil)
	if code != 403 {
		t.Fatal("configured browser route accepted CLI channel")
	}
}
