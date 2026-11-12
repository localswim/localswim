package v2

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	errBoardExists         = errors.New("board already exists")
	errBoardNotFound       = errors.New("board not found")
	errIdentityConflict    = errors.New("user identity conflicts with an existing workspace user")
	errStoreClosed         = errors.New("workspace store is closed")
	errWriteFenced         = errors.New("writes are fenced after an uncertain commit")
	errDurableStateChanged = errors.New("durable board snapshot differs from accepted state")
)

type store struct {
	root          string
	boardsDirInfo os.FileInfo

	activityWriterID string
	activitySince    time.Time

	recoveryIO       sync.RWMutex // keep the writer lease through out-of-lock recovery I/O
	mu               sync.RWMutex
	lease            *os.File
	boards           map[string]snapshot
	failedBoards     map[string]error // protected by mu after startup
	recovering       map[string]bool  // failed sources and fresh destinations reserved under mu
	recoveryContexts map[string]recoveryContext
	principals       []user
	events           *eventHub
	ownerNotices     *ownerNoticeWorker
	publishers       map[string]*automaticPublisher
	admissions       map[string]*boardAdmission

	workspaceWriteFenced bool
	uncertainBoards      map[string]snapshot // tentative identities, never accepted state

	durableValidation durableValidationCache

	beforeRecoverySettlement func(string) error // fault injection at the final reference replacement
}

func resolveStoreRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace data root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("create workspace data root: %w", err)
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize workspace data root: %w", err)
	}
	return absolute, nil
}

func openStore(root string, principals ...user) (*store, error) {
	absolute, err := resolveStoreRoot(root)
	if err != nil {
		return nil, err
	}
	lease, err := acquireWriter(absolute)
	if err != nil {
		return nil, fmt.Errorf("acquire workspace writer lease: %w", err)
	}
	s := &store{
		root:             absolute,
		lease:            lease,
		boards:           make(map[string]snapshot),
		failedBoards:     make(map[string]error),
		recovering:       make(map[string]bool),
		recoveryContexts: make(map[string]recoveryContext),
		admissions:       make(map[string]*boardAdmission),
		principals:       slices.Clone(principals),
	}
	if err := s.load(); err != nil {
		return nil, errors.Join(err, s.close())
	}
	return s, nil
}

func (s *store) load() error {
	dir := filepath.Join(s.root, "boards")
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create workspace boards directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect workspace boards directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("workspace boards path must be a directory")
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open workspace boards directory: %w", err)
	}
	defer directory.Close()
	// Capture the identity from the opened directory. On Windows a path-based
	// FileInfo can resolve its identity lazily after the path has been replaced.
	s.boardsDirInfo, err = directory.Stat()
	if err != nil {
		return fmt.Errorf("identify workspace boards directory: %w", err)
	}
	if err := s.checkBoardsDirectory(dir); err != nil {
		return err
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("list workspace boards directory: %w", err)
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	loadedIDs := make([]string, 0, len(entries))
	referenceIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		id, isSnapshot := strings.CutSuffix(entry.Name(), ".json")
		if !isSnapshot {
			id, isReference := strings.CutSuffix(entry.Name(), recoveryReferenceSuffix)
			if isReference && validSnapshotID(id) {
				referenceIDs = append(referenceIDs, id)
			}
			continue
		}
		if canonical, ok := normalizeID(id); !ok || canonical != id {
			return fmt.Errorf("invalid board snapshot filename: %w", errInvalidSnapshot)
		}
		if err := s.checkBoardsDirectory(dir); err != nil {
			return err
		}
		value, err := loadBoardSnapshot(s.snapshotPath(id), id)
		if err != nil {
			s.failedBoards[id] = err
			continue
		}
		s.boards[id] = value
		loadedIDs = append(loadedIDs, id)
	}

	// Detect workspace-wide conflicts before excluding any structurally valid
	// snapshots, including boards that may later lose recovery actor evidence.
	known := s.globalUsers(nil)
	for _, id := range loadedIDs {
		value := s.boards[id]
		if err := s.checkIdentityConflicts(value, false); err != nil {
			return fmt.Errorf("load board %s: %w", id, err)
		}
		if _, err := s.checkRecoveryActors(value, known); err != nil {
			return fmt.Errorf("load board %s: %w", id, err)
		}
	}
	backfill, err := s.checkStartupRecoveryReferences(loadedIDs)
	if err != nil {
		return err
	}
	hasFailedSnapshot := len(s.failedBoards) != 0
	hasMissingSnapshot := s.reserveMissingSnapshots(referenceIDs)
	if err := s.checkStartupRecoveryActors(loadedIDs, hasFailedSnapshot || hasMissingSnapshot); err != nil {
		return err
	}
	s.finishStartupRecoveryReferences(backfill)
	return s.checkBoardsDirectory(dir)
}

