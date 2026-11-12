package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

type failedRecoveryObservation struct {
	snapshot, reference string
}

// The validator binds explicit recovery to observed disk state in this writer.
// It includes the damaged snapshot and reference fingerprints, never the report time.
func (s *store) failedRecoveryTagLocked(id string) (string, error) {
	if !s.isFailedBoardLocked(id) {
		return "", errMissing
	}
	observed, err := s.observeFailedRecovery(id)
	if err != nil {
		return "", err
	}
	return observed.tag(s.activityWriterID, id), nil
}

func (s *store) observeFailedRecovery(id string) (failedRecoveryObservation, error) {
	if err := s.checkBoardsDirectory(s.boardsPath()); err != nil {
		return failedRecoveryObservation{}, err
	}
	snapshot, err := recoveryFileFingerprint(s.snapshotPath(id))
	if err != nil {
		return failedRecoveryObservation{}, err
	}
	reference, err := recoveryFileFingerprint(s.recoveryReferencePath(id))
	if err != nil {
		return failedRecoveryObservation{}, err
	}
	return failedRecoveryObservation{snapshot, reference}, nil
}

func (o failedRecoveryObservation) tag(writer, id string) string {
	return `"recovery-` + recoveryDigest([]byte(strings.Join([]string{writer, id, o.snapshot, o.reference}, "\n"))) + `"`
}

func recoveryFileFingerprint(path string) (string, error) {
	file, err := readSnapshotFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		// Fresh recovery leaves inaccessible or nonregular source entries in
		// place. Their metadata still binds the explicit failure observation.
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return "", errInvalidSnapshot
		}
		return fmt.Sprintf("unreadable:%d:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano()), nil
	}
	if err := checkNamedSnapshot(path, file); err != nil {
		return "", err
	}
	return recoveryDigest(file.data) + ":" + strconv.FormatInt(file.info.ModTime().UnixNano(), 10), nil
}

func (x *request) recoverFailedBoard(id string) {
	if !x.service.store.canSeeFailedBoard(id, x.actor) {
		x.fail(errMissing)
		return
	}
	mode, err := singleQuery(x.r, "mode")
	if err != nil || (mode != "same_id" && mode != "fresh") {
		x.fail(invalid("mode", "Choose same_id or fresh explicitly."))
		return
	}
	expected, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	media, params, err := mime.ParseMediaType(x.r.Header.Get("Content-Type"))
	if err != nil || (media != "application/json" && media != "application/zip") ||
		(params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		x.problem(http.StatusUnsupportedMediaType, "unsupported_media_type", "Use application/json with UTF-8 or application/zip.", "", nil)
		return
	}
	var candidate snapshot
	var attachmentData func(attachment) ([]byte, error)
	if media == "application/zip" {
		archive, err := x.readUploadedBackup(id)
		if err != nil {
			x.recoveryFailure(err)
			return
		}
		defer archive.close()
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
	result, err := x.service.store.recoverFailed(id, mode, x.actor, expected, candidate, attachmentData)
	if err != nil {
		x.recoveryFailure(err)
		return
	}
	x.w.Header().Set("Location", Prefix+"/boards/"+result.Board.ID)
	x.w.Header().Set("X-Board-ETag", result.Board.entityTag())
	x.respond(http.StatusCreated, result.Board, result.Board.entityTag())
}

// Reservation is scoped to this failed source and its optional new address. It
// keeps other recoveries and ordinary creation out of these paths during I/O.
func (s *store) reserveFailedRecovery(id, destination, actor string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return nil, err
	}
	if actor != humanID || !s.isFailedBoardLocked(id) {
		return nil, errMissing
	}
	if s.recovering[id] || s.recovering[destination] {
		return nil, errIdentityConflict
	}
	if destination != id {
		if _, exists := s.boards[destination]; exists || s.isFailedBoardLocked(destination) {
			return nil, errIdentityConflict
		}
	}
	s.recovering[id], s.recovering[destination] = true, true
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.recovering, id)
		delete(s.recovering, destination)
	}, nil
}

