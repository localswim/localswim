//go:build windows && amd64 && h01_native

package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

const grantAccessBoard = "10000000-0000-0000-0000-000000000001"
const grantAccessInc = "20000000-0000-0000-0000-000000000001"

func grantAccessFresh(t *testing.T) (*Service, T.SecretToken) {
	t.Helper()
	if os.Getenv("H01_NATIVE_OVERLAY") != "1" {
		t.Fatal("explicit private overlay required")
	}
	base := os.Getenv("S01_TEST_ROOT")
	if !filepath.IsAbs(base) {
		t.Fatal("absolute private fixture root required")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(filepath.Clean(base))+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("private root outside repository required")
	}
	root, err := os.MkdirTemp(base, "grant-access-")
	if err != nil {
		t.Fatal("private fixture unavailable")
	}
	t.Setenv("H01_NATIVE_TEST_ROOT", filepath.Join(root, "installation"))
	check, err := setup.H01QualificationRoot()
	if err != nil || check != filepath.Join(root, "installation") {
		t.Fatal("compiled private overlay sentinel required")
	}
	m, err := setup.New(nil)
	if err != nil || m.Init(nil).Exit != 0 {
		t.Fatal("private native initialization failed")
	}
	owner, err := m.LoadNativeOwner()
	if err != nil {
		t.Fatal("native owner unavailable")
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal("account unavailable")
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal("ACL unavailable")
	}
	dir := filepath.Join(root, "board")
	if windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}) != nil {
		t.Fatal("private board unavailable")
	}
	store, err := S.Create(context.Background(), dir, schema.Identity{BoardID: grantAccessBoard, Incarnation: grantAccessInc})
	if err != nil {
		t.Fatal("store unavailable")
	}
	s, err := SourceRoot(store)
	if err != nil {
		t.Fatal("source composition unavailable")
	}
	t.Cleanup(func() {
		s.Close()
		if store.Close() != nil {
			t.Error("store cleanup")
		}
	})
	return s, T.SecretToken(strings.TrimPrefix(owner.Authorization(), "Bearer "))
}

func TestGrantAccessNativeSnapshotAndObservedSubmission(t *testing.T) {
	s, token := grantAccessFresh(t)
	var actor string
	sentinel := errors.New("callback scope absent")
	err := s.WithNativeSnapshot(context.Background(), token, func(id string, r writer.Read) error {
		actor = id
		if _, ok := r.(writer.Write); ok {
			t.Fatal("write seam exposed")
		}
		if r.Scan("PRAGMA query_only=OFF", nil, new(int)) == nil {
			t.Fatal("PRAGMA exposed")
		}
		var board, inc string
		if err := r.Scan("SELECT board_id,incarnation FROM board", nil, &board, &inc); err != nil {
			return err
		}
		if board != grantAccessBoard || inc != grantAccessInc {
			t.Fatal("snapshot scope mismatch")
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) || actor != s.Context().OwnerActorID {
		t.Fatal("actual owner/error not preserved")
	}
	called := false
	if err = s.WithNativeSnapshot(context.Background(), "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", func(string, writer.Read) error { called = true; return nil }); !errors.Is(err, ErrDenied) || called {
		t.Fatal("invalid native token opened callback")
	}
	freeze := func(command, op, target string, rev uint64) contract.Intent {
		var routeTarget any
		input := any(map[string]any{})
		if command == "grant.create" {
			input = map[string]any{"granteeActorId": "30000000-0000-0000-0000-000000000003", "kind": "automation", "rights": []string{"read", "read-own-outcome"}}
		} else {
			routeTarget = map[string]string{"grantId": target}
		}
		raw, _ := json.Marshal(map[string]any{"boardId": grantAccessBoard, "incarnation": grantAccessInc, "opId": op, "target": routeTarget, "expectedRevision": rev, "expectedCardRevision": nil, "input": input})
		i, err := contract.Freeze(raw, contract.ResolvedRoute{Command: contract.Command(command), BoardID: grantAccessBoard, TargetID: target}, contract.AuthenticatedActor{ActorID: actor})
		if err != nil {
			t.Fatal("freeze failed")
		}
		return i
	}
	i := freeze("grant.create", "50000000-0000-0000-0000-000000000001", "", 0)
	route := grants.Route{BoardID: grantAccessBoard, Incarnation: grantAccessInc}
	result, fresh := s.SubmitNativeObserved(context.Background(), token, i, route)
	if result.Preparation != Confirmed || result.Result == nil || result.Result.Terminal == nil || !fresh {
		t.Fatal("new Apply not observed")
	}
	replay, replayFresh := s.SubmitNativeObserved(context.Background(), token, i, route)
	if replay.Preparation != Confirmed || replay.Result == nil || replay.Result.Terminal == nil || replayFresh {
		t.Fatal("retained receipt reported fresh")
	}
	var body struct {
		GrantID string `json:"grantId"`
	}
	if json.Unmarshal(*result.Result.Terminal.Result, &body) != nil {
		t.Fatal("grant result invalid")
	}
	route.GrantID = body.GrantID
	revoke, revokeFresh := s.SubmitNativeObserved(context.Background(), token, freeze("grant.revoke", "50000000-0000-0000-0000-000000000002", body.GrantID, 1), route)
	if revoke.Result == nil || revoke.Result.Terminal == nil || revoke.Result.Terminal.Outcome != "committed" || revokeFresh {
		t.Fatal("revoke freshness invalid")
	}
}
