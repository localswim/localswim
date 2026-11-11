package v2

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// These checks intentionally exercise real disposable repositories and a bare
// origin. They inspect Git independently of the publication command helpers.
func m34Git(t *testing.T, cwd string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = cwd
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Git %s failed: %v\n%s", args[0], err, data)
	}
	return data
}

func m34Write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type m34Fixture struct {
	app        *Service
	editor     turnOneClient
	state      snapshot
	item       backup
	repository string
	remote     string
	prefix     string
	contents   map[string][]byte
}

func m34PublicationFixture(t *testing.T) m34Fixture {
	t.Helper()
	app, editor, state, contents := backupCaptureFixture(t)
	repository := filepath.Dir(app.store.root)
	remote := t.TempDir()
	m34Git(t, repository, "init", "--initial-branch=main")
	m34Git(t, repository, "config", "user.name", "Publication check")
	m34Git(t, repository, "config", "user.email", "publication@example.invalid")
	m34Git(t, repository, "config", "core.autocrlf", "false")
	m34Git(t, repository, "config", "core.fsmonitor", "false")
	m34Git(t, remote, "init", "--bare", "--initial-branch=main")
	m34Git(t, repository, "remote", "add", "origin", remote)
	for _, name := range []string{"unchanged.txt", "modified.txt", "deleted.txt"} {
		m34Write(t, filepath.Join(repository, name), []byte("original "+name+"\n"))
	}
	m34Git(t, repository, "add", "--", "unchanged.txt", "modified.txt", "deleted.txt")
	m34Git(t, repository, "commit", "-m", "Disposable publication baseline")
	m34Git(t, repository, "push", "origin", "HEAD:refs/heads/main")
	capture, err := app.store.captureBackupSnapshot(state.Board.ID, humanID, state.Board.ETag)
	if err != nil {
		t.Fatal(err)
	}
	checked, data, err := capture.verify()
	if err != nil {
		t.Fatal(err)
	}
	item, err := buildBackup(app.store.root, newOperationID(), checked, data)
	if err != nil {
		t.Fatal(err)
	}
	contents["boards/"+state.Board.ID+".json"] = data
	prefix, err := filepath.Rel(repository, app.store.root)
	if err != nil {
		t.Fatal(err)
	}
	return m34Fixture{app, editor, state, item, repository, remote, filepath.ToSlash(prefix), contents}
}

