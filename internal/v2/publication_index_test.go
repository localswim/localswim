package v2

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestM91PublicationRefusesOperatorStaging(t *testing.T) {
	for _, kind := range []string{"addition", "intent-to-add", "edit", "deletion", "ancestor", "descendant", "busy"} {
		t.Run(kind, func(t *testing.T) {
			fixture := m34PublicationFixture(t)
			if kind == "edit" || kind == "deletion" {
				if err := publishVerifiedBackup(fixture.app.store.root, fixture.item); err != nil {
					t.Fatal(err)
				}
			}
			repository := fixture.repository
			boardPath := fixture.prefix + "/boards/" + fixture.state.Board.ID + ".json"
			index := filepath.Join(repository, ".git", "index")
			want := "publication_index_overlap"
			switch kind {
			case "addition":
				m34Git(t, repository, "add", "--", boardPath)
			case "intent-to-add":
				m34Git(t, repository, "add", "--intent-to-add", "--", boardPath)
			case "edit":
				m34Write(t, filepath.Join(repository, filepath.FromSlash(boardPath)), []byte("operator staged replacement"))
				m34Git(t, repository, "add", "--", boardPath)
			case "deletion":
				m34Git(t, repository, "rm", "--cached", "--", boardPath)
			case "ancestor", "descendant":
				overlap := fixture.prefix + "/boards"
				if kind == "descendant" {
					overlap = boardPath + "/operator.txt"
				}
				blob := strings.TrimSpace(string(m34Git(t, repository, "rev-parse", "HEAD:unchanged.txt")))
				m34Git(t, repository, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+overlap)
			case "busy":
				m34Write(t, index+".lock", []byte("operator lock"))
				want = "publication_index_busy"
			}
			before, err := os.ReadFile(index)
			if err != nil {
				t.Fatal(err)
			}
			head := m34Git(t, repository, "rev-parse", "HEAD")
			id := newOperationID()
			body := fmt.Sprintf(`{"id":%q,"board_id":%q,"backup_id":%q}`, id, fixture.state.Board.ID, fixture.item.ID)
			raw, _ := fixture.editor.call("POST", "/publications", body, "", 202)
			item := turnOneDecode[publication](t, raw)
			if item.State != "failed" || snapshotString(item.ErrorCode) != want {
				t.Fatalf("publication outcome state=%q code=%q, want failed %q", item.State, snapshotString(item.ErrorCode), want)
			}
			readback, _ := fixture.editor.call("GET", "/publications/"+id, "", "", 200)
			after, err := os.ReadFile(index)
			if err != nil || !bytes.Equal(raw, readback) || !bytes.Equal(before, after) {
				t.Fatal("refusal changed staged work or lost durable diagnostics", err)
			}
			if !bytes.Equal(head, m34Git(t, repository, "rev-parse", "HEAD")) ||
				!bytes.Equal(head, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
				t.Fatal("refusal advanced a branch")
			}
			if kind == "busy" {
				data, err := os.ReadFile(index + ".lock")
				if err != nil || string(data) != "operator lock" {
					t.Fatal("refusal changed the existing Git lock", err)
				}
			}
		})
	}
}

