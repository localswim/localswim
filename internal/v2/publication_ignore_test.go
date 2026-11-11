package v2

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestM88ManualAndAutomaticPublicationRefuseIgnoredBoard(t *testing.T) {
	fixture := m34PublicationFixture(t)
	repo := fixture.repository
	boardPath := fixture.prefix + "/boards/" + fixture.state.Board.ID + ".json"
	ignore := filepath.Join(repo, ".gitignore")
	pattern := "/" + fixture.prefix + "/boards/*.json\n"
	m34Write(t, ignore, []byte(pattern))
	m34Write(t, filepath.Join(repo, "unrelated.txt"), []byte("staged, outside the capture\n"))
	m34Git(t, repo, "add", "--", "unrelated.txt")
	m34Write(t, filepath.Join(fixture.app.store.root, "attachments", "unaudited.txt"), []byte("outside the audit\n"))
	indexPath := filepath.Join(repo, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(m34Git(t, repo, "rev-parse", "HEAD")))
	refs := m34Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	remoteRefs := m34Git(t, fixture.remote, "for-each-ref", "--format=%(refname) %(objectname)")
	objects := m34Git(t, repo, "count-objects", "-v")
	assertUnpublished := func() {
		t.Helper()
		if !bytes.Equal(refs, m34Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")) ||
			!bytes.Equal(remoteRefs, m34Git(t, fixture.remote, "for-each-ref", "--format=%(refname) %(objectname)")) ||
			!bytes.Equal(objects, m34Git(t, repo, "count-objects", "-v")) {
			t.Fatal("ignored capture changed refs or wrote Git objects")
		}
		if len(m34Git(t, fixture.remote, "ls-tree", "-r", "refs/heads/main", "--", boardPath)) != 0 {
			t.Fatal("ignored board appeared on the remote")
		}
	}

	raw, _ := fixture.editor.call("POST", "/publications", fmt.Sprintf(`{"board_id":%q,"backup_id":%q}`, fixture.state.Board.ID, fixture.item.ID), "", 202)
	item := turnOneDecode[publication](t, raw)
	readback, _ := fixture.editor.call("GET", "/publications/"+item.ID, "", "", 200)
	if item.State != "failed" || snapshotString(item.ErrorCode) != "publication_path_ignored" || !bytes.Equal(raw, readback) {
		t.Fatalf("manual publication did not record the ignore refusal: %+v", item)
	}
	assertUnpublished()

	address := "/boards/" + fixture.state.Board.ID
	raw, _ = fixture.editor.call("PATCH", address, `{"publishing":{"enabled":true}}`, fixture.state.Board.ETag, 200)
	enabled := turnOneDecode[board](t, raw)
	failed := waitBoardPublishing(t, fixture.editor, address, func(info publishing) bool {
		return info.Pending && snapshotString(info.ErrorCode) == "publication_path_ignored"
	})
	if failed.ETag != enabled.ETag || failed.Publishing.LastSuccessAt != nil {
		t.Fatal("ignore refusal changed the revision or claimed a successful push")
	}
	assertUnpublished()

	// Git's negation rule admits this board while other boards remain ignored.
	m34Write(t, ignore, []byte(pattern+"!/"+boardPath+"\n"))
	allowed := waitBoardPublishing(t, fixture.editor, address, func(info publishing) bool {
		return !info.Pending && info.ErrorCode == nil && info.LastSuccessAt != nil
	})
	if allowed.ETag != enabled.ETag {
		t.Fatal("automatic retry changed the board revision")
	}
	fixture.contents["boards/"+allowed.ID+".json"], err = os.ReadFile(fixture.app.store.snapshotPath(allowed.ID))
	if err != nil {
		t.Fatal(err)
	}
	pushed := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")))
	if pushed == head || pushed != strings.TrimSpace(string(m34Git(t, repo, "rev-parse", "HEAD"))) {
		t.Fatal("allowed capture did not advance the local and remote branch together")
	}
	got := strings.Split(strings.TrimSuffix(string(m34Git(t, fixture.remote, "diff-tree", "--no-commit-id",
		"--name-only", "-r", "-z", head, pushed)), "\x00"), "\x00")
	want := make([]string, 0, len(fixture.contents))
	for name, contents := range fixture.contents {
		path := fixture.prefix + "/" + name
		want = append(want, path)
		if !bytes.Equal(contents, m34Git(t, fixture.remote, "show", pushed+":"+path)) {
			t.Fatalf("published artifact differs from capture: %s", name)
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("remote commit inventory %q, want %q", got, want)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(indexBefore, indexAfter) {
		t.Fatal("publication changed the caller's index", err)
	}
	fixture.editor.call("PATCH", address, `{"publishing":{"enabled":false}}`, allowed.ETag, 200)
}

func TestM88PublicationRefusesIgnoredStagedDetachedAttachment(t *testing.T) {
	fixture := m34PublicationFixture(t)
	var detached string
	for _, attachment := range fixture.state.Attachments {
		if !attachment.Active {
			detached = fixture.prefix + "/" + filepath.ToSlash(attachmentPath(attachment))
			break
		}
	}
	if detached == "" {
		t.Fatal("fixture needs an audited detached attachment")
	}
	repo := fixture.repository
	m34Write(t, filepath.Join(repo, ".git", "info", "exclude"), []byte("/"+detached+"\n"))
	m34Git(t, repo, "add", "--force", "--", detached)
	indexPath := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	head := m34Git(t, repo, "rev-parse", "HEAD")
	if err := publishVerifiedBackup(fixture.app.store.root, fixture.item); err != publicationGitError("publication_path_ignored") {
		t.Fatalf("ignored detached attachment produced %v", err)
	}
	if !bytes.Equal(head, m34Git(t, repo, "rev-parse", "HEAD")) ||
		!bytes.Equal(head, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
		t.Fatal("ignored detached attachment changed a branch")
	}
	after, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("ignore refusal changed the caller's index", err)
	}
}

func TestM88PublicationRejectsAmbiguousInventoryBeforeReading(t *testing.T) {
	for _, test := range []struct {
		name  string
		paths []string
	}{
		{"empty", nil},
		{"duplicate", []string{"artifact", "artifact"}},
		{"nested", []string{"artifact", "artifact/child"}},
		{"noncanonical", []string{"artifact/../child"}},
		{"nul", []string{"artifact\x00other"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var capture publicationCapture
			for _, name := range test.paths {
				capture.files = append(capture.files, publicationFile{path: name, read: func() ([]byte, error) {
					t.Fatal("invalid inventory read an artifact")
					return nil, nil
				}})
			}
			repo := publicationRepository{prefix: "nested"}
			if _, err := repo.prepareCommit("", capture); err != publicationGitError("capture_unavailable") {
				t.Fatalf("invalid inventory produced %v", err)
			}
		})
	}
}

func TestM88PublicationRefusesSkippedIgnoreRules(t *testing.T) {
	fixture := m34PublicationFixture(t)
	repo := fixture.repository
	ignore := filepath.Join(repo, ".gitignore")
	// Git on Windows cannot open a directory as an ignore file. On Unix,
	// Git refuses to follow an ignore symlink. Both can warn and return 1.
	var err error
	if runtime.GOOS == "windows" {
		err = os.Mkdir(ignore, 0o700)
	} else {
		m34Write(t, filepath.Join(repo, "ignore-rules"), []byte("/"+fixture.prefix+"/boards/\n"))
		err = os.Symlink("ignore-rules", ignore)
	}
	if err != nil {
		t.Fatal(err)
	}
	head := m34Git(t, repo, "rev-parse", "HEAD")
	objects := m34Git(t, repo, "count-objects", "-v")
	if err := publishVerifiedBackup(fixture.app.store.root, fixture.item); err != publicationGitError("git_ignore_unavailable") {
		t.Fatalf("skipped ignore rules produced %v", err)
	}
	if !bytes.Equal(objects, m34Git(t, repo, "count-objects", "-v")) ||
		!bytes.Equal(head, m34Git(t, repo, "rev-parse", "HEAD")) ||
		!bytes.Equal(head, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
		t.Fatal("skipped ignore rules allowed publication")
	}
}

func TestM88PublicationIgnoreCheckFailsClosed(t *testing.T) {
	if err := publicationGitIgnore(t.TempDir(), []string{"boards/disposable.json"}); err != publicationGitError("git_ignore_unavailable") {
		t.Fatalf("failed ignore check produced %v", err)
	}
}