func (s *store) recoverFailed(id, mode, actor, expected string, source snapshot, attachmentData func(attachment) ([]byte, error)) (snapshot, error) {
	if err := validateSnapshot(source, id); err != nil {
		return snapshot{}, err
	}
	if mode != "same_id" && mode != "fresh" {
		return snapshot{}, invalid("mode", "Choose same_id or fresh explicitly.")
	}
	next := cloneSnapshot(source)
	var sourceBoardID *string
	if mode == "fresh" {
		sourceBoardID = &id
		var err error
		next, err = freshRecoverySnapshot(source)
		if err != nil {
			return snapshot{}, err
		}
		originals := make(map[string]attachment, len(next.Attachments))
		for i, item := range next.Attachments {
			originals[item.ID] = source.Attachments[i]
		}
		readSource := attachmentData
		if readSource == nil {
			readSource = func(item attachment) ([]byte, error) { return readVerifiedAttachment(s.root, item) }
		}
		attachmentData = func(item attachment) ([]byte, error) { return readSource(originals[item.ID]) }
	}

	s.recoveryIO.RLock()
	defer s.recoveryIO.RUnlock()
	release, err := s.reserveFailedRecovery(id, next.Board.ID, actor)
	if err != nil {
		return snapshot{}, err
	}
	defer release()
	observed, err := s.observeFailedRecovery(id)
	if err != nil {
		return snapshot{}, err
	}
	// Wildcards cannot stand in for inspecting this failure.
	if expected != observed.tag(s.activityWriterID, id) {
		return snapshot{}, errStale
	}

	previousRevision := source.Board.Revision
	now := maxCardTime(canonicalTime(time.Now()), source.Board.UpdatedAt)
	var trustedContext recoveryContext
	if mode == "same_id" {
		s.mu.RLock()
		context, trusted := s.failedRecoveryContextLocked(id)
		s.mu.RUnlock()
		if !trusted || !slices.Contains(context.RestoreActors, recoveryActor{actor, "human"}) {
			return snapshot{}, errForbidden
		}
		if source.Board.CreatedAt != context.CreatedAt {
			return snapshot{}, errIdentityConflict
		}
		trustedContext = context
		previousRevision = context.RevisionFloor
		now = maxCardTime(now, context.UpdatedAt)
		// Readable state can establish an additional revision and immutable-identity floor.
		path := s.snapshotPath(id)
		file, err := readSnapshotFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return snapshot{}, errInvalidSnapshot
		}
		if err == nil {
			if err := checkNamedSnapshot(path, file); err != nil {
				return snapshot{}, err
			}
			if durable, err := parseSnapshot(file.data, id); err == nil {
				if durable.Board.CreatedAt != context.CreatedAt {
					return snapshot{}, errIdentityConflict
				}
				previousRevision = max(previousRevision, durable.Board.Revision)
				if err := recoveryCommentsCompatible(durable.Comments, next.Comments); err != nil {
					return snapshot{}, err
				}
				for _, item := range next.Attachments {
					if err := recoveryAttachmentCompatible(durable, item); err != nil {
						return snapshot{}, err
					}
				}
			}
		}
	}
	// A quarantined board has no accepted publishing settings to authorize
	// scheduling a worker from source history.
	next.Board.Publishing = publishing{}
	// Existing workspace users retain their canonical presentation. Verified
	// source-only users are admitted with the recovered board.
	s.mu.RLock()
	known := s.globalUsers(nil)
	s.mu.RUnlock()
	for i, member := range next.Users {
		if current, exists := known[member.ID]; exists {
			if current.Kind != member.Kind {
				return snapshot{}, errIdentityConflict
			}
			next.Users[i] = current
		}
	}
	if max(previousRevision, source.Board.Revision) >= maxSnapshotInteger {
		return snapshot{}, invalid("board.revision", "Board revision is exhausted.")
	}
	next.Board.Revision = max(previousRevision, source.Board.Revision) + 1
	next.Board.UpdatedAt = now
	restampRecoveryVersions(&next)
	evidence, err := marshalCanonical(recoveryEvidence{SourceRevision: source.Board.Revision,
		PreviousRevision: previousRevision, ActorKind: "human", SourceBoardID: sourceBoardID, PublishingReset: true})
	if err != nil {
		return snapshot{}, err
	}
	before, after, field := strconv.FormatUint(previousRevision, 10), string(evidence), recoveryField
	next.Activity = append(next.Activity, activity{ID: newOperationID(), BoardID: next.Board.ID,
		Revision: next.Board.Revision, At: now, ActorID: actor, Operation: recoveryOperation,
		ResourceType: "board", ResourceID: next.Board.ID, Field: &field, Before: &before, After: &after})
	next.Board.Attention, next.Board.LastCardEditAt = deriveAttention(next)
	if err := validateSnapshot(next, next.Board.ID); err != nil {
		return snapshot{}, err
	}
	data, err := snapshotCommitBytes(next)
	if err != nil {
		return snapshot{}, err
	}
	s.mu.RLock()
	err = s.checkIdentities(next, false)
	s.mu.RUnlock()
	if err != nil {
		return snapshot{}, err
	}
	// Check all bytes before publishing any attachment or snapshot. The failed
	// address remains reserved while healthy boards serve reads and writes.
	for _, item := range next.Attachments {
		var data []byte
		var err error
		if attachmentData == nil {
			data, err = readVerifiedAttachment(s.root, item)
		} else {
			data, err = attachmentData(item)
		}
		if err != nil || !attachmentBytesMatch(item, data) {
			return snapshot{}, errAttachmentIntegrity
		}
	}
	return s.publishRecoveredSnapshot(next, attachmentData, func() error {
		return s.commitFailedRecovery(id, next, data, observed, trustedContext)
	})
}

