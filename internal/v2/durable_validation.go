package v2

import (
	"crypto/sha256"
	"sync"
)

const durableValidationEntries = 64

type durableValidation struct {
	boardID  string
	revision uint64
	digest   [sha256.Size]byte
	result   error
}

// Cache only validation results, never file bytes or decoded snapshots. Every
// lookup follows a fresh bounded read and digest, including same-mtime edits.
// The store lock protects the accepted revision; this lock protects concurrent
// readers. Neither cache operation acquires the store lock or performs I/O.
type durableValidationCache struct {
	mu      sync.Mutex
	entries [durableValidationEntries]durableValidation
	next    int
}

func (c *durableValidationCache) get(boardID string, revision uint64, digest [sha256.Size]byte) (error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.boardID == boardID && entry.revision == revision && entry.digest == digest {
			return entry.result, true
		}
	}
	return nil, false
}

func (c *durableValidationCache) put(entry durableValidation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Retain one verdict per board. Changed bytes replace the previous verdict
	// without accumulating entries during edits or repeated damage and repair.
	for i := range c.entries {
		if c.entries[i].boardID == entry.boardID {
			c.entries[i] = entry
			return
		}
	}
	c.entries[c.next] = entry
	c.next = (c.next + 1) % len(c.entries)
}

func (s *store) checkAcceptedFileCached(path string, current snapshot, file snapshotFile) error {
	digest := sha256.Sum256(file.data)
	result, found := s.durableValidation.get(current.Board.ID, current.Board.Revision, digest)
	if !found {
		result = compareAcceptedSnapshot(current, file.data)
		s.durableValidation.put(durableValidation{current.Board.ID, current.Board.Revision, digest, result})
	}
	if result != nil {
		return result
	}
	// A cached verdict never bypasses the final path, identity, size and mtime
	// check. A replacement during hashing or validation still refuses the read.
	return checkNamedSnapshot(path, file)
}
