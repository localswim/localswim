package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

var errWorkspaceUnavailable = errors.New("workspace preferences unavailable")

const workspaceFileName = "localswim-workspace.json"
const maxWorkspaceFile = 64 << 10

type workspace struct {
	ETag           string     `json:"-"`
	Revision       uint64     `json:"revision"`
	PinnedBoardIDs []string   `json:"pinned_board_ids"`
	Publishing     publishing `json:"publishing"`
}

type workspacePreferences struct {
	Revision       uint64                          `json:"revision"`
	PinnedBoardIDs []string                        `json:"pinned_board_ids"`
	Publishing     *workspacePublishingPreferences `json:"publishing,omitempty"`
}

type workspacePublishingPreferences struct {
	Enabled    bool    `json:"enabled"`
	Repository *string `json:"repository"`
}

func (p *workspacePublishingPreferences) config() publishing {
	if p == nil {
		return publishing{}
	}
	return publishing{Enabled: p.Enabled, Repository: p.Repository}
}

func (p *workspacePublishingPreferences) matches(config publishing) bool {
	current := p.config()
	if current.Enabled != config.Enabled {
		return false
	}
	if current.Repository == nil || config.Repository == nil {
		return current.Repository == config.Repository
	}
	return *current.Repository == *config.Repository
}

type workspaceStore struct {
	events      *eventHub
	mu          sync.Mutex
	path        string
	identity    string
	publisher   *automaticPublisher
	publication *publicationCapture
}

func newWorkspace(root string) *workspaceStore {
	digest := sha256.Sum256([]byte(root))
	identity := hex.EncodeToString(digest[:16])
	store := &workspaceStore{path: filepath.Join(root, workspaceFileName), identity: "workspace-" + identity}
	store.publisher = newAutomaticPublisher(root, store.identity, store.capturePublication)
	if preferences, data, err := store.load(); err == nil && preferences.Publishing.config().Enabled {
		store.publication = workspacePublication(preferences.Revision, data)
		store.publisher.schedule()
	}
	return store
}

func (s *workspaceStore) view(p workspacePreferences) workspace {
	return workspace{
		ETag: etag(s.identity, p.Revision), Revision: p.Revision,
		PinnedBoardIDs: p.PinnedBoardIDs, Publishing: s.publisher.status(p.Publishing.config()),
	}
}

func (s *workspaceStore) read() (workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _, err := s.load()
	if err != nil {
		return workspace{}, err
	}
	return s.view(p), nil
}

func (s *workspaceStore) load() (workspacePreferences, []byte, error) {
	unavailable := workspacePreferences{}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return workspacePreferences{Revision: 1, PinnedBoardIDs: []string{}}, nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxWorkspaceFile {
		return unavailable, nil, errWorkspaceUnavailable
	}
	file, err := os.Open(s.path)
	if err != nil {
		return unavailable, nil, errWorkspaceUnavailable
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !os.SameFile(info, current) {
		return unavailable, nil, errWorkspaceUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWorkspaceFile+1))
	if err != nil || len(data) > maxWorkspaceFile {
		return unavailable, nil, errWorkspaceUnavailable
	}
	fields, err := decodeObject(data)
	rawPublishing, hasPublishing := fields["publishing"]
	if err != nil || len(fields) != 2 && !(len(fields) == 3 && hasPublishing) {
		return unavailable, nil, errWorkspaceUnavailable
	}
	var revision *uint64
	if json.Unmarshal(fields["revision"], &revision) != nil || revision == nil || *revision == 0 || *revision > maxSnapshotInteger {
		return unavailable, nil, errWorkspaceUnavailable
	}
	ids, err := parseWorkspacePins(fields["pinned_board_ids"], nil)
	if err != nil {
		return unavailable, nil, errWorkspaceUnavailable
	}
	preferences := workspacePreferences{Revision: *revision, PinnedBoardIDs: ids}
	if hasPublishing {
		config, err := patchPublishing(publishing{}, rawPublishing)
		if err != nil {
			return unavailable, nil, errWorkspaceUnavailable
		}
		preferences.Publishing = &workspacePublishingPreferences{Enabled: config.Enabled, Repository: config.Repository}
	}
	return preferences, data, nil
}

