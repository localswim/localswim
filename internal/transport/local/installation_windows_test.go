//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallationHTTPSelectedReadOnly(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Fatal("requires explicit private native fixture parent")
	}
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "configured"}[configured], func(t *testing.T) {
			parent := testRoot(t)
			defer os.RemoveAll(parent)
			root := filepath.Join(parent, "board")
			r, e := openEngine(context.Background(), EngineOptions{DataDir: root, Port: freePort(t), Workflow: func() *A.Workflow {
				if configured {
					return configuredFixture()
				}
				return nil
			}()}, nil)
			if e != nil {
				t.Fatal(e)
			}
			r.listener, e = net.Listen("tcp4", strings.TrimPrefix(r.Origin(), "http://"))
			if e != nil {
				t.Fatal(e)
			}
			r.server = newServer(r)
			go func() { _ = r.server.Serve(r.listener) }()
			defer r.Close()
			info, e := ReadClient(root)
			if e != nil {
				t.Fatal(e)
			}
			cookie := bootstrapTest(t, r)
			statusPath := r.boardPath() + "recipient/status?incarnation=" + info.Incarnation
			before := request(t, r, "GET", statusPath, nil, info.Token, nil, nil)
			generation := string(r.recipient.RuntimeGeneration())
			path := r.boardPath() + "recipient/tool/inspect?incarnation=" + info.Incarnation + "&expectedRuntimeGeneration=" + generation
			for _, human := range []bool{false, true} {
				var x response
				if human {
					x = request(t, r, "GET", path, nil, "", cookie, nil)
				} else {
					x = request(t, r, "GET", path, nil, info.Token, nil, nil)
				}
				v, e := T.Decode[T.InstallationInspectionResponse](x.b)
				if x.status != 200 || e != nil || v.Result.State != "candidate" || string(v.Result.RuntimeGeneration) != generation {
					t.Fatalf("inspection status=%d decode=%v state=%s code=%s", x.status, e, v.Result.State, v.Result.Code)
				}
				if bytes.Contains(x.b, []byte("codex.exe")) || bytes.Contains(x.b, []byte("threadId")) {
					t.Fatal("private metadata leaked")
				}
			}
			for _, bad := range []string{path + "&extra=x", path + "&incarnation=" + info.Incarnation} {
				x := request(t, r, "GET", bad, nil, info.Token, nil, nil)
				assertActivityRefusal(t, x.status, x.b, 400, "INVALID_INPUT")
			}
			stale := r.boardPath() + "recipient/tool/inspect?incarnation=" + info.Incarnation + "&expectedRuntimeGeneration=00000000-0000-4000-8000-000000000001"
			x := request(t, r, "GET", stale, nil, info.Token, nil, nil)
			assertActivityRefusal(t, x.status, x.b, 409, "CONTROL_STALE")
			x = request(t, r, "GET", path, nil, "", nil, nil)
			assertActivityRefusal(t, x.status, x.b, 401, "UNAUTHENTICATED")
			x = request(t, r, "GET", path, []byte("{}"), info.Token, nil, nil)
			assertActivityRefusal(t, x.status, x.b, 400, "INVALID_INPUT")
			after := request(t, r, "GET", statusPath, nil, info.Token, nil, nil)
			if !bytes.Equal(before.b, after.b) {
				t.Fatal("recipient state changed")
			}
		})
	}
}
