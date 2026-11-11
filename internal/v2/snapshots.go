package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	recoveryField     = "snapshot"
	recoveryOperation = "restore"
)

type recoveryEvidence struct {
	SourceRevision   uint64  `json:"source_revision"`
	PreviousRevision uint64  `json:"previous_revision"`
	BackupID         *string `json:"backup_id"`
	ActorKind        string  `json:"actor_kind"`
}

type restoration struct {
	ID       string    `json:"id"`
	BoardID  string    `json:"board_id"`
	BackupID string    `json:"backup_id"`
	At       time.Time `json:"at"`
	Revision uint64    `json:"revision"`
}

func validRecoveryAudit(event activity, boardID string, sourceRevision uint64, member user) bool {
	if event.Operation != recoveryOperation || event.ResourceType != "board" || event.ResourceID != boardID ||
		snapshotString(event.Field) != recoveryField || event.Before == nil || event.After == nil ||
		!validSnapshotID(event.ActorID) {
		return false
	}
	evidence, ok := recoveryEventEvidence(event)
	if !ok {
		return false
	}
	canonical, err := marshalCanonical(evidence)
	return err == nil && bytes.Equal(canonical, []byte(*event.After)) &&
		evidence.SourceRevision == sourceRevision && evidence.SourceRevision > 0 &&
		evidence.PreviousRevision > 0 && evidence.PreviousRevision < maxSnapshotInteger &&
		(evidence.ActorKind == "human" || evidence.ActorKind == "bot") &&
		(member.ID == "" || member.Kind == evidence.ActorKind) &&
		event.Revision == max(evidence.SourceRevision, evidence.PreviousRevision)+1 &&
		*event.Before == strconv.FormatUint(evidence.PreviousRevision, 10) &&
		(evidence.BackupID == nil || validSnapshotOperationID(*evidence.BackupID))
}

// Recovery replaces board-owned state while reusing the current global user
// definitions. Unknown users are created only by the workspace user operation.
func (s *store) recoveryUsers(value *snapshot) error {
	known := s.globalUsers(nil)
	for index, member := range value.Users {
		current, exists := known[member.ID]
		if !exists || current.Kind != member.Kind {
			return errIdentityConflict
		}
		value.Users[index] = current
	}
	return nil
}

