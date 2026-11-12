package v2

import (
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"
)

type publication struct {
	ID         string     `json:"id"`
	BoardID    string     `json:"board_id"`
	Workspace  bool       `json:"workspace"`
	State      string     `json:"state"`
	FinishedAt *time.Time `json:"finished_at"`
	BackupID   string     `json:"backup_id"`
	ErrorCode  *string    `json:"error_code"`
}

var errPublicationExists = errors.New("publication already exists")
var errPublicationUnavailable = errors.New("publication unavailable")

func publicationDirectory(rootPath string, create bool) (*os.Root, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, errPublicationUnavailable
	}
	defer root.Close()
	if create {
		if err := root.Mkdir("publications", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, errPublicationUnavailable
		}
		if err := syncAttachmentDirectory(root); err != nil {
			return nil, errPublicationUnavailable
		}
	}
	if _, err := root.Lstat("publications"); errors.Is(err, os.ErrNotExist) {
		return nil, errMissing
	}
	dir, err := inspectAttachmentDirectory(root, "publications")
	if err != nil {
		return nil, errPublicationUnavailable
	}
	return dir, nil
}

// Reserve the caller's operation UUID durably before Git can change any refs.
// A lost response or interrupted process leaves inspectable state and the same
// UUID always conflicts. No accepted operation is automatically replayed.
func savePublication(rootPath string, item publication, create bool) error {
	dir, err := publicationDirectory(rootPath, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	name := item.ID + ".json"
	data, err := marshalCanonical(item)
	if err != nil {
		return errPublicationUnavailable
	}
	pending := ".pending-" + newID()
	file, err := dir.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return errWriteUncertain
	}
	defer file.Close()
	defer dir.Remove(pending)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if err != nil {
		return errWriteUncertain
	}
	// Install the flushed running record without replacing an existing UUID.
	// The record itself reserves the identity, so a pre-install interruption
	// leaves only an ignored pending file and cannot block future admission.
	if create {
		err = dir.Link(pending, name)
		if errors.Is(err, os.ErrExist) {
			return errPublicationExists
		}
	} else {
		err = dir.Rename(pending, name)
	}
	if err != nil {
		return errWriteUncertain
	}
	if runtime.GOOS == "windows" {
		err = file.Sync()
	} else {
		err = syncAttachmentDirectory(dir)
	}
	if errors.Join(err, file.Close()) != nil {
		return errWriteUncertain
	}
	return nil
}