func (s *store) checkStartupRecoveryActors(ids []string, hasFailedSnapshot bool) error {
	// An observed snapshot failure can explain missing evidence. A missing
	// definition alone cannot establish that any saved board failed to load.
	for {
		known := s.globalUsers(nil)
		removed := false
		for _, id := range ids {
			value, exists := s.boards[id]
			if !exists {
				continue
			}
			missing, err := s.checkRecoveryActors(value, known)
			if err != nil || (missing && !hasFailedSnapshot) {
				return fmt.Errorf("load board %s: %w", id, errIdentityConflict)
			}
			if missing {
				delete(s.boards, id)
				s.failedBoards[id] = errInvalidSnapshot
				removed = true
			}
		}
		// Quarantined snapshots cannot supply actor evidence to another board.
		if !removed {
			return nil
		}
	}
}

func loadBoardSnapshot(path, id string) (snapshot, error) {
	file, err := readSnapshotFile(path)
	if err != nil {
		return snapshot{}, err
	}
	value, err := parseSnapshot(file.data, id)
	if err != nil {
		return snapshot{}, err
	}
	if err := checkNamedSnapshot(path, file); err != nil {
		return snapshot{}, err
	}
	return value, nil
}

func (s *store) checkBoardsDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect workspace boards directory: %w", err)
	}
	if !info.IsDir() || !os.SameFile(s.boardsDirInfo, info) {
		return errors.New("workspace boards directory was replaced")
	}
	return nil
}

func (s *store) close() error {
	s.recoveryIO.Lock()
	defer s.recoveryIO.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lease == nil {
		return nil
	}
	err := errors.Join(unlockWriter(s.lease), s.lease.Close())
	s.lease = nil
	return err
}

func (s *store) accepted(id string) (snapshot, bool) {
	s.mu.RLock()
	value, ok := s.boards[id]
	s.mu.RUnlock()
	return cloneSnapshot(value), ok
}

func (s *store) isFailedBoard(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isFailedBoardLocked(id)
}

func (s *store) isFailedBoardLocked(id string) bool {
	_, failed := s.failedBoards[id]
	return failed
}

func (s *store) list() []snapshot {
	s.mu.RLock()
	values := make([]snapshot, 0, len(s.boards))
	for _, value := range s.boards {
		values = append(values, value)
	}
	s.mu.RUnlock()
	slices.SortFunc(values, func(a, b snapshot) int {
		return strings.Compare(a.Board.ID, b.Board.ID)
	})
	for index := range values {
		values[index] = cloneSnapshot(values[index])
	}
	return values
}

func (s *store) boardsPath() string {
	return filepath.Join(s.root, "boards")
}

func (s *store) snapshotPath(id string) string {
	return filepath.Join(s.boardsPath(), id+".json")
}

