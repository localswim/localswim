//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/board/noticeinterval"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func nativeRecoverBody(t *testing.T, incarnation string, generation T.UUIDv4, revisions ...A.Number) []byte {
	t.Helper()
	revision := A.Number(1)
	if len(revisions) > 0 {
		revision = revisions[0]
	}
	body, err := T.Readable(T.NoticeControlRequest{ExpectedConfigurationRevision: revision, Incarnation: A.UUID(incarnation), ExpectedControlGeneration: generation})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func nativeRecoveryView(t *testing.T, out response, info ClientInfo) T.NoticeView {
	t.Helper()
	view, err := T.Decode[T.NoticeResponse](out.b)
	if out.status != http.StatusOK || err != nil || string(view.Result.Identity.BoardID) != info.BoardID ||
		string(view.Result.Identity.Incarnation) != info.Incarnation || bytes.Contains(out.b, []byte("pendingAttempt")) {
		t.Fatalf("invalid recovery response: status=%d decode=%v", out.status, err)
	}
	return view.Result
}

func TestNativeNoticeRecoveryHTTPAdmission(t *testing.T) {
	r, root := startNativeNoticeTestBoard(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	path := r.boardPath() + "notices/recover"
	statePath := filepath.Join(root, noticeStateName)
	read := func() T.NoticeView {
		return nativeRecoveryView(t, request(t, r, "GET", r.boardPath()+"notices/status?incarnation="+info.Incarnation, nil, info.Token, nil, nil), info)
	}
	initial := read()
	initialBody := nativeRecoverBody(t, info.Incarnation, initial.ControlGeneration, 0)
	if v := nativeRecoveryView(t, request(t, r, "POST", path, initialBody, info.Token, nil, nil), info); v.State != "unconfigured" || v.TargetID != nil {
		t.Fatal("unconfigured recovery did not return a read-only view")
	}
	if _, err := os.Lstat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unconfigured recovery allocated native evidence", err)
	}
	target := configureNativeNotices(t, r, info)
	configured := read()
	body := nativeRecoverBody(t, info.Incarnation, configured.ControlGeneration)
	extra := []byte(strings.TrimSuffix(strings.TrimSpace(string(body)), "}") + `,"targetId":"` + string(target) + `"}`)
	cookie := bootstrapTest(t, r)
	before := noticeTestBytes(t, statePath)
	for _, c := range []struct {
		name, method, suffix string
		body                 []byte
		cli                  T.SecretToken
		browser              *http.Cookie
		change               func(*http.Request)
		status               int
		code                 string
	}{
		{name: "wrong method", method: "GET", cli: info.Token, status: 405, code: "INVALID_INPUT"},
		{name: "query", suffix: "?extra=1", body: body, cli: info.Token, status: 400, code: "INVALID_INPUT"},
		{name: "unexpected target", body: extra, cli: info.Token, status: 400, code: "INVALID_INPUT"},
		{name: "wrong incarnation", body: nativeRecoverBody(t, uuid(), configured.ControlGeneration), cli: info.Token, status: 403, code: "FORBIDDEN"},
		{name: "anonymous", body: body, status: 401, code: "UNAUTHENTICATED"},
		{name: "foreign origin", body: body, browser: cookie, change: func(q *http.Request) { q.Header.Set("Origin", "http://127.0.0.1:1") }, status: 403, code: "FORBIDDEN"},
		{name: "missing origin", body: body, browser: cookie, change: func(q *http.Request) { q.Header.Del("Origin") }, status: 403, code: "FORBIDDEN"},
		{name: "stale generation", body: initialBody, cli: info.Token, status: 409, code: "CONTROL_STALE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			method := c.method
			if method == "" {
				method = "POST"
			}
			out := request(t, r, method, path+c.suffix, c.body, c.cli, c.browser, c.change)
			assertActivityRefusal(t, out.status, out.b, c.status, c.code)
		})
	}
	configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.BotGrant)
	revoked := request(t, r, "POST", path, body, info.Token, nil, nil)
	assertActivityRefusal(t, revoked.status, revoked.b, 401, "UNAUTHENTICATED")
	configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=1 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.BotGrant)
	configuredGrantSQL(t, r, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", r.id.Incarnation, r.id.BotGrant)
	denied := request(t, r, "POST", path, body, info.Token, nil, nil)
	assertActivityRefusal(t, denied.status, denied.b, 403, "FORBIDDEN")
	stopped := nativeRecoveryView(t, request(t, r, "POST", path, body, "", cookie, nil), info)
	if stopped.State != "stopped" || stopped.TargetID == nil || A.UUID(*stopped.TargetID) != target ||
		stopped.Pending != nil || !bytes.Equal(before, noticeTestBytes(t, statePath)) {
		t.Fatal("browser recovery of a clean target changed durable evidence")
	}
	start := request(t, r, "POST", r.boardPath()+"notices/start", nativeRecoverBody(t, info.Incarnation, stopped.ControlGeneration), "", cookie, nil)
	running := nativeRecoveryView(t, start, info)
	if running.State != "running" {
		t.Fatal("fixture did not start the monitor")
	}
	busy := request(t, r, "POST", path, nativeRecoverBody(t, info.Incarnation, running.ControlGeneration), "", cookie, nil)
	assertActivityRefusal(t, busy.status, busy.b, 409, "CONTROL_BUSY")
}

