package workspace

import (
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

// SelectionRoot is private native custody, not a public workspace projection.
// Callers retain it even when acquisition returns an error, until Close succeeds.
type SelectionRoot struct {
	WorkspaceID     T.UUIDv4
	Identity        T.WorkspaceIdentity
	dataDir         string
	project, member *L.WorkspacePath
}

func (*SelectionRoot) String() string               { return "workspace selection [redacted]" }
func (s *SelectionRoot) GoString() string           { return s.String() }
func (*SelectionRoot) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (s *SelectionRoot) DataDir() string            { return s.dataDir }
func (s *SelectionRoot) Close() error {
	if s.member != nil {
		if s.member.Close() != nil {
			return ErrUnavailable
		}
		s.member = nil
	}
	if s.project != nil {
		if s.project.Close() != nil {
			return ErrUnavailable
		}
		s.project = nil
	}
	return nil
}

// ReadSelection accepts only the immutable registered mapping and verifies its
// physical roots. It never opens SQL, bootstraps a board or rewrites membership.
func ReadSelection(root string, p, b T.WorkspaceKey) (*SelectionRoot, error) {
	raw, e := L.ReadWorkspaceFile(root, "workspace.json", ConfigLimit)
	if e != nil {
		return nil, e
	}
	c, e := decodeConfig(raw)
	if e != nil {
		return nil, e
	}
	pr, ok := c.project(p)
	if !ok {
		return nil, ErrUnavailable
	}
	m, ok := c.member(p, b)
	if !ok {
		return nil, ErrUnavailable
	}
	s := &SelectionRoot{WorkspaceID: c.WorkspaceID, Identity: m.Identity, dataDir: string(m.DataDir)}
	s.project, e = L.OpenProjectRoot(string(pr.Root), false)
	if e != nil {
		return s, e
	}
	if s.project.PhysicalKey() != pr.PhysicalKey || !pathEqual(s.project.Path(), string(pr.Root)) {
		return s, ErrUnavailable
	}
	s.member, e = L.OpenMemberRoot(s.project.Path(), string(m.DataDir), false)
	if e != nil {
		return s, e
	}
	if s.member.PhysicalKey() != m.PhysicalKey || !pathEqual(s.member.Path(), string(m.DataDir)) {
		return s, ErrUnavailable
	}
	return s, nil
}