func (s *store) durable(id string) (snapshot, error) {
	if canonical, ok := normalizeID(id); !ok || canonical != id {
		return snapshot{}, errBoardNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readDurableLocked(id)
}

func (s *store) readDurableLocked(id string) (snapshot, error) {
	saved, err := s.readDurableFileLocked(s.snapshotPath(id))
	if err != nil {
		return snapshot{}, err
	}
	return parseSnapshot(saved.data, id)
}

func (s *store) readDurableFileLocked(path string) (snapshotFile, error) {
	if s.lease == nil {
		return snapshotFile{}, errStoreClosed
	}
	if err := s.checkBoardsDirectory(filepath.Dir(path)); err != nil {
		return snapshotFile{}, invalidSnapshot("snapshot", "Native snapshot directory is unavailable.")
	}
	return readSnapshotFile(path)
}

func (s *store) checkAcceptedDurableLocked(current snapshot) error {
	path := s.snapshotPath(current.Board.ID)
	file, err := s.readDurableFileLocked(path)
	if err != nil {
		return errInvalidSnapshot
	}
	return s.checkAcceptedFileCached(path, current, file)
}

func checkAcceptedSnapshotFile(path string, current snapshot, file snapshotFile) error {
	if err := compareAcceptedSnapshot(current, file.data); err != nil {
		return err
	}
	return checkNamedSnapshot(path, file)
}

func compareAcceptedSnapshot(current snapshot, data []byte) error {
	saved, err := parseSnapshot(data, current.Board.ID)
	if err != nil {
		return errInvalidSnapshot
	}
	if !reflect.DeepEqual(current, saved) {
		return errDurableStateChanged
	}
	return nil
}

func checkNamedSnapshot(path string, file snapshotFile) error {
	// Validation takes time. Check that the name still points at the opened file
	// and that its observable size and modification time have stayed stable.
	named, err := os.Lstat(path)
	if err != nil || !named.Mode().IsRegular() {
		return errInvalidSnapshot
	}
	if named.Size() != int64(len(file.data)) || named.Size() != file.info.Size() ||
		!named.ModTime().Equal(file.info.ModTime()) || !os.SameFile(named, file.info) {
		return errDurableStateChanged
	}
	return nil
}

func (s *store) fencedBoardContaining(contains func(snapshot) bool) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, candidate := range s.uncertainBoards {
		if contains(candidate) {
			return id
		}
	}
	return ""
}

// readback uses durable state only after the writer fences an uncertain commit.
func (s *store) readback(id string) (snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lease == nil {
		return snapshot{}, errStoreClosed
	}
	current, exists := s.boards[id]
	if !exists {
		return snapshot{}, errBoardNotFound
	}
	if _, fenced := s.uncertainBoards[id]; !fenced {
		return cloneSnapshot(current), nil
	}
	return s.readDurableLocked(id)
}