func (s *store) commitFailedRecovery(id string, next snapshot, data []byte, observed failedRecoveryObservation, trustedContext recoveryContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A previous unrelated uncertain write cannot make this recovery uncertain:
	// its snapshot has not started publication, so its new bytes can be cleaned.
	if err := s.writable(); err != nil {
		return errInvalidSnapshot
	}
	if !s.isFailedBoardLocked(id) {
		return errStale
	}
	known := s.globalUsers(nil)
	for _, member := range next.Users {
		if current, exists := known[member.ID]; exists && current != member {
			return errIdentityConflict
		}
	}
	if err := s.checkIdentities(next, false); err != nil {
		return err
	}

	checkFailure := func() error {
		current, err := s.observeFailedRecovery(id)
		if err != nil {
			return err
		}
		if current != observed {
			return errStale
		}
		return nil
	}
	if next.Board.ID == id {
		if s.collidesWithHealthyBoardID(id) || !reflect.DeepEqual(s.recoveryContexts[id], trustedContext) {
			return errStale
		}
		return s.writeCommitChecked(next, data, func() error {
			current, err := recoveryFileFingerprint(s.snapshotPath(id))
			if err != nil {
				return err
			}
			if current != observed.snapshot {
				return errStale
			}
			return nil
		}, checkFailure)
	}
	return s.writeCommitChecked(next, data, func() error {
		if err := checkFailure(); err != nil {
			return err
		}
		for _, path := range []string{s.snapshotPath(next.Board.ID), s.recoveryReferencePath(next.Board.ID), s.notificationRoutePath(next.Board.ID)} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return errIdentityConflict
			}
		}
		return nil
	}, nil)
}

// Fresh recovery replaces only typed identities. User text and historical
// provenance such as backup IDs and previous source board IDs stay exact.
func freshRecoverySnapshot(source snapshot) (snapshot, error) {
	ids := map[string]string{}
	add := func(id string) {
		if id != "" && ids[id] == "" {
			if validSnapshotOperationID(id) {
				ids[id] = newOperationID()
			} else {
				ids[id] = newID()
			}
		}
	}
	add(source.Board.ID)
	add(source.Policy.ID)
	for _, item := range source.Lanes {
		add(item.ID)
	}
	for _, item := range source.Priorities {
		add(item.ID)
	}
	for _, item := range source.Cards {
		add(item.ID)
	}
	for _, item := range source.Comments {
		add(item.ID)
	}
	for _, item := range source.Relationships {
		add(item.ID)
	}
	for _, item := range source.Attachments {
		add(item.ID)
	}
	for _, event := range source.Activity {
		add(event.ID)
		if event.ResourceType != "user" {
			add(event.ResourceID)
		}
	}
	data, err := marshalCanonical(source)
	if err != nil {
		return snapshot{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return snapshot{}, err
	}
	remapRecoveryIDs(document, ids)
	events := document["activity"].([]any)
	for _, raw := range events {
		event := raw.(map[string]any)
		field, _ := event["field"].(string)
		resource, _ := event["resource_type"].(string)
		operation, _ := event["operation"].(string)
		for _, key := range []string{"before", "after"} {
			text, ok := event[key].(string)
			if !ok {
				continue
			}
			if strings.HasSuffix(field, "_id") && ids[text] != "" {
				event[key] = ids[text]
			}
			if resource == "policy" || resource == "relationship" || (resource == "attachment" && operation == "attach" && key == "after") {
				var nested map[string]any
				decoder := json.NewDecoder(strings.NewReader(text))
				decoder.UseNumber()
				if err := decoder.Decode(&nested); err != nil {
					return snapshot{}, err
				}
				remapRecoveryIDs(nested, ids)
				encoded, err := marshalCanonical(nested)
				if err != nil {
					return snapshot{}, err
				}
				event[key], err = normalizedAuditPayload(resource, encoded)
				if err != nil {
					return snapshot{}, err
				}
			}
		}
	}
	data, err = marshalCanonical(document)
	if err != nil {
		return snapshot{}, err
	}
	return parseSnapshot(data, ids[source.Board.ID])
}

func remapRecoveryIDs(value any, ids map[string]string) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "source_board_id" || key == "backup_id" {
				continue
			}
			if key == "id" || strings.HasSuffix(key, "_id") {
				if text, ok := child.(string); ok && ids[text] != "" {
					value[key] = ids[text]
				}
			}
			if strings.HasSuffix(key, "_ids") {
				if values, ok := child.([]any); ok {
					for i, raw := range values {
						if text, ok := raw.(string); ok && ids[text] != "" {
							values[i] = ids[text]
						}
					}
					if key == "child_ids" {
						slices.SortFunc(values, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
					}
				}
			}
			remapRecoveryIDs(child, ids)
		}
	case []any:
		for _, child := range value {
			remapRecoveryIDs(child, ids)
		}
	}
}
