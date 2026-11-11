package v2

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func backupHTTPContent(t *testing.T, client turnOneClient, id, method, accept string, status int) ([]byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, client.url+Prefix+"/backups/"+id+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+client.token)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	response, err := client.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("backup content status %d, want %d: %s", response.StatusCode, status, data)
	}
	return data, response.Header
}

func TestM29BackupHTTPAndIntegrity(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Backup source"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	id := newOperationID()
	body := fmt.Sprintf(`{"id":%q,"board_id":%q,"expected_board_etag":%q,"ignored":true}`, strings.ToUpper(id), strings.ToUpper(b.ID), b.entityTag())
	raw, headers := editor.call("POST", "/backups", body, "", 201)
	item := turnOneDecode[backup](t, raw)
	if item.ID != id || item.BoardID != b.ID || item.BoardRevision != b.Revision || item.AttachmentIDs == nil || len(item.AttachmentIDs) != 0 {
		t.Fatalf("unexpected backup metadata: %+v", item)
	}
	if headers.Get("Location") != Prefix+"/backups/"+id || headers.Get("Cache-Control") != "no-store" {
		t.Fatal("incorrect capture response headers")
	}
	editor.call("POST", "/backups", body, "", 409)
	read, _ := editor.call("GET", "/backups/"+id, "", "", 200)
	if !bytes.Equal(raw, read) {
		t.Fatal("backup metadata readback changed")
	}
	content, headers := backupHTTPContent(t, editor, id, "GET", "application/zip", 200)
	if int64(len(content)) != item.Size || fmt.Sprintf("%x", sha256.Sum256(content)) != item.SHA256 || headers.Get("Content-Type") != "application/zip" || !strings.Contains(headers.Get("Content-Disposition"), id+".zip") {
		t.Fatal("download does not match metadata")
	}
	request, err := http.NewRequest("GET", editor.url+Prefix+"/backups/"+id+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+editor.token)
	request.Header.Set("If-None-Match", "W/"+headers.Get("ETag"))
	response, err := editor.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 304 || len(conditional) != 0 || response.Header.Get("Cache-Control") != "private, no-cache" {
		t.Fatal("conditional ZIP read did not retain its validators")
	}
	request.Header.Set("If-None-Match", "invalid")
	response, err = editor.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("malformed ZIP condition was accepted")
	}
	head, headers := backupHTTPContent(t, editor, id, "HEAD", "application/zip", 200)
	if len(head) != 0 || headers.Get("Content-Length") != fmt.Sprint(item.Size) {
		t.Fatal("incorrect backup HEAD")
	}
	backupHTTPContent(t, editor, id, "GET", "image/png", 406)
	current, _ := app.store.accepted(b.ID)
	if current.Board.Revision != b.Revision {
		t.Fatal("capture changed the live revision")
	}
	path := filepath.Join(app.store.root, "backups", id, "content.zip")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, len(content)), 0o600); err != nil {
		t.Fatal(err)
	}
	failed, _ := backupHTTPContent(t, editor, id, "GET", "application/zip", 503)
	if bytes.Contains(failed, []byte(app.store.root)) {
		t.Fatal("backup failure exposed a filesystem path")
	}
}

func TestM29BackupRequestValidation(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Backup validation"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	editor.call("POST", "/backups", `{}`, "", 400)
	editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q}`, b.ID), "", 428)
	for _, tag := range []string{"invalid", "W/" + b.entityTag()} {
		editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`, b.ID, tag), "", 400)
	}
	editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`, b.ID, etag(b.ID, 99)), "", 412)
	editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"actor_id":%q}`, b.ID, b.entityTag(), botID), "", 400)
	editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`, newID(), b.entityTag()), "", 404)
	editor.call("GET", "/backups/"+newOperationID(), "", "", 404)
	if err := os.WriteFile(app.store.snapshotPath(b.ID), []byte("damaged snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`, b.ID, b.entityTag()), "", 503)
	accepted, _ := app.store.accepted(b.ID)
	if accepted.Board.Revision != b.Revision {
		t.Fatal("failed backup changed accepted state")
	}
}

