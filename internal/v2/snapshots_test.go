package v2

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func captureRecoveryBackup(t *testing.T, app *Service, state snapshot) (backup, []byte) {
	t.Helper()
	captured, err := app.store.captureBackupSnapshot(state.Board.ID, humanID, state.Board.Revision)
	if err != nil {
		t.Fatal(err)
	}
	state, data, err := captured.verify()
	if err != nil {
		t.Fatal(err)
	}
	item, err := buildBackup(app.store.root, newOperationID(), state, data)
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(app.store.root, "backups", item.ID, "content.zip"))
	if err != nil {
		t.Fatal(err)
	}
	return item, data
}

func recoveryPut(t *testing.T, client turnOneClient, boardID, expected, media string, data []byte, status int) ([]byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, client.url+Prefix+"/snapshots/"+boardID, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+client.token)
	req.Header.Set("Content-Type", media)
	if expected != "" {
		req.Header.Set("If-Match", expected)
	}
	response, err := client.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("recovery status %d want %d, read error %v, body %s", response.StatusCode, status, err, body)
	}
	return body, response.Header
}

func TestM30ZIPRecoveryRefusesDamageThenRecoversSameBoard(t *testing.T) {
	app, editor, source, contents := backupCaptureFixture(t)
	_, zipData := captureRecoveryBackup(t, app, source)
	originalCard := source.Cards[0]
	raw, _ := editor.call("PATCH", "/cards/"+originalCard.ID, `{"title":"after backup"}`, originalCard.entityTag(), 200)
	currentCard := turnOneDecode[card](t, raw)
	current, _ := app.store.accepted(source.Board.ID)
	before, err := os.ReadFile(app.store.snapshotPath(source.Board.ID))
	if err != nil {
		t.Fatal(err)
	}
	recoveryPut(t, editor, source.Board.ID, snapshotRepresentationTag(current), "application/zip", zipData[:len(zipData)/2], 400)
	after, _ := os.ReadFile(app.store.snapshotPath(source.Board.ID))
	if !bytes.Equal(before, after) {
		t.Fatal("damaged backup changed durable snapshot")
	}
	for _, item := range source.Attachments {
		if err := os.WriteFile(filepath.Join(app.store.root, attachmentPath(item)), []byte("damaged bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	raw, headers := recoveryPut(t, editor, source.Board.ID, snapshotRepresentationTag(current), "application/zip", zipData, 200)
	recovered := turnOneDecode[board](t, raw)
	if recovered.ID != source.Board.ID || recovered.Revision <= current.Board.Revision || headers.Get("ETag") != "" || headers.Get("X-Board-ETag") != recovered.entityTag() {
		t.Fatal("recovery identity/version headers are incorrect")
	}
	raw, _ = editor.call("GET", "/cards/"+originalCard.ID, "", "", 200)
	gotCard := turnOneDecode[card](t, raw)
	if gotCard.Title != originalCard.Title || gotCard.ID != currentCard.ID {
		t.Fatal("original card content was not restored")
	}
	for _, item := range source.Attachments {
		data, err := readVerifiedAttachment(app.store.root, item)
		if err != nil || !bytes.Equal(data, contents[filepath.ToSlash(attachmentPath(item))]) {
			t.Fatal("active/detached attachment bytes were not recovered")
		}
	}
	state, err := app.store.durable(source.Board.ID)
	if err != nil || validateSnapshot(state, source.Board.ID) != nil {
		t.Fatalf("recovered durable audit invalid: %v", err)
	}
	if !reflect.DeepEqual(state.Activity[:len(source.Activity)], source.Activity) {
		t.Fatal("historical source audit changed")
	}
	recoveryPut(t, editor, source.Board.ID, snapshotRepresentationTag(current), "application/zip", zipData, 412)
	root := app.store.root
	if err := app.store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(root, app.store.principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	persisted, ok := reopened.accepted(source.Board.ID)
	if !ok || persisted.Board.Revision != recovered.Revision {
		t.Fatal("recovery did not survive restart")
	}
}

func TestM30StoredRestorationReceiptAndDuplicate(t *testing.T) {
	app, editor, source, _ := backupCaptureFixture(t)
	item, _ := captureRecoveryBackup(t, app, source)
	id := "00000000-0000-7000-8000-000000000002"
	body := fmt.Sprintf(`{"id":%q,"board_id":%q,"backup_id":%q,"expected_board_etag":%q}`, id, source.Board.ID, item.ID, source.Board.entityTag())
	raw, headers := editor.call("POST", "/restorations", body, "", 201)
	assertNoResponseValidators(t, raw)
	result := turnOneDecode[restoration](t, raw)
	if result.At.Before(source.Board.UpdatedAt) || result.At.Year() < 2026 {
		t.Fatal("restoration execution time was derived from its preallocated ID")
	}
	if result.ID != id || result.BackupID != item.ID || result.Revision != source.Board.Revision+1 ||
		headers.Get("Location") != Prefix+"/restorations/"+id {
		t.Fatal("restoration response invalid")
	}
	raw, _ = editor.call("GET", "/restorations/"+strings.ToUpper(id), "", "", 200)
	assertNoResponseValidators(t, raw)
	if read := turnOneDecode[restoration](t, raw); !reflect.DeepEqual(read, result) {
		t.Fatal("restoration readback differs")
	}

	committed, ok := app.store.accepted(source.Board.ID)
	if !ok {
		t.Fatal("restoration not committed")
	}
	assertNoResponseValidators(t, marshalVerificationFixture(t, committed))
	event := committed.Activity[len(committed.Activity)-1]
	if snapshotString(event.Before) != fmt.Sprint(source.Board.Revision) || committed.Policy.Revision != result.Revision {
		t.Fatal("recovery did not preserve numeric revision evidence")
	}
	body = fmt.Sprintf(`{"id":%q,"board_id":%q,"backup_id":%q,"expected_board_etag":%q}`, id, source.Board.ID, item.ID, etag(result.BoardID, result.Revision))
	editor.call("POST", "/restorations", body, "", 409)
	raw, headers = editor.call("GET", "/snapshots/"+source.Board.ID, "", "", 200)
	raw = bytes.ReplaceAll(raw, []byte(item.ID), []byte(strings.ToUpper(item.ID)))
	recoveryPut(t, editor, source.Board.ID, headers.Get("ETag"), "application/json", raw, 200)
	editor.call("GET", "/restorations/"+id, "", "", 200)
}

func TestM30NativeJSONNormalizesIDsPreservesTextAndGlobalUsers(t *testing.T) {
	app, editor, source, _ := backupCaptureFixture(t)
	data, err := marshalCanonical(source)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(data, &document) != nil {
		t.Fatal("decode fixture")
	}
	users := document["users"].([]any)
	users[0].(map[string]any)["label"] = "historical label"
	var uppercase func(any)
	uppercase = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if text, ok := child.(string); ok && (key == "id" || strings.HasSuffix(key, "_id")) {
					value[key] = strings.ToUpper(text)
				}
				if ids, ok := child.([]any); ok && strings.HasSuffix(key, "_ids") {
					for index, id := range ids {
						ids[index] = strings.ToUpper(id.(string))
					}
				}
				uppercase(child)
			}
		case []any:
			for _, child := range value {
				uppercase(child)
			}
		}
	}
	uppercase(document)
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	recoveryPut(t, editor, source.Board.ID, snapshotRepresentationTag(source), "application/json", data, 200)
	state, _ := app.store.accepted(source.Board.ID)
	expectedCards := slices.Clone(source.Cards)
	for index := range expectedCards {
		expectedCards[index].Revision = state.Board.Revision
	}
	if !reflect.DeepEqual(state.Users, source.Users) || !reflect.DeepEqual(state.Cards, expectedCards) {
		t.Fatal("recovery changed global users or user text")
	}
}

func TestM30RecoveryIdentityAndAttachmentValidationBeforeMutation(t *testing.T) {
	app, _, source, _ := backupCaptureFixture(t)
	original, _ := os.ReadFile(app.store.snapshotPath(source.Board.ID))
	unknown := cloneSnapshot(source)
	oldID, newUserID := unknown.Users[0].ID, newID()
	data, _ := marshalCanonical(unknown)
	data = bytes.ReplaceAll(data, []byte(oldID), []byte(newUserID))
	if unmarshalCanonical(data, &unknown) != nil {
		t.Fatal("decode unknown fixture")
	}
	_, err := app.store.recoverSnapshot(source.Board.ID, humanID, newOperationID(), matchingSnapshotTag(snapshotRepresentationTag(source)), unknown, nil, nil)
	if !errors.Is(err, errIdentityConflict) {
		t.Fatalf("unknown user result %v", err)
	}
	missing := source.Attachments[0]
	if err := os.Remove(filepath.Join(app.store.root, attachmentPath(missing))); err != nil {
		t.Fatal(err)
	}
	_, err = app.store.recoverSnapshot(source.Board.ID, humanID, newOperationID(), matchingSnapshotTag(snapshotRepresentationTag(source)), source, nil, nil)
	if !errors.Is(err, errAttachmentIntegrity) {
		t.Fatalf("missing file result %v", err)
	}
	after, _ := os.ReadFile(app.store.snapshotPath(source.Board.ID))
	if !bytes.Equal(original, after) {
		t.Fatal("refused admission mutated board")
	}
}

func TestM30ArchiveReaderRejectsUnlistedDuplicateMissingAndCorruptEntries(t *testing.T) {
	app, _, source, _ := backupCaptureFixture(t)
	_, zipData := captureRecoveryBackup(t, app, source)
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"unlisted", "duplicate", "missing", "corrupt", "traversal"} {
		t.Run(mode, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			for index, entry := range reader.File {
				if mode == "missing" && index == 1 {
					continue
				}
				file, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(file)
				_ = file.Close()
				if err != nil {
					t.Fatal(err)
				}
				if mode == "corrupt" && index == 1 {
					data = []byte("bad")
				}
				if err := writeBackupEntry(writer, entry.Name, data); err != nil {
					t.Fatal(err)
				}
				if mode == "duplicate" && index == 0 {
					_ = writeBackupEntry(writer, entry.Name, data)
				}
			}
			if mode == "unlisted" {
				_ = writeBackupEntry(writer, "extra", []byte("bad"))
			}
			if mode == "traversal" {
				_ = writeBackupEntry(writer, "../escape", []byte("bad"))
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			file, err := os.CreateTemp(t.TempDir(), "archive-*")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := file.Write(buffer.Bytes()); err != nil {
				t.Fatal(err)
			}
			if _, err := inspectBackupArchive(file, source.Board.ID); !errors.Is(err, errBackupIntegrity) {
				t.Fatalf("damaged archive result %v", err)
			}
			if _, err := VerifyBackupFile(file.Name()); !errors.Is(err, errBackupIntegrity) {
				t.Fatalf("offline damaged archive result %v", err)
			}
		})
	}
}

