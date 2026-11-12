package v2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func restartFailedRecovery(t *testing.T, initial *Service, human turnOneClient, id string, removeReference bool) (*Service, turnOneClient) {
	t.Helper()
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(initial.store.snapshotPath(id), []byte("damaged native snapshot")); err != nil {
		t.Fatal(err)
	}
	if removeReference {
		if err := os.Remove(initial.store.recoveryReferencePath(id)); err != nil {
			t.Fatal(err)
		}
	}
	app, err := Open(initial.DataRoot(), human.token, base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)
	human.url = server.URL
	return app, human
}

func failedRecoveryPost(t *testing.T, client turnOneClient, id, mode, tag, media string, data []byte, status int) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, client.url+Prefix+"/recoveries/"+id+"?mode="+mode, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", media)
	if tag != "" {
		request.Header.Set("If-Match", tag)
	}
	response, err := client.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("recovery status %d want %d: %s (%v)", response.StatusCode, status, body, err)
	}
	return body, response.Header
}

func TestM84RecoverFailedBoardFromBackup(t *testing.T) {
	initial, human, source, contents := backupCaptureFixture(t)
	_, archive := captureRecoveryBackup(t, initial, source)
	raw, healthyHeaders := human.call("POST", "/boards", `{"name":"Healthy neighbor"}`, "", 201)
	healthy := turnOneDecode[board](t, raw)
	human.call("PATCH", "/boards/"+source.Board.ID, `{"name":"Later committed name"}`, source.Board.entityTag(), 200)
	app, human := restartFailedRecovery(t, initial, human, source.Board.ID, false)
	_, headers := human.call("GET", "/verifications/"+source.Board.ID, "", "", 200)
	tag := headers.Get("ETag")
	bot := human
	bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	failedRecoveryPost(t, bot, source.Board.ID, "same_id", tag, "application/zip", archive, 404)
	failedRecoveryPost(t, human, source.Board.ID, "same_id", "", "application/zip", archive, 428)
	failedRecoveryPost(t, human, source.Board.ID, "same_id", `"stale"`, "application/zip", archive, 412)
	failedRecoveryPost(t, human, source.Board.ID, "same_id", tag, "application/zip", archive[:len(archive)/2], 400)
	other, _ := app.store.accepted(healthy.ID)
	data, _ := marshalCanonical(other)
	failedRecoveryPost(t, human, source.Board.ID, "same_id", tag, "application/json", data, 400)
	for _, item := range source.Attachments {
		if err := os.WriteFile(filepath.Join(app.store.root, attachmentPath(item)), []byte("damaged"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.ReadFile(app.store.snapshotPath(source.Board.ID))
	failedRecoveryPost(t, human, source.Board.ID, "same_id", tag, "application/json", mustSnapshotJSON(t, source), 503)
	after, _ := os.ReadFile(app.store.snapshotPath(source.Board.ID))
	if !bytes.Equal(before, after) {
		t.Fatal("refused source changed the failed snapshot")
	}
	raw, recoveredHeaders := failedRecoveryPost(t, human, source.Board.ID, "same_id", tag, "application/zip", archive, 201)
	restored := turnOneDecode[board](t, raw)
	if restored.ID != source.Board.ID || restored.Revision != source.Board.Revision+2 || restored.Name != source.Board.Name || recoveredHeaders.Get("X-Board-ETag") != restored.entityTag() {
		t.Fatal("same-ID recovery lost trusted identity, revision floor, or response validator")
	}
	if app.store.isFailedBoard(restored.ID) {
		t.Fatal("successful recovery retained the diagnostic")
	}
	verified, _ := human.call("GET", "/verifications/"+restored.ID, "", "", 200)
	report := turnOneDecode[verification](t, verified)
	if !report.Valid || report.Recovery != nil || report.Revision == nil || *report.Revision != restored.Revision {
		t.Fatal("verification did not switch to the recovered board revision")
	}
	durable, err := app.store.durable(restored.ID)
	if err != nil || validateSnapshot(durable, restored.ID) != nil {
		t.Fatalf("recovered durable state is invalid: %v", err)
	}
	for _, item := range durable.Attachments {
		bytes, err := readVerifiedAttachment(app.store.root, item)
		if err != nil || !strings.EqualFold(string(bytes), string(contents[filepath.ToSlash(attachmentPath(item))])) {
			t.Fatal("backup did not repair exact attachment bytes")
		}
	}
	human.call("PATCH", "/boards/"+healthy.ID, `{"name":"Still writable"}`, healthyHeaders.Get("ETag"), 200)
	failedRecoveryPost(t, human, source.Board.ID, "same_id", tag, "application/zip", archive, 404)
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(app.DataRoot(), app.principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if _, exists := reopened.accepted(restored.ID); !exists {
		t.Fatal("recovery was not admitted on restart")
	}
}

func mustSnapshotJSON(t *testing.T, value snapshot) []byte {
	t.Helper()
	data, err := marshalCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestM84FreshRecoveryPreservesFailureAndRemapsIdentity(t *testing.T) {
	initial, human, source, _ := backupCaptureFixture(t)
	title := source.Board.ID
	human.call("PATCH", "/cards/"+source.Cards[0].ID, fmt.Sprintf(`{"title":%q}`, title), source.Cards[0].entityTag(), 200)
	source, _ = initial.store.accepted(source.Board.ID)
	_, archive := captureRecoveryBackup(t, initial, source)
	app, human := restartFailedRecovery(t, initial, human, source.Board.ID, true)
	_, headers := human.call("GET", "/verifications/"+source.Board.ID, "", "", 200)
	tag := headers.Get("ETag")
	failedRecoveryPost(t, human, source.Board.ID, "same_id", tag, "application/zip", archive, 403)
	raw, _ := failedRecoveryPost(t, human, source.Board.ID, "fresh", tag, "application/zip", archive, 201)
	restored := turnOneDecode[board](t, raw)
	if restored.ID == source.Board.ID || !app.store.isFailedBoard(source.Board.ID) || restored.Publishing.Enabled {
		t.Fatal("fresh recovery did not isolate the new identity")
	}
	next, err := app.store.durable(restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Cards[0].ID == source.Cards[0].ID || next.Cards[0].Title != title || next.Policy.ID == source.Policy.ID {
		t.Fatal("fresh recovery changed prose or reused owned identities")
	}
	for _, item := range next.Attachments {
		if _, err := readVerifiedAttachment(app.store.root, item); err != nil {
			t.Fatal(err)
		}
	}
	evidence, ok := recoveryEventEvidence(next.Activity[len(next.Activity)-1])
	if !ok || evidence.SourceBoardID == nil || *evidence.SourceBoardID != source.Board.ID {
		t.Fatal("fresh recovery lost origin evidence")
	}
	original, _ := os.ReadFile(app.store.snapshotPath(source.Board.ID))
	if string(original) != "damaged native snapshot" {
		t.Fatal("fresh recovery rewrote the failed source")
	}
}

func TestM84RecoveryRefusesChangedReferenceOrSnapshot(t *testing.T) {
	for _, reference := range []bool{false, true} {
		t.Run(fmt.Sprint(reference), func(t *testing.T) {
			initial, human := attachmentHTTPServer(t)
			raw, _ := human.call("POST", "/boards", `{"name":"Bound failure"}`, "", 201)
			b := turnOneDecode[board](t, raw)
			source, _ := initial.store.accepted(b.ID)
			app, human := restartFailedRecovery(t, initial, human, b.ID, false)
			_, headers := human.call("GET", "/verifications/"+b.ID, "", "", 200)
			path := app.store.snapshotPath(b.ID)
			if reference {
				path = app.store.recoveryReferencePath(b.ID)
			}
			if err := writeAtomic(path, []byte("changed evidence")); err != nil {
				t.Fatal(err)
			}
			failedRecoveryPost(t, human, b.ID, "same_id", headers.Get("ETag"), "application/json", mustSnapshotJSON(t, source), 412)
			current, _ := os.ReadFile(path)
			if string(current) != "changed evidence" {
				t.Fatal("stale recovery replaced changed evidence")
			}
		})
	}
}

func TestM84FreshRecoveryWithNonregularReference(t *testing.T) {
	initial, human := attachmentHTTPServer(t)
	raw, _ := human.call("POST", "/boards", `{"name":"Bad reference"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	source, _ := initial.store.accepted(b.ID)
	app, human := restartFailedRecovery(t, initial, human, b.ID, true)
	if err := os.Mkdir(app.store.recoveryReferencePath(b.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	_, headers := human.call("GET", "/verifications/"+b.ID, "", "", 200)
	raw, _ = failedRecoveryPost(t, human, b.ID, "fresh", headers.Get("ETag"), "application/json", mustSnapshotJSON(t, source), 201)
	recovered := turnOneDecode[board](t, raw)
	if recovered.ID == b.ID || !app.store.isFailedBoard(b.ID) {
		t.Fatal("fresh recovery lost source isolation")
	}
	if info, err := os.Stat(app.store.recoveryReferencePath(b.ID)); err != nil || !info.IsDir() {
		t.Fatal("fresh recovery replaced invalid reference")
	}
}