func (s *store) recoverSnapshot(boardID, actor, eventID string, matches func(snapshot) bool, candidate snapshot,
	backupID *string, attachmentData func(attachment) ([]byte, error)) (snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validSnapshotOperationID(eventID) || (backupID != nil && !validSnapshotOperationID(*backupID)) {
		return snapshot{}, invalid("id", "Expected a UUIDv7.")
	}
	if err := s.writable(); err != nil {
		return snapshot{}, err
	}
	current, exists := s.boards[boardID]
	if !exists || !slices.Contains(current.Board.UserIDs, actor) {
		return snapshot{}, errMissing
	}
	if !current.can(actor, "restore") {
		return snapshot{}, errForbidden
	}
	// Use the same durable representation exported by GET. If disk is
	// damaged or absent, only the accepted snapshot validator can recover it.
	path := s.snapshotPath(boardID)
	if err := s.checkBoardsDirectory(filepath.Dir(path)); err != nil {
		return snapshot{}, err
	}
	durable, durableErr := readSnapshot(path, boardID)
	representation := current
	if backupID == nil {
		if durableErr == nil {
			representation = durable
		}
	}
	if !matches(representation) {
		return snapshot{}, errStale
	}
	if err := validateSnapshot(candidate, boardID); err != nil {
		return snapshot{}, err
	}
	if candidate.Board.Availability != current.Board.Availability {
		return snapshot{}, invalid("board.availability", "Use PATCH to change board availability.")
	}
	if !samePublishingSettings(current.Board.Publishing, candidate.Board.Publishing) {
		return snapshot{}, invalid("board.publishing", "Use PATCH to reconcile publishing settings before restoring this snapshot.")
	}
	for _, other := range s.boards {
		if snapshotContainsIdentity(other, map[string]bool{eventID: true}) {
			return snapshot{}, errIdentityConflict
		}
	}
	if snapshotContainsIdentity(candidate, map[string]bool{eventID: true}) {
		return snapshot{}, errIdentityConflict
	}
	next := cloneSnapshot(candidate)
	if err := s.recoveryUsers(&next); err != nil {
		return snapshot{}, err
	}
	// Board membership can roll back while global user identities stay available.
	// Custom users currently have no separate durable definition.
	futureUsers := s.globalUsers(&next)
	for _, member := range current.Users {
		if _, exists := futureUsers[member.ID]; !exists {
			return snapshot{}, errIdentityConflict
		}
	}
	if err := s.checkIdentities(next, false); err != nil {
		return snapshot{}, err
	}
	if err := recoveryCommentsCompatible(current.Comments, next.Comments); err != nil {
		return snapshot{}, err
	}
	if durableErr == nil {
		if err := recoveryCommentsCompatible(durable.Comments, next.Comments); err != nil {
			return snapshot{}, err
		}
	}
	previousRevision := current.Board.Revision
	if durableErr == nil {
		previousRevision = max(previousRevision, durable.Board.Revision)
	}
	if max(previousRevision, candidate.Board.Revision) >= maxSnapshotInteger {
		return snapshot{}, invalid("board.revision", "Board revision is exhausted.")
	}
	if next.Board.CreatedAt != current.Board.CreatedAt {
		return snapshot{}, invalid("board.created_at", "Recovery must retain the board creation identity.")
	}
	now := maxCardTime(canonicalTime(time.Now()), current.Board.UpdatedAt)
	now = maxCardTime(now, candidate.Board.UpdatedAt)
	next.Board.Revision = max(previousRevision, candidate.Board.Revision) + 1
	next.Board.UpdatedAt = now
	restampRecoveryVersions(&next)
	actorIndex := slices.IndexFunc(current.Users, func(member user) bool { return member.ID == actor })
	evidence, err := marshalCanonical(recoveryEvidence{candidate.Board.Revision, previousRevision, backupID, current.Users[actorIndex].Kind})
	if err != nil {
		return snapshot{}, err
	}
	before, after := strconv.FormatUint(previousRevision, 10), string(evidence)
	field := recoveryField
	next.Activity = append(next.Activity, activity{ID: eventID, BoardID: boardID,
		Revision: next.Board.Revision, At: now, ActorID: actor, Operation: recoveryOperation,
		ResourceType: "board", ResourceID: boardID, Field: &field, Before: &before, After: &after})
	next.Board.Attention, next.Board.LastCardEditAt = deriveAttention(next)
	if err := validateSnapshot(next, boardID); err != nil {
		return snapshot{}, err
	}
	data, err := marshalCanonical(next)
	if err != nil || len(data) > maxSnapshotBytes {
		return snapshot{}, errTooBig
	}
	// Verify every file before publishing any new bytes. The archive reader has
	// already verified the private ZIP, including detached attachments.
	for _, item := range next.Attachments {
		if err := recoveryAttachmentCompatible(current, item); err != nil {
			return snapshot{}, err
		}
		if durableErr == nil {
			if err := recoveryAttachmentCompatible(durable, item); err != nil {
				return snapshot{}, err
			}
		}
		if attachmentData == nil {
			if _, err := readVerifiedAttachment(s.root, item); err != nil {
				return snapshot{}, errAttachmentIntegrity
			}
		}
	}
	published := []*publishedAttachment{}
	committed := false
	defer func() {
		for _, file := range published {
			if !committed {
				file.discard()
			}
			_ = file.dir.Close()
		}
	}()
	if attachmentData != nil {
		for _, item := range next.Attachments {
			if _, err := readVerifiedAttachment(s.root, item); err == nil {
				continue
			}
			data, err := attachmentData(item)
			if err != nil {
				return snapshot{}, err
			}
			file, err := publishRecoveryAttachment(s.root, item, data)
			if file != nil {
				published = append(published, file)
			}
			if err != nil {
				return snapshot{}, err
			}
		}
	}
	if err := s.commit(next, false, nil); err != nil {
		committed = errors.Is(err, errWriteUncertain)
		return snapshot{}, err
	}
	committed = true
	return cloneSnapshot(next), nil
}

