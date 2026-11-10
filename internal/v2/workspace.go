package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

var errWorkspaceUnavailable = errors.New("workspace preferences unavailable")

const workspaceFileName = "localswim-workspace.json"
const maxWorkspaceFile = 64 << 10

type workspace struct {
	ETag           string     `json:"etag"`
	PinnedBoardIDs []string   `json:"pinned_board_ids"`
	Publishing     publishing `json:"publishing"`
}

type workspacePreferences struct {
	Revision       uint64   `json:"revision"`
	PinnedBoardIDs []string `json:"pinned_board_ids"`
}

type workspaceStore struct {
	mu       sync.Mutex
	path     string
	identity string
}

func newWorkspace(root string) *workspaceStore {
	digest := sha256.Sum256([]byte(root))
	identity := hex.EncodeToString(digest[:16])
	return &workspaceStore{path: filepath.Join(root, workspaceFileName), identity: "workspace-" + identity}
}

func (s *workspaceStore) view(p workspacePreferences) workspace {
	return workspace{ETag: etag(s.identity, p.Revision), PinnedBoardIDs: p.PinnedBoardIDs}
}

func (s *workspaceStore) read() (workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.load()
	if err != nil {
		return workspace{}, err
	}
	return s.view(p), nil
}

func (s *workspaceStore) load() (workspacePreferences, error) {
	unavailable := workspacePreferences{}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return workspacePreferences{PinnedBoardIDs: []string{}}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxWorkspaceFile {
		return unavailable, errWorkspaceUnavailable
	}
	file, err := os.Open(s.path)
	if err != nil {
		return unavailable, errWorkspaceUnavailable
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !os.SameFile(info, current) {
		return unavailable, errWorkspaceUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWorkspaceFile+1))
	if err != nil || len(data) > maxWorkspaceFile {
		return unavailable, errWorkspaceUnavailable
	}
	fields, err := decodeObject(data)
	if err != nil || len(fields) != 2 {
		return unavailable, errWorkspaceUnavailable
	}
	var revision *uint64
	if json.Unmarshal(fields["revision"], &revision) != nil || revision == nil || *revision == 0 {
		return unavailable, errWorkspaceUnavailable
	}
	rawIDs := fields["pinned_board_ids"]
	if rawIDs == nil {
		rawIDs = fields["pinnedBoardIds"]
	}
	ids, err := parseWorkspacePins(rawIDs, nil)
	if err != nil {
		return unavailable, errWorkspaceUnavailable
	}
	return workspacePreferences{Revision: *revision, PinnedBoardIDs: ids}, nil
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
	if _, present := fields["publishing"]; present {
		return workspace{}, invalid("publishing", "workspace publishing is unavailable")
	}
	raw, present := fields["pinned_board_ids"]
	if !present {
		return workspace{}, invalid("pinned_board_ids", "required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.load()
	if err != nil {
		return workspace{}, err
	}
	if !matchesStrongTag(expected, s.view(p).ETag) {
		return workspace{}, errStale
	}
	ids, err := parseWorkspacePins(raw, visible)
	if err != nil {
		return workspace{}, err
	}
	if slices.Equal(ids, p.PinnedBoardIDs) {
		return s.view(p), nil
	}
	if p.Revision == math.MaxUint64 {
		return workspace{}, errWorkspaceUnavailable
	}
	p.Revision++
	p.PinnedBoardIDs = ids
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
	return s.view(p), nil
}
