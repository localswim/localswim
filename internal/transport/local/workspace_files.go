package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
)

var (
	ErrWorkspaceFiles            = errors.New("workspace storage unavailable")
	ErrWorkspaceChanged          = errors.New("workspace file changed")
	ErrWorkspaceMissing          = errors.New("workspace root missing")
	ErrWorkspaceBusy             = errors.New("workspace root busy")
	ErrWorkspaceIdentityConflict = errors.New("workspace member identity changed")
)

// WorkspacePath pins the complete native directory chain. Its private path and
// physical key are control-plane values and must never enter browser projections.
// Close may be retried: failed native closes keep the remaining owned handles.
type WorkspacePath struct {
	path, key string
	chain     []string
	close     func() error
	mu        sync.Mutex
	closed    bool
}

// WorkspaceCleanup is opaque custody of native resources whose cleanup did not
// complete. Callers must retain this capability (obtainable with errors.As) and
// retry Close before claiming ownership was released. Formatting is sanitized.
type WorkspaceCleanup interface {
	error
	Close() error
}
type workspaceCleanupError struct {
	mu     sync.Mutex
	cause  error
	close  func() error
	closed bool
}

func (e *workspaceCleanupError) Error() string    { return "workspace cleanup pending" }
func (e *workspaceCleanupError) String() string   { return e.Error() }
func (e *workspaceCleanupError) GoString() string { return e.Error() }
func (e *workspaceCleanupError) Unwrap() error    { return e.cause }
func (e *workspaceCleanupError) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	if e.close() != nil {
		return ErrWorkspaceFiles
	}
	e.closed = true
	return nil
}
func workspaceRetain(cause error, close func() error) error {
	if cause == nil {
		cause = ErrWorkspaceFiles
	}
	return &workspaceCleanupError{cause: cause, close: close}
}

func (p *WorkspacePath) Path() string        { return p.path }
func (p *WorkspacePath) PhysicalKey() string { return p.key }
func (*WorkspacePath) String() string        { return "workspace path [private]" }
func (p *WorkspacePath) GoString() string    { return p.String() }
func (p *WorkspacePath) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if e := p.close(); e != nil {
		return ErrWorkspaceFiles
	}
	p.closed = true
	return nil
}