func recoveryAttachmentCompatible(current snapshot, item attachment) error {
	for _, previous := range current.Attachments {
		if previous.ID == item.ID && (previous.CardID != item.CardID || previous.BoardID != item.BoardID ||
			previous.Size != item.Size || previous.SHA256 != item.SHA256 || previous.MediaType != item.MediaType ||
			previous.OriginalName != item.OriginalName || previous.AddedBy != item.AddedBy ||
			!previous.CreatedAt.Equal(item.CreatedAt)) {
			return errIdentityConflict
		}
	}
	return nil
}

// Existing verified immutable files are reused. A damaged file is repaired only
// with fully verified bytes for the same immutable identity. New files retain
// the ordinary upload cleanup rule until the single snapshot publication.
func publishRecoveryAttachment(root string, item attachment, data []byte) (*publishedAttachment, error) {
	if !attachmentBytesMatch(item, data) {
		return nil, errAttachmentIntegrity
	}
	path := filepath.Join(root, attachmentPath(item))
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return publishAttachment(root, item, data)
	}
	parent, err := os.OpenRoot(root)
	if err != nil {
		return nil, errAttachmentIntegrity
	}
	defer parent.Close()
	for _, name := range []string{"attachments", item.BoardID, item.CardID} {
		child, err := inspectAttachmentDirectory(parent, name)
		if err != nil {
			return nil, errAttachmentIntegrity
		}
		defer child.Close()
		parent = child
	}
	named, err := parent.Lstat(item.ID)
	if err != nil || !named.Mode().IsRegular() {
		return nil, errAttachmentIntegrity
	}
	temp := ".recovery-" + newID()
	file, err := parent.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errAttachmentIntegrity
	}
	defer func() { _ = file.Close(); _ = parent.Remove(temp) }()
	_, writeErr := file.Write(data)
	if errors.Join(writeErr, file.Sync()) != nil {
		return nil, errAttachmentIntegrity
	}
	if err := parent.Rename(temp, item.ID); err != nil {
		return nil, errAttachmentIntegrity
	}
	if err := flushRecoveryAttachment(parent, file); err != nil {
		return nil, errAttachmentIntegrity
	}
	if _, err := readVerifiedAttachment(root, item); err != nil {
		return nil, errAttachmentIntegrity
	}
	return nil, nil
}

