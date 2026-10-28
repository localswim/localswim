//go:build windows && amd64

package workspace

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	W "github.com/localswim/localswim/internal/workspace"
)

func TestNativeWorkspaceSelectionControlAndAuthoritySeparation(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	if configured := os.Getenv("L02_TEST_ROOT"); configured != "" {
		var e error
		base, e = os.MkdirTemp(configured, "cli-ws-")
		if e != nil {
			t.Fatal("fixture root unavailable")
		}
	}
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal("port unavailable")
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	root := filepath.Join(base, "workspace")
	project := filepath.Join(base, "harbor")
	board := filepath.Join(project, "berth")
	h, e := W.Start(ctx, W.Options{DataDir: root, Port: port})
	if e != nil {
		if h != nil {
			_ = h.Close()
		}
		t.Fatal("host start failed")
	}
	defer func() {
		if h.Close() != nil {
			t.Error("host close failed")
		}
	}()
	c, e := Open(ctx, root)
	if e != nil {
		if c != nil {
			_ = c.Close()
		}
		t.Fatal("authenticated Open failed")
	}
	defer func() {
		if c.Close() != nil {
			t.Error("client cleanup failed")
		}
	}()
	if v, e := c.CreateProject(ctx, T.WorkspaceProjectRequest{Key: "harbor", Label: "Harbor", Root: T.WorkspacePath(project)}); e != nil || v.Status != "registered" {
		t.Fatal("project creation failed")
	}
	// Supplementary fault: the actual local host publishes the board once, but
	// this transport deliberately loses the usable response. No automatic status
	// or create is performed by Client after the ambiguous reply.
	realDo := c.do
	posts := 0
	c.do = func(q *http.Request) (*http.Response, error) {
		if q.Method == "POST" {
			posts++
		}
		r, e := realDo(q)
		if e == nil && q.Method == "POST" && q.URL.Path == "/api/v1/workspace/boards" {
			_ = r.Body.Close()
			r.Body = io.NopCloser(strings.NewReader(`{"invalid":true}`))
			r.ContentLength = -1
			r.Header.Del("Content-Length")
		}
		return r, e
	}
	v, e := c.CreateBoard(ctx, T.WorkspaceBoardRequest{ProjectKey: "harbor", BoardKey: "berth", Label: "Berth", DataDir: T.WorkspacePath(board), Action: "create"})
	assertFailure(t, e, "OUTCOME_UNKNOWN", 4)
	if v.Status != "UNKNOWN" || posts != 1 {
		t.Fatal("ambiguous create replayed")
	}
	c.do = realDo
	path := T.WorkspacePath(board)
	status, e := c.Status(ctx, T.WorkspaceStatusRequest{ProjectKey: "harbor", BoardKey: "berth", DataDir: &path})
	if e != nil || status.Status != "registered" || status.Identity == nil {
		t.Fatal("explicit status did not identify existing registration")
	}
	var selection *Selection
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		selection, e = c.Select(ctx, "harbor", "berth")
		if e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e != nil || selection == nil || selection.DataDir() != board {
		t.Fatal("exact live selection failed")
	}
	if selection.Context().Origin != h.Origin() || selection.Selectors().BoardID != string(status.Identity.BoardID) {
		t.Fatal("selection context changed")
	}
	cl, e := core.Open(ctx, selection.Provider(), selection.Selectors())
	if e != nil {
		t.Fatal("selected automation session failed")
	}
	defer cl.Close()
	s, _, _, e := cl.Request(ctx, "GET", "/api/v1/boards/"+selection.Selectors().BoardID, nil, "", "application/json", 1<<20)
	if e != nil || s != 200 {
		t.Fatal("selected board adapter failed")
	}
	if _, e := selection.NativeOwner(ctx, evidence.Record{}); e == nil {
		t.Fatal("workspace exposed native authority")
	}
	s, _, e = c.request(ctx, "POST", "/api/v1/boards/"+selection.Selectors().BoardID+"/cards", []byte(`{}`))
	if e != nil || (s != 401 && s != 403) {
		t.Fatal("workspace capability reached board mutation admission")
	}
	if _, e := c.Select(ctx, "harbor", "other"); e == nil {
		t.Fatal("missing key silently selected another board")
	}
	if e := c.Close(); e != nil {
		t.Fatal("client close failed")
	}
	if e := h.Close(); e != nil {
		t.Fatal("host close failed")
	}
	// Focused evidence location is independent of a running host or rendezvous.
	location, e := Locate(root, "harbor", "berth")
	if e != nil || location == nil || location.DataDir() != board {
		t.Fatal("offline original location required live host")
	}
	if location.Close() != nil {
		t.Fatal("location cleanup failed")
	}
	if next, e := Open(ctx, root); next != nil || e == nil {
		if next != nil {
			_ = next.Close()
		}
		t.Fatal("offline rendezvous treated as authority")
	}
}
