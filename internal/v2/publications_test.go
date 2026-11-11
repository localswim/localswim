package v2

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestM34PublicationClosedInputAndDurableFailure(t *testing.T) {
	app, editor, state, _ := backupCaptureFixture(t)
	raw, _ := editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`, state.Board.ID, state.Board.ETag), "", 201)
	capture := turnOneDecode[backup](t, raw)
	id := newOperationID()
	body := fmt.Sprintf(`{"id":%q,"board_id":%q,"backup_id":%q}`, strings.ToUpper(id), strings.ToUpper(state.Board.ID), strings.ToUpper(capture.ID))
	for _, bad := range []string{
		strings.TrimSuffix(body, "}") + `,"unknown":true}`,
		strings.TrimSuffix(body, "}") + `,"workspace":true}`,
		strings.TrimSuffix(body, "}") + `,"workspace":null}`,
		strings.TrimSuffix(body, "}") + `,"workspace":"false"}`,
		strings.TrimSuffix(body, "}") + `,"remote":""}`,
		strings.TrimSuffix(body, "}") + `,"remote":"a\nb"}`,
		strings.TrimSuffix(body, "}") + `,"branch":null}`,
		fmt.Sprintf(`{"board_id":%q}`, state.Board.ID),
	} {
		editor.call("POST", "/publications", bad, "", 400)
	}
	if _, err := os.Stat(filepath.Join(app.store.root, "publications")); !os.IsNotExist(err) {
		t.Fatal("invalid input reserved a publication")
	}
	raw, headers := editor.call("POST", "/publications", body, "", 202)
	item := turnOneDecode[publication](t, raw)
	if item.ID != id || item.BoardID != state.Board.ID || item.BackupID != capture.ID ||
		item.State != "failed" || item.ErrorCode == nil || *item.ErrorCode != "repository_unavailable" ||
		item.FinishedAt == nil || item.Workspace || headers.Get("Location") != Prefix+"/publications/"+id {
		t.Fatalf("unexpected bounded failure outcome: %+v", item)
	}
	read, readHeaders := editor.call("GET", "/publications/"+id, "", "", 200)
	if !bytes.Equal(raw, read) || readHeaders.Get("ETag") == "" {
		t.Fatal("publication readback changed or lacks a validator")
	}
	editor.call("GET", "/publications/"+id, "", "", 200)
	editor.call("POST", "/publications", body, "", 409)
	stored, err := readPublication(app.store.root, id)
	if err != nil || stored.State != "failed" || stored.ID != id {
		t.Fatal("failed publication was not retained durably")
	}
	raw, _ = editor.call("GET", "/publications?board_id="+state.Board.ID, "", "", 200)
	page := turnOneDecode[struct {
		Publications []publication `json:"publications"`
		NextCursor   *string       `json:"next_cursor"`
	}](t, raw)
	if len(page.Publications) != 1 || page.Publications[0].ID != id || page.NextCursor != nil {
		t.Fatal("publication collection did not return the board's outcome")
	}
}

func TestM34PublicationRefusesDifferentBoardCapture(t *testing.T) {
	_, editor, state, _ := backupCaptureFixture(t)
	raw, _ := editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`, state.Board.ID, state.Board.ETag), "", 201)
	capture := turnOneDecode[backup](t, raw)
	raw, _ = editor.call("POST", "/boards", `{"name":"Other publication board"}`, "", 201)
	other := turnOneDecode[board](t, raw)
	editor.call("POST", "/publications", fmt.Sprintf(`{"board_id":%q,"backup_id":%q}`, other.ID, capture.ID), "", 404)
}

func TestM34InterruptedPublicationIsInspectableAndReserved(t *testing.T) {
	root := t.TempDir()
	item := publication{ID: newOperationID(), BoardID: newID(), BackupID: newOperationID(), State: "running"}
	// An interruption after writing the temporary manifest but before its
	// atomic install leaves no accepted operation or orphan reservation.
	dir, err := publicationDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := dir.OpenFile(".pending-"+item.ID, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	_ = dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalCanonical(item)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pending.Write(data)
	if err := errors.Join(err, pending.Sync(), pending.Close()); err != nil {
		t.Fatal(err)
	}
	if _, err := readPublication(root, item.ID); !errors.Is(err, errMissing) {
		t.Fatalf("temporary record became visible: %v", err)
	}
	if err := savePublication(root, item, true); err != nil {
		t.Fatal(err)
	}
	read, err := readPublication(root, item.ID)
	if err != nil || read.State != "running" || read.FinishedAt != nil {
		t.Fatal("interrupted publication did not remain inspectable")
	}
	if err := savePublication(root, item, true); err != errPublicationExists {
		t.Fatal("an interrupted operation allowed reuse of its original UUID")
	}
}

func TestM34PublicationRefusesFileDirectoryConflicts(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		name := "captured-file-replaces-directory"
		if ancestor {
			name = "captured-file-requires-existing-file-as-directory"
		}
		t.Run(name, func(t *testing.T) {
			fixture := m34PublicationFixture(t)
			// Commit a path collision from the index without touching the live
			// data tree or changing the already verified capture.
			conflict := fixture.prefix + "/boards/" + fixture.state.Board.ID + ".json/unrelated.txt"
			if ancestor {
				conflict = fixture.prefix + "/boards"
			}
			blob := strings.TrimSpace(string(m34Git(t, fixture.repository, "rev-parse", "HEAD:unchanged.txt")))
			m34Git(t, fixture.repository, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+conflict)
			m34Git(t, fixture.repository, "commit", "-m", "Seed conflicting path")
			m34Git(t, fixture.repository, "push", "origin", "HEAD:refs/heads/main")
			head := m34Git(t, fixture.repository, "rev-parse", "HEAD")
			indexPath := filepath.Join(fixture.repository, ".git", "index")
			before, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := publishVerifiedBackup(fixture.app.store.root, fixture.item); err != publicationGitError("git_index_failed") {
				t.Fatalf("path collision produced %v", err)
			}
			if !bytes.Equal(head, m34Git(t, fixture.repository, "rev-parse", "HEAD")) ||
				!bytes.Equal(head, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
				t.Fatal("refused collision changed a ref")
			}
			after, err := os.ReadFile(indexPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("refused collision changed caller index", err)
			}
		})
	}
}

func TestM34PublicationPushDoesNotFollowTags(t *testing.T) {
	fixture := m34PublicationFixture(t)
	m34Git(t, fixture.repository, "tag", "-a", "unrelated", "-m", "Local annotated tag")
	m34Git(t, fixture.repository, "config", "push.followTags", "true")
	if err := publishVerifiedBackup(fixture.app.store.root, fixture.item); err != nil {
		t.Fatal(err)
	}
	if refs := m34Git(t, fixture.remote, "for-each-ref", "--format=%(refname)"); string(refs) != "refs/heads/main\n" {
		t.Fatalf("publication pushed unexpected refs %q", refs)
	}
}
