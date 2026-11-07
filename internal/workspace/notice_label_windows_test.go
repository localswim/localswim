//go:build windows && amd64

package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestWorkspaceNoticeLabelCreatedAndRegisteredActivation(t *testing.T) {
	for _, label := range []T.WorkspaceLabel{"  Harbor 😀  ", "unsafe\u202elabel"} {
		t.Run(string(label), func(t *testing.T) {
			base := testBase(t)
			t.Cleanup(func() {
				if err := os.RemoveAll(base); err != nil {
					t.Error(err)
				}
			})
			root := filepath.Join(base, "workspace")
			r := testStart(t, root, testPort(t))
			project := filepath.Join(base, "project")
			code, _ := testHTTP(t, r, "POST", "/api/v1/workspace/projects", projectRequest(project, 1), nil)
			if code != 200 {
				t.Fatal("project setup failed")
			}
			request := T.WorkspaceBoardRequest{APIVersion: 1, ExpectedConfigRevision: 2, ProjectKey: "harbor", BoardKey: "main", Label: label, DataDir: T.WorkspacePath(filepath.Join(project, "main")), Action: "create"}
			code, _ = testHTTP(t, r, "POST", "/api/v1/workspace/boards", request, nil)
			if code != 200 {
				t.Fatal("board creation failed")
			}
			await := func(runtime *Runtime) string {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for {
					runtime.mu.Lock()
					state := runtime.states[memberKey("harbor", "main")]
					var got string
					var want string
					if state != nil && state.engine != nil {
						// Limited test-only inspection avoids an exported production
						// getter for private immutable engine construction metadata.
						got = reflect.ValueOf(state.engine).Elem().FieldByName("noticeLabel").String()
						want = string(label)
						if label == "unsafe\u202elabel" {
							want = "Board " + state.engine.Identity().BoardID
						}
					}
					runtime.mu.Unlock()
					if got != "" {
						if got != want {
							t.Fatal("workspace label did not bind to actual physical engine")
						}
						return got
					}
					if time.Now().After(deadline) {
						t.Fatal("registered activation unavailable")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			created := await(r)
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := testStart(t, root, 0)
			if await(reopened) != created {
				t.Fatal("registered activation changed physical identity label")
			}
		})
	}
}