func TestM29BackupRefusesDamagedBytes(t *testing.T) {
	for _, damage := range []string{"missing", "size", "digest"} {
		t.Run(damage, func(t *testing.T) {
			app, editor := attachmentHTTPServer(t)
			raw, _ := editor.call("POST", "/boards", `{"name":"Backup damage"}`, "", 201)
			b := turnOneDecode[board](t, raw)
			c := attachmentHTTPCard(t, editor, b)
			raw, _ = attachmentHTTPUpload(t, editor, []attachmentFormField{{"card_id", c.ID}, {"expected_card_etag", c.entityTag()}}, "source.txt", []byte("source"), 201)
			attachment := turnOneDecode[attachment](t, raw)
			path := filepath.Join(app.store.root, attachmentPath(attachment))
			var err error
			switch damage {
			case "missing":
				err = os.Remove(path)
			case "size":
				err = os.WriteFile(path, []byte("s"), 0o600)
			case "digest":
				err = os.WriteFile(path, []byte("broken"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			state, _ := app.store.accepted(b.ID)
			id := newOperationID()
			editor.call("POST", "/backups", fmt.Sprintf(`{"id":%q,"board_id":%q,"expected_board_etag":%q}`, id, b.ID, state.Board.entityTag()), "", 503)
			editor.call("GET", "/backups/"+id, "", "", 404)
			if _, err := os.Stat(filepath.Join(app.store.root, "backups", id)); !os.IsNotExist(err) {
				t.Fatal("failed capture retained a partial backup directory")
			}
			current, _ := app.store.accepted(b.ID)
			if current.Board.Revision != state.Board.Revision {
				t.Fatal("failed backup changed revision")
			}
		})
	}
}

func TestM29PublicationFailureRetainsOnlyUncertainCaptures(t *testing.T) {
	for _, phase := range []string{"refused", "uncertain_before_rename", "uncertain_after_rename"} {
		t.Run(phase, func(t *testing.T) {
			app, editor := attachmentHTTPServer(t)
			raw, _ := editor.call("POST", "/boards", `{"name":"Publication failure"}`, "", 201)
			b := turnOneDecode[board](t, raw)
			captured, err := app.store.captureBackupSnapshot(b.ID, humanID, b.Revision)
			if err != nil {
				t.Fatal(err)
			}
			state, data, err := captured.verify()
			if err != nil {
				t.Fatal(err)
			}
			id := newOperationID()
			publisher := func(dir *os.Root, manifest *os.File) error {
				if phase == "refused" {
					return errors.New("synthetic refusal")
				}
				if phase == "uncertain_after_rename" {
					if err := publishBackupManifest(dir, manifest); err != nil {
						return err
					}
				}
				return errors.Join(errWriteUncertain, errors.New("synthetic final sync failure"))
			}
			_, err = buildBackupWithPublisher(app.store.root, id, state, data, publisher)
			if phase == "refused" {
				if !errors.Is(err, errBackupIntegrity) {
					t.Fatalf("refused publication: %v", err)
				}
				if _, err := os.Stat(filepath.Join(app.store.root, "backups", id)); !os.IsNotExist(err) {
					t.Fatal("definitely unpublished files survived")
				}
				return
			}
			if !errors.Is(err, errWriteUncertain) {
				t.Fatalf("publication lost unknown outcome: %v", err)
			}
			if _, err := os.Stat(filepath.Join(app.store.root, "backups", id, "content.zip")); err != nil {
				t.Fatal("uncertain archive was removed", err)
			}
			recorder := httptest.NewRecorder()
			request := &request{w: recorder, r: httptest.NewRequest("POST", Prefix+"/backups", nil)}
			request.backupFailure(err)
			problem := turnOneDecode[problemBody](t, recorder.Body.Bytes())
			if recorder.Code != 503 || problem.Code != "outcome_unknown" || problem.Outcome != "unknown" {
				t.Fatal("uncertain publication response encourages replay")
			}
			if phase == "uncertain_after_rename" {
				item, err := readBackup(app.store.root, id)
				if err != nil {
					t.Fatal("visible manifest was removed", err)
				}
				file, err := copyVerifiedBackup(app.store.root, item)
				if err != nil {
					t.Fatal("retained backup cannot be verified", err)
				}
				_ = file.Close()
				_ = os.Remove(file.Name())
			} else if _, err := os.Stat(filepath.Join(app.store.root, "backups", id, "pending.json")); err != nil {
				t.Fatal("uncertain pending manifest was removed", err)
			}
			_, err = buildBackup(app.store.root, id, state, data)
			if !errors.Is(err, errBackupExists) {
				t.Fatal("uncertain capture could be silently overwritten")
			}
		})
	}
}

func TestM29ConcurrentIdentityAndRestartPersistence(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Persistent backup"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	captured, err := app.store.captureBackupSnapshot(b.ID, humanID, b.Revision)
	if err != nil {
		t.Fatal(err)
	}
	state, data, err := captured.verify()
	if err != nil {
		t.Fatal(err)
	}
	id := newOperationID()
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			_, err := buildBackup(app.store.root, id, state, data)
			results <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	if !(first == nil && errors.Is(second, errBackupExists) || second == nil && errors.Is(first, errBackupExists)) {
		t.Fatalf("same-ID publications did not serialize: %v, %v", first, second)
	}
	original, err := readBackup(app.store.root, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(app.store.root, app.principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	restored, err := readBackup(reopened.root, id)
	if err != nil || restored.ID != original.ID || restored.SHA256 != original.SHA256 || restored.BoardRevision != original.BoardRevision {
		t.Fatal("backup metadata changed across store restart", err)
	}
	file, err := copyVerifiedBackup(reopened.root, restored)
	if err != nil {
		t.Fatal("restarted backup content failed verification", err)
	}
	_ = file.Close()
	_ = os.Remove(file.Name())
}

func TestM29ManifestPublicationStaysWithinMovedRoot(t *testing.T) {
	parent := t.TempDir()
	named := filepath.Join(parent, "named")
	original := filepath.Join(named, "captured")
	if err := os.MkdirAll(original, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.OpenRoot(original)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	manifest, err := dir.OpenFile("pending.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	expected := []byte("captured manifest")
	if _, err := manifest.Write(expected); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Sync(); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(named, moved); err != nil {
		t.Skipf("platform prevents moving an ancestor of an opened root: %v", err)
	}
	if err := os.MkdirAll(original, 0o700); err != nil {
		t.Fatal(err)
	}
	foreignPending, foreignManifest := []byte("foreign pending"), []byte("foreign manifest")
	if err := os.WriteFile(filepath.Join(original, "pending.json"), foreignPending, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "backup.json"), foreignManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishBackupManifest(dir, manifest); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string][]byte{"pending.json": foreignPending, "backup.json": foreignManifest} {
		actual, err := os.ReadFile(filepath.Join(original, name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("publication changed foreign %s: %v", name, err)
		}
	}
	actual, err := os.ReadFile(filepath.Join(moved, "captured", "backup.json"))
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatal("publication lost the opened root", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "captured", "pending.json")); !os.IsNotExist(err) {
		t.Fatal("rooted publication did not consume the original pending manifest")
	}
}
