//go:build windows && amd64

package local

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

func TestNativeNoticeAuthorityDoesNotRefreshBrowserIdle(t *testing.T) {
	r, _ := startNativeNoticeTestBoard(t)
	token, err := r.auth.browser()
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte(token))
	idle := time.Now().UTC().Add(5 * time.Minute)
	r.auth.mu.Lock()
	credential := r.auth.credentials[key]
	credential.IdleExpiresAt = idle
	r.auth.credentials[key] = credential
	r.auth.mu.Unlock()
	ctx := context.Background()
	authorize := r.noticeAuthority("browser", token)
	if err := r.checkNoticeAuthority(ctx, authorize, nil); err != nil {
		t.Fatal(err)
	}
	r.auth.mu.Lock()
	idleAfter := r.auth.credentials[key].IdleExpiresAt
	r.auth.mu.Unlock()
	if idleAfter != idle {
		t.Fatal("background read extended the browser session")
	}
	if err := r.checkNoticeAuthority(ctx, r.noticeAuthority("cli", token), nil); err == nil {
		t.Fatal("browser credential admitted in CLI mode")
	}
	configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.HumanGrant)
	if err := r.checkNoticeAuthority(ctx, authorize, nil); !errors.Is(err, grants.ErrDenied) {
		t.Fatal("revoked grant retained background read", err)
	}
}

func TestNativeNoticeSignalTracksDBOnly(t *testing.T) {
	r, root := startNativeNoticeTestBoard(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, noticeStateName)
	if _, err := os.Lstat(state); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("opening an unconfigured engine allocated notice state")
	}
	dir, key := filepath.Join(root, "board"), r.noticeState.binding.DatabaseKey
	sample := func() string {
		t.Helper()
		signature, err := noticeNativeSignal(dir, key)
		if err != nil {
			t.Fatal(err)
		}
		return signature
	}
	initial := sample()
	if out := request(t, r, "GET", "/api/v1/boards/"+info.BoardID, nil, info.Token, nil, nil); out.status != 200 {
		t.Fatal("ordinary board read failed")
	}
	configured := configureNativeNotices(t, r, info)
	if configured == "" {
		t.Fatal("configuration has no target")
	}
	service := filepath.Join(root, "service.json")
	metadata := noticeTestBytes(t, service)
	if err := publish(service, metadata, true); err != nil {
		t.Fatal(err)
	}
	if sample() != initial {
		t.Fatal("ordinary reads, private notice state or service publication woke the source")
	}
	configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.HumanGrant)
	if after := sample(); after == initial || sample() != after {
		t.Fatal("real database-only commit did not produce one stable changed signature")
	}
	if _, err := noticeNativeSignal(dir, r.noticeState.binding.RootKey); !errors.Is(err, notices.ErrForeign) {
		t.Fatal("sample accepted an unrelated file identity", err)
	}
}

