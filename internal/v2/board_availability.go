package v2

import (
	"errors"
	"sync"
)

var errBoardUnavailable = errors.New("board is closed or draining")
var errBoardFlush = errors.New("board publication flush failed")

// Admission is scoped to the addressed board. The workspace's writer lock is
// never held while draining its operations or performing Git work.
type boardAdmission struct {
	active   sync.WaitGroup
	draining bool
}

func (s *store) admissionLocked(id string) *boardAdmission {
	gate := s.admissions[id]
	if gate == nil {
		gate = &boardAdmission{}
		s.admissions[id] = gate
	}
	return gate
}

func (s *store) admitBoard(id string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.boards[id]
	if !exists {
		return nil, errBoardNotFound
	}
	gate := s.admissionLocked(id)
	if gate.draining || current.Board.Availability != "open" {
		return nil, errBoardUnavailable
	}
	gate.active.Add(1)
	return gate.active.Done, nil
}

func (x *request) admitBoard(id string) error {
	done, err := x.service.store.admitBoard(id)
	if err != nil {
		return err
	}
	x.boardDone = done
	return nil
}

func (x *request) useBoard(id string) bool {
	if err := x.admitBoard(id); err != nil {
		x.fail(err)
		return false
	}
	return true
}

func (x *request) updateBoard(id, expected string, change func(*snapshot) error) (snapshot, error) {
	return x.updateBoardChecked(id, func(current snapshot) bool {
		return matchesStrongTag(expected, current.Board.ETag)
	}, change)
}

func (x *request) updateBoardChecked(id string, matches func(snapshot) bool, change func(*snapshot) error) (snapshot, error) {
	if err := x.admitBoard(id); err != nil {
		return snapshot{}, err
	}
	return x.service.store.updateChecked(id, matches, change)
}

func (s *store) patchBoard(id, expected, availability string, hasSettings bool, allowed func(snapshot) bool, change func(*snapshot) error) (snapshot, error) {
	s.mu.Lock()
	current, err := s.boardForPatchLocked(id, expected, allowed)
	if err != nil {
		s.mu.Unlock()
		return snapshot{}, err
	}
	gate := s.admissionLocked(id)
	if gate.draining || (current.Board.Availability == "closed" && availability != "open" && hasSettings) {
		s.mu.Unlock()
		return snapshot{}, errBoardUnavailable
	}
	if availability != "closed" || current.Board.Availability == "closed" {
		defer s.mu.Unlock()
		return s.updateLocked(current, change)
	}
	gate.draining = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		gate.draining = false
		s.mu.Unlock()
	}()

	gate.active.Wait()
	return s.closeBoard(id, expected, allowed, change)
}

func (s *store) boardForPatchLocked(id, expected string, allowed func(snapshot) bool) (snapshot, error) {
	if err := s.writable(); err != nil {
		return snapshot{}, err
	}
	current, exists := s.boards[id]
	if !exists {
		return snapshot{}, errBoardNotFound
	}
	if !allowed(current) {
		return snapshot{}, errForbidden
	}
	if !matchesStrongTag(expected, current.Board.ETag) {
		return snapshot{}, errStale
	}
	return current, nil
}

func (s *store) closeBoard(id, expected string, allowed func(snapshot) bool, change func(*snapshot) error) (snapshot, error) {
	s.mu.Lock()
	current, err := s.boardForPatchLocked(id, expected, allowed)
	if err != nil {
		s.mu.Unlock()
		return snapshot{}, err
	}
	next, _, err := s.prepareUpdateLocked(current, change)
	if err != nil {
		s.mu.Unlock()
		return snapshot{}, err
	}
	data, err := s.prepareCommit(next, false)
	publisher := s.publishers[id]
	s.mu.Unlock()
	if err != nil {
		return snapshot{}, err
	}

	// Publish the final accepted open revision. The closed candidate is
	// only durable after the last accepted work has reached the remote.
	if current.Board.Publishing.Enabled {
		if publisher == nil || publisher.flush() != nil {
			return snapshot{}, errBoardFlush
		}
	}

	s.mu.Lock()
	err = s.finishBoardCloseLocked(current, next, data)
	s.mu.Unlock()
	if err != nil {
		return snapshot{}, err
	}
	if publisher != nil {
		publisher.close()
	}
	return next, nil
}

func (s *store) finishBoardCloseLocked(current, next snapshot, data []byte) error {
	if err := s.writable(); err != nil {
		return err
	}
	if s.boards[next.Board.ID].Board.ETag != current.Board.ETag {
		return errStale
	}
	if err := s.checkAcceptedDurableLocked(current); err != nil {
		return err
	}
	if err := s.checkIdentities(next, false); err != nil {
		return err
	}
	return s.writeCommit(cloneSnapshot(next), data)
}
