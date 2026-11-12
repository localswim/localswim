package v2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestM35BoardPublicationResumesExactPendingCommit(t *testing.T) {
	fixture := m34PublicationFixture(t)
	original := fixture.state.Board
	path := "/boards/" + original.ID
	editor := fixture.editor
	raw, _ := editor.call("GET", path, "", "", 200)
	disabled := turnOneDecode[board](t, raw)
	if disabled.Publishing != (publishing{}) {
		t.Fatalf("board defaults are not disabled: %+v", disabled.Publishing)
	}
	for _, body := range []string{
		`{"publishing":{}}`,
		`{"publishing":null}`,
		`{"publishing":{"enabled":null}}`,
		`{"publishing":{"repository":"https://unexpected.invalid"}}`,
		`{"publishing":{"enabled":true,"pending":false}}`,
	} {
		editor.call("PATCH", path, body, disabled.entityTag(), 400)
	}
	editor.call("PATCH", path, `{"publishing":{"enabled":true}}`, etag(original.ID, 1), 412)

	m34Write(t, filepath.Join(fixture.repository, "staged.txt"), []byte("unrelated staged content\n"))
	m34Git(t, fixture.repository, "add", "--", "staged.txt")
	staged := m34Git(t, fixture.repository, "ls-files", "--stage", "-z", "--", "staged.txt")
	baseline := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", "HEAD")))
	offline := fixture.remote + "-offline"
	if err := os.Rename(fixture.remote, offline); err != nil {
		t.Fatal(err)
	}

	raw, _ = editor.call("PATCH", path, `{"publishing":{"enabled":true,"repository":"origin"}}`, disabled.entityTag(), 200)
	enabled := turnOneDecode[board](t, raw)
	if !enabled.Publishing.Enabled || !enabled.Publishing.Pending || snapshotString(enabled.Publishing.Repository) != "origin" {
		t.Fatalf("enable did not schedule publication: %+v", enabled.Publishing)
	}
	start := time.Now()
	raw, _ = editor.call("PATCH", path, `{"name":"Board edit during quiet"}`, enabled.entityTag(), 200)
	edited := turnOneDecode[board](t, raw)
	if got := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", "HEAD"))); got != baseline {
		t.Fatal("publication ran before quiet")
	}
	failed := waitBoardPublishing(t, editor, path, func(info publishing) bool {
		return info.Pending && snapshotString(info.ErrorCode) == "git_push_failed"
	})
	if failed.entityTag() != edited.entityTag() || failed.Publishing.LastSuccessAt != nil || time.Since(start) < automaticPublicationQuiet {
		t.Fatal("failed status advanced the revision or ran before quiet")
	}
	checkEventConditionalGet(t, editor, failed, failed.entityTag(), http.StatusOK)
	checkEventConditionalGet(t, editor, failed, "*", http.StatusNotModified)
	pending := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", "HEAD")))
	if pending == baseline {
		t.Fatal("failed push did not retain its accepted local commit")
	}
	if got := strings.TrimSpace(string(m34Git(t, offline, "rev-parse", "refs/heads/main"))); got != baseline {
		t.Fatal("unavailable origin unexpectedly advanced")
	}
	durable, err := os.ReadFile(fixture.app.store.snapshotPath(original.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.app.Close(); err != nil {
		t.Fatal(err)
	}

	app, restarted := restartBoardPublication(t, fixture.app.store.root, editor.token)
	if err := os.Rename(offline, fixture.remote); err != nil {
		t.Fatal(err)
	}
	recovered := waitBoardPublishing(t, restarted, path, func(info publishing) bool {
		return info.LastSuccessAt != nil && !info.Pending && info.ErrorCode == nil
	})
	if recovered.entityTag() != edited.entityTag() || recovered.Revision != edited.Revision {
		t.Fatal("push recovery rewrote the board revision")
	}
	checkEventConditionalGet(t, restarted, recovered, failed.entityTag(), http.StatusOK)
	if got := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", "refs/heads/main"))); got != pending {
		t.Fatal("retry pushed a different commit")
	}
	if got := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", "HEAD"))); got != pending {
		t.Fatal("retry created another commit")
	}
	fixture.contents["boards/"+original.ID+".json"] = durable
	for artifact, expected := range fixture.contents {
		got := m34Git(t, fixture.remote, "show", pending+":"+fixture.prefix+"/"+artifact)
		if !bytes.Equal(got, expected) {
			t.Fatalf("published bytes differ for %s", artifact)
		}
	}
	if names := m34Git(t, fixture.repository, "diff", "--cached", "--name-only", "-z"); string(names) != "staged.txt\x00" {
		t.Fatalf("automatic publication left unexpected staged paths: %q", names)
	}
	if after := m34Git(t, fixture.repository, "ls-files", "--stage", "-z", "--", "staged.txt"); !bytes.Equal(staged, after) {
		t.Fatal("automatic publication changed unrelated staged work")
	}
	stored, err := app.store.durable(original.ID)
	if err != nil || stored.Board.Publishing.Pending || stored.Board.Publishing.ErrorCode != nil || stored.Board.Publishing.LastSuccessAt != nil {
		t.Fatal("worker status entered the durable snapshot", err)
	}
	rejected, _ := restarted.call("POST", "/restorations", fmt.Sprintf(`{"board_id":%q,"backup_id":%q,"expected_board_etag":%q}`,
		original.ID, fixture.item.ID, recovered.entityTag()), "", 400)
	if !bytes.Contains(rejected, []byte("board.publishing")) {
		t.Fatal("restore did not report the publishing mismatch")
	}
	restarted.call("PATCH", path, `{"publishing":{"enabled":false}}`, recovered.entityTag(), 200)
}