func (x *request) admitSnapshot(id string) {
	expected, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	state, ok := x.service.store.accepted(id)
	if !ok || !slices.Contains(state.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !state.can(x.actor, "restore") {
		x.fail(errForbidden)
		return
	}
	if !x.useBoard(id) {
		return
	}
	mediaType, params, err := mime.ParseMediaType(x.r.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/json" && mediaType != "application/zip") ||
		(params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		x.problem(http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Use application/json with UTF-8 or application/zip.", "", nil)
		return
	}
	var candidate snapshot
	var attachmentData func(attachment) ([]byte, error)
	if mediaType == "application/zip" {
		file, err := os.CreateTemp("", "localswim-recovery-*")
		if err != nil {
			x.recoveryFailure(err)
			return
		}
		defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
		if _, err := io.Copy(file, http.MaxBytesReader(x.w, x.r.Body, maxSnapshotBytes)); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				x.fail(errTooBig)
			} else {
				x.fail(invalid("body", "Unreadable recovery archive."))
			}
			return
		}
		archive, err := inspectBackupArchive(file, id)
		if err != nil {
			x.fail(invalid("body", "The native backup archive failed validation."))
			return
		}
		candidate, attachmentData = archive.state, archive.attachmentData
	} else {
		obj, err := x.body()
		if err != nil {
			x.fail(err)
			return
		}
		data, err := marshalCanonical(obj)
		if err == nil {
			data, err = normalizeSnapshotInput(data)
		}
		if err == nil {
			candidate, err = parseSnapshot(data, id)
		}
		if err != nil {
			x.fail(invalid("body", "The native snapshot failed validation."))
			return
		}
	}
	updated, err := x.service.store.recoverSnapshot(id, x.actor, newOperationID(), matchingSnapshotTag(expected), candidate, nil, attachmentData)
	if err != nil {
		x.recoveryFailure(err)
		return
	}
	x.w.Header().Set("X-Board-ETag", updated.Board.entityTag())
	x.respond(http.StatusOK, updated.Board, "")
}

func (x *request) restoreBackup() {
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	if err := rejectFields(obj, []string{"id", "board_id", "backup_id", "expected_board_etag"}, true); err != nil {
		x.fail(err)
		return
	}
	boardID, err := inputID(obj["board_id"], "board_id")
	if err != nil {
		x.fail(err)
		return
	}
	backupID, err := inputOperationID(obj["backup_id"], "backup_id")
	if err != nil {
		x.fail(err)
		return
	}
	id := newOperationID()
	if raw, exists := obj["id"]; exists {
		id, err = inputOperationID(raw, "id")
		if err != nil {
			x.fail(err)
			return
		}
	}
	if _, exists := obj["expected_board_etag"]; !exists {
		x.problem(428, "precondition_required", "expected_board_etag is required.", "", nil)
		return
	}
	expected, err := textField(obj["expected_board_etag"], "expected_board_etag", 1, 130)
	if err != nil || !snapshotETagPattern.MatchString(expected) {
		x.fail(invalid("expected_board_etag", "Expected one canonical entity tag."))
		return
	}
	state, exists := x.service.store.accepted(boardID)
	if !exists || !slices.Contains(state.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !state.can(x.actor, "restore") {
		x.fail(errForbidden)
		return
	}
	if !x.useBoard(boardID) {
		return
	}
	item, err := readBackup(x.service.store.root, backupID)
	if err != nil {
		x.recoveryFailure(err)
		return
	}
	if item.BoardID != boardID {
		x.fail(invalid("backup_id", "The backup must belong to the target board."))
		return
	}
	archive, err := openVerifiedBackupArchive(x.service.store.root, item)
	if err != nil {
		x.recoveryFailure(err)
		return
	}
	defer archive.close()
	updated, err := x.service.store.recoverSnapshot(boardID, x.actor, id, matchingBoardTag(expected),
		archive.state, &backupID, archive.attachmentData)
	if err != nil {
		x.recoveryFailure(err)
		return
	}
	x.w.Header().Set("X-Board-ETag", updated.Board.entityTag())
	x.w.Header().Set("Location", Prefix+"/restorations/"+id)
	x.respond(http.StatusCreated, restoration{id, boardID, backupID, updated.Board.UpdatedAt,
		updated.Board.Revision}, "")
}

func (x *request) recoveryFailure(err error) {
	switch {
	case errors.Is(err, errBackupIntegrity), errors.Is(err, errAttachmentIntegrity):
		x.problem(503, "unavailable", "Verified recovery content is unavailable.", "", nil)
	default:
		x.fail(err)
	}
}

// Normalize only identity-bearing native fields. User text and
// opaque strings remain byte-for-byte user content.
func normalizeSnapshotInput(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	normalizeSnapshotIDs(value)
	if events, ok := value["activity"].([]any); ok {
		for _, raw := range events {
			event, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			field, _ := event["field"].(string)
			for _, key := range []string{"before", "after"} {
				text, ok := event[key].(string)
				if !ok {
					continue
				}
				if strings.HasSuffix(field, "_id") {
					if id, valid := normalizeID(text); valid {
						event[key] = id
					}
				}
				resource, _ := event["resource_type"].(string)
				operation, _ := event["operation"].(string)
				if resource == "policy" || resource == "relationship" ||
					(resource == "attachment" && operation == "attach" && key == "after") ||
					(resource == "board" && operation == recoveryOperation && key == "after") {
					if _, err := decodeObject([]byte(text)); err != nil {
						return nil, err
					}
					var nested map[string]any
					input := json.NewDecoder(strings.NewReader(text))
					input.UseNumber()
					if input.Decode(&nested) == nil {
						normalizeSnapshotIDs(nested)
						encoded, err := marshalCanonical(nested)
						if err != nil {
							return nil, err
						}
						event[key], err = normalizedAuditPayload(resource, encoded)
						if err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	return marshalCanonical(value)
}

func normalizeSnapshotIDs(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "id" || strings.HasSuffix(key, "_id") {
				if text, ok := child.(string); ok {
					if id, valid := normalizeID(text); valid {
						value[key] = id
					} else if id, valid := normalizeOperationID(text); valid {
						value[key] = id
					}
				}
			}
			if strings.HasSuffix(key, "_ids") {
				if ids, ok := child.([]any); ok {
					for index, raw := range ids {
						if text, ok := raw.(string); ok {
							if id, valid := normalizeID(text); valid {
								ids[index] = id
							}
						}
					}
				}
			}
			normalizeSnapshotIDs(child)
		}
	case []any:
		for _, child := range value {
			normalizeSnapshotIDs(child)
		}
	}
}

func normalizedAuditPayload(resource string, data []byte) (string, error) {
	switch resource {
	case "board":
		var value recoveryEvidence
		if err := unmarshalCanonical(data, &value); err != nil {
			return "", err
		}
		canonical, err := marshalCanonical(value)
		if err != nil || !matchesSnapshotJSON(data, canonical) {
			return "", errInvalidSnapshot
		}
		return string(canonical), nil
	case "attachment":
		var value attachment
		if err := unmarshalCanonical(data, &value); err != nil {
			return "", err
		}
		canonical, err := marshalCanonical(value)
		if err != nil || !matchesSnapshotJSON(data, canonical) {
			return "", errInvalidSnapshot
		}
		return attachmentAuditValue(value), nil
	case "relationship":
		var value relationshipLink
		if err := unmarshalCanonical(data, &value); err != nil {
			return "", err
		}
		canonical, err := marshalCanonical(value)
		if err != nil || !matchesSnapshotJSON(data, canonical) {
			return "", errInvalidSnapshot
		}
		return relationshipAuditValue(value), nil
	case "policy":
		var value struct {
			DefaultPriorityID string          `json:"default_priority_id"`
			CreateGrants      []createGrant   `json:"create_grants"`
			MoveGrants        []moveGrant     `json:"move_grants"`
			ResourceGrants    []resourceGrant `json:"resource_grants"`
		}
		if err := unmarshalCanonical(data, &value); err != nil {
			return "", err
		}
		canonical, err := marshalCanonical(value)
		if err != nil || !matchesSnapshotJSON(data, canonical) {
			return "", errInvalidSnapshot
		}
		return policyAuditValue(policy{DefaultPriorityID: value.DefaultPriorityID, CreateGrants: value.CreateGrants,
			MoveGrants: value.MoveGrants, ResourceGrants: value.ResourceGrants}), nil
	}
	return "", errInvalidSnapshot
}

func flushRecoveryAttachment(dir *os.Root, file *os.File) error {
	if runtime.GOOS == "windows" {
		return file.Sync()
	}
	return syncAttachmentDirectory(dir)
}

func (x *request) restorationResource(id string) {
	for _, accepted := range x.service.store.list() {
		if !accepted.can(x.actor, "read") {
			continue
		}
		state, err := x.service.store.readback(accepted.Board.ID)
		if err != nil {
			x.recoveryFailure(err)
			return
		}
		if !state.can(x.actor, "read") {
			continue
		}
		for _, event := range state.Activity {
			if event.ID != id || event.Operation != recoveryOperation || event.After == nil {
				continue
			}
			evidence, ok := recoveryEventEvidence(event)
			if !ok || evidence.BackupID == nil {
				continue
			}
			x.respond(http.StatusOK, restoration{id, state.Board.ID, *evidence.BackupID,
				event.At, event.Revision}, "")
			return
		}
	}
	x.fail(errMissing)
}

// Refresh mutation validators while retaining the source content timestamps.
func restampRecoveryVersions(next *snapshot) {
	revision := next.Board.Revision
	for i := range next.Cards {
		next.Cards[i].Revision = revision
	}
	for i := range next.Lanes {
		next.Lanes[i].Revision = revision
	}
	for i := range next.Relationships {
		next.Relationships[i].Revision = revision
	}
	for i := range next.Attachments {
		next.Attachments[i].Revision = revision
	}
	next.Policy.Revision = revision
}

// Comments use a fixed revision for their immutable representation.
func recoveryCommentsCompatible(previous, candidate []comment) error {
	if len(previous) == 0 || len(candidate) == 0 {
		return nil
	}
	byID := make(map[string]comment, len(previous))
	for _, item := range previous {
		byID[item.ID] = item
	}
	for _, item := range candidate {
		if old, exists := byID[item.ID]; exists && old != item {
			return errIdentityConflict
		}
	}
	return nil
}

func recoveryEventEvidence(event activity) (recoveryEvidence, bool) {
	var evidence recoveryEvidence
	if event.After == nil || unmarshalCanonical([]byte(*event.After), &evidence) != nil {
		return recoveryEvidence{}, false
	}
	return evidence, true
}