func TestM91PendingPublicationRechecksExactInventory(t *testing.T) {
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
	initial := m35Capture("unchanged-capture")
	previous, err = repo.publishCapture(ref, previous, initial)
	if err != nil {
		t.Fatal(err)
	}
	capture := m35Capture("new-capture")
	capture.files = append(capture.files, initial.files...)
	commit, names, err := repo.preparePublication(ref, previous, capture)
	if err != nil || commit == previous {
		t.Fatal("failed to prepare pending capture", err)
	}
	target, err := repo.pushTarget()
	if err != nil {
		t.Fatal(err)
	}
	stateRef := publicationCheckpointRef(root, "index-recovery")
	checkpoint := publicationCheckpoint{Revision: 2, Pending: &pendingPublication{
		Commit: commit, Parent: previous, Branch: ref, Remote: publicationRemoteDigest(target), Names: names,
	}}
	if _, err := repo.saveCheckpoint(stateRef, "", checkpoint); err != nil {
		t.Fatal(err)
	}
	// Stage changes after the capture and journal. The unchanged capture path
	// is absent from the prepared commit's diff and still must be protected.
	name := fixture.prefix + "/unchanged-capture.txt"
	m34Write(t, filepath.Join(root, "unchanged-capture.txt"), []byte("staged while pending"))
	m34Write(t, filepath.Join(root, "unrelated-stage.txt"), []byte("independent staging"))
	m34Git(t, fixture.repository, "add", "--", name, fixture.prefix+"/unrelated-stage.txt")
	loaded, oid, err := repo.checkpoint(stateRef)
	if err != nil || loaded.Pending == nil {
		t.Fatal("failed to reload pending checkpoint", err)
	}
	before := m34Git(t, fixture.repository, "diff", "--cached", "--binary")
	if _, err := repo.finishPending(stateRef, oid, loaded); err != publicationGitError("publication_index_overlap") {
		t.Fatal("interrupted publication overwrote staged overlap", err)
	}
	if got := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", ref))); got != previous {
		t.Fatal("staged overlap moved the local branch")
	}
	if after := m34Git(t, fixture.repository, "diff", "--cached", "--binary"); !bytes.Equal(before, after) {
		t.Fatal("staged overlap changed the index")
	}
	m34Git(t, fixture.repository, "reset", "--quiet", "HEAD", "--", name)
	settled, err := repo.finishPending(stateRef, oid, loaded)
	if err != nil || settled.Pending != nil {
		t.Fatal("publication could not resume after overlap was cleared", err)
	}
	if got := string(m34Git(t, fixture.repository, "diff", "--cached", "--name-only", "-z")); got != fixture.prefix+"/unrelated-stage.txt\x00" {
		t.Fatalf("unexpected staged paths after retry: %q", got)
	}
	live, err := os.ReadFile(filepath.Join(root, "unchanged-capture.txt"))
	if err != nil || string(live) != "staged while pending" {
		t.Fatal("publication changed a later working-tree edit", err)
	}
}

func TestM91InterruptedIndexInstallLeavesInspectableFailure(t *testing.T) {
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
	commit, names, err := repo.preparePublication(ref, previous, m35Capture("interrupted"))
	if err != nil || commit == previous {
		t.Fatal("failed to prepare capture", err)
	}
	target, err := repo.pushTarget()
	if err != nil {
		t.Fatal(err)
	}
	stateRef := publicationCheckpointRef(root, "index-install-interrupted")
	checkpoint := publicationCheckpoint{Revision: 1, Pending: &pendingPublication{
		Commit: commit, Parent: previous, Branch: ref, Remote: publicationRemoteDigest(target), Names: names,
	}}
	oid, err := repo.saveCheckpoint(stateRef, "", checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the distinct ref and index transactions being interrupted.
	m34Git(t, fixture.repository, "update-ref", ref, commit, previous)
	staged := m34Git(t, fixture.repository, "diff", "--cached", "--binary")
	if _, err := repo.finishPending(stateRef, oid, checkpoint); err != publicationGitError("publication_index_uncertain") {
		t.Fatal("interrupted index install had no explicit uncertainty", err)
	}
	if after := m34Git(t, fixture.repository, "diff", "--cached", "--binary"); !bytes.Equal(staged, after) {
		t.Fatal("uncertainty caused a blind index repair")
	}
	if got := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", ref))); got != previous {
		t.Fatal("publication pushed before resolving local index uncertainty")
	}
	if _, readOID, err := repo.checkpoint(stateRef); err != nil || readOID != oid {
		t.Fatal("interruption changed durable pending intent", err)
	}
}
