package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	recoveryReferenceSuffix   = ".recovery"
	maxRecoveryReferenceBytes = maxSnapshotBytes
)

type recoveryActor struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type recoveryContext struct {
	BoardID        string          `json:"board_id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	RevisionFloor  uint64          `json:"revision_floor"`
	SnapshotDigest string          `json:"snapshot_sha2_256"`
	RestoreActors  []recoveryActor `json:"restore_actors"`
}

// Pending fences a superseded authorization or revision. After an interruption
// only a fully validated snapshot matching one of the two digests can settle it.
type recoveryReference struct {
	Committed recoveryContext  `json:"committed"`
	Pending   *recoveryContext `json:"pending"`
}

type sealedRecoveryReference struct {
	Reference recoveryReference `json:"reference"`
	Digest    string            `json:"sha2_256"`
}

type recoveryReport struct {
	Mode          string  `json:"mode"`
	RevisionFloor *uint64 `json:"revision_floor"`
}

func recoveryDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func recoveryContextFor(value snapshot, data []byte) recoveryContext {
	actors := []recoveryActor{}
	for _, member := range value.Users {
		if value.can(member.ID, "restore") {
			actors = append(actors, recoveryActor{member.ID, member.Kind})
		}
	}
	slices.SortFunc(actors, func(a, b recoveryActor) int { return strings.Compare(a.ID, b.ID) })
	return recoveryContext{
		BoardID: value.Board.ID, CreatedAt: value.Board.CreatedAt, UpdatedAt: value.Board.UpdatedAt,
		RevisionFloor: value.Board.Revision, SnapshotDigest: recoveryDigest(data), RestoreActors: actors,
	}
}

func (c recoveryContext) valid(id string) bool {
	if c.BoardID != id || c.RevisionFloor == 0 || c.RevisionFloor > maxSnapshotInteger ||
		!validSnapshotTime(c.CreatedAt) || !validSnapshotTime(c.UpdatedAt) || c.UpdatedAt.Before(c.CreatedAt) ||
		len(c.SnapshotDigest) != sha256.Size*2 || strings.ToLower(c.SnapshotDigest) != c.SnapshotDigest || c.RestoreActors == nil {
		return false
	}
	if _, err := hex.DecodeString(c.SnapshotDigest); err != nil {
		return false
	}
	last := ""
	for _, actor := range c.RestoreActors {
		if !validSnapshotID(actor.ID) || actor.ID <= last || (actor.Kind != "human" && actor.Kind != "bot") {
			return false
		}
		last = actor.ID
	}
	return true
}

func (s *store) recoveryReferencePath(id string) string {
	return filepath.Join(s.boardsPath(), id+recoveryReferenceSuffix)
}

func (s *store) readRecoveryReference(id string) (recoveryReference, bool) {
	dir := s.boardsPath()
	if s.checkBoardsDirectory(dir) != nil {
		return recoveryReference{}, false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return recoveryReference{}, false
	}
	defer root.Close()
	name := id + recoveryReferenceSuffix
	file, err := openBackupFile(root, name, maxRecoveryReferenceBytes)
	if err != nil {
		return recoveryReference{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return recoveryReference{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRecoveryReferenceBytes+1))
	if err != nil || len(data) > maxRecoveryReferenceBytes ||
		checkNamedSnapshot(s.recoveryReferencePath(id), snapshotFile{data: data, info: info}) != nil ||
		s.checkBoardsDirectory(dir) != nil {
		return recoveryReference{}, false
	}
	var sealed sealedRecoveryReference
	if unmarshalCanonical(data, &sealed) != nil {
		return recoveryReference{}, false
	}
	ref := sealed.Reference
	canonical, err := marshalCanonical(sealed)
	if err != nil || !bytes.Equal(data, canonical) || !ref.Committed.valid(id) {
		return recoveryReference{}, false
	}
	if next := ref.Pending; next != nil && (!next.valid(id) || next.CreatedAt != ref.Committed.CreatedAt ||
		next.UpdatedAt.Before(ref.Committed.UpdatedAt) || next.RevisionFloor <= ref.Committed.RevisionFloor) {
		return recoveryReference{}, false
	}
	payload, err := marshalCanonical(ref)
	return ref, err == nil && recoveryDigest(payload) == sealed.Digest
}

func (s *store) writeRecoveryReference(ref recoveryReference) error {
	id := ref.Committed.BoardID
	payload, err := marshalCanonical(ref)
	if err != nil {
		return err
	}
	data, err := marshalCanonical(sealedRecoveryReference{Reference: ref, Digest: recoveryDigest(payload)})
	if err != nil || len(data) > maxRecoveryReferenceBytes {
		return errInvalidSnapshot
	}
	dir := s.boardsPath()
	if err := s.checkBoardsDirectory(dir); err != nil {
		return err
	}
	return writeAtomicChecked(s.recoveryReferencePath(id), data, func() error {
		return s.checkBoardsDirectory(dir)
	})
}

func (s *store) checkStartupRecoveryReferences(ids []string) (map[string]recoveryContext, error) {
	backfill := make(map[string]recoveryContext)
	for _, id := range ids {
		value := s.boards[id]
		data, err := marshalCanonical(value)
		if err != nil {
			return nil, err
		}
		current := recoveryContextFor(value, data)
		ref, valid := s.readRecoveryReference(id)
		if valid && !reflect.DeepEqual(ref.Committed, current) &&
			(ref.Pending == nil || !reflect.DeepEqual(*ref.Pending, current)) {
			// A validated but different snapshot cannot lower the last trusted
			// revision or silently change the identity and restore authority.
			s.failedBoards[id] = errDurableStateChanged
			delete(s.boards, id)
			continue
		}
		s.recoveryContexts[id] = current
		if !valid || ref.Pending != nil {
			backfill[id] = current
		}
	}
	return backfill, nil
}

func (s *store) reserveMissingSnapshots(ids []string) bool {
	trustedMissing := false
	for _, id := range ids {
		if _, loaded := s.boards[id]; loaded || s.isFailedBoard(id) {
			continue
		}
		// The filename reserves the identity. Only a settled, noncolliding
		// reference establishes that a committed snapshot went missing.
		s.failedBoards[id] = os.ErrNotExist
		if ref, valid := s.readRecoveryReference(id); valid && ref.Pending == nil && !s.collidesWithHealthyBoardID(id) {
			trustedMissing = true
		}
	}
	return trustedMissing
}

func (s *store) finishStartupRecoveryReferences(backfill map[string]recoveryContext) {
	for id, current := range backfill {
		if _, admitted := s.boards[id]; admitted {
			// A board-specific metadata failure must not prevent healthy boards
			// from serving. Future recovery still requires a settled reference.
			_ = s.writeRecoveryReference(recoveryReference{Committed: current})
		}
	}
	for id := range s.failedBoards {
		delete(s.recoveryContexts, id)
		if ref, valid := s.readRecoveryReference(id); valid && ref.Pending == nil {
			s.recoveryContexts[id] = ref.Committed
		}
	}
}

// The caller holds s.mu. Recheck the current file before using the context
// captured at startup. A file added, removed, or modified later grants nothing.
func (s *store) failedRecoveryContextLocked(id string) (recoveryContext, bool) {
	startup, known := s.recoveryContexts[id]
	if !s.isFailedBoard(id) || !known || startup.RevisionFloor == maxSnapshotInteger {
		return recoveryContext{}, false
	}
	ref, valid := s.readRecoveryReference(id)
	if !valid || ref.Pending != nil || !reflect.DeepEqual(ref.Committed, startup) {
		return recoveryContext{}, false
	}
	if s.collidesWithHealthyBoardID(id) {
		return recoveryContext{}, false
	}
	for _, actor := range s.principals {
		if slices.Contains(startup.RestoreActors, recoveryActor{actor.ID, actor.Kind}) {
			return startup, true
		}
	}
	return recoveryContext{}, false
}

func (s *store) collidesWithHealthyBoardID(id string) bool {
	for _, principal := range s.principals {
		if principal.ID == id {
			return true
		}
	}
	ids := map[string]bool{id: true}
	for otherID, other := range s.boards {
		if otherID != id && snapshotContainsIdentity(other, ids) {
			return true
		}
	}
	return false
}

func (s *store) failedBoardVerification(id string) verification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	report := snapshotVerification(id, time.Now(), snapshot{}, s.failedBoards[id])
	report.Recovery = &recoveryReport{Mode: "fresh_only"}
	if context, ok := s.failedRecoveryContextLocked(id); ok {
		report.Recovery = &recoveryReport{Mode: "same_id", RevisionFloor: &context.RevisionFloor}
	}
	return report
}

func (s *store) writeSnapshotWithRecoveryReference(value snapshot, data []byte, beforeReplace func() error) error {
	id := value.Board.ID
	current := recoveryContextFor(value, data)
	previous, exists := s.recoveryContexts[id]
	if exists {
		if err := s.writeRecoveryReference(recoveryReference{Committed: previous, Pending: &current}); err != nil {
			// The snapshot has not been touched. Either old reference outcome
			// is safe and the normal write preconditions still apply on retry.
			return errInvalidSnapshot
		}
	}
	if err := writeAtomicChecked(s.snapshotPath(id), data, beforeReplace); err != nil {
		if exists && !errors.Is(err, errWriteUncertain) {
			// A known refusal preserves the last accepted commit. If this
			// metadata write fails, the pending reference still fails closed.
			_ = s.writeRecoveryReference(recoveryReference{Committed: previous})
		}
		return err
	}
	if err := s.writeRecoveryReference(recoveryReference{Committed: current}); err != nil {
		return errors.Join(errWriteUncertain, err)
	}
	s.recoveryContexts[id] = current
	return nil
}
