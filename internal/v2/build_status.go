package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

const (
	buildManifestName  = "candidate.json"
	buildManifestLimit = 4 << 10
	buildArtifactLimit = 64 << 20
	buildPackage       = "github.com/localswim/localswim/cmd/localswim"
	buildModule        = "github.com/localswim/localswim"
)

type serviceStatus struct {
	Port              int     `json:"port"`
	ETag              string  `json:"etag"`
	ServiceInstanceID string  `json:"service_instance_id"`
	BuildID           string  `json:"build_id"`
	State             string  `json:"state"`
	APIVersion        string  `json:"api_version"`
	RestartErrorCode  *string `json:"restart_error_code"`
}

type build struct {
	ID         string  `json:"id"`
	Version    string  `json:"version"`
	Validation string  `json:"validation"`
	ErrorCode  *string `json:"error_code"`
}

type buildStatus struct {
	root    string
	running serviceStatus

	mu             sync.Mutex
	stagedID       string
	stagedVersion  string
	stagedIdentity string
	validatedID    string
	activeVersion  string
	activeDigest   string
}

var runningBuildID = newID()

func newBuildStatus(root string) *buildStatus {
	instanceID := newOperationID()
	return &buildStatus{
		root: root,
		running: serviceStatus{
			ETag:              etag(instanceID, 0),
			ServiceInstanceID: instanceID,
			BuildID:           runningBuildID,
			State:             "running",
			APIVersion:        Version,
		},
	}
}

func (s *buildStatus) service() serviceStatus {
	return s.running
}

func (s *buildStatus) builds() []build {
	if s.root == "" {
		return []build{}
	}

	// Keep the cost of reading and inspecting a local candidate to one at a time.
	s.mu.Lock()
	defer s.mu.Unlock()
	items, _ := s.inspectCandidate()
	s.validatedID = ""
	if len(items) == 1 && items[0].Validation == "valid" {
		s.validatedID = items[0].ID
	}
	return items
}

type activationImage struct {
	bytes   []byte
	version string
	digest  string
}

func (s *buildStatus) activationImage(id string) (activationImage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.validatedID || id == s.running.BuildID {
		return activationImage{}, errActivationBuild
	}
	items, image := s.inspectCandidate()
	if len(items) != 1 || items[0].ID != id || items[0].Validation != "valid" {
		return activationImage{}, errActivationBuild
	}
	return activationImage{bytes: image, version: s.stagedVersion, digest: s.stagedIdentity}, nil
}

func (s *buildStatus) inspectCandidate() ([]build, []byte) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return s.candidate("unknown", "", "candidate_area_unavailable"), nil
		}
		return s.emptyStage(), nil
	}
	defer root.Close()

	raw, err := readBuildFile(root, buildManifestName, buildManifestLimit)
	if errors.Is(err, fs.ErrNotExist) {
		_, artifactErr := root.Lstat(buildArtifactName())
		if !errors.Is(artifactErr, fs.ErrNotExist) {
			return s.candidate("unknown", "", "candidate_manifest_missing"), nil
		}
		return s.emptyStage(), nil
	}
	if err != nil {
		return s.candidate("unknown", "", "candidate_manifest_unreadable"), nil
	}

	version, digest, ok := parseBuildManifest(raw)
	if !ok {
		return s.candidate("unknown", string(raw), "candidate_manifest_invalid"), nil
	}
	artifact, err := readBuildFile(root, buildArtifactName(), buildArtifactLimit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s.candidate(version, digest, "candidate_binary_missing"), nil
	case err != nil:
		return s.candidate(version, digest, "candidate_binary_unreadable"), nil
	}
	sum := sha256.Sum256(artifact)
	if hex.EncodeToString(sum[:]) != digest {
		return s.candidate(version, digest, "candidate_binary_digest_mismatch"), nil
	}
	if !compatibleBuild(artifact) {
		return s.candidate(version, digest, "candidate_binary_incompatible"), nil
	}
	return s.candidate(version, digest, ""), artifact
}

func buildArtifactName() string {
	if runtime.GOOS == "windows" {
		return "candidate.exe"
	}
	return "candidate"
}

