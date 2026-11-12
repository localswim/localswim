package v2

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

var errBackupIntegrity = errors.New("backup integrity verification failed")
var errBackupExists = errors.New("backup already exists")

type backup struct {
	ID            string   `json:"id"`
	BoardID       string   `json:"board_id"`
	BoardRevision uint64   `json:"board_revision"`
	SHA256        string   `json:"sha2_256"`
	Size          int64    `json:"size"`
	AttachmentIDs []string `json:"attachment_ids"`
}

type backupCapture struct {
	state snapshot
	data  []byte
}

// Copy the accepted revision and bounded durable bytes under the same admission
// lock. Close the native file before releasing it so Windows can replace the
// snapshot during subsequent edits. Parsing, audit validation and all attachment
// verification and archive work happen after unlock.
func (s *store) captureBackupSnapshot(boardID, actor string, expected uint64) (backupCapture, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(boardID); err != nil {
		return backupCapture{}, err
	}
	state, ok := s.boards[boardID]
	if !ok || !slices.Contains(state.Board.UserIDs, actor) {
		return backupCapture{}, errMissing
	}
	if !state.can(actor, "backup") {
		return backupCapture{}, errForbidden
	}
	if expected != state.Board.Revision {
		return backupCapture{}, errStale
	}
	return s.captureSnapshotLocked(state)
}

func (s *store) captureSnapshotLocked(state snapshot) (backupCapture, error) {
	boardID := state.Board.ID
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return backupCapture{}, errBackupIntegrity
	}
	defer root.Close()
	boards, err := inspectAttachmentDirectory(root, "boards")
	if err != nil {
		return backupCapture{}, errBackupIntegrity
	}
	defer boards.Close()
	directory, err := boards.Stat(".")
	if err != nil || !os.SameFile(directory, s.boardsDirInfo) {
		return backupCapture{}, errBackupIntegrity
	}
	file, err := openBackupFile(boards, boardID+".json", maxSnapshotBytes)
	if err != nil {
		return backupCapture{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	closeErr := file.Close()
	if errors.Join(readErr, closeErr) != nil || len(data) > maxSnapshotBytes {
		return backupCapture{}, errBackupIntegrity
	}
	return backupCapture{state: cloneSnapshot(state), data: data}, nil
}

func (captured backupCapture) verify() (snapshot, []byte, error) {
	state, err := parseSnapshot(captured.data, captured.state.Board.ID)
	if err != nil || !reflect.DeepEqual(state, captured.state) {
		return snapshot{}, nil, errBackupIntegrity
	}
	return state, captured.data, nil
}
func openBackupFile(root *os.Root, name string, limit int64) (*os.File, error) {
	named, err := root.Lstat(name)
	if err != nil || !named.Mode().IsRegular() || named.Size() > limit {
		return nil, errBackupIntegrity
	}
	file, err := openBuildFile(root, name)
	if err != nil {
		return nil, errBackupIntegrity
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(named, opened) {
		_ = file.Close()
		return nil, errBackupIntegrity
	}
	return file, nil
}

// buildBackup publishes metadata only after the complete verified archive is synced.
// The directory reservation makes concurrent requests with the same ID conflict.
func buildBackup(rootPath, id string, state snapshot, data []byte) (backup, error) {
	return buildBackupWithPublisher(rootPath, id, state, data, publishBackupManifest)
}

func buildBackupWithPublisher(rootPath, id string, state snapshot, data []byte,
	publish func(*os.Root, *os.File) error) (backup, error) {
	if !validSnapshotOperationID(id) {
		return backup{}, errBackupIntegrity
	}
	item := backup{ID: id, BoardID: state.Board.ID, BoardRevision: state.Board.Revision,
		AttachmentIDs: []string{}}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return backup{}, errBackupIntegrity
	}
	defer root.Close()
	if err := root.Mkdir("backups", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return backup{}, errBackupIntegrity
	}
	backups, err := inspectAttachmentDirectory(root, "backups")
	if err != nil {
		return backup{}, errBackupIntegrity
	}
	defer backups.Close()
	if err := syncAttachmentDirectory(root); err != nil {
		return backup{}, errBackupIntegrity
	}
	if err := backups.Mkdir(id, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return backup{}, errBackupExists
		}
		return backup{}, errBackupIntegrity
	}
	dir, err := inspectAttachmentDirectory(backups, id)
	if err != nil {
		return backup{}, errBackupIntegrity
	}
	complete := false
	defer func() {
		if !complete {
			_ = dir.Remove("backup.json")
			_ = dir.Remove("pending.json")
			_ = dir.Remove("content.zip")
		}
		_ = dir.Close()
		if !complete {
			_ = backups.Remove(id)
		}
	}()
	if err := syncAttachmentDirectory(backups); err != nil {
		return backup{}, errBackupIntegrity
	}
	file, err := dir.OpenFile("content.zip", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return backup{}, errBackupIntegrity
	}
	defer file.Close()
	hash := sha256.New()
	archive := zip.NewWriter(io.MultiWriter(file, hash))
	if err := writeBackupEntry(archive, "boards/"+state.Board.ID+".json", data); err != nil {
		return backup{}, errBackupIntegrity
	}
	attachments := slices.Clone(state.Attachments)
	slices.SortFunc(attachments, func(a, b attachment) int {
		return strings.Compare(attachmentPath(a), attachmentPath(b))
	})
	for _, attachment := range attachments {
		data, err := readVerifiedAttachment(rootPath, attachment)
		if err != nil {
			return backup{}, errBackupIntegrity
		}
		if err := writeBackupEntry(archive, filepath.ToSlash(attachmentPath(attachment)), data); err != nil {
			return backup{}, errBackupIntegrity
		}
		item.AttachmentIDs = append(item.AttachmentIDs, attachment.ID)
	}
	if err := archive.Close(); err != nil {
		return backup{}, errBackupIntegrity
	}
	if err := file.Sync(); err != nil {
		return backup{}, errBackupIntegrity
	}
	info, err := file.Stat()
	if err != nil {
		return backup{}, errBackupIntegrity
	}
	item.Size, item.SHA256 = info.Size(), hex.EncodeToString(hash.Sum(nil))
	if err := file.Close(); err != nil {
		return backup{}, errBackupIntegrity
	}
	metadata, err := marshalCanonical(item)
	if err != nil {
		return backup{}, errBackupIntegrity
	}
	manifest, err := dir.OpenFile("pending.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return backup{}, errBackupIntegrity
	}
	defer manifest.Close()
	_, writeErr := manifest.Write(metadata)
	syncErr := manifest.Sync()
	if errors.Join(writeErr, syncErr) != nil {
		return backup{}, errBackupIntegrity
	}
	if err := syncAttachmentDirectory(dir); err != nil {
		return backup{}, errBackupIntegrity
	}
	// Keep the original writable manifest handle through the rooted rename.
	// Windows flushes that same file's renamed metadata; POSIX flushes the
	// containing directory. Neither publication step resolves an absolute path.
	if err := publish(dir, manifest); err != nil {
		if errors.Is(err, errWriteUncertain) {
			complete = true
			return backup{}, errWriteUncertain
		}
		return backup{}, errBackupIntegrity
	}
	complete = true
	if err := manifest.Close(); err != nil {
		return backup{}, errWriteUncertain
	}
	return item, nil
}

