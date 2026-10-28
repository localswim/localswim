// Package workspace owns explicit independent-board membership and the private
// workspace lifecycle. Board engines retain all durable domain authority.
package workspace

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

const ConfigLimit int64 = 1 << 20
const MemberLimit = 256

var ErrUnavailable = errors.New("workspace unavailable")

type project struct {
	Key         T.WorkspaceKey   `json:"key"`
	Label       T.WorkspaceLabel `json:"label"`
	Root        T.WorkspacePath  `json:"root"`
	PhysicalKey string           `json:"physicalKey"`
}
type member struct {
	ProjectKey  T.WorkspaceKey      `json:"projectKey"`
	BoardKey    T.WorkspaceKey      `json:"boardKey"`
	Label       T.WorkspaceLabel    `json:"label"`
	DataDir     T.WorkspacePath     `json:"dataDir"`
	PhysicalKey string              `json:"physicalKey"`
	Identity    T.WorkspaceIdentity `json:"identity"`
}
type configuration struct {
	SchemaVersion  A.Version  `json:"schemaVersion"`
	WorkspaceID    T.UUIDv4   `json:"workspaceId"`
	Port           A.Positive `json:"port"`
	ConfigRevision A.Positive `json:"configRevision"`
	Projects       []project  `json:"projects"`
	Members        []member   `json:"members"`
}

func configBytes(c configuration) ([]byte, error) {
	if e := validateConfig(c); e != nil {
		return nil, e
	}
	b, e := json.Marshal(c)
	if e != nil || int64(len(b)) > ConfigLimit {
		return nil, ErrUnavailable
	}
	return b, nil
}
func decodeConfig(b []byte) (configuration, error) {
	if int64(len(b)) > ConfigLimit {
		return configuration{}, ErrUnavailable
	}
	c, e := A.Decode[configuration](b)
	if e != nil {
		return configuration{}, ErrUnavailable
	}
	return c, validateConfig(c)
}
func validateConfig(c configuration) error {
	if c.SchemaVersion != 1 || c.ConfigRevision < 1 || uint64(c.ConfigRevision) >= contract.MaxInteger || c.Port < 1024 || c.Port > 65535 || len(c.Members) > MemberLimit || len(c.Projects) > MemberLimit || T.Validate(T.WorkspaceRuntimeMode{APIVersion: 1, Mode: "workspace", WorkspaceID: c.WorkspaceID}) != nil {
		return ErrUnavailable
	}
	projects := map[T.WorkspaceKey]project{}
	roots := map[string]bool{}
	for _, p := range c.Projects {
		if T.Validate(T.WorkspaceProject{Key: p.Key, Label: p.Label}) != nil || !validRoot(string(p.Root)) || p.PhysicalKey == "" || len(p.PhysicalKey) > 256 || projects[p.Key].Key != "" || roots[p.PhysicalKey] {
			return ErrUnavailable
		}
		projects[p.Key] = p
		roots[p.PhysicalKey] = true
	}
	keys := map[string]bool{}
	ids := map[string]bool{}
	paths := map[string]bool{}
	for _, m := range c.Members {
		k := memberKey(m.ProjectKey, m.BoardKey)
		if projects[m.ProjectKey].Key == "" || T.Validate(T.WorkspaceProject{Key: m.BoardKey, Label: m.Label}) != nil || T.Validate(m.Identity) != nil || !validRoot(string(m.DataDir)) || !strings.HasPrefix(strings.ToLower(string(m.DataDir)), strings.ToLower(string(projects[m.ProjectKey].Root))+`\`) || m.PhysicalKey == "" || len(m.PhysicalKey) > 256 || keys[k] || ids[string(m.Identity.BoardID)] || paths[m.PhysicalKey] {
			return ErrUnavailable
		}
		keys[k] = true
		ids[string(m.Identity.BoardID)] = true
		paths[m.PhysicalKey] = true
	}
	return nil
}
func memberKey(p, b T.WorkspaceKey) string { return string(p) + "/" + string(b) }
func newConfiguration(port int) configuration {
	return configuration{1, T.UUIDv4(uuid.NewString()), A.Positive(port), 1, []project{}, []member{}}
}
func (c configuration) project(key T.WorkspaceKey) (project, bool) {
	for _, p := range c.Projects {
		if p.Key == key {
			return p, true
		}
	}
	return project{}, false
}
func (c configuration) member(p, b T.WorkspaceKey) (member, bool) {
	for _, m := range c.Members {
		if m.ProjectKey == p && m.BoardKey == b {
			return m, true
		}
	}
	return member{}, false
}
func pathEqual(a, b string) bool { return strings.EqualFold(a, b) }

func validRoot(p string) bool {
	if len(p) < 4 || len(p) > 180 || p[1] != ':' || p[2] != '\\' || !((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		return false
	}
	for _, part := range strings.Split(p[3:], `\`) {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:/*?"<>|~`) || strings.ContainsRune(part, 0) {
			return false
		}
	}
	return true
}