func waitBoardPublishing(t *testing.T, editor turnOneClient, path string, ready func(publishing) bool) board {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var state board
	for time.Now().Before(deadline) {
		raw, _ := editor.call("GET", path, "", "", 200)
		state = turnOneDecode[board](t, raw)
		if ready(state.Publishing) {
			return state
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("board publishing did not reach expected state (pending=%t, code=%q)", state.Publishing.Pending, snapshotString(state.Publishing.ErrorCode))
	return board{}
}

func restartBoardPublication(t *testing.T, root, human string) (*Service, turnOneClient) {
	t.Helper()
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(func() {
		server.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	return app, turnOneClient{t, server.URL, human, server.Client()}
}

func TestM35PendingPublicationSettlesAfterAnotherPublisherPushesDescendant(t *testing.T) {
	fixture := m34PublicationFixture(t)
	root := fixture.app.store.root
	repo, err := lockPublication(root)
	if err != nil {
		t.Fatal(err)
	}
	defer releasePublicationRepository(repo.lease)
	ref, previous, err := repo.head()
	if err != nil {
		t.Fatal(err)
	}
	commit, names, err := repo.preparePublication(ref, previous, m35Capture("first"))
	if err != nil || commit == previous {
		t.Fatal("failed to prepare first publication", err)
	}
	selection := publicationSelection{branch: "descendant"}
	destination, err := repo.resolveDestination(ref, selection)
	if err != nil {
		t.Fatal(err)
	}
	firstRef := publicationCheckpointRef(root, "first")
	intent := publicationCheckpoint{Revision: 1, Pending: &pendingPublication{
		Commit: commit, Parent: previous, SourceRef: ref, Target: destination.target, Names: names,
	}}
	// Simulate an interruption after the journal was installed but before the branch CAS.
	if _, err := repo.saveCheckpoint(firstRef, "", intent); err != nil {
		t.Fatal(err)
	}
	m34Git(t, fixture.repository, "gc", "--prune=now")
	offline := fixture.remote + "-offline"
	if err := os.Rename(fixture.remote, offline); err != nil {
		t.Fatal(err)
	}
	loaded, oid, err := repo.checkpoint(firstRef)
	if err != nil || loaded.Pending == nil || loaded.Pending.Commit != commit {
		t.Fatal("pending publication was not recoverable", err)
	}
	if _, err := repo.finishPending(firstRef, oid, loaded, selection); err != publicationGitError("git_push_failed") {
		t.Fatal("offline exact push did not fail as expected", err)
	}
	if err := os.Rename(offline, fixture.remote); err != nil {
		t.Fatal(err)
	}
	secondRef := publicationCheckpointRef(root, "second")
	second, err := repo.acceptPublication(secondRef, "", publicationCheckpoint{}, m35Capture("second"), selection, func() bool { return true })
	if err != nil || second.LastSuccessAt == nil || second.Pending != nil {
		t.Fatal("second publisher could not push a descendant", err)
	}
	descendant := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", destination.target.Ref)))
	if descendant == commit || !repo.containsCommit(commit, descendant) {
		t.Fatal("second publication did not push a descendant")
	}
	// The local branch can still name the pending commit even when the
	// descendant object is present locally and already accepted by origin.
	m34Git(t, fixture.repository, "update-ref", ref, commit, descendant)
	settled, err := repo.finishPending(firstRef, oid, loaded, selection)
	if err != nil || settled.LastSuccessAt == nil || settled.Pending != nil {
		t.Fatal("first publisher failed to settle after the remote advanced", err)
	}
	if got := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", destination.target.Ref))); got != descendant {
		t.Fatal("first publisher reverted the remote")
	}
	if got := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", ref))); got != commit {
		t.Fatal("settling remote proof moved the local branch")
	}
}

func m35Capture(name string) publicationCapture {
	return publicationCapture{revision: 1, message: "Publication test " + name, files: []publicationFile{{
		path: name + ".txt",
		read: func() ([]byte, error) { return []byte(name), nil },
	}}}
}

func commitPublicationFixture(t *testing.T, repo publicationRepository, ref, previous string, capture publicationCapture) string {
	t.Helper()
	commit, names, err := repo.preparePublication(ref, previous, capture)
	if err != nil || commit == previous {
		t.Fatal("failed to prepare fixture commit", err)
	}
	if err := repo.advancePublication(ref, previous, commit, names); err != nil {
		t.Fatal("failed to advance fixture branch", err)
	}
	return commit
}

func TestM35PinnedTransportIgnoresRetargetAndSecondRewrite(t *testing.T) {
	fixture := m34PublicationFixture(t)
	repo, err := lockPublication(fixture.app.store.root)
	if err != nil {
		t.Fatal(err)
	}
	defer releasePublicationRepository(repo.lease)
	ref, previous, err := repo.head()
	if err != nil {
		t.Fatal(err)
	}
	commit, _, err := repo.preparePublication(ref, previous, m35Capture("pinned"))
	if err != nil || commit == previous {
		t.Fatal("failed to prepare pinned publication", err)
	}
	target, err := repo.resolvedPushURL("origin")
	if err != nil {
		t.Fatal(err)
	}

	other := t.TempDir()
	m34Git(t, other, "init", "--bare", "--initial-branch=main")
	m34Git(t, fixture.repository, "push", "--", other, previous+":"+ref)
	// The worker already read and checked target. Exercise both a changed
	// origin alias and a new rewrite that would affect a literal URL push.
	m34Git(t, fixture.repository, "config", "remote.origin.pushurl", other)
	m34Git(t, fixture.repository, "config", "url."+other+".insteadOf", target)
	remote := pinnedPublicationRemote(target)
	if err := repo.push(remote, commit, commit, ref); err != nil {
		t.Fatal("pinned transport failed", err)
	}
	if got := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", ref))); got != commit {
		t.Fatal("checked target did not receive the commit")
	}
	if got := strings.TrimSpace(string(m34Git(t, other, "rev-parse", ref))); got != previous {
		t.Fatal("retargeted origin received captured data")
	}
	if !repo.remoteContains(remote, pendingPublication{Commit: commit, Target: publicationTarget{Ref: ref}}) {
		t.Fatal("remote confirmation did not inspect the checked target")
	}
}

func TestM35NoDeltaCaptureConfirmsRemoteBeforeSuccess(t *testing.T) {
	fixture := m34PublicationFixture(t)
	root := fixture.app.store.root
	repo, err := lockPublication(root)
	if err != nil {
		t.Fatal(err)
	}
	defer releasePublicationRepository(repo.lease)
	ref, previous, err := repo.head()
	if err != nil {
		t.Fatal(err)
	}
	capture := m35Capture("already-committed")
	commit := commitPublicationFixture(t, repo, ref, previous, capture)
	offline := fixture.remote + "-offline"
	if err := os.Rename(fixture.remote, offline); err != nil {
		t.Fatal(err)
	}
	checkpointRef := publicationCheckpointRef(root, "no-delta")
	pending, err := repo.acceptPublication(checkpointRef, "", publicationCheckpoint{}, capture, publicationSelection{}, func() bool { return true })
	if err != publicationGitError("git_push_failed") || pending.LastSuccessAt != nil ||
		pending.Pending == nil || pending.Pending.Commit != commit || pending.Pending.Parent != commit {
		t.Fatal("no-delta capture settled without remote proof", err)
	}
	loaded, oid, err := repo.checkpoint(checkpointRef)
	if err != nil || loaded.Pending == nil || loaded.Pending.Commit != commit {
		t.Fatal("no-delta capture failed to persist the pending commit", err)
	}
	if err := os.Rename(offline, fixture.remote); err != nil {
		t.Fatal(err)
	}
	finished, err := repo.finishPending(checkpointRef, oid, loaded, publicationSelection{})
	if err != nil || finished.Pending != nil || finished.LastSuccessAt == nil {
		t.Fatal("no-delta capture did not finish after origin recovered", err)
	}
	for _, directory := range []string{fixture.repository, fixture.remote} {
		if got := strings.TrimSpace(string(m34Git(t, directory, "rev-parse", ref))); got != commit {
			t.Fatal("no-delta publication changed the commit identity")
		}
	}
}

func TestM35ExactCommitIgnoresReplacementObjects(t *testing.T) {
	fixture := m34PublicationFixture(t)
	repo, err := lockPublication(fixture.app.store.root)
	if err != nil {
		t.Fatal(err)
	}
	defer releasePublicationRepository(repo.lease)
	ref, previous, err := repo.head()
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(string(m34Git(t, fixture.repository, "mktree")))
	replacement := strings.TrimSpace(string(m34Git(t, fixture.repository, "commit-tree", tree, "-m", "Replacement empty tree")))
	m34Git(t, fixture.repository, "replace", previous, replacement)
	if got := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", previous+"^{tree}"))); got != tree {
		t.Fatal("fixture did not activate the replacement object")
	}
	commit, _, err := repo.preparePublication(ref, previous, m35Capture("replacement-proof"))
	if err != nil || commit == previous {
		t.Fatal("failed to prepare exact commit", err)
	}
	if got := string(m34Git(t, fixture.repository, "show", commit+":unchanged.txt")); got != "original unchanged.txt\n" {
		t.Fatal("replacement objects changed the inherited tree")
	}
}