func (s *store) create(value snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := value.Board.ID
	if err := s.writable(id); err != nil {
		return err
	}
	if canonical, ok := normalizeID(id); !ok || canonical != id {
		return errInvalidSnapshot
	}
	if _, exists := s.boards[id]; exists {
		return errBoardExists
	}
	if s.isFailedBoardLocked(id) || s.recovering[id] {
		return errBoardExists
	}
	for _, path := range []string{s.snapshotPath(id), s.recoveryReferencePath(id)} {
		if _, err := os.Lstat(path); err == nil {
			return errBoardExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// An orphan route cannot opt a newly created board into delivery.
	if _, err := os.Lstat(s.notificationRoutePath(id)); err == nil {
		return errBoardExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.commit(cloneSnapshot(value), true, nil)
}

func (s *store) update(id string, expected uint64, change func(*snapshot) error) (snapshot, error) {
	return s.updateChecked(id, func(current snapshot) bool {
		return expected == current.Board.Revision
	}, change)
}

func (s *store) updateChecked(id string, matches func(snapshot) bool, change func(*snapshot) error) (snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(id); err != nil {
		return snapshot{}, err
	}
	current, ok := s.boards[id]
	if !ok {
		return snapshot{}, errBoardNotFound
	}
	if matches != nil && !matches(current) {
		return snapshot{}, errStale
	}
	if current.Board.Availability != "open" {
		return snapshot{}, errBoardUnavailable
	}
	return s.updateLocked(current, change)
}

func (s *store) updateLocked(current snapshot, change func(*snapshot) error) (snapshot, error) {
	next, changed, err := s.prepareUpdateLocked(current, change)
	if err != nil || !changed {
		return next, err
	}
	// Detach from data that the callback supplied or retained.
	if err := s.commit(cloneSnapshot(next), false, &current); err != nil {
		return snapshot{}, err
	}
	return next, nil
}

func (s *store) prepareUpdateLocked(current snapshot, change func(*snapshot) error) (snapshot, bool, error) {
	if err := s.checkAcceptedDurableLocked(current); err != nil {
		return snapshot{}, false, err
	}
	next := cloneSnapshot(current)
	if err := change(&next); err != nil {
		return snapshot{}, false, err
	}
	// The callback owns the business fields and audit. The store owns the board version.
	next.Board.entity = current.Board.entity
	next.Board.Revision = current.Board.Revision
	next.Board.Attention, next.Board.LastCardEditAt = deriveAttention(next)
	if reflect.DeepEqual(current, next) {
		return cloneSnapshot(current), false, nil
	}
	if current.Board.Revision == math.MaxUint64 {
		return snapshot{}, false, errors.New("board revision exhausted")
	}
	next.Board.Revision++
	next.Board.UpdatedAt = canonicalTime(time.Now())
	if len(next.Activity) > len(current.Activity) {
		next.Board.UpdatedAt = next.Activity[len(next.Activity)-1].At
	}
	if next.Board.UpdatedAt.Before(current.Board.UpdatedAt) {
		next.Board.UpdatedAt = current.Board.UpdatedAt
	}
	return next, true, nil
}

// The caller holds the store lock. Refusing a subsequent operation does not
// make that operation uncertain: none of its writes have started.
func (s *store) writable(id string) error {
	if s.lease == nil {
		return errStoreClosed
	}
	if _, fenced := s.uncertainBoards[id]; s.workspaceWriteFenced || fenced {
		return errWriteFenced
	}
	return nil
}

func (s *store) commit(value snapshot, reuseUsers bool, accepted *snapshot) error {
	data, err := s.prepareCommit(value, reuseUsers)
	if err != nil {
		return err
	}
	return s.writeCommit(value, data, accepted)
}

func (s *store) prepareCommit(value snapshot, reuseUsers bool) ([]byte, error) {
	id := value.Board.ID
	if err := validateSnapshot(value, id); err != nil {
		return nil, err
	}
	if err := s.checkIdentities(value, reuseUsers); err != nil {
		return nil, err
	}
	return snapshotCommitBytes(value)
}

func snapshotCommitBytes(value snapshot) ([]byte, error) {
	data, err := marshalCanonical(value)
	if err != nil {
		return nil, err
	}
	if len(data) > maxSnapshotBytes {
		return nil, fmt.Errorf("snapshot exceeds durable size limit: %w", errInvalidSnapshot)
	}
	return data, nil
}

func (s *store) writeCommit(value snapshot, data []byte, accepted *snapshot) error {
	id := value.Board.ID
	path := s.snapshotPath(id)
	if err := s.checkBoardsDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	var beforeReplace func() error
	if accepted != nil {
		beforeReplace = func() error {
			return s.checkAcceptedDurableLocked(*accepted)
		}
	}
	return s.writeCommitChecked(value, data, beforeReplace, nil)
}

func (s *store) writeCommitChecked(value snapshot, data []byte, beforeReplace, beforePending func() error) error {
	id := value.Board.ID
	if err := s.writeSnapshotWithRecoveryReference(value, data, beforeReplace, beforePending); err != nil {
		if errors.Is(err, errWriteUncertain) {
			s.fenceUncertainCommit(value, data)
		}
		return err
	}
	previous := s.boards[id]
	s.boards[id] = value
	delete(s.failedBoards, id)
	s.boardPublishingCommitted(value.Board)
	if s.ownerNotices != nil {
		if value.Activity[len(value.Activity)-1].Operation == recoveryOperation {
			// The restored audit may no longer contain cards from an old burst.
			s.ownerNotices.settingsChanged(id, false)
		} else if previous.Board.ID == id {
			route, err := s.readNotificationRoute(id)
			s.ownerNotices.submitCommitted(previous, value, err == nil && ownerNoticeEnabled(route.notifications))
		}
	}
	if s.events != nil {
		s.events.changed()
	}
	return nil
}

func (s *store) fenceUncertainCommit(candidate snapshot, data []byte) {
	id := candidate.Board.ID
	if s.uncertainBoards == nil {
		s.uncertainBoards = make(map[string]snapshot)
	}
	s.uncertainBoards[id] = candidate
	previous, exists := s.boards[id]
	// Shared user definitions and board creation can affect the whole workspace.
	// For an existing board with unchanged users, reserve both possible sets of
	// resource identities and verify that disk still names one of our outcomes.
	if !exists || !slices.Equal(previous.Users, candidate.Users) || !s.knownUncertainSnapshot(previous, data) {
		s.workspaceWriteFenced = true
		for boardID := range s.boards {
			s.cancelBoardEffects(boardID)
		}
	} else {
		s.cancelBoardEffects(id)
	}
}

func (s *store) knownUncertainSnapshot(previous snapshot, data []byte) bool {
	path := s.snapshotPath(previous.Board.ID)
	file, err := s.readDurableFileLocked(path)
	return err == nil && (bytes.Equal(file.data, data) || compareAcceptedSnapshot(previous, file.data) == nil) &&
		checkNamedSnapshot(path, file) == nil && s.checkBoardsDirectory(filepath.Dir(path)) == nil
}

func (s *store) cancelBoardEffects(id string) {
	if publisher := s.publishers[id]; publisher != nil {
		publisher.disable()
	}
	if s.ownerNotices != nil {
		s.ownerNotices.settingsChanged(id, false)
	}
}

func (s *store) checkIdentities(value snapshot, reuseUsers bool) error {
	if err := s.checkIdentityConflicts(value, reuseUsers); err != nil {
		return err
	}
	missing, err := s.checkRecoveryActors(value, nil)
	if missing {
		return errIdentityConflict
	}
	return err
}

func (s *store) checkIdentityConflicts(value snapshot, reuseUsers bool) error {
	cardIDs, laneIDs, commentIDs, attachmentIDs := resourceIDs(value.Cards, func(c card) string { return c.ID }),
		resourceIDs(value.Lanes, func(l lane) string { return l.ID }),
		resourceIDs(value.Comments, func(c comment) string { return c.ID }),
		resourceIDs(value.Attachments, func(a attachment) string { return a.ID })
	additionalIDs := auditedResourceIDs(value.Activity)
	conflicts := func(other snapshot) bool {
		id := other.Board.ID
		return id != value.Board.ID && (snapshotContainsIdentity(other, map[string]bool{value.Board.ID: true}) ||
			snapshotContainsIdentity(value, map[string]bool{id: true}) ||
			hasResourceID(other.Cards, cardIDs, func(c card) string { return c.ID }) ||
			hasResourceID(other.Lanes, laneIDs, func(l lane) string { return l.ID }) ||
			hasResourceID(other.Comments, commentIDs, func(c comment) string { return c.ID }) ||
			hasResourceID(other.Attachments, attachmentIDs, func(a attachment) string { return a.ID }) ||
			snapshotContainsIdentity(other, additionalIDs) ||
			snapshotContainsIdentity(value, auditedResourceIDs(other.Activity)))
	}
	for _, other := range s.boards {
		if conflicts(other) {
			return errIdentityConflict
		}
	}
	for _, candidate := range s.uncertainBoards {
		if conflicts(candidate) {
			return errIdentityConflict
		}
	}
	known := s.globalUsers(nil)
	if _, exists := known[value.Board.ID]; exists {
		return errIdentityConflict
	}

	for index, member := range value.Users {
		previous, exists := known[member.ID]
		if exists && (member.Kind != previous.Kind || member.Label != previous.Label || member.Color != previous.Color) {
			return errIdentityConflict
		}
		if exists && reuseUsers {
			value.Users[index] = previous
		}
	}
	return nil
}

func (s *store) checkRecoveryActors(value snapshot, known map[string]user) (missing bool, err error) {
	for _, event := range value.Activity {
		if event.Operation != recoveryOperation {
			continue
		}
		if known == nil {
			known = s.globalUsers(&value)
		}
		evidence, ok := recoveryEventEvidence(event)
		actor, exists := known[event.ActorID]
		if !ok || (exists && actor.Kind != evidence.ActorKind) {
			return false, errIdentityConflict
		}
		missing = missing || !exists
	}
	return missing, nil
}

func auditedResourceIDs(events []activity) map[string]bool {
	var ids map[string]bool
	for _, event := range events {
		var id string
		switch {
		case event.ResourceType == "relationship":
			id = event.ResourceID
		case event.Operation == approvalOperation:
			id = event.ID
		case event.Operation == recoveryOperation:
			evidence, ok := recoveryEventEvidence(event)
			if !ok || evidence.BackupID == nil {
				continue
			}
			id = event.ID
		default:
			continue
		}
		if ids == nil {
			ids = make(map[string]bool)
		}
		ids[id] = true
	}
	return ids
}

func snapshotContainsIdentity(value snapshot, ids map[string]bool) bool {
	if len(ids) == 0 {
		return false
	}
	matches := func(id string) bool { return ids[id] }
	return ids[value.Board.ID] || ids[value.Policy.ID] ||
		slices.ContainsFunc(value.Board.UserIDs, matches) ||
		slices.ContainsFunc(value.Board.LaneIDs, matches) ||
		slices.ContainsFunc(value.Board.PriorityIDs, matches) ||
		hasResourceID(value.Cards, ids, func(item card) string { return item.ID }) ||
		hasResourceID(value.Comments, ids, func(item comment) string { return item.ID }) ||
		hasResourceID(value.Attachments, ids, func(item attachment) string { return item.ID }) ||
		slices.ContainsFunc(value.Activity, func(event activity) bool {
			return ids[event.ID] || (event.ResourceType == "relationship" && ids[event.ResourceID])
		})
}

func resourceIDs[T any](items []T, id func(T) string) map[string]bool {
	ids := make(map[string]bool, len(items))
	for _, item := range items {
		ids[id(item)] = true
	}
	return ids
}

func hasResourceID[T any](items []T, ids map[string]bool, id func(T) string) bool {
	for _, item := range items {
		if ids[id(item)] {
			return true
		}
	}
	return false
}

// Choose the earliest durable user entity as in board creation. A stable board
// order resolves equal creation timestamps consistently before and after restart.
func (s *store) globalUsers(replacement *snapshot) map[string]user {
	known := make(map[string]user)
	for _, principal := range s.principals {
		known[principal.ID] = principal
	}
	ids := make([]string, 0, len(s.boards)+1)
	for id := range s.boards {
		ids = append(ids, id)
	}
	if replacement != nil {
		if _, exists := s.boards[replacement.Board.ID]; !exists {
			ids = append(ids, replacement.Board.ID)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		members := s.boards[id].Users
		if replacement != nil && replacement.Board.ID == id {
			members = replacement.Users
		}
		for _, member := range members {
			previous, exists := known[member.ID]
			if !exists || member.CreatedAt.Before(previous.CreatedAt) {
				known[member.ID] = member
			}
		}
	}
	return known
}
