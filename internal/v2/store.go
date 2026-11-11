package v2

import (
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
	errBoardExists      = errors.New("board already exists")
	errBoardNotFound    = errors.New("board not found")
	errIdentityConflict = errors.New("user identity conflicts with an existing workspace user")
	errStoreClosed      = errors.New("workspace store is closed")
)

type store struct {
	root          string
	boardsDirInfo os.FileInfo

	mu               sync.RWMutex
	lease            *os.File
	boards           map[string]snapshot
	principals       []user
	writeUncertain   bool
	uncertainBoardID string
	events           *eventHub
	ownerNotices     *ownerNoticeWorker
	publishers       map[string]*automaticPublisher
}

func openStore(root string, principals ...user) (*store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace data root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace data root: %w", err)
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("canonicalize workspace data root: %w", err)
	}
	lease, err := acquireWriter(absolute)
	if err != nil {
		return nil, fmt.Errorf("acquire workspace writer lease: %w", err)
	}
	s := &store{
		root:       absolute,
		lease:      lease,
		boards:     make(map[string]snapshot),
		principals: slices.Clone(principals),
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
	for _, entry := range entries {
		id, isSnapshot := strings.CutSuffix(entry.Name(), ".json")
		if !isSnapshot {
			continue
		}
		if canonical, ok := normalizeID(id); !ok || canonical != id {
			return fmt.Errorf("invalid board snapshot filename: %w", errInvalidSnapshot)
		}
		value, err := s.durable(id)
		if err != nil {
			return fmt.Errorf("load board %s: %w", id, err)
		}
		s.boards[id] = value
		loadedIDs = append(loadedIDs, id)
	}
	// No boards are served until every snapshot passes cross-board checks.
	// Recovery actors can be backed by any board, independent of load order.
	for _, id := range loadedIDs {
		if err := s.checkIdentities(s.boards[id], false); err != nil {
			return fmt.Errorf("load board %s: %w", id, err)
		}
	}
	return nil
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

func (s *store) ownerNoticeSettings(id string) (notifications, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.writeUncertain && s.uncertainBoardID == id {
		return notifications{}, false
	}
	value, ok := s.boards[id]
	return value.Board.Notifications, ok
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

func (s *store) snapshotPath(id string) string {
	return filepath.Join(s.root, "boards", id+".json")
}

func (s *store) durable(id string) (snapshot, error) {
	if canonical, ok := normalizeID(id); !ok || canonical != id {
		return snapshot{}, errBoardNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lease == nil {
		return snapshot{}, errStoreClosed
	}
	path := s.snapshotPath(id)
	if err := s.checkBoardsDirectory(filepath.Dir(path)); err != nil {
		return snapshot{}, invalidSnapshot("snapshot", "Native snapshot directory is unavailable.")
	}
	return readSnapshot(path, id)
}

func (s *store) fencedBoard() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.uncertainBoardID
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
	if !s.writeUncertain || s.uncertainBoardID != id {
		return cloneSnapshot(current), nil
	}
	path := s.snapshotPath(id)
	if err := s.checkBoardsDirectory(filepath.Dir(path)); err != nil {
		return snapshot{}, invalidSnapshot("snapshot", "Native snapshot directory is unavailable.")
	}
	return readSnapshot(path, id)
}

func (s *store) create(value snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	id := value.Board.ID
	if canonical, ok := normalizeID(id); !ok || canonical != id {
		return errInvalidSnapshot
	}
	if _, exists := s.boards[id]; exists {
		return errBoardExists
	}
	if _, err := os.Lstat(s.snapshotPath(id)); err == nil {
		return errBoardExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.commit(cloneSnapshot(value), true)
}

func (s *store) update(id, expected string, change func(*snapshot) error) (snapshot, error) {
	return s.updateChecked(id, func(current snapshot) bool {
		return matchesStrongTag(expected, current.Board.ETag)
	}, change)
}

func (s *store) updateChecked(id string, matches func(snapshot) bool, change func(*snapshot) error) (snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return snapshot{}, err
	}
	current, ok := s.boards[id]
	if !ok {
		return snapshot{}, errBoardNotFound
	}
	if matches != nil && !matches(current) {
		return snapshot{}, errStale
	}
	next := cloneSnapshot(current)
	if err := change(&next); err != nil {
		return snapshot{}, err
	}
	// The callback owns the business fields and audit. The store owns the board version.
	next.Board.entity = current.Board.entity
	next.Board.Revision = current.Board.Revision
	next.Board.Attention, next.Board.LastCardEditAt = deriveAttention(next)
	if reflect.DeepEqual(current, next) {
		return cloneSnapshot(current), nil
	}
	if current.Board.Revision == math.MaxUint64 {
		return snapshot{}, errors.New("board revision exhausted")
	}
	info, err := os.Lstat(s.snapshotPath(id))
	if err != nil {
		return snapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return snapshot{}, errInvalidSnapshot
	}
	next.Board.Revision++
	next.Board.UpdatedAt = canonicalTime(time.Now())
	if len(next.Activity) > len(current.Activity) {
		next.Board.UpdatedAt = next.Activity[len(next.Activity)-1].At
	}
	if next.Board.UpdatedAt.Before(current.Board.UpdatedAt) {
		next.Board.UpdatedAt = current.Board.UpdatedAt
	}
	next.Board.ETag = etag(id, next.Board.Revision)
	// Detach from data that the callback supplied or retained.
	if err := s.commit(cloneSnapshot(next), false); err != nil {
		return snapshot{}, err
	}
	return next, nil
}

func (s *store) writable() error {
	if s.lease == nil {
		return errStoreClosed
	}
	if s.writeUncertain {
		return errWriteUncertain
	}
	return nil
}

func (s *store) commit(value snapshot, reuseUsers bool) error {
	id := value.Board.ID
	if err := validateSnapshot(value, id); err != nil {
		return err
	}
	if err := s.checkIdentities(value, reuseUsers); err != nil {
		return err
	}
	data, err := marshalCanonical(value)
	if err != nil {
		return err
	}
	if len(data) > maxSnapshotBytes {
		return fmt.Errorf("snapshot exceeds durable size limit: %w", errInvalidSnapshot)
	}
	path := s.snapshotPath(id)
	if err := s.checkBoardsDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err := writeAtomic(path, data); err != nil {
		if errors.Is(err, errWriteUncertain) {
			// Fence new commits until restart reloads the disk outcome. Durable
			// inspection remains available for resolving the uncertain mutation.
			s.writeUncertain = true
			s.uncertainBoardID = id
			if publisher := s.publishers[id]; publisher != nil {
				publisher.disable()
			}
			if s.ownerNotices != nil {
				s.ownerNotices.settingsChanged(id, false)
			}
		}
		return err
	}
	previous := s.boards[id]
	s.boards[id] = value
	s.boardPublishingCommitted(value.Board)
	if s.ownerNotices != nil {
		s.ownerNotices.submitCommitted(previous, value)
	}
	if s.events != nil {
		s.events.changed()
	}
	return nil
}

func (s *store) checkIdentities(value snapshot, reuseUsers bool) error {
	cardIDs, laneIDs, commentIDs, attachmentIDs := resourceIDs(value.Cards, func(c card) string { return c.ID }),
		resourceIDs(value.Lanes, func(l lane) string { return l.ID }),
		resourceIDs(value.Comments, func(c comment) string { return c.ID }),
		resourceIDs(value.Attachments, func(a attachment) string { return a.ID })
	additionalIDs := auditedResourceIDs(value.Activity)
	for id, other := range s.boards {
		if id == value.Board.ID {
			continue
		}
		if hasResourceID(other.Cards, cardIDs, func(c card) string { return c.ID }) ||
			hasResourceID(other.Lanes, laneIDs, func(l lane) string { return l.ID }) ||
			hasResourceID(other.Comments, commentIDs, func(c comment) string { return c.ID }) ||
			hasResourceID(other.Attachments, attachmentIDs, func(a attachment) string { return a.ID }) ||
			snapshotContainsIdentity(other, additionalIDs) ||
			snapshotContainsIdentity(value, auditedResourceIDs(other.Activity)) {
			return errIdentityConflict
		}
	}
	known := s.globalUsers(nil)

	for index, member := range value.Users {
		previous, exists := known[member.ID]
		if exists && (member.Kind != previous.Kind || member.Label != previous.Label || member.Color != previous.Color) {
			return errIdentityConflict
		}
		if exists && reuseUsers {
			value.Users[index] = previous
		}
	}
	var after map[string]user
	for _, event := range value.Activity {
		if event.Operation != recoveryOperation {
			continue
		}
		if after == nil {
			after = s.globalUsers(&value)
		}
		evidence, ok := recoveryEventEvidence(event)
		if !ok || after[event.ActorID].ID == "" || after[event.ActorID].Kind != evidence.ActorKind {
			return errIdentityConflict
		}
	}
	return nil
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