func TestM34ChecksCapturedInventoryAndStagedWorkSurvive(t *testing.T) {
	fixture := m34PublicationFixture(t)
	root := fixture.app.store.root
	c := fixture.state.Cards[0]
	raw, _ := fixture.editor.call("PATCH", "/cards/"+c.ID, `{"title":"Later live title"}`, c.entityTag(), 200)
	c = turnOneDecode[card](t, raw)
	attachmentHTTPUpload(t, fixture.editor, []attachmentFormField{
		{"card_id", c.ID}, {"expected_card_etag", c.entityTag()},
	}, "later.txt", []byte("later live attachment"), 201)
	fixture.editor.call("POST", "/boards", `{"name":"Uncaptured board"}`, "", 201)
	m34Write(t, filepath.Join(root, "attachments", "uncaptured.txt"), []byte("not audited"))
	live, err := os.ReadFile(fixture.app.store.snapshotPath(fixture.state.Board.ID))
	if err != nil {
		t.Fatal(err)
	}
	m34Write(t, filepath.Join(fixture.repository, "added.txt"), []byte("staged addition\n"))
	m34Write(t, filepath.Join(fixture.repository, "modified.txt"), []byte("staged modification\n"))
	if err := os.Remove(filepath.Join(fixture.repository, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	unrelated := []string{"added.txt", "modified.txt", "deleted.txt"}
	m34Git(t, fixture.repository, append([]string{"add", "--"}, unrelated...)...)
	beforeDiff := m34Git(t, fixture.repository, append([]string{"diff", "--cached", "--binary", "--"}, unrelated...)...)
	indexPath := filepath.Join(fixture.repository, ".git", "index")
	beforeIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	previous := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", "HEAD")))
	m34Write(t, filepath.Join(fixture.repository, ".gitattributes"), []byte("* text eol=crlf\n"))
	m34Git(t, fixture.repository, "config", "core.autocrlf", "true")
	if err := publishVerifiedBackup(root, fixture.item); err != nil {
		t.Fatal(err)
	}
	afterIndex, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(beforeIndex, afterIndex) {
		t.Fatalf("publication changed caller index: %v", err)
	}
	afterDiff := m34Git(t, fixture.repository, append([]string{"diff", "--cached", "--binary", "--"}, unrelated...)...)
	if !bytes.Equal(beforeDiff, afterDiff) {
		t.Fatal("unrelated staged additions/modifications/deletions changed")
	}
	gotPaths := strings.Split(strings.TrimSuffix(string(m34Git(t, fixture.repository, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", previous, "HEAD")), "\x00"), "\x00")
	wantPaths := make([]string, 0, len(fixture.contents))
	for name, want := range fixture.contents {
		path := fixture.prefix + "/" + name
		wantPaths = append(wantPaths, path)
		got := m34Git(t, fixture.repository, "show", "HEAD:"+path)
		if !bytes.Equal(got, want) {
			t.Fatalf("published %q differs from capture", name)
		}
	}
	slices.Sort(wantPaths)
	slices.Sort(gotPaths)
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("published paths %q, want exact capture %q", gotPaths, wantPaths)
	}
	afterLive, err := os.ReadFile(fixture.app.store.snapshotPath(fixture.state.Board.ID))
	if err != nil || !bytes.Equal(live, afterLive) {
		t.Fatal("publication changed later live snapshot", err)
	}
	localHead := m34Git(t, fixture.repository, "rev-parse", "HEAD")
	remoteHead := m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")
	if !bytes.Equal(localHead, remoteHead) {
		t.Fatal("remote did not receive captured commit")
	}
	// A second identical capture must be a tree no-op and still preserve the index.
	if err := publishVerifiedBackup(root, fixture.item); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(localHead, m34Git(t, fixture.repository, "rev-parse", "HEAD")) {
		t.Fatal("identical capture created another commit")
	}
	afterIndex, err = os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(beforeIndex, afterIndex) {
		t.Fatal("no-op publication changed caller index", err)
	}
}

func TestM34ChecksUnsafeGitFeaturesRefusedBeforePublication(t *testing.T) {
	for _, kind := range []string{"hook", "hookspath", "filter", "fsmonitor-true", "fsmonitor-command"} {
		t.Run(kind, func(t *testing.T) {
			fixture := m34PublicationFixture(t)
			marker := filepath.Join(fixture.repository, "executed-feature")
			want := "unsupported_hooks"
			switch kind {
			case "hook":
				m34Write(t, filepath.Join(fixture.repository, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\n"))
			case "hookspath":
				m34Git(t, fixture.repository, "config", "core.hooksPath", "custom-hooks")
			case "filter":
				m34Write(t, filepath.Join(fixture.repository, ".gitattributes"), []byte(fixture.prefix+"/** filter=unsupported\n"))
				want = "unsupported_filter"
			case "fsmonitor-true":
				m34Git(t, fixture.repository, "config", "core.fsmonitor", "true")
				want = "unsupported_fsmonitor"
			case "fsmonitor-command":
				m34Git(t, fixture.repository, "config", "core.fsmonitor", marker)
				want = "unsupported_fsmonitor"
			}
			headBefore := m34Git(t, fixture.repository, "rev-parse", "HEAD")
			indexPath := filepath.Join(fixture.repository, ".git", "index")
			indexBefore, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			err = publishVerifiedBackup(fixture.app.store.root, fixture.item)
			var refusal publicationGitError
			if !errors.As(err, &refusal) || string(refusal) != want {
				t.Fatalf("refusal %v, want %s", err, want)
			}
			if !bytes.Equal(headBefore, m34Git(t, fixture.repository, "rev-parse", "HEAD")) {
				t.Fatal("refused publication changed HEAD")
			}
			if !bytes.Equal(headBefore, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
				t.Fatal("refused publication changed origin")
			}
			afterIndex, err := os.ReadFile(indexPath)
			if err != nil || !bytes.Equal(indexBefore, afterIndex) {
				t.Fatal("refused publication changed caller index", err)
			}
			lease, lockErr := acquirePublicationRepository(fixture.repository, 0)
			if lockErr != nil {
				t.Fatal("refused publication leaked repository lock", lockErr)
			}
			releasePublicationRepository(lease)
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsupported Git feature executed", err)
			}
		})
	}
}

func TestM34ChecksPersistentCommonDirectoryLock(t *testing.T) {
	fixture := m34PublicationFixture(t)
	linked := filepath.Join(t.TempDir(), "linked")
	m34Git(t, fixture.repository, "worktree", "add", "-b", "linked-check", linked)
	lease, err := acquirePublicationRepository(fixture.repository, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			releasePublicationRepository(lease)
		}
	}()
	lockPath := filepath.Join(fixture.repository, ".git", "localswim-autopush.lock")
	before, err := os.Stat(lockPath)
	if err != nil || before.Size() != 0 {
		t.Fatal("shared persistent lock was initialized before locking", err)
	}
	start := time.Now()
	other, err := acquirePublicationRepository(linked, 75*time.Millisecond)
	if other != nil {
		releasePublicationRepository(other)
		t.Fatal("linked worktree acquired held common-directory lock")
	}
	if err == nil || err.Error() != "repository_busy" || time.Since(start) > 3*time.Second {
		t.Fatalf("unexpected bounded lock refusal: %v", err)
	}
	releasePublicationRepository(lease)
	released = true
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("lock path was removed/replaced after release", err)
	}
	other, err = acquirePublicationRepository(linked, 0)
	if err != nil {
		t.Fatal("lock did not become available after release", err)
	}
	releasePublicationRepository(other)
	final, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, final) || final.Size() != 0 {
		t.Fatal("shared lock identity/empty bytes were not persistent", err)
	}
}
