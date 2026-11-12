package v2

import (
	"strconv"
)

func (s *store) startBoardPublishers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishers = make(map[string]*automaticPublisher)
	for _, value := range s.boards {
		s.boardPublishingCommitted(value.Board)
	}
}

func (s *store) closeBoardPublishers() {
	s.mu.Lock()
	publishers := s.publishers
	s.publishers = nil
	s.mu.Unlock()
	waits := make([]<-chan struct{}, 0, len(publishers))
	for _, publisher := range publishers {
		if done := publisher.stop(); done != nil {
			waits = append(waits, done)
		}
	}
	for _, done := range waits {
		<-done
	}
}

// Called only with command admission held, after the snapshot is durable.
func (s *store) boardPublishingCommitted(board board) {
	if s.publishers == nil || board.Availability != "open" {
		return
	}
	publisher := s.publishers[board.ID]
	if !board.Publishing.Enabled {
		if publisher != nil {
			publisher.disable()
		}
		return
	}
	var checkpoint publicationCheckpoint
	if publisher != nil {
		last, closed := publisher.closedCheckpoint()
		if closed {
			checkpoint = last
			publisher = nil
		}
	}
	if publisher == nil {
		publisher = newAutomaticPublisher(s.root, board.ID, func() (publicationCapture, error) {
			return s.captureBoardPublication(board.ID)
		})
		publisher.checkpoint = checkpoint
		s.publishers[board.ID] = publisher
	}
	publisher.schedule(board.Publishing)
}

func (s *store) boardPublishingStatus(value board) board {
	s.mu.RLock()
	publisher := s.publishers[value.ID]
	s.mu.RUnlock()
	if publisher != nil {
		value.Publishing = publisher.status(value.Publishing)
	}
	return value
}

func (s *store) captureBoardPublication(id string) (publicationCapture, error) {
	s.mu.RLock()
	var captured backupCapture
	value, ok := s.boards[id]
	err := s.writable(id)
	if err == nil && ok && value.Board.Availability == "open" && value.Board.Publishing.Enabled {
		captured, err = s.captureSnapshotLocked(value)
	} else {
		err = errBackupIntegrity
	}
	s.mu.RUnlock()
	if err != nil {
		return publicationCapture{}, gitPublicationError("backup_integrity")
	}
	value, data, err := captured.verify()
	if err != nil {
		return publicationCapture{}, gitPublicationError("backup_integrity")
	}
	files := []publicationFile{{path: "boards/" + id + ".json", read: func() ([]byte, error) {
		return data, nil
	}}}
	for _, attachment := range value.Attachments {
		files = append(files, publicationFile{path: attachmentPath(attachment), read: func() ([]byte, error) {
			return readVerifiedAttachment(s.root, attachment)
		}})
	}
	return publicationCapture{revision: value.Board.Revision,
		message: "localswim board " + id + " revision " + strconv.FormatUint(value.Board.Revision, 10), files: files}, nil
}
