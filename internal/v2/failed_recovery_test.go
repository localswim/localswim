package v2

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
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
	catalogIDs := app.store.catalogBoardIDs(humanID)
	catalogIndex := slices.Index(catalogIDs, source.Board.ID)
	if catalogIndex < 0 || slices.Contains(app.store.catalogBoardIDs(botID), source.Board.ID) {
		t.Fatal("catalog enumeration lost or disclosed the quarantined board")
	}
	if row, visible := app.store.boardSummary(source.Board.ID, humanID); !visible || row.Availability != "unhealthy" || row.Revision != nil {
		t.Fatal("catalog did not show the quarantine diagnostic before recovery")
	}
	raw, recoveredHeaders := failedRecoveryPost(t, human, source.Board.ID, "same_id", tag, "application/zip", archive, 201)
	restored := turnOneDecode[board](t, raw)
	if restored.ID != source.Board.ID || restored.Revision != source.Board.Revision+2 || restored.Name != source.Board.Name || recoveredHeaders.Get("X-Board-ETag") != restored.entityTag() {
		t.Fatal("same-ID recovery lost trusted identity, revision floor, or response validator")
	}
	if app.store.isFailedBoard(restored.ID) {
		t.Fatal("successful recovery retained the diagnostic")
	}
	// A catalog response that enumerated IDs before recovery can still resolve
	// that same ID to its admitted row after the recovery commits.
	if row, visible := app.store.boardSummary(catalogIDs[catalogIndex], humanID); !visible || row.Availability != "open" || row.Revision == nil || *row.Revision != restored.Revision {
		t.Fatal("catalog enumeration across recovery lost the current board row")
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

func TestM84QuarantinedRecoveryDisablesSourcePublishing(t *testing.T) {
	initial, human := attachmentHTTPServer(t)
	raw, _ := human.call("POST", "/boards", `{"name":"Publishing state"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	source, _ := initial.store.accepted(b.ID)
	source.Board.Revision++
	source.Board.Publishing.Enabled = true
	field, before, after := "publishing.enabled", "false", "true"
	source.Activity = append(source.Activity, activity{ID: newOperationID(), BoardID: b.ID,
		Revision: source.Board.Revision, At: source.Board.UpdatedAt, ActorID: humanID,
		Operation: "patch", ResourceType: "board", ResourceID: b.ID, Field: &field, Before: &before, After: &after})
	if err := validateSnapshot(source, b.ID); err != nil {
		t.Fatalf("source fixture invalid: %v", err)
	}
	app, human := restartFailedRecovery(t, initial, human, b.ID, false)
	_, headers := human.call("GET", "/verifications/"+b.ID, "", "", 200)
	raw, _ = failedRecoveryPost(t, human, b.ID, "same_id", headers.Get("ETag"), "application/json", mustSnapshotJSON(t, source), 201)
	if recovered := turnOneDecode[board](t, raw); recovered.Publishing.Enabled {
		t.Fatal("recovery scheduled source publishing")
	}
	durable, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := recoveryEventEvidence(durable.Activity[len(durable.Activity)-1])
	if !ok || !evidence.PublishingReset {
		t.Fatal("durable audit did not record the publishing reset")
	}
}

func TestM84FreshRecoverySortsRemappedChildren(t *testing.T) {
	originalFirst, originalSecond := "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	newFirst, newSecond := "10000000-0000-4000-8000-000000000001", "10000000-0000-4000-8000-000000000002"
	card := map[string]any{"child_ids": []any{originalFirst, originalSecond}, "title": originalFirst}
	remapRecoveryIDs(card, map[string]string{originalFirst: newSecond, originalSecond: newFirst})
	if !reflect.DeepEqual(card["child_ids"], []any{newFirst, newSecond}) || card["title"] != originalFirst {
		t.Fatal("fresh recovery did not sort remapped children while preserving card prose")
	}
}

func TestM84RecoveryReferenceReplacementGuards(t *testing.T) {
	for _, stage := range []string{"before pending", "after pending", "unchanged pending"} {
		t.Run(stage, func(t *testing.T) {
			app, human := attachmentHTTPServer(t)
			raw, _ := human.call("POST", "/boards", `{"name":"Recovery reference guard"}`, "", 201)
			b := turnOneDecode[board](t, raw)
			s := app.store
			source, _ := s.accepted(b.ID)
			originalSnapshot, err := os.ReadFile(s.snapshotPath(b.ID))
			if err != nil {
				t.Fatal(err)
			}
			path := s.recoveryReferencePath(b.ID)
			originalReference, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			externalReference := []byte("external reference observation\n")
			wanted := externalReference
			if stage == "unchanged pending" {
				wanted = originalReference
			}
			next := cloneSnapshot(source)
			next.Board.Revision++
			data := mustSnapshotJSON(t, next)
			beforePending := func() error {
				if stage == "before pending" {
					if err := writeAtomic(path, externalReference); err != nil {
						return err
					}
				}
				current, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if !bytes.Equal(current, originalReference) {
					return errStale
				}
				return nil
			}
			beforeSnapshot := func() error {
				ref, valid := s.readRecoveryReference(b.ID)
				if !valid || ref.Pending == nil {
					return errors.New("fixture did not observe writer's pending proposal")
				}
				if stage == "after pending" {
					return writeAtomic(path, externalReference)
				}
				return errStale
			}
			s.mu.Lock()
			err = s.writeSnapshotWithRecoveryReference(next, data, beforeSnapshot, beforePending)
			s.mu.Unlock()
			if !errors.Is(err, errStale) {
				t.Fatalf("expected stale refusal, got %v", err)
			}
			afterSnapshot, err := os.ReadFile(s.snapshotPath(b.ID))
			if err != nil || !bytes.Equal(originalSnapshot, afterSnapshot) {
				t.Fatal("refused write replaced snapshot")
			}
			afterReference, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(wanted, afterReference) {
				t.Fatal("refusal replaced independently changed reference or failed to roll back its own proposal")
			}
		})
	}
}

func TestM84RecoveryAttachmentIOLeavesNeighborWritable(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(fmt.Sprint(drift), func(t *testing.T) {
			initial, human, source, contents := backupCaptureFixture(t)
			raw, healthyHeaders := human.call("POST", "/boards", `{"name":"Healthy during staging"}`, "", 201)
			healthy := turnOneDecode[board](t, raw)
			app, human := restartFailedRecovery(t, initial, human, source.Board.ID, false)
			_, headers := human.call("GET", "/verifications/"+source.Board.ID, "", "", 200)
			tag := headers.Get("ETag")
			for _, item := range source.Attachments {
				if err := os.Remove(filepath.Join(app.store.root, attachmentPath(item))); err != nil {
					t.Fatal(err)
				}
			}
			paused := make(chan struct{})
			resume := make(chan struct{})
			defer func() {
				if resume != nil {
					close(resume)
				}
			}()
			// Verify every member first. Pause the first apply read, when the
			// recovery owns the failed ID but has yet to write its new bytes.
			reads := 0
			result := make(chan error, 1)
			continueApply := resume
			go func() {
				_, err := app.store.recoverFailed(source.Board.ID, "same_id", humanID, tag, source, func(item attachment) ([]byte, error) {
					reads++
					if reads == len(source.Attachments)+1 {
						close(paused)
						<-continueApply
					}
					return contents[filepath.ToSlash(attachmentPath(item))], nil
				})
				result <- err
			}()
			select {
			case <-paused:
			case err := <-result:
				t.Fatalf("recovery failed before pausing: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("recovery did not reach apply read")
			}
			neighbor := human
			neighbor.client = &http.Client{Timeout: 3 * time.Second}
			neighbor.call("GET", "/boards/"+healthy.ID, "", "", 200)
			neighbor.call("PATCH", "/boards/"+healthy.ID, `{"name":"Neighbor edit committed"}`, healthyHeaders.Get("ETag"), 200)
			if _, err := app.store.reserveFailedRecovery(source.Board.ID, source.Board.ID, humanID); !errors.Is(err, errIdentityConflict) {
				t.Fatalf("concurrent recovery entered the same failed address: %v", err)
			}
			external := []byte("changed while attachments staged")
			referencePath := app.store.recoveryReferencePath(source.Board.ID)
			if drift {
				if err := writeAtomic(referencePath, external); err != nil {
					t.Fatal(err)
				}
			}
			close(resume)
			resume = nil
			select {
			case err := <-result:
				if drift && !errors.Is(err, errStale) || !drift && err != nil {
					t.Fatalf("unexpected recovery result: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("recovery did not finish after resuming")
			}
			if drift {
				saved, err := os.ReadFile(referencePath)
				if err != nil || !bytes.Equal(saved, external) {
					t.Fatal("stale recovery changed external reference")
				}
				for _, item := range source.Attachments {
					if _, err := os.Stat(filepath.Join(app.store.root, attachmentPath(item))); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("stale recovery retained new attachment bytes")
					}
				}
			}
		})
	}
}