// Quiesce the synthetic board's monitor and materialize an interrupted
// snapshot with its native writer while the engine still owns the root.
func seedNativeInterruptedRecovery(t *testing.T, frozen bool) (*Runtime, string, ClientInfo) {
	t.Helper()
	r, root := startNativeNoticeTestBoard(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	configureNativeNotices(t, r, info)
	if err := r.notices.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, noticeStateName)
	s, err := notices.Open(r.noticeState.binding, r.noticeState.read, r.noticeState.write)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRun(); err != nil {
		t.Fatal(err)
	}
	if frozen {
		upper := s.Snapshot().Checkpoint
		if err := s.Observe(upper.At); err != nil {
			t.Fatal(err)
		}
		zero := A.Number(0)
		if _, err := s.Prepare(noticeinterval.Evidence{Class: "observed-empty", Upper: &upper, Count: &zero}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r = reopenNoticeTestBoard(t, root)
	info, err = ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	return r, path, info
}

func TestNativeNoticeRecoveryRequiresReadableCoverage(t *testing.T) {
	for _, f := range []struct {
		name   string
		frozen bool
	}{
		{name: "dirty idle"}, {name: "frozen pending", frozen: true},
	} {
		for _, fault := range []string{"malformed floor", "unreadable gap watermark"} {
			t.Run(f.name+"/"+fault, func(t *testing.T) {
				r, path, info := seedNativeInterruptedRecovery(t, f.frozen)
				before := noticeTestBytes(t, path)
				nativeRecoveryCoverageFault(t, r, fault, true)
				read := func() T.NoticeView {
					return nativeRecoveryView(t, request(t, r, "GET", r.boardPath()+"notices/status?incarnation="+info.Incarnation, nil, info.Token, nil, nil), info)
				}
				recover := func(v T.NoticeView) response {
					return request(t, r, "POST", r.boardPath()+"notices/recover", nativeRecoverBody(t, info.Incarnation, v.ControlGeneration), info.Token, nil, nil)
				}
				prior := read()
				if prior.State != "guarded" || (prior.Pending != nil) != f.frozen {
					t.Fatal("fixture did not reopen the intended interrupted state")
				}
				out := recover(prior)
				v := nativeRecoveryView(t, out, info)
				if v.State != "guarded" || v.Reason == nil || *v.Reason != "unavailable" || v.TargetID == nil || *v.TargetID != *prior.TargetID ||
					v.Checkpoint == nil || *v.Checkpoint != *prior.Checkpoint || (v.Pending != nil) != f.frozen ||
					(v.Pending != nil && (v.Pending.DeliveryID == nil || *v.Pending.DeliveryID != *prior.Pending.DeliveryID)) {
					t.Fatal("coverage failure did not retain the guarded evidence")
				}
				if !bytes.Equal(before, noticeTestBytes(t, path)) || len(r.notices.Inbox()) != 0 {
					t.Fatal("recovery wrote with unproved coverage lineage")
				}
				nativeRecoveryCoverageFault(t, r, fault, false)
				settled := nativeRecoveryView(t, recover(read()), info)
				if settled.State != "stopped" || settled.Pending != nil || settled.Checkpoint == nil ||
					*settled.Checkpoint != *prior.Checkpoint || settled.ControlGeneration == prior.ControlGeneration ||
					(settled.Last != nil) != f.frozen {
					t.Fatal("valid lineage did not settle the same interrupted state")
				}
				if f.frozen && (settled.Last.DeliveryID == nil || *settled.Last.DeliveryID != *prior.Pending.DeliveryID) {
					t.Fatal("recovery changed the immutable frozen delivery ID")
				}
				confirmed := noticeTestBytes(t, path)
				if repeated := nativeRecoveryView(t, recover(settled), info); repeated.State != "stopped" ||
					!bytes.Equal(confirmed, noticeTestBytes(t, path)) {
					t.Fatal("repeated recovery changed a settled board")
				}
				pairs := r.notices.Inbox()
				if (f.frozen && (len(pairs) != 1 || pairs[0].DeliveryID != string(*settled.Last.DeliveryID))) ||
					(!f.frozen && len(pairs) != 0) {
					t.Fatal("recovery did not retain exactly the eligible acceptance")
				}
			})
		}
	}
}

func nativeRecoveryCoverageFault(t *testing.T, r *Runtime, fault string, set bool) {
	t.Helper()
	ctx := context.Background()
	lease, err := r.store.Writer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(ctx, func(c *sql.Conn) error {
		var queries []string
		switch {
		case fault == "malformed floor" && set:
			queries = []string{"PRAGMA ignore_check_constraints=ON", "UPDATE board SET activity_floor='invalid' WHERE singleton=1", "PRAGMA ignore_check_constraints=OFF"}
		case fault == "malformed floor":
			queries = []string{"UPDATE board SET activity_floor=NULL WHERE singleton=1"}
		case set:
			queries = []string{"ALTER TABLE coverage_gap RENAME TO coverage_gap_fixture"}
		default:
			queries = []string{"ALTER TABLE coverage_gap_fixture RENAME TO coverage_gap"}
		}
		for _, query := range queries {
			if _, err := c.ExecContext(ctx, query); err != nil {
				return err
			}
		}
		return nil
	})
	if err := errors.Join(err, lease.Close()); err != nil {
		t.Fatal(err)
	}
}