func readPublication(rootPath, id string) (publication, error) {
	dir, err := publicationDirectory(rootPath, false)
	if err != nil {
		return publication{}, err
	}
	defer dir.Close()
	if _, err := dir.Lstat(id + ".json"); errors.Is(err, os.ErrNotExist) {
		return publication{}, errMissing
	}
	file, err := openBackupFile(dir, id+".json", 16*1024)
	if err != nil {
		return publication{}, errPublicationUnavailable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	var item publication
	if err != nil || len(data) > 16*1024 || unmarshalCanonical(data, &item) != nil ||
		item.ID != id || !validSnapshotOperationID(item.ID) || !validSnapshotID(item.BoardID) || !validSnapshotOperationID(item.BackupID) ||
		item.Workspace ||
		!slices.Contains([]string{"running", "succeeded", "failed"}, item.State) {
		return publication{}, errPublicationUnavailable
	}
	canonical, err := marshalCanonical(item)
	if err != nil || !matchesSnapshotJSON(data, canonical) {
		return publication{}, errPublicationUnavailable
	}
	if item.State == "running" && (item.FinishedAt != nil || item.ErrorCode != nil) ||
		item.State != "running" && (item.FinishedAt == nil || !validSnapshotTime(*item.FinishedAt)) ||
		item.State == "succeeded" && item.ErrorCode != nil ||
		item.State == "failed" && (item.ErrorCode == nil || !validPublicationCode(*item.ErrorCode)) {
		return publication{}, errPublicationUnavailable
	}
	return item, nil
}

func validPublicationCode(code string) bool {
	return slices.Contains([]string{"backup_integrity", "repository_unavailable", "repository_lock_unavailable",
		"repository_busy", "unsupported_hooks", "git_configuration_unavailable", "unsupported_fsmonitor",
		"git_attributes_unavailable", "unsupported_filter", "publication_path_ignored", "git_ignore_unavailable",
		"branch_unavailable", "remote_unavailable", "unsupported_remote_transport",
		"publication_target_ambiguous", "publication_destination_unavailable",
		"head_unavailable", "capture_unavailable", "git_index_failed", "git_object_failed", "git_tree_failed",
		"publication_branch_changed", "publication_index_busy", "publication_index_overlap", "publication_index_uncertain",
		"git_commit_failed", "git_ref_failed", "git_push_failed", "publication_failed"}, code)
}

func (x *request) publicationsCollection() {
	if x.r.Method == http.MethodPost {
		x.createPublication()
		return
	}
	filter, err := singleQuery(x.r, "board_id")
	if err != nil {
		x.fail(err)
		return
	}
	if filter != "" {
		var ok bool
		filter, ok = normalizeID(filter)
		if !ok {
			x.fail(invalid("board_id", "Expected a UUIDv4."))
			return
		}
		if state, ok := x.service.store.accepted(filter); !ok || !state.can(x.actor, "read") {
			x.fail(errMissing)
			return
		}
	}
	items := []publication{}
	dir, err := publicationDirectory(x.service.store.root, false)
	if err != nil && !errors.Is(err, errMissing) {
		x.publicationFailure(err)
		return
	}
	if err == nil {
		defer dir.Close()
		folder, err := dir.Open(".")
		if err != nil {
			x.publicationFailure(errPublicationUnavailable)
			return
		}
		entries, err := folder.ReadDir(-1)
		_ = folder.Close()
		if err != nil {
			x.publicationFailure(errPublicationUnavailable)
			return
		}
		for _, entry := range entries {
			id, hasSuffix := strings.CutSuffix(entry.Name(), ".json")
			if !hasSuffix || !validSnapshotOperationID(id) {
				continue
			}
			item, err := readPublication(x.service.store.root, id)
			if err != nil {
				x.publicationFailure(err)
				return
			}
			state, ok := x.service.store.accepted(item.BoardID)
			if ok && state.can(x.actor, "read") && (filter == "" || item.BoardID == filter) {
				items = append(items, item)
			}
		}
	}
	slices.SortFunc(items, func(a, b publication) int { return strings.Compare(a.ID, b.ID) })
	page(x, "publications", items, filter)
}

func (x *request) publicationResource(id string) {
	item, err := readPublication(x.service.store.root, id)
	if err != nil {
		x.publicationFailure(err)
		return
	}
	state, ok := x.service.store.accepted(item.BoardID)
	if !ok || !state.can(x.actor, "read") {
		x.fail(errMissing)
		return
	}
	x.respond(http.StatusOK, item, "")
}

func (x *request) createPublication() {
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	if err := rejectFields(obj, []string{"id", "board_id", "backup_id", "workspace", "remote", "branch"}, true); err != nil {
		x.fail(err)
		return
	}
	if raw, ok := obj["workspace"]; ok && string(raw) != "false" {
		x.fail(invalid("workspace", "Manual board publication requires false."))
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
	var selection publicationSelection
	for _, field := range []struct {
		name  string
		value *string
	}{{"remote", &selection.remote}, {"branch", &selection.branch}} {
		if raw, ok := obj[field.name]; ok {
			text, err := textField(raw, field.name, 1, 1024)
			if err != nil {
				x.fail(err)
				return
			}
			if strings.ContainsAny(text, "\x00\r\n") {
				x.fail(invalid(field.name, "Expected a Git name without NUL or line breaks."))
				return
			}
			*field.value = text
		}
	}
	id := newOperationID()
	if raw, ok := obj["id"]; ok {
		id, err = inputOperationID(raw, "id")
		if err != nil {
			x.fail(err)
			return
		}
	}
	state, ok := x.service.store.accepted(boardID)
	if !ok || !slices.Contains(state.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !state.can(x.actor, "publish") {
		x.fail(errForbidden)
		return
	}
	if !x.useBoard(boardID) {
		return
	}
	backup, err := readBackup(x.service.store.root, backupID)
	if err != nil {
		x.backupFailure(err)
		return
	}
	if backup.BoardID != boardID {
		x.fail(errMissing)
		return
	}
	item := publication{ID: id, BoardID: boardID, BackupID: backupID, State: "running"}
	if err := savePublication(x.service.store.root, item, true); err != nil {
		x.publicationFailure(err)
		return
	}
	err = publishVerifiedBackup(x.service.store.root, backup, selection)
	finished := canonicalTime(time.Now())
	item.FinishedAt, item.State = &finished, "succeeded"
	if err != nil {
		code := "publication_failed"
		var failure publicationGitError
		if errors.As(err, &failure) && validPublicationCode(string(failure)) {
			code = string(failure)
		}
		item.State, item.ErrorCode = "failed", &code
	}
	if err := savePublication(x.service.store.root, item, false); err != nil {
		x.publicationFailure(err)
		return
	}
	x.w.Header().Set("Location", Prefix+"/publications/"+id)
	x.respond(http.StatusAccepted, item, "")
}

func (x *request) publicationFailure(err error) {
	switch {
	case errors.Is(err, errPublicationExists):
		x.problem(409, "conflict", "Inspect the publication by its original ID.", "", nil)
	case errors.Is(err, errWriteUncertain):
		x.problem(503, "outcome_unknown", "Inspect the publication by its original ID before attempting another write.", "unknown", nil)
	case errors.Is(err, errMissing), errors.Is(err, errForbidden):
		x.fail(err)
	default:
		x.problem(503, "unavailable", "Publication status is unavailable.", "", nil)
	}
}