func publishBackupManifest(dir *os.Root, manifest *os.File) error {
	if err := dir.Rename("pending.json", "backup.json"); err != nil {
		// POSIX permits an uncertain destination after an I/O error.
		if errors.Is(err, syscall.EIO) {
			return errors.Join(errWriteUncertain, err)
		}
		return err
	}
	var err error
	if runtime.GOOS == "windows" {
		// File.Sync calls FlushFileBuffers. Microsoft specifies flushing the
		// file to persist cached metadata, including a rename. The retained
		// rooted handle identifies the exact manifest across the rename.
		err = manifest.Sync()
	} else {
		err = syncAttachmentDirectory(dir)
	}
	if err != nil {
		return errors.Join(errWriteUncertain, err)
	}
	return nil
}
func writeBackupEntry(archive *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(0o600)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func backupDirectory(rootPath, id string) (*os.Root, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, errBackupIntegrity
	}
	defer root.Close()
	if _, err := root.Lstat("backups"); errors.Is(err, os.ErrNotExist) {
		return nil, errMissing
	}
	backups, err := inspectAttachmentDirectory(root, "backups")
	if err != nil {
		return nil, errBackupIntegrity
	}
	defer backups.Close()
	if _, err := backups.Lstat(id); errors.Is(err, os.ErrNotExist) {
		return nil, errMissing
	}
	return inspectAttachmentDirectory(backups, id)
}