func (s *buildStatus) emptyStage() []build {
	s.stagedID, s.stagedVersion, s.stagedIdentity = "", "", ""
	return []build{}
}

func (s *buildStatus) candidate(version, identity, code string) []build {
	if s.stagedID == "" || s.stagedVersion != version || s.stagedIdentity != identity {
		s.stagedID = newID()
		if s.activeVersion == version && s.activeDigest == identity {
			s.stagedID = s.running.BuildID
		}
		s.stagedVersion, s.stagedIdentity = version, identity
	}
	item := build{ID: s.stagedID, Version: version, Validation: "valid"}
	if code != "" {
		item.Validation = "invalid"
		item.ErrorCode = &code
	}
	return []build{item}
}

func parseBuildManifest(data []byte) (version, digest string, valid bool) {
	fields, err := decodeObject(data)
	if err != nil || len(fields) != 2 ||
		json.Unmarshal(fields["version"], &version) != nil ||
		json.Unmarshal(fields["sha256"], &digest) != nil {
		return "", "", false
	}
	if !validBuildManifestFields(version, digest) {
		return "", "", false
	}
	return version, digest, true
}

func validBuildManifestFields(version, digest string) bool {
	if len(version) == 0 || len(version) > 128 || len(digest) != 64 {
		return false
	}
	for _, ch := range version {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' ||
			ch >= 'A' && ch <= 'Z' || strings.ContainsRune(".+_-", ch)) {
			return false
		}
	}
	_, err := hex.DecodeString(digest)
	return err == nil && strings.ToLower(digest) == digest
}

func readBuildFile(root *os.Root, name string, limit int64) ([]byte, error) {
	entry, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() || entry.Size() > limit {
		return nil, fs.ErrInvalid
	}
	file, err := openBuildFile(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(entry, info) || info.Size() > limit {
		return nil, fs.ErrInvalid
	}
	// Read at most the observed size plus one byte, and detect concurrent changes.
	buf := make([]byte, info.Size()+1)
	n, err := io.ReadFull(file, buf)
	if (err != io.EOF && err != io.ErrUnexpectedEOF) || int64(n) != info.Size() {
		return nil, fs.ErrInvalid
	}
	current, err := root.Lstat(name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) ||
		info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
		return nil, fs.ErrInvalid
	}
	return buf[:n], nil
}

func compatibleBuild(image []byte) bool {
	data := nativeBuildRegion(image)
	if len(data) == 0 {
		return false
	}
	// The current Go toolchain emits 16-byte-aligned inline build information.
	// Search only the file-backed data region selected from the native image.
	const magic = "\xff Go buildinf:"
	const maxCandidates = 8
	offset := 0
	for range maxCandidates {
		match := bytes.Index(data[offset:], []byte(magic))
		if match < 0 {
			return false
		}
		start := offset + match
		offset = start + len(magic)
		if start%16 != 0 || len(data)-start < 32 || data[start+15]&2 == 0 {
			continue
		}
		goVersion, rest := buildInfoString(data[start+32:], 128)
		module, _ := buildInfoString(rest, 32<<10)
		if len(module) < 33 || module[len(module)-17] != '\n' {
			continue
		}
		info, err := debug.ParseBuildInfo(string(module[16 : len(module)-16]))
		if err != nil || !bytes.HasPrefix(goVersion, []byte("go1.")) ||
			info.Path != buildPackage || info.Main.Path != buildModule {
			continue
		}
		settings := make(map[string]string)
		for _, setting := range info.Settings {
			settings[setting.Key] = setting.Value
		}
		return settings["GOOS"] == runtime.GOOS && settings["GOARCH"] == runtime.GOARCH &&
			(settings["-buildmode"] == "exe" || settings["-buildmode"] == "pie")
	}
	return false
}

func buildInfoString(data []byte, limit uint64) (value, rest []byte) {
	size, count := binary.Uvarint(data)
	if count <= 0 || size > limit || size > uint64(len(data)-count) {
		return nil, nil
	}
	end := count + int(size)
	return data[count:end], data[end:]
}