func TestNativeNoticeStateBoundToPinnedDatabase(t *testing.T) {
	r, root := startNativeNoticeTestBoard(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	target := configureNativeNotices(t, r, info)
	generation := r.notices.Snapshot().Generation
	p := filepath.Join(root, noticeStateName)
	original := noticeTestBytes(t, p)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Storage(context.Background(), root, schema.Identity{BoardID: info.BoardID, Incarnation: info.Incarnation}, false); err != nil {
		t.Fatal("offline board inspection refused the reserved private file", err)
	}
	reopened := reopenNoticeTestBoard(t, root)
	status := reopened.notices.Snapshot()
	if status.Generation == generation || status.State == nil || status.State.TargetID != target {
		t.Fatal("reopen lost the target or reused a control generation")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	// Identical SQLite bytes, on the same root, with a different native file
	// identity. Installation, board, incarnation and revision are identical.
	db := filepath.Join(root, "board", "board.sqlite")
	if err := publish(db, noticeTestBytes(t, db), true); err != nil {
		t.Fatal(err)
	}
	foreign := reopenNoticeTestBoard(t, root)
	if foreign.notices.Snapshot().Reason != "foreign" || !bytes.Equal(original, noticeTestBytes(t, p)) {
		t.Fatal("replacement database adopted or changed retained notice evidence")
	}
	current, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := foreign.notices.Check(context.Background(), foreign.noticeAuthority("cli", current.Token)); err != nil {
		t.Fatal("authorized operator cannot inspect unavailable notice status", err)
	}
	if out := request(t, foreign, "GET", "/api/v1/boards/"+info.BoardID, nil, current.Token, nil, nil); out.status != 200 {
		t.Fatal("foreign notice state disabled ordinary board use")
	}
}

func TestNativeNoticeRegressedCounterRefusesRetainedState(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "checkpoint"
		if pending {
			name = "pending upper"
		}
		t.Run(name, func(t *testing.T) {
			r, root := startNativeNoticeTestBoard(t)
			info, err := ReadClient(root)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			db := filepath.Join(root, "board", "board.sqlite")
			oldDB := noticeTestBytes(t, db)
			auth := r.noticeAuthority("cli", info.Token)
			var target A.UUID
			if pending {
				target = configureNativeNotices(t, r, info)
				controlled := func(ctx context.Context, rd writer.Read) error {
					// Keep accepted proof durable but force the original
					// worker to stop before its final disposition.
					if len(r.notices.Inbox()) != 0 {
						return grants.ErrDenied
					}
					return auth(ctx, rd)
				}
				if err := r.notices.Start(ctx, r.notices.Snapshot().Generation, controlled); err != nil {
					t.Fatal(err)
				}
			}
			cookie := bootstrapTest(t, r)
			term(t, request(t, r, "POST", r.boardPath()+"cards", mutationWire(t, r, "", 0, 0,
				map[string]any{"title": "Synthetic counter rollback", "description": ""}), "", cookie, nil), "committed")
			var deliveryID string
			if pending {
				until := time.Now().Add(7 * time.Second)
				for r.notices.Snapshot().Reason != "authority" {
					if time.Now().After(until) {
						t.Fatal("controlled authority fault did not leave pending proof")
					}
					time.Sleep(25 * time.Millisecond)
				}
				state := r.notices.Snapshot().State
				if state.Checkpoint.Event != 0 || state.Pending == nil || state.Pending.Upper == nil || state.Pending.Upper.Event != 1 || len(r.notices.Inbox()) != 1 {
					t.Fatal("fixture needs accepted proof strictly above the checkpoint")
				}
				deliveryID = state.Pending.DeliveryID
			} else {
				configureNativeNotices(t, r, info)
				if r.notices.Snapshot().State.Checkpoint.Event != 1 {
					t.Fatal("fixture needs a positive retained checkpoint")
				}
			}
			statePath := filepath.Join(root, noticeStateName)
			sidecar := noticeTestBytes(t, statePath)
			reopened := reopenWithRolledBackNoticeDB(t, r, root, oldDB)
			info, err = ReadClient(root)
			if err != nil {
				t.Fatal(err)
			}
			auth = reopened.noticeAuthority("cli", info.Token)
			if err := reopened.notices.Check(ctx, reopened.noticeAuthority("cli", "")); errors.Is(err, notices.ErrForeign) || err == nil {
				t.Fatal("failed authority disclosed notice continuity", err)
			}
			if err := reopened.notices.Check(ctx, auth); !errors.Is(err, notices.ErrForeign) {
				t.Fatal("regressed counters admitted cached status", err)
			}
			if pending {
				if pair, err := reopened.notices.Lookup(ctx, target, deliveryID, auth); pair != nil || !errors.Is(err, notices.ErrForeign) {
					t.Fatal("regressed counters admitted original receipt", err)
				}
			}
			if !bytes.Equal(sidecar, noticeTestBytes(t, statePath)) {
				t.Fatal("foreign source changed retained evidence")
			}
		})
	}
}

func TestNativeNoticeRegressedCoverageRefusesRetainedState(t *testing.T) {
	for _, class := range []string{"floor", "watermark"} {
		t.Run(class, func(t *testing.T) {
			r, root := startNativeNoticeTestBoard(t)
			info, err := ReadClient(root)
			if err != nil {
				t.Fatal(err)
			}
			oldDB := noticeTestBytes(t, filepath.Join(root, "board", "board.sqlite"))
			const at = "2026-01-01T00:00:00Z"
			switch class {
			case "floor":
				configuredGrantSQL(t, r, "UPDATE board SET activity_floor=? WHERE singleton=1", at)
			case "watermark":
				configuredGrantSQL(t, r, "INSERT INTO coverage_gap (incarnation,origin_board,origin_incarnation,class,reason,through_inclusive,recorded_at,board_revision) VALUES (?,?,?,'activity','cleared',?,?,0)",
					r.id.Incarnation, r.id.BoardID, r.id.Incarnation, at, at)
			}
			configureNativeNotices(t, r, info)
			reopened := reopenWithRolledBackNoticeDB(t, r, root, oldDB)
			info, err = ReadClient(root)
			if err != nil {
				t.Fatal(err)
			}
			auth := reopened.noticeAuthority("cli", info.Token)
			if err := reopened.notices.Check(context.Background(), auth); !errors.Is(err, notices.ErrForeign) {
				t.Fatal("regressed coverage admitted retained state", err)
			}
		})
	}
}

func TestNativeNoticeArchiveRestoreUsesSeparateTarget(t *testing.T) {
	r, root := startNativeNoticeTestBoard(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	target := configureNativeNotices(t, r, info)
	auth := r.noticeAuthority("cli", info.Token)
	if err := r.notices.Start(context.Background(), r.notices.Snapshot().Generation, auth); err != nil {
		t.Fatal(err)
	}
	cookie := bootstrapTest(t, r)
	term(t, request(t, r, "POST", r.boardPath()+"cards", mutationWire(t, r, "", 0, 0,
		map[string]any{"title": "Synthetic restored activity", "description": ""}), "", cookie, nil), "committed")
	until := time.Now().Add(7 * time.Second)
	for r.notices.Snapshot().State.Checkpoint.Event != 1 {
		if time.Now().After(until) {
			t.Fatal("source notice did not finalize")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := r.notices.Stop(context.Background(), r.notices.Snapshot().Generation, auth); err != nil {
		t.Fatal(err)
	}
	pairs := r.notices.Inbox()
	if len(pairs) != 1 || pairs[0].Class != notices.Human {
		t.Fatal("archive fixture did not produce one source notice and receipt")
	}
	body, err := json.Marshal(map[string]any{"incarnation": info.Incarnation, "expectedRevision": 1, "input": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	out := request(t, r, "POST", r.boardPath()+"exports", body, info.Token, nil, nil)
	if out.status != 200 {
		t.Fatalf("export failed with HTTP %d", out.status)
	}
	z, err := zip.NewReader(bytes.NewReader(out.b), int64(len(out.b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range z.File {
		f, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(f)
		if err := errors.Join(err, f.Close()); err != nil {
			t.Fatal(err)
		}
		if entry.Name == noticeStateName || bytes.Contains(data, []byte(target)) || bytes.Contains(data, []byte(pairs[0].DeliveryID)) {
			t.Fatal("export included source notice state")
		}
	}
	parent := filepath.Dir(root)
	source := filepath.Join(root, "cli-evidence", "synthetic-notice-export.zip")
	if err := publish(source, out.b, false); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(parent, "restored")
	result := Restore(context.Background(), RestoreOptions{
		SourceArchive: source, DataDir: dest, RequestID: A.UUID(uuid()), ProtectedRoots: []string{root}, Port: freePort(t),
	})
	if result.Exit != 0 {
		t.Fatal("real restore from the exported board failed", result.Status.Outcome)
	}
	restored := reopenNoticeTestBoard(t, dest)
	destination, err := ReadClient(dest)
	if err != nil {
		t.Fatal(err)
	}
	if restored.notices.Snapshot().State != nil || len(restored.notices.Inbox()) != 0 {
		t.Fatal("restored engine inherited notice state")
	}
	if got := configureNativeNotices(t, restored, destination); got == target || got == "" {
		t.Fatal("restored configuration did not allocate its own target")
	}
	s := restored.notices.Snapshot().State
	if s.Checkpoint.Event != 0 || len(restored.notices.Inbox()) != 0 {
		t.Fatal("inherited M11 activity became new destination notices")
	}
}

func TestNativeNoticeWorkspaceDetachRetainsExclusionDuringDrain(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(nativeNoticeTestParent(t), "member")
	workspaceID := uuid()
	h, err := StartWorkspaceHost(ctx, WorkspaceHostOptions{WorkspaceID: workspaceID, Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	r, err := OpenEngine(ctx, EngineOptions{DataDir: root, WorkspaceID: workspaceID, Origin: h.Origin(), Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := h.Attach(r); err != nil {
		t.Fatal(err)
	}
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	target := configureNativeNotices(t, r, info)
	auth := r.noticeAuthority("cli", info.Token)
	entered, resume := make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(resume) }) }
	defer unblock()
	var calls atomic.Int32
	var started atomic.Bool
	stalled := func(ctx context.Context, rd writer.Read) error {
		// Arm after Start's own phase and response checks have finished.
		// The first later worker snapshot retains the original run authority.
		if started.Load() && calls.Add(1) == 1 {
			close(entered)
			<-resume
		}
		return auth(ctx, rd)
	}
	if err := r.notices.Start(ctx, r.notices.Snapshot().Generation, stalled); err != nil {
		t.Fatal(err)
	}
	started.Store(true)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never entered the controlled native read stall")
	}
	before := time.Now()
	if err := r.notices.Stop(ctx, r.notices.Snapshot().Generation, auth); err != nil {
		t.Fatal(err)
	}
	if time.Since(before) > 3*time.Second || r.notices.Snapshot().Phase != "partial-drain" {
		t.Fatal("stalled worker failed to return a bounded partial drain")
	}
	detached := make(chan error, 1)
	go func() { detached <- h.Detach(r.id.BoardID) }()
	select {
	case err := <-detached:
		t.Fatal("detach released the engine while its worker was stalled", err)
	case <-time.After(100 * time.Millisecond):
	}
	if other, err := Start(ctx, Options{DataDir: root}); err == nil {
		_ = other.Close()
		t.Fatal("standalone engine acquired the workspace member's live root")
	}
	unblock()
	select {
	case err := <-detached:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workspace detach did not finish when the worker quiesced")
	}
	reopened := reopenNoticeTestBoard(t, root)
	if status := reopened.notices.Snapshot(); status.Phase != "stopped" || status.State == nil || status.State.TargetID != target {
		t.Fatal("standalone reopen lost the cleanly drained target")
	}
}

func nativeNoticeTestParent(t *testing.T) string {
	t.Helper()
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Skip("requires explicit private native test parent")
	}
	parent := testRoot(t)
	t.Cleanup(func() {
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	return parent
}

func startNativeNoticeTestBoard(t *testing.T) (*Runtime, string) {
	t.Helper()
	root := filepath.Join(nativeNoticeTestParent(t), "local")
	r, err := Start(context.Background(), Options{DataDir: root, Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r, root
}

func noticeTestBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func reopenNoticeTestBoard(t *testing.T, root string) *Runtime {
	t.Helper()
	r, err := Start(context.Background(), Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func reopenWithRolledBackNoticeDB(t *testing.T, r *Runtime, root string, oldDB []byte) *Runtime {
	t.Helper()
	dbKey := r.noticeState.binding.DatabaseKey
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// Restore only the earlier SQLite bytes in the original native file,
	// retaining its physical identity and private ACL.
	if err := os.WriteFile(filepath.Join(root, "board", "board.sqlite"), oldDB, 0600); err != nil {
		t.Fatal(err)
	}
	reopened := reopenNoticeTestBoard(t, root)
	if reopened.noticeState.binding.DatabaseKey != dbKey {
		t.Fatal("fixture changed the native DB identity")
	}
	return reopened
}

func configureNativeNotices(t *testing.T, r *Runtime, info ClientInfo) A.UUID {
	t.Helper()
	status := r.notices.Snapshot()
	if err := r.notices.Configure(context.Background(), status.Generation, r.noticeAuthority("cli", info.Token)); err != nil {
		t.Fatal("configure selected native notices", err)
	}
	return r.notices.Snapshot().State.TargetID
}