func readBackup(rootPath, id string) (backup, error) {
	dir, err := backupDirectory(rootPath, id)
	if err != nil {
		return backup{}, err
	}
	defer dir.Close()
	file, err := openBackupFile(dir, "backup.json", maxSnapshotBytes)
	if err != nil {
		return backup{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	var item backup
	if err != nil || len(data) > maxSnapshotBytes || unmarshalCanonical(data, &item) != nil {
		return backup{}, errBackupIntegrity
	}
	canonical, err := marshalCanonical(item)
	if err != nil || !matchesSnapshotJSON(data, canonical) || item.ID != id || !validSnapshotOperationID(item.ID) ||
		!validSnapshotID(item.BoardID) || item.BoardRevision == 0 || item.BoardRevision > maxSnapshotInteger ||
		item.Size <= 0 || item.Size > maxSnapshotInteger ||
		len(item.SHA256) != 64 || strings.ToLower(item.SHA256) != item.SHA256 || item.AttachmentIDs == nil {
		return backup{}, errBackupIntegrity
	}
	if _, err := hex.DecodeString(item.SHA256); err != nil {
		return backup{}, errBackupIntegrity
	}
	seen := make(map[string]bool, len(item.AttachmentIDs))
	for _, attachmentID := range item.AttachmentIDs {
		if !validSnapshotID(attachmentID) || seen[attachmentID] {
			return backup{}, errBackupIntegrity
		}
		seen[attachmentID] = true
	}
	return item, nil
}

// Verify into a private file so content mutations during or after verification
// cannot change the bytes sent to the caller. Memory use is independent of size.
func copyVerifiedBackup(rootPath string, item backup) (*os.File, error) {
	dir, err := backupDirectory(rootPath, item.ID)
	if err != nil {
		return nil, errBackupIntegrity
	}
	defer dir.Close()
	file, err := openBackupFile(dir, "content.zip", item.Size)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	copy, err := os.CreateTemp("", "localswim-backup-*")
	if err != nil {
		return nil, errBackupIntegrity
	}
	complete := false
	defer func() {
		if !complete {
			_ = copy.Close()
			_ = os.Remove(copy.Name())
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(copy, hash), io.LimitReader(file, item.Size+1))
	if err != nil || n != item.Size || hex.EncodeToString(hash.Sum(nil)) != item.SHA256 {
		return nil, errBackupIntegrity
	}
	if _, err := copy.Seek(0, io.SeekStart); err != nil {
		return nil, errBackupIntegrity
	}
	complete = true
	return copy, nil
}

func (x *request) createBackup() {
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	if err := rejectFields(obj, []string{"id", "board_id", "expected_board_etag"}, false); err != nil {
		x.fail(err)
		return
	}
	boardID, err := inputID(obj["board_id"], "board_id")
	if err != nil {
		x.fail(err)
		return
	}
	id := newOperationID()
	if raw, ok := obj["id"]; ok {
		id, err = inputOperationID(raw, "id")
		if err != nil {
			x.fail(err)
			return
		}
	}
	if _, ok := obj["expected_board_etag"]; !ok {
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
	if !state.can(x.actor, "backup") {
		x.fail(errForbidden)
		return
	}
	if !x.useBoard(boardID) {
		return
	}
	captured, err := x.service.store.captureBackupSnapshot(boardID, x.actor, expectedBoardRevision(expected, boardID))
	if err != nil {
		x.backupFailure(err)
		return
	}
	state, data, err := captured.verify()
	if err != nil {
		x.backupFailure(err)
		return
	}
	item, err := buildBackup(x.service.store.root, id, state, data)
	if err != nil {
		x.backupFailure(err)
		return
	}
	x.w.Header().Set("Location", Prefix+"/backups/"+id)
	x.respond(http.StatusCreated, item, "")
}

func (x *request) authorizedBackup(id string) (backup, error) {
	item, err := readBackup(x.service.store.root, id)
	if err != nil {
		return backup{}, err
	}
	state, ok := x.service.store.accepted(item.BoardID)
	if !ok || !state.can(x.actor, "read") {
		return backup{}, errMissing
	}
	return item, nil
}

func (x *request) backupResource(id string) {
	item, err := x.authorizedBackup(id)
	if err != nil {
		x.backupFailure(err)
		return
	}
	x.respond(http.StatusOK, item, "")
}

func (x *request) backupContent(id string) {
	var conditions []conditionTag
	if len(x.r.Header.Values("If-None-Match")) != 0 {
		var ok bool
		if conditions, ok = parseTagList(x.r.Header.Get("If-None-Match")); !ok {
			x.fail(invalid("If-None-Match", "Expected an entity tag list."))
			return
		}
	}
	if !acceptsMedia(x.r.Header.Values("Accept"), "application", "zip") {
		x.problem(406, "not_acceptable", "The backup content type is not accepted.", "", nil)
		return
	}
	item, err := x.authorizedBackup(id)
	if err != nil {
		x.backupFailure(err)
		return
	}
	if !x.useBoard(item.BoardID) {
		return
	}
	file, err := copyVerifiedBackup(x.service.store.root, item)
	if err != nil {
		x.backupFailure(err)
		return
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	tag := "\"" + item.SHA256 + "\""
	x.w.Header().Set("Cache-Control", "private, no-cache")
	x.w.Header().Set("ETag", tag)
	for _, condition := range conditions {
		if condition.opaque == "*" || condition.opaque == tag {
			x.w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	x.w.Header().Set("Content-Type", "application/zip")
	x.w.Header().Set("Content-Disposition", attachmentDisposition("attachment", id+".zip"))
	x.w.Header().Set("Content-Length", strconv.FormatInt(item.Size, 10))
	x.w.WriteHeader(http.StatusOK)
	if x.r.Method != http.MethodHead {
		_, _ = io.Copy(x.w, file)
	}
}

func (x *request) backupFailure(err error) {
	switch {
	case errors.Is(err, errWriteUncertain):
		x.problem(503, "outcome_unknown", "Inspect the backup by its original ID before attempting another capture.", "unknown", nil)
	case errors.Is(err, errBackupExists):
		x.problem(409, "conflict", "The backup ID already exists.", "", nil)
	case errors.Is(err, errMissing), errors.Is(err, errForbidden), errors.Is(err, errStale), errors.Is(err, errWriteFenced):
		x.fail(err)
	default:
		x.problem(503, "unavailable", "Durable backup content is unavailable.", "", nil)
	}
}