func TestM35RefusesExplicitOriginTransportBeforeCaptureAndRetry(t *testing.T) {
	fixture := m34PublicationFixture(t)
	root := fixture.app.store.root
	repo, err := lockPublication(root)
	if err != nil {
		t.Fatal(err)
	}
	defer releasePublicationRepository(repo.lease)
	ref, previous, err := repo.head()
	if err != nil {
		t.Fatal(err)
	}
	checkpointRef := publicationCheckpointRef(root, "unsupported-transport")
	capture := m35Capture("unsupported-transport")
	read := capture.files[0].read
	readAttempted := false
	capture.files[0].read = func() ([]byte, error) {
		readAttempted = true
		return read()
	}

	// An explicit empty proxy selects direct routing and must not be ignored.
	m34Git(t, fixture.repository, "config", "remote.origin.proxy", "")
	_, err = repo.acceptPublication(checkpointRef, "", publicationCheckpoint{}, capture, publicationSelection{}, func() bool { return true })
	if err != publicationGitError("unsupported_remote_transport") || readAttempted {
		t.Fatal("explicit direct routing did not refuse before capture preparation", err)
	}
	if _, oid, err := repo.checkpoint(checkpointRef); err != nil || oid != "" {
		t.Fatal("refused origin routing reserved a checkpoint", err)
	}
	for _, directory := range []string{fixture.repository, fixture.remote} {
		if got := strings.TrimSpace(string(m34Git(t, directory, "rev-parse", ref))); got != previous {
			t.Fatal("refused origin routing moved a branch")
		}
	}

	m34Git(t, fixture.repository, "config", "--unset", "remote.origin.proxy")
	offline := fixture.remote + "-offline"
	if err := os.Rename(fixture.remote, offline); err != nil {
		t.Fatal(err)
	}
	_, err = repo.acceptPublication(checkpointRef, "", publicationCheckpoint{}, capture, publicationSelection{}, func() bool { return true })
	if err != publicationGitError("git_push_failed") {
		t.Fatal("expected an ordinary pending push before transport changed", err)
	}
	pending, oid, err := repo.checkpoint(checkpointRef)
	if err != nil || pending.Pending == nil {
		t.Fatal("failed push did not leave a pending checkpoint", err)
	}
	if err := os.Rename(offline, fixture.remote); err != nil {
		t.Fatal(err)
	}
	m34Git(t, fixture.repository, "config", "remote.origin.receivepack", "custom-receive-pack")
	_, err = repo.finishPending(checkpointRef, oid, pending, publicationSelection{})
	if err != publicationGitError("unsupported_remote_transport") {
		t.Fatal("pending push ignored a configured receive-pack command", err)
	}
	if _, after, err := repo.checkpoint(checkpointRef); err != nil || after != oid {
		t.Fatal("refused retry replaced the pending checkpoint", err)
	}
	if got := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", ref))); got != previous {
		t.Fatal("refused retry moved the remote branch")
	}
	if got := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", ref))); got != pending.Pending.Commit {
		t.Fatal("refused retry changed the pending local commit")
	}
}
