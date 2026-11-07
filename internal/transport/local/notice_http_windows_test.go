//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestNoticeNativeOrdinaryHTTP(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Skip("requires explicit private native test parent")
	}
	parent := testRoot(t)
	t.Cleanup(func() {
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	root := filepath.Join(parent, "local")
	r, err := Start(context.Background(), Options{DataDir: root, Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	inc := A.UUID(info.Incarnation)
	prefix := r.boardPath() + "notices/"
	cookie := bootstrapTest(t, r)
	view := func(x response) T.NoticeView {
		t.Helper()
		out, err := T.Decode[T.NoticeResponse](x.b)
		if x.status != http.StatusOK || err != nil || out.Result.Identity.Incarnation != inc ||
			string(out.Result.Identity.BoardID) != info.BoardID || bytes.Contains(x.b, []byte("PRIVATE-")) {
			t.Fatalf("invalid notice response: status=%d decode=%v", x.status, err)
		}
		return out.Result
	}
	read := func(action string) T.NoticeView {
		return view(request(t, r, "GET", prefix+action+"?incarnation="+string(inc), nil, info.Token, nil, nil))
	}
	control := func(action string, generation T.UUIDv4, browser bool) response {
		t.Helper()
		if generation == "" {
			t.Fatal("missing control generation")
		}
		body, err := T.Readable(T.NoticeControlRequest{Incarnation: inc, ExpectedControlGeneration: generation, ExpectedConfigurationRevision: noticeTestRevision(r)})
		if err != nil {
			t.Fatal(err)
		}
		if browser {
			return request(t, r, "POST", prefix+action, body, "", cookie, nil)
		}
		return request(t, r, "POST", prefix+action, body, info.Token, nil, nil)
	}
	initial := read("status")
	if initial.State != "unconfigured" || initial.TargetID != nil {
		t.Fatal("unexpected initial notices state")
	}
	configured := view(control("configure", initial.ControlGeneration, false))
	if configured.State != "stopped" || configured.TargetID == nil {
		t.Fatal("configuration did not create fixed target")
	}
	stale := control("start", initial.ControlGeneration, false)
	assertActivityRefusal(t, stale.status, stale.b, http.StatusConflict, "CONTROL_STALE")
	running := view(control("start", configured.ControlGeneration, true))
	if running.State != "running" || running.TargetID == nil || *running.TargetID != *configured.TargetID {
		t.Fatal("browser did not start the fixed target")
	}
	waitFor := func(match func(T.NoticeView) bool) T.NoticeView {
		t.Helper()
		until := time.Now().Add(9 * time.Second)
		for {
			got := read("inbox")
			if match(got) {
				return got
			}
			if time.Now().After(until) {
				t.Fatalf("notice state did not reach expectation: %s", got.State)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	botCard := mutationWire(t, r, "", 0, 0, map[string]any{"title": "PRIVATE-BOT", "description": "PRIVATE-DESCRIPTION"})
	term(t, request(t, r, "POST", r.boardPath()+"cards", botCard, info.Token, nil, nil), "committed")
	suppressed := waitFor(func(v T.NoticeView) bool { return v.Last != nil && v.Last.Class == "suppressed" })
	if len(suppressed.Entries) != 0 || suppressed.Checkpoint == nil || *suppressed.Checkpoint != 1 {
		t.Fatal("verified automation did not advance without a notice")
	}
	humanCard := mutationWire(t, r, "", 1, 0, map[string]any{"title": "PRIVATE-HUMAN", "description": "PRIVATE-DESCRIPTION"})
	term(t, request(t, r, "POST", r.boardPath()+"cards", humanCard, "", cookie, nil), "committed")
	delivered := waitFor(func(v T.NoticeView) bool {
		return v.Pending == nil && v.Last != nil && v.Last.Class == "human" && len(v.Entries) > 0
	})
	p := delivered.Entries[0]
	body, err := T.Readable(T.NoticeLookupRequest{Incarnation: inc, ExpectedControlGeneration: read("status").ControlGeneration, ExpectedConfigurationRevision: 1, TargetID: p.TargetID, DeliveryID: p.DeliveryID})
	if err != nil {
		t.Fatal(err)
	}
	lookup := view(request(t, r, "POST", prefix+"lookup", body, info.Token, nil, nil))
	if lookup.Lookup == nil || lookup.Lookup.State != "accepted" || lookup.Lookup.Receipt == nil ||
		lookup.Lookup.Receipt.DeliveryID != p.DeliveryID {
		t.Fatal("lookup did not return the original receipt")
	}
	stale = control("stop", configured.ControlGeneration, false)
	assertActivityRefusal(t, stale.status, stale.b, http.StatusConflict, "CONTROL_STALE")
	stopped := view(control("stop", read("status").ControlGeneration, false))
	if stopped.State != "stopped" || stopped.TargetID == nil || *stopped.TargetID != p.TargetID {
		t.Fatal("stop did not drain the fixed target")
	}
	configuredGrantSQL(t, r, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", r.id.Incarnation, r.id.BotGrant)
	denied := request(t, r, "GET", prefix+"inbox?incarnation="+string(inc), nil, info.Token, nil, nil)
	assertActivityRefusal(t, denied.status, denied.b, http.StatusForbidden, "FORBIDDEN")
}

func noticeTestRevision(r *Runtime) A.Number {
	if s := r.notices.Snapshot().State; s != nil {
		return s.ConfigurationRevision
	}
	return 0
}