func workspaceSafeDir(path string) bool {
	if !safeDir(path) {
		return false
	}
	for _, part := range strings.Split(path[3:], `\`) {
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" {
			return false
		}
		if len(base) > 3 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
			suffix := strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT")
			if strings.Contains("123456789¹²³", suffix) && suffix != "" {
				return false
			}
		}
	}
	return true
}

// OpenProjectRoot accepts an explicit local directory, creating only the named
// leaf when requested. Existing project roots need not be account-private.
func OpenProjectRoot(path string, create bool) (*WorkspacePath, error) {
	return workspacePath(path, create, false, false)
}

// OpenMemberRoot requires a strict physical descendant of the explicit project
// root. Creation refuses an existing leaf; no recursive parent creation occurs.
func OpenMemberRoot(projectRoot, dataDir string, create bool) (*WorkspacePath, error) {
	p, e := OpenProjectRoot(projectRoot, false)
	if e != nil {
		return p, e
	}
	if !workspaceSafeDir(dataDir) || !strings.HasPrefix(strings.ToLower(dataDir), strings.ToLower(p.path)+`\`) {
		if p.Close() != nil {
			return p, ErrWorkspaceFiles
		}
		return nil, ErrWorkspaceFiles
	}
	m, e := workspacePathWithAncestor(dataDir, create, true, create, p)
	if e == nil && (!strings.HasPrefix(strings.ToLower(m.path), strings.ToLower(p.path)+`\`) || m.key == p.key || len(m.chain) <= len(p.chain) || m.chain[len(p.chain)-1] != p.key) {
		e = ErrWorkspaceFiles
	}
	if ce := p.Close(); ce != nil { // Keep the project pin reachable for retry.
		if m == nil {
			return p, ce
		}
		previous := m.close
		m.close = func() error { return errors.Join(previous(), p.Close()) }
		e = ErrWorkspaceFiles
	}
	if e != nil && m != nil {
		if m.Close() != nil {
			return m, e
		}
		return nil, e
	}
	return m, e
}

// WorkspaceFiles owns one private root and its native exclusion handle. Callers
// perform strict schema/revision validation before byte-exact CAS publication.
// Its mutex is local to this file owner, never the registry bookkeeping mutex.
type WorkspaceFiles struct {
	mu     sync.Mutex
	root   *WorkspacePath
	lock   *os.File
	closed bool
}

func OpenWorkspaceFiles(root string, create bool) (*WorkspaceFiles, error) {
	p, e := workspacePath(root, create, true, false)
	if e != nil {
		if p != nil {
			return &WorkspaceFiles{root: p}, e
		}
		return nil, e
	}
	f := &WorkspaceFiles{root: p}
	f.lock, e = workspaceLock(p.path)
	if e != nil {
		if f.Close() != nil {
			return f, e
		}
		return nil, e
	}
	return f, nil
}
func (f *WorkspaceFiles) Root() string     { return f.root.path }
func (*WorkspaceFiles) String() string     { return "workspace files [private]" }
func (f *WorkspaceFiles) GoString() string { return f.String() }
func workspaceName(name string, limit int64) bool {
	switch name {
	case "workspace.json":
		return limit > 0 && limit <= 1<<20
	case "workspace-preferences.json", "workspace-service.json":
		return limit > 0 && limit <= 64<<10
	}
	return false
}
func (f *WorkspaceFiles) Read(name string, limit int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.lock == nil || !workspaceName(name, limit) {
		return nil, ErrWorkspaceFiles
	}
	return workspaceRead(filepath.Join(f.root.path, name), limit)
}
func (f *WorkspaceFiles) CompareAndSwap(name string, expected, replacement []byte, limit int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.lock == nil || !workspaceName(name, limit) || int64(len(replacement)) > limit {
		return ErrWorkspaceFiles
	}
	p := filepath.Join(f.root.path, name)
	current, e := workspaceRead(p, limit)
	if errors.Is(e, os.ErrNotExist) {
		if expected != nil {
			return ErrWorkspaceChanged
		}
	} else if e != nil {
		return e
	} else if expected == nil || !bytes.Equal(current, expected) {
		return ErrWorkspaceChanged
	}
	return workspacePublish(p, replacement, expected != nil)
}
func (f *WorkspaceFiles) RemoveIf(name string, expected []byte, limit int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.lock == nil || name != "workspace-service.json" || !workspaceName(name, limit) || expected == nil {
		return ErrWorkspaceFiles
	}
	p := filepath.Join(f.root.path, name)
	current, e := workspaceRead(p, limit)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if !bytes.Equal(current, expected) {
		return ErrWorkspaceChanged
	}
	return workspaceRemove(p)
}
func (f *WorkspaceFiles) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	// Keep exclusion if any pin close is unresolved.
	if f.root != nil {
		if e := f.root.Close(); e != nil {
			return e
		}
	}
	if f.lock != nil {
		if e := f.lock.Close(); e != nil {
			return ErrWorkspaceFiles
		}
		f.lock = nil
	}
	f.closed = true
	return nil
}

// ReadWorkspaceFile supplies bounded private rendezvous reads while the host
// owns exclusion. It grants no authority and does not interpret file contents.
func ReadWorkspaceFile(root, name string, limit int64) (out []byte, err error) {
	if !workspaceName(name, limit) {
		return nil, ErrWorkspaceFiles
	}
	p, e := workspacePath(root, false, true, false)
	if e != nil {
		if p != nil {
			return nil, workspaceRetain(e, p.Close)
		}
		return nil, e
	}
	defer func() {
		var cleanup WorkspaceCleanup
		if errors.As(err, &cleanup) {
			err = workspaceRetain(ErrWorkspaceFiles, func() error {
				if cleanup.Close() != nil {
					return ErrWorkspaceFiles
				}
				return p.Close()
			})
			return
		}
		if p.Close() != nil {
			out = nil
			err = workspaceRetain(ErrWorkspaceFiles, p.Close)
		}
	}()
	return workspaceRead(filepath.Join(p.path, name), limit)
}

// InspectBoard validates an offline initialized store without publishing service
// metadata, creating missing files or altering actors/grants. Inspection uses the
// existing read-only store validator under the existing native exclusion file.
func InspectBoard(ctx context.Context, dataDir string, expected *BoardIdentity) (out BoardIdentity, err error) {
	return inspectWorkspaceBoard(ctx, dataDir, expected, func(f *os.File) io.Closer { return f })
}

type workspaceInspectionOwner struct {
	root           *WorkspacePath
	lock, identity io.Closer
	cleanup        WorkspaceCleanup
}

func (o *workspaceInspectionOwner) Close() error {
	if o.cleanup != nil {
		if o.cleanup.Close() != nil {
			return ErrWorkspaceFiles
		}
		o.cleanup = nil
	}
	if o.identity != nil {
		if o.identity.Close() != nil {
			return ErrWorkspaceFiles
		}
		o.identity = nil
	}
	if o.root != nil {
		if o.root.Close() != nil {
			return ErrWorkspaceFiles
		}
		o.root = nil
	}
	if o.lock != nil {
		if o.lock.Close() != nil {
			return ErrWorkspaceFiles
		}
		o.lock = nil
	}
	return nil
}
func inspectWorkspaceBoard(ctx context.Context, dataDir string, expected *BoardIdentity, wrapIdentity func(*os.File) io.Closer) (out BoardIdentity, err error) {
	if ctx == nil || ctx.Err() != nil {
		return out, ErrWorkspaceFiles
	}
	p, e := workspacePath(dataDir, false, true, false)
	if e != nil {
		if p != nil {
			return out, workspaceRetain(e, p.Close)
		}
		return out, e
	}
	owned := &workspaceInspectionOwner{root: p}
	defer func() {
		if owned.Close() != nil {
			out = BoardIdentity{}
			err = workspaceRetain(ErrWorkspaceFiles, owned.Close)
		}
	}()
	f, e := workspaceExistingLock(p.path)
	if e != nil {
		var cleanup WorkspaceCleanup
		if errors.As(e, &cleanup) {
			owned.cleanup = cleanup
		}
		return out, e
	}
	owned.lock = f
	idFile, b, e := workspaceIdentity(p.path)
	if e != nil {
		var cleanup WorkspaceCleanup
		if errors.As(e, &cleanup) {
			owned.cleanup = cleanup
		}
		return out, e
	}
	owned.identity = wrapIdentity(idFile)
	var id identity
	if decode(b, &id) != nil || !id.valid() {
		return out, ErrWorkspaceFiles
	}
	out = BoardIdentity{id.InstallationID, id.BoardID, id.Incarnation}
	if expected != nil && out != *expected {
		return BoardIdentity{}, ErrWorkspaceIdentityConflict
	}
	boardDir := filepath.Join(p.path, "board")
	if id.Version == 2 {
		if _, e := restoreEvidence(p.path, id, id.RestoredRequestID); e != nil {
			return BoardIdentity{}, ErrWorkspaceFiles
		}
		boardDir = filepath.Join(p.path, "data")
	}
	// InspectExisting owns its SELECT-only SQL/native cleanup under the accepted
	// store contract. It exposes no retry handle; this seam does not extend that
	// contract or claim custody of its hidden resources.
	e = S.InspectExisting(ctx, boardDir, schema.Identity{BoardID: id.BoardID, Incarnation: id.Incarnation}, func(S.InspectionRead) error { return nil })
	if e != nil {
		return BoardIdentity{}, ErrWorkspaceFiles
	}
	return out, nil
}

func (r *Runtime) Epoch() string { return r.epoch }
