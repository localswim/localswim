package v2

import (
	"errors"
	"path/filepath"
)

// DataRoot is the canonical workspace selected when the writer was opened.
func (s *Service) DataRoot() string {
	return s.store.root
}

// LockServiceStartup excludes other native launches while the current
// process releases the writer and its prepared child takes ownership.
func LockServiceStartup(root string) (func() error, error) {
	absolute, err := resolveStoreRoot(root)
	if err != nil {
		return nil, err
	}
	gate, err := acquireWorkspaceLock(filepath.Join(absolute, ".startup.lock"))
	if err != nil {
		return nil, err
	}
	return func() error { return errors.Join(unlockWriter(gate), gate.Close()) }, nil
}

// ReleaseForActivation follows HTTP and event drain. The native caller holds
// the startup lock until the child takes ownership or this service resumes.
func (s *Service) ReleaseForActivation() error {
	if err := s.store.checkBoardsDirectory(s.store.boardsPath()); err != nil {
		return err
	}
	s.StopEvents()
	s.stopBackground()
	return s.store.close()
}

// ResumeAfterActivationFailure keeps the accepted state and browser capability
// of the previous instance. No HTTP handlers can run until it has returned.
func (s *Service) ResumeAfterActivationFailure(id string) error {
	s.store.mu.Lock()
	suspended := s.store.lease == nil
	if suspended {
		lease, err := acquireWriter(s.store.root)
		if err != nil {
			s.store.mu.Unlock()
			return err
		}
		s.store.lease = lease
	}
	s.store.mu.Unlock()
	s.events = newEventHub()
	s.store.events = s.events
	if suspended {
		s.workspace = newWorkspace(s.store.root)
		s.store.startBoardPublishers()
		s.store.ownerNotices = newOwnerNoticeWorker(ownerNoticeQuietPeriod, s.store.ownerNoticeSettings, deliverOwnerNotice)
		s.store.ownerNotices.writerID = s.store.activityWriterID
	}
	s.workspace.events = s.events
	// A receipt write error is exposed in status. It must not stop the old
	// listener from coming back with its accepted board data.
	_ = s.failActivation(id)
	return nil
}