func TestM30RecoveryActorCanBeAbsentFromSourceBoard(t *testing.T) {
	root := t.TempDir()
	store, current := m01CreateBoard(t, root)
	source := cloneSnapshot(current)
	source.Users = slices.DeleteFunc(source.Users, func(member user) bool { return member.ID == m01HumanID })
	source.Board.UserIDs = []string{m01BotID}
	source.Policy.ResourceGrants = slices.DeleteFunc(source.Policy.ResourceGrants, func(grant resourceGrant) bool { return grant.UserID == m01HumanID })
	source.Policy.CreateGrants = slices.DeleteFunc(source.Policy.CreateGrants, func(grant createGrant) bool { return grant.UserID == m01HumanID })
	source.Policy.MoveGrants = []moveGrant{}
	for _, grant := range current.Policy.MoveGrants {
		grant.UserID = m01BotID
		if !slices.Contains(source.Policy.MoveGrants, grant) {
			source.Policy.MoveGrants = append(source.Policy.MoveGrants, grant)
		}
	}
	for index := range source.Activity {
		source.Activity[index].ActorID = m01BotID
	}
	if err := validateSnapshot(source, source.Board.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.recoverSnapshot(current.Board.ID, m01HumanID, newOperationID(), matchingSnapshotTag(snapshotRepresentationTag(current)), source, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(recovered.Board.UserIDs, m01HumanID) || recovered.can(m01HumanID, "restore") {
		t.Fatal("recovery silently retained source actor membership")
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(root, store.principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if _, err := reopened.durable(current.Board.ID); err != nil {
		t.Fatal(err)
	}
}

func TestM30SnapshotValidatorRejectsConcurrentCardAndPolicyChanges(t *testing.T) {
	app, editor, source, _ := backupCaptureFixture(t)
	raw, headers := editor.call("GET", "/snapshots/"+source.Board.ID, "", "", 200)
	stale := headers.Get("ETag")
	editor.call("PATCH", "/cards/"+source.Cards[0].ID, `{"title":"concurrent card edit"}`, source.Cards[0].entityTag(), 200)
	recoveryPut(t, editor, source.Board.ID, stale, "application/json", raw, 412)
	current, _ := app.store.accepted(source.Board.ID)
	raw, headers = editor.call("GET", "/snapshots/"+source.Board.ID, "", "", 200)
	stale = headers.Get("ETag")
	_, err := app.store.update(current.Board.ID, current.Board.Revision, func(next *snapshot) error {
		policy := next.Policy
		policy.CreateGrants = slices.Clone(policy.CreateGrants)
		for _, lane := range next.Lanes {
			grant := createGrant{UserID: botID, LaneID: lane.ID}
			if !lane.Retired && !slices.Contains(policy.CreateGrants, grant) {
				policy.CreateGrants = append(policy.CreateGrants, grant)
				return addPolicyGrants(next, policy, humanID, next.Board.UpdatedAt)
			}
		}
		return errors.New("fixture has no additional creation grant")
	})
	if err != nil {
		t.Fatal(err)
	}
	recoveryPut(t, editor, source.Board.ID, stale, "application/json", raw, 412)
}

func TestM30DurableSnapshotPreconditionAndDamagedFallback(t *testing.T) {
	app, client, original, _ := backupCaptureFixture(t)
	address := "/snapshots/" + original.Board.ID
	payload, h := client.call("GET", address, "", "", 200)
	acceptedTag := h.Get("ETag")
	writeExternal := func(name string) {
		t.Helper()
		external := cloneSnapshot(original)
		field, before := "name", external.Board.Name
		external.Board.Name = name
		external.Board.Revision++

		external.Activity = append(external.Activity, activity{ID: newOperationID(), BoardID: external.Board.ID,
			Revision: external.Board.Revision, At: external.Board.UpdatedAt, ActorID: humanID, Operation: "patch",
			ResourceType: "board", ResourceID: external.Board.ID, Field: &field, Before: &before, After: &name})
		data := marshalVerificationFixture(t, external)
		if _, err := parseSnapshot(data, original.Board.ID); err != nil {
			t.Fatal(err)
		}
		if err := writeAtomic(app.store.snapshotPath(original.Board.ID), data); err != nil {
			t.Fatal(err)
		}
	}
	writeExternal("First valid external replacement")
	_, h = client.call("GET", address, "", "", 200)
	firstTag := h.Get("ETag")
	recoveryPut(t, client, original.Board.ID, acceptedTag, "application/json", payload, 412)
	writeExternal("Second valid external replacement")
	_, h = client.call("GET", address, "", "", 200)
	secondTag := h.Get("ETag")
	recoveryPut(t, client, original.Board.ID, firstTag, "application/json", payload, 412)
	raw, _ := recoveryPut(t, client, original.Board.ID, secondTag, "application/json", payload, 200)
	result := turnOneDecode[board](t, raw)
	if result.Name != original.Board.Name || result.Revision != original.Board.Revision+2 {
		t.Fatal("durable replacement was not used as revision evidence")
	}
	_, h = client.call("GET", address, "", "", 200)
	lastAcceptedTag := h.Get("ETag")
	if err := writeAtomic(app.store.snapshotPath(original.Board.ID), []byte("damaged")); err != nil {
		t.Fatal(err)
	}
	client.call("GET", address, "", "", 503)
	raw, _ = client.call("GET", "/verifications/"+original.Board.ID, "", "", 200)
	if turnOneDecode[verification](t, raw).Valid {
		t.Fatal("damaged snapshot reported healthy")
	}
	recoveryPut(t, client, original.Board.ID, secondTag, "application/json", payload, 412)
	recoveryPut(t, client, original.Board.ID, result.entityTag(), "application/json", payload, 412)
	recoveryPut(t, client, original.Board.ID, lastAcceptedTag, "application/json", payload, 200)
	client.call("GET", address, "", "", 200)
}

func TestM30RecoveryInvalidatesAllMutableResourceValidators(t *testing.T) {
	app, client, original, _ := backupCaptureFixture(t)
	c, l, a := original.Cards[0], original.Lanes[0], original.Attachments[1]
	cardBody := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Second"}`,
		original.Board.ID, original.Board.entityTag(), c.LaneID)
	raw, headers := client.call("POST", "/cards", cardBody, "", 201)
	other := turnOneDecode[card](t, raw)
	linkBody := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"references"}`,
		original.Board.ID, headers.Get("X-Board-ETag"), c.ID, other.ID)
	raw, _ = client.call("POST", "/relationships", linkBody, "", 201)
	link := turnOneDecode[relationship](t, raw)
	state, _ := app.store.accepted(original.Board.ID)
	c = state.Cards[0]
	source := cloneSnapshot(state)
	source.Cards[0].Title = "Same historical revision, different valid content"
	source.Lanes[0].Label = "Changed source lane label"
	payload := marshalVerificationFixture(t, source)
	raw, _ = recoveryPut(t, client, state.Board.ID, snapshotRepresentationTag(state), "application/json", payload, 200)
	result := turnOneDecode[board](t, raw)
	raw, headers = client.call("GET", "/cards/"+c.ID, "", "", 200)
	if got := assertCardRevision(t, raw, result.Revision, headers.Get("ETag")); got.Title != source.Cards[0].Title {
		t.Fatal("recovered card content differs")
	}
	client.call("PATCH", "/cards/"+c.ID, `{"title":"stale write"}`, c.entityTag(), 412)
	raw, headers = client.call("GET", "/lanes/"+l.ID, "", "", 200)
	restoredLane := assertLaneRevision(t, raw, result.Revision, headers.Get("ETag"))
	lanePatch := fmt.Sprintf(`{"board_etag":%q,"label":"stale write"}`, result.entityTag())
	client.call("PATCH", "/lanes/"+l.ID, lanePatch, l.entityTag(), 412)
	raw, headers = client.call("GET", "/attachments/"+a.ID, "", "", 200)
	restoredAttachment := assertAttachmentRevision(t, raw, result.Revision, headers.Get("ETag"))
	attachmentHTTPDetach(t, client, "/attachments/"+a.ID, a.entityTag(), result.entityTag(), 412)
	raw, headers = client.call("GET", "/relationships/"+link.ID, "", "", 200)
	restoredLink := assertRelationshipRevision(t, raw, result.Revision, headers.Get("ETag"))
	linkPatch := fmt.Sprintf(`{"expected_board_etag":%q,"kind":"blocks"}`, result.entityTag())
	client.call("PATCH", "/relationships/"+link.ID, linkPatch, link.entityTag(), 412)
	raw, _ = client.call("GET", "/policies/"+source.Policy.ID, "", "", 200)
	restoredPolicy := turnOneDecode[policy](t, raw)
	if restoredPolicy.entityTag() != etag(source.Policy.ID, result.Revision) {
		t.Fatal("policy validator was not refreshed")
	}
	client.call("PUT", "/policies/"+source.Policy.ID, policyPutBody(t, restoredPolicy, result.entityTag()), source.Policy.entityTag(), 412)

	// Ordinary edits after recovery must still advance exact historical versions.
	_, headers = client.call("PATCH", "/lanes/"+l.ID, lanePatch, restoredLane.entityTag(), 200)
	boardTag := headers.Get("X-Board-ETag")
	linkPatch = fmt.Sprintf(`{"expected_board_etag":%q,"kind":"blocks"}`, boardTag)
	_, headers = client.call("PATCH", "/relationships/"+link.ID, linkPatch, restoredLink.entityTag(), 200)
	attachmentHTTPDetach(t, client, "/attachments/"+a.ID, restoredAttachment.entityTag(), headers.Get("X-Board-ETag"), 204)
	if _, err := app.store.durable(state.Board.ID); err != nil {
		t.Fatal(err)
	}
}

func TestM30DuplicateStoredRestorationIDAcrossBoards(t *testing.T) {
	app, client, first, _ := backupCaptureFixture(t)
	backupID := newOperationID()
	id := newOperationID()
	if _, err := app.store.recoverSnapshot(first.Board.ID, humanID, id, matchingBoardTag(first.Board.entityTag()), first, &backupID, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := client.call("POST", "/boards", `{"name":"Second restore board"}`, "", 201)
	secondBoard := turnOneDecode[board](t, raw)
	second, _ := app.store.accepted(secondBoard.ID)
	restored, err := app.store.recoverSnapshot(second.Board.ID, humanID, newOperationID(), matchingBoardTag(second.Board.entityTag()), second, &backupID, nil)
	if err != nil {
		t.Fatal(err)
	}
	collision := cloneSnapshot(restored)
	collision.Activity[len(collision.Activity)-1].ID = id
	data := marshalVerificationFixture(t, collision)
	if _, err := parseSnapshot(data, second.Board.ID); err != nil {
		t.Fatal(err)
	}
	recoveryPut(t, client, second.Board.ID, snapshotRepresentationTag(restored), "application/json", data, 409)
	durable, err := app.store.durable(second.Board.ID)
	if err != nil || !reflect.DeepEqual(durable, restored) {
		t.Fatal("receipt identity refusal altered the second board")
	}
	if err := writeAtomic(app.store.snapshotPath(second.Board.ID), data); err != nil {
		t.Fatal(err)
	}
	root := app.store.root
	if err := app.store.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(root, app.store.principals...); !errors.Is(err, errIdentityConflict) {
		t.Fatalf("startup accepted duplicate restoration identity: %v", err)
	}
}

func TestM30GlobalRecoveryUsersAreCanonicalAndIndependentOfLoadOrder(t *testing.T) {
	initial, principals := m01Fixture()
	at := initial.Board.CreatedAt
	custom := user{ID: "10000000-0000-4000-8000-000000000004", Revision: 1, CreatedAt: canonicalTime(at.Add(-time.Hour)),
		Label: "Shared author", Kind: "human", Color: "#112233"}
	members := append(slices.Clone(principals), custom)
	newFixture := func(id string) snapshot {
		value := freshSnapshot(id, "Shared user recovery", defaultWorkflow(id, at, members), m01HumanID, at)
		value.Board.DefaultOwnerID = m01BotID
		return value
	}
	withoutCustom := func(value snapshot) snapshot {
		value = cloneSnapshot(value)
		value.Users = slices.DeleteFunc(value.Users, func(item user) bool { return item.ID == custom.ID })
		value.Board.UserIDs = slices.DeleteFunc(value.Board.UserIDs, func(id string) bool { return id == custom.ID })
		value.Policy.CreateGrants = slices.DeleteFunc(value.Policy.CreateGrants, func(g createGrant) bool { return g.UserID == custom.ID })
		value.Policy.MoveGrants = slices.DeleteFunc(value.Policy.MoveGrants, func(g moveGrant) bool { return g.UserID == custom.ID })
		value.Policy.ResourceGrants = slices.DeleteFunc(value.Policy.ResourceGrants, func(g resourceGrant) bool { return g.UserID == custom.ID })
		return value
	}
	root := t.TempDir()
	s, err := openStore(root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	first := newFixture(initial.Board.ID)
	if err := s.create(first); err != nil {
		t.Fatal(err)
	}
	source := withoutCustom(first)
	_, err = s.recoverSnapshot(first.Board.ID, custom.ID, newOperationID(), matchingSnapshotTag(snapshotRepresentationTag(first)), source, nil, nil)
	if !errors.Is(err, errIdentityConflict) {
		t.Fatalf("orphaned custom user result %v", err)
	}
	second := newFixture("20000000-0000-4000-8000-000000000001")
	if err := s.create(second); err != nil {
		t.Fatal(err)
	}
	firstResult, err := s.recoverSnapshot(first.Board.ID, custom.ID, newOperationID(), matchingSnapshotTag(snapshotRepresentationTag(first)), source, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(firstResult.Board.UserIDs, custom.ID) {
		t.Fatal("historical actor still belongs to first board")
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	freshPrincipals := slices.Clone(principals)
	for index := range freshPrincipals {
		freshPrincipals[index].CreatedAt = canonicalTime(at.Add(time.Hour))
	}
	reopened, err := openStore(root, freshPrincipals...)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	current, _ := reopened.accepted(first.Board.ID)
	legacy := cloneSnapshot(source)
	legacy.Users[0].Label = "historical user label"
	recovered, err := reopened.recoverSnapshot(first.Board.ID, m01HumanID, newOperationID(), matchingSnapshotTag(snapshotRepresentationTag(current)), legacy, nil, nil)
	if err != nil || !reflect.DeepEqual(recovered.Users, source.Users) {
		t.Fatalf("global metadata changed after restart: %v", err)
	}
	second, _ = reopened.accepted(second.Board.ID)
	_, err = reopened.recoverSnapshot(second.Board.ID, m01HumanID, newOperationID(), matchingSnapshotTag(snapshotRepresentationTag(second)), withoutCustom(second), nil, nil)
	if !errors.Is(err, errIdentityConflict) {
		t.Fatalf("lost later-board actor definition: %v", err)
	}
}
