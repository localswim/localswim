package v2

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func backupCaptureFixture(t *testing.T) (*Service, turnOneClient, snapshot, map[string][]byte) {
	t.Helper()
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Capture boundary"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, editor, b)
	contents := make(map[string][]byte)
	for i, data := range [][]byte{[]byte("retained detached bytes"), []byte("active bytes")} {
		raw, _ = editor.call("GET", "/cards/"+c.ID, "", "", 200)
		c = turnOneDecode[card](t, raw)
		raw, headers := attachmentHTTPUpload(t, editor, []attachmentFormField{
			{"card_id", c.ID}, {"expected_card_etag", c.entityTag()},
		}, fmt.Sprintf("file-%d.txt", i), data, 201)
		item := turnOneDecode[attachment](t, raw)
		contents[filepath.ToSlash(attachmentPath(item))] = data
		if i == 0 {
			attachmentHTTPDetach(t, editor, "/attachments/"+item.ID, item.entityTag(), headers.Get("X-Board-ETag"), 204)
		}
	}
	state, err := app.store.readback(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Attachments) != 2 || state.Attachments[0].Active || !state.Attachments[1].Active {
		t.Fatal("fixture must contain one detached and one active attachment")
	}
	return app, editor, state, contents
}

func TestM29CapturedSnapshotKeepsRevisionWhileMutationCommits(t *testing.T) {
	app, editor, before, contents := backupCaptureFixture(t)
	snapshotPath := app.store.snapshotPath(before.Board.ID)
	original, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := app.store.captureBackupSnapshot(before.Board.ID, humanID, before.Board.ETag)
	if err != nil {
		t.Fatal(err)
	}

	// Defer verification and archive construction until a real HTTP mutation commits.
	editor.client = &http.Client{Timeout: 3 * time.Second}
	c := before.Cards[0]
	raw, _ := editor.call("PATCH", "/cards/"+c.ID, `{"title":"Committed after capture"}`, c.entityTag(), 200)
	updated := turnOneDecode[card](t, raw)
	if updated.Title != "Committed after capture" || updated.Revision <= before.Board.Revision {
		t.Fatal("the post-capture mutation did not commit")
	}
	raw, _ = attachmentHTTPUpload(t, editor, []attachmentFormField{
		{"card_id", c.ID}, {"expected_card_etag", updated.entityTag()},
	}, "later.txt", []byte("only in later captures"), 201)
	later := turnOneDecode[attachment](t, raw)

	// Extra files in the workspace and attachment tree must never enter the ZIP.
	for _, path := range []string{
		filepath.Join(app.store.root, "unrelated.txt"),
		filepath.Join(app.store.root, filepath.Dir(attachmentPath(later)), "unaudited-file"),
	} {
		if err := os.WriteFile(path, []byte("unrelated bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	editor.call("POST", "/boards", `{"name":"Unrelated board"}`, "", 201)

	state, data, err := captured.verify()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, before) || !bytes.Equal(data, original) {
		t.Fatal("capture changed after a later durable commit")
	}
	id := newID()
	item, err := buildBackup(app.store.root, id, state, data)
	if err != nil {
		t.Fatal(err)
	}
	if item.BoardID != before.Board.ID || item.BoardRevision != before.Board.Revision {
		t.Fatal("backup metadata does not identify the captured revision")
	}
	contents["boards/"+before.Board.ID+".json"] = original
	archivePath := filepath.Join(app.store.root, "backups", id, "content.zip")
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != len(contents) {
		t.Fatalf("ZIP contains %d entries, want exact inventory of %d", len(archive.File), len(contents))
	}
	seen := make(map[string]bool)
	for _, file := range archive.File {
		want, ok := contents[file.Name]
		if !ok || seen[file.Name] {
			t.Fatalf("ZIP contains an unexpected or duplicate entry %q", file.Name)
		}
		seen[file.Name] = true
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("ZIP entry %q differs from captured bytes", file.Name)
		}
	}
	wantIDs := []string{before.Attachments[0].ID, before.Attachments[1].ID}
	gotIDs := slices.Clone(item.AttachmentIDs)
	slices.Sort(wantIDs)
	slices.Sort(gotIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatal("metadata inventory differs from captured active and detached attachments")
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if item.Size != int64(len(archiveBytes)) || item.SHA256 != fmt.Sprintf("%x", sha256.Sum256(archiveBytes)) {
		t.Fatal("backup metadata does not match final archive bytes")
	}
	live, err := app.store.readback(before.Board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Board.Revision <= before.Board.Revision || live.Cards[0].Title != updated.Title || len(live.Attachments) != 3 {
		t.Fatal("backup work replaced or lost the later live revision")
	}
	next, err := app.store.captureBackupSnapshot(live.Board.ID, humanID, live.Board.ETag)
	if err != nil {
		t.Fatal(err)
	}
	nextState, _, err := next.verify()
	if err != nil || !reflect.DeepEqual(nextState, live) {
		t.Fatal("the next capture did not include the later committed changes", err)
	}
}

func TestM29CaptureRefusesDamagedDurableSnapshot(t *testing.T) {
	app, _, before, _ := backupCaptureFixture(t)
	if err := os.WriteFile(app.store.snapshotPath(before.Board.ID), []byte("damaged snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, err := app.store.captureBackupSnapshot(before.Board.ID, humanID, before.Board.ETag)
	if err == nil {
		_, _, err = captured.verify()
	}
	if !errors.Is(err, errBackupIntegrity) {
		t.Fatalf("damaged durable snapshot returned %v, want integrity refusal", err)
	}
}
func TestM29CaptureRefusesMissingOrTamperedAttachments(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, damage := range []string{"missing", "tampered"} {
			t.Run(fmt.Sprintf("active=%t/%s", active, damage), func(t *testing.T) {
				app, _, before, _ := backupCaptureFixture(t)
				captured, err := app.store.captureBackupSnapshot(before.Board.ID, humanID, before.Board.ETag)
				if err != nil {
					t.Fatal(err)
				}
				state, data, err := captured.verify()
				if err != nil {
					t.Fatal(err)
				}
				index := slices.IndexFunc(state.Attachments, func(item attachment) bool { return item.Active == active })
				path := filepath.Join(app.store.root, attachmentPath(state.Attachments[index]))
				if damage == "missing" {
					err = os.Remove(path)
				} else {
					original, readErr := os.ReadFile(path)
					if readErr != nil {
						t.Fatal(readErr)
					}
					original[0] ^= 1
					err = os.WriteFile(path, original, 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
				id := newID()
				if _, err := buildBackup(app.store.root, id, state, data); !errors.Is(err, errBackupIntegrity) {
					t.Fatalf("damaged attachment returned %v, want integrity refusal", err)
				}
				if _, err := readBackup(app.store.root, id); !errors.Is(err, errMissing) {
					t.Fatalf("failed backup left readable metadata: %v", err)
				}
			})
		}
	}
}