func parseWorkspacePins(raw json.RawMessage, visible func(string) bool) ([]string, error) {
	var ids []string
	if json.Unmarshal(raw, &ids) != nil || ids == nil || len(ids) > 1000 {
		return nil, invalid("pinned_board_ids", "expected an array of up to 1000 board UUIDs")
	}
	seen := make(map[string]bool, len(ids))
	for i, id := range ids {
		canonical, ok := normalizeID(id)
		if !ok || (visible == nil && canonical != id) {
			return nil, invalid("pinned_board_ids", "invalid board UUID")
		}
		if seen[canonical] {
			return nil, invalid("pinned_board_ids", "duplicate board UUID")
		}
		if visible != nil && !visible(canonical) {
			return nil, invalid("pinned_board_ids", "board is not visible")
		}
		seen[canonical] = true
		ids[i] = canonical
	}
	return ids, nil
}

func (s *workspaceStore) patch(expected string, fields map[string]json.RawMessage, visible func(string) bool) (workspace, error) {
	if err := rejectFields(fields, []string{"pinned_board_ids", "publishing"}, false); err != nil {
		return workspace{}, err
	}
	rawPins, hasPins := fields["pinned_board_ids"]
	rawPublishing, hasPublishing := fields["publishing"]
	if !hasPins && !hasPublishing {
		return workspace{}, invalid("workspace", "Expected at least one preference.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _, err := s.load()
	if err != nil {
		return workspace{}, err
	}
	if !matchesStrongTag(expected, etag(s.identity, p.Revision)) {
		return workspace{}, errStale
	}
	ids := p.PinnedBoardIDs
	if hasPins {
		ids, err = parseWorkspacePins(rawPins, visible)
		if err != nil {
			return workspace{}, err
		}
	}
	config := p.Publishing.config()
	if hasPublishing {
		config, err = patchPublishing(config, rawPublishing)
		if err != nil {
			return workspace{}, err
		}
	}
	publishingChanged := !p.Publishing.matches(config)
	if slices.Equal(ids, p.PinnedBoardIDs) && !publishingChanged {
		return s.view(p), nil
	}
	if p.Revision == maxSnapshotInteger {
		return workspace{}, errWorkspaceUnavailable
	}
	p.Revision++
	p.PinnedBoardIDs = ids
	if publishingChanged {
		p.Publishing = &workspacePublishingPreferences{Enabled: config.Enabled, Repository: config.Repository}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return workspace{}, errWorkspaceUnavailable
	}
	if err := writeAtomic(s.path, data); err != nil {
		if errors.Is(err, errWriteUncertain) {
			return workspace{}, err
		}
		return workspace{}, errWorkspaceUnavailable
	}
	if config.Enabled {
		s.publication = workspacePublication(p.Revision, data)
		s.publisher.schedule()
	} else {
		s.publication = nil
		s.publisher.disable()
	}
	if s.events != nil {
		s.events.changed()
	}
	return s.view(p), nil
}

func (s *workspaceStore) closePublisher() {
	s.publisher.close()
}

func (s *workspaceStore) capturePublication() (publicationCapture, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep only startup-admitted bytes and confirmed atomic writes. A later
	// replacement cannot change this capture or join an earlier scheduled edit.
	if s.publication == nil {
		return publicationCapture{}, gitPublicationError("capture_unavailable")
	}
	return *s.publication, nil
}

func workspacePublication(revision uint64, data []byte) *publicationCapture {
	return &publicationCapture{
		revision: revision,
		message:  "localswim workspace preferences",
		files: []publicationFile{
			{path: workspaceFileName, read: func() ([]byte, error) { return data, nil }},
		},
	}
}
