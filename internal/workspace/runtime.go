package workspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

type Options struct {
	DataDir string
	Port    int
	Assets  http.Handler
}
type rendezvous struct {
	MetadataVersion A.Version `json:"metadataVersion"`
	WorkspaceID     T.UUIDv4  `json:"workspaceId"`
	Origin          string    `json:"origin"`
	Epoch           T.UUIDv4  `json:"epoch"`
	AutomationToken string    `json:"automationToken"`
}
type ClientInfo struct {
	WorkspaceID, Origin, Epoch string
	Token                      T.SecretToken
}

func (ClientInfo) String() string               { return "workspace connection [redacted]" }
func (c ClientInfo) GoString() string           { return c.String() }
func (ClientInfo) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func ReadClient(root string) (ClientInfo, error) {
	b, e := L.ReadWorkspaceFile(root, "workspace.json", ConfigLimit)
	if e != nil {
		return ClientInfo{}, e
	}
	c, e := decodeConfig(b)
	if e != nil {
		return ClientInfo{}, e
	}
	b, e = L.ReadWorkspaceFile(root, "workspace-service.json", 64<<10)
	if e != nil {
		return ClientInfo{}, e
	}
	m, e := decodeRendezvous(b, c)
	if e != nil {
		return ClientInfo{}, e
	}
	return ClientInfo{string(m.WorkspaceID), m.Origin, string(m.Epoch), T.SecretToken(m.AutomationToken)}, nil
}

type activation struct {
	availability T.WorkspaceAvailability
	engine       *L.Runtime
	running      bool
}
type runtimeHost interface {
	Origin() string
	Done() <-chan struct{}
	Close() error
	Attach(*L.Runtime) error
}
type Runtime struct {
	mu                sync.Mutex
	control           sync.Mutex
	closeMu           sync.Mutex
	files             *L.WorkspaceFiles
	host              runtimeHost
	config            configuration
	raw               []byte
	states            map[string]*activation
	slots             chan struct{}
	jobs              sync.WaitGroup
	ctx               context.Context
	cancel            context.CancelFunc
	closing, closed   bool
	closeErr          error
	service           []byte
	cleanups          []interface{ Close() error }
	guards            []*L.WorkspacePath
	orphans           []*L.Runtime
	done              chan struct{}
	catalogJobs       map[string]*catalogJob
	catalogPending    []*catalogJob
	activationPending []member
	preferActivation  bool
	// catalogProbe is a test seam; production reads the attached engine.
	catalogProbe func(context.Context, *L.Runtime, string) (L.CatalogSnapshot, error)
	// publishFault is a labeled test seam. after-config-publication models an
	// ambiguous reply after durable publication; production never sets it.
	publishFault func(string) error

	// Preference publication is independent of config and board revisions.
	preferencesMu       sync.Mutex
	preferencesRaw      []byte
	preferencesObserved bool
	// preferencesFault models an interrupted reply; production leaves it nil.
	preferencesFault func(string) error
}

func Start(ctx context.Context, o Options) (*Runtime, error) {
	if ctx == nil || ctx.Err() != nil || o.Port != 0 && (o.Port < 1024 || o.Port > 65535) {
		return nil, ErrUnavailable
	}
	f, e := L.OpenWorkspaceFiles(o.DataDir, true)
	runctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{files: f, states: map[string]*activation{}, slots: make(chan struct{}, 4), ctx: runctx, cancel: cancel, done: make(chan struct{})}
	fail := func() (*Runtime, error) {
		if ce := r.Close(); ce != nil {
			return r, ErrUnavailable
		}
		return nil, ErrUnavailable
	}
	if e != nil {
		r.retainCleanup(e)
		return fail()
	}
	raw, e := f.Read("workspace.json", ConfigLimit)
	var c configuration
	if errors.Is(e, os.ErrNotExist) {
		if o.Port == 0 {
			return fail()
		}
		entries, er := os.ReadDir(f.Root())
		if er != nil {
			return fail()
		}
		for _, entry := range entries {
			if entry.Name() != "workspace.lock" {
				return fail()
			}
		}
		c = newConfiguration(o.Port)
		raw, e = configBytes(c)
		if e == nil {
			e = f.CompareAndSwap("workspace.json", nil, raw, ConfigLimit)
		}
	} else if e == nil {
		c, e = decodeConfig(raw)
	}
	if e != nil || o.Port != 0 && A.Positive(o.Port) != c.Port {
		r.retainCleanup(e)
		return fail()
	}
	r.config = c
	r.raw = raw
	h, e := L.StartWorkspaceHost(runctx, L.WorkspaceHostOptions{WorkspaceID: string(c.WorkspaceID), Port: int(c.Port), Assets: o.Assets, Workspace: r})
	if e != nil {
		cancel()
		return fail()
	}
	r.host = h
	m := rendezvous{1, c.WorkspaceID, h.Origin(), T.UUIDv4(h.Epoch()), string(h.AutomationToken())}
	r.service, _ = json.Marshal(m)
	old, er := f.Read("workspace-service.json", 64<<10)
	if errors.Is(er, os.ErrNotExist) {
		old = nil
		er = nil
	}
	if er == nil && old != nil {
		if _, ve := decodeRendezvous(old, c); ve != nil {
			er = ve
		}
	}
	if er == nil {
		er = f.CompareAndSwap("workspace-service.json", old, r.service, 64<<10)
	}
	if er != nil {
		r.retainCleanup(er)
		return fail()
	}
	for _, m := range c.Members {
		r.activate(m)
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
		case <-h.Done():
			_ = r.Close()
		}
	}()
	return r, nil
}
func (r *Runtime) Origin() string {
	if r.host == nil {
		return ""
	}
	return r.host.Origin()
}
func (r *Runtime) Done() <-chan struct{} { return r.done }
func (r *Runtime) Close() error {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return r.closeErr
	}
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	r.cancel()
	if r.host != nil {
		if e := r.host.Close(); e != nil {
			select {
			case <-r.host.Done():
				r.closeErr = ErrUnavailable
			default:
				return ErrUnavailable
			}
		}
	}
	r.jobs.Wait()
	for _, cleanup := range r.cleanups {
		if e := cleanup.Close(); e != nil {
			return ErrUnavailable
		}
	}
	r.cleanups = nil
	if r.service != nil && r.files != nil {
		if e := r.files.RemoveIf("workspace-service.json", r.service, 64<<10); e != nil && !errors.Is(e, L.ErrWorkspaceChanged) {
			r.retainCleanup(e)
			return ErrUnavailable
		}
	}
	for _, engine := range r.orphans {
		if e := engine.Close(); e != nil {
			select {
			case <-engine.Done():
				r.closeErr = ErrUnavailable
			default:
				return ErrUnavailable
			}
		}
	}
	r.orphans = nil
	for _, g := range r.guards {
		if e := g.Close(); e != nil {
			return ErrUnavailable
		}
	}
	r.guards = nil
	if r.files != nil {
		if e := r.files.Close(); e != nil {
			return ErrUnavailable
		}
	}
	r.closed = true
	close(r.done)
	return r.closeErr
}
func (r *Runtime) snapshot() configuration { r.mu.Lock(); defer r.mu.Unlock(); return r.config }
func (r *Runtime) retain(g *L.WorkspacePath) {
	if g == nil {
		return
	}
	if e := g.Close(); e != nil {
		r.mu.Lock()
		r.guards = append(r.guards, g)
		r.mu.Unlock()
	}
}
func localIdentity(x T.WorkspaceIdentity) *L.BoardIdentity {
	return &L.BoardIdentity{InstallationID: string(x.InstallationID), BoardID: string(x.BoardID), Incarnation: string(x.Incarnation)}
}
func wireIdentity(x L.BoardIdentity) T.WorkspaceIdentity {
	return T.WorkspaceIdentity{InstallationID: T.UUIDv4(x.InstallationID), BoardID: A.UUID(x.BoardID), Incarnation: A.UUID(x.Incarnation)}
}
func (r *Runtime) activate(m member) {
	k := memberKey(m.ProjectKey, m.BoardKey)
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return
	}
	s := r.states[k]
	if s == nil {
		s = &activation{availability: "pending"}
		r.states[k] = s
	}
	if s.running || s.engine != nil {
		r.mu.Unlock()
		return
	}
	s.running = true
	s.availability = "pending"
	r.activationPending = append(r.activationPending, m)
	r.pumpActivationLocked()
	r.mu.Unlock()
}

// Pending activation is data, not a goroutine waiting for a slot. Activation
// and projection therefore share at most four actually outstanding jobs.
func (r *Runtime) pumpActivationLocked() {
	r.pumpCatalogLocked()
}

func (r *Runtime) startActivation(m member) {
	k := memberKey(m.ProjectKey, m.BoardKey)
	go func() {
		defer r.jobs.Done()
		defer func() {
			r.mu.Lock()
			<-r.slots
			// Give projections already queued a turn before admitting more
			// activations. Their own FIFO prevents first-member starvation.
			r.pumpCatalogLocked()
			r.pumpActivationLocked()
			r.mu.Unlock()
		}()
		if r.ctx.Err() != nil {
			r.finish(k, nil, "unavailable")
			return
		}
		c := r.snapshot()
		p, ok := c.project(m.ProjectKey)
		if !ok {
			r.finish(k, nil, "invalid")
			return
		}
		g, e := r.openMember(p, string(m.DataDir), false)
		if e != nil {
			r.retain(g)
			r.finish(k, nil, classify(e))
			return
		}
		defer r.retain(g)
		if g.PhysicalKey() != m.PhysicalKey || !pathEqual(g.Path(), string(m.DataDir)) {
			r.finish(k, nil, "identity_conflict")
			return
		}
		acquirectx, cancel := context.WithTimeout(r.ctx, time.Second)
		defer cancel()
		if _, e = L.InspectBoard(acquirectx, g.Path(), localIdentity(m.Identity)); e != nil {
			r.retainCleanup(e)
			r.finish(k, nil, classify(e))
			return
		}
		engine, e := L.OpenWorkspaceEngine(r.ctx, acquirectx, L.EngineOptions{DataDir: g.Path(), Origin: r.Origin(), WorkspaceID: string(c.WorkspaceID), ExpectedIdentity: localIdentity(m.Identity)})
		if e != nil {
			r.retainEngine(engine)
			r.finish(k, nil, classify(e))
			return
		}
		if e = r.host.Attach(engine); e != nil {
			if ce := engine.Close(); ce != nil {
				r.mu.Lock()
				r.orphans = append(r.orphans, engine)
				r.mu.Unlock()
			}
			r.finish(k, nil, "unavailable")
			return
		}
		r.finish(k, engine, "ready")
	}()
}
func classify(e error) T.WorkspaceAvailability {
	switch {
	case errors.Is(e, L.ErrWorkspaceMissing):
		return "missing"
	case errors.Is(e, L.ErrWorkspaceBusy):
		return "busy"
	case errors.Is(e, L.ErrWorkspaceIdentityConflict):
		return "identity_conflict"
	default:
		return "invalid"
	}
}
func (r *Runtime) finish(k string, e *L.Runtime, a T.WorkspaceAvailability) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[k]
	s.running = false
	s.engine = e
	s.availability = a
}
func (r *Runtime) projection(ctx context.Context, channel string, m member) T.WorkspaceMember {
	out := T.WorkspaceMember{ProjectKey: m.ProjectKey, BoardKey: m.BoardKey, Label: m.Label, Availability: "pending"}
	r.mu.Lock()
	s := r.states[memberKey(m.ProjectKey, m.BoardKey)]
	var engine *L.Runtime
	if s != nil {
		engine = s.engine
	}
	var availability T.WorkspaceAvailability
	if s != nil {
		availability = s.availability
	}
	r.mu.Unlock()
	if s == nil {
		return out
	}
	out.Availability = availability
	if engine != nil && availability == "ready" {
		readctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if !engine.ConfiguredReadAllowed(readctx, channel) {
			out.Availability = "unavailable"
			return out
		}
		id := m.Identity
		epoch := T.UUIDv4(engine.Epoch())
		out.Identity = &id
		out.MemberEpoch = &epoch
	}
	return out
}

func (r *Runtime) openMember(p project, path string, create bool) (*L.WorkspacePath, error) {
	guard, e := L.OpenProjectRoot(string(p.Root), false)
	if e != nil {
		r.retain(guard)
		return nil, e
	}
	defer r.retain(guard)
	if guard.PhysicalKey() != p.PhysicalKey || !pathEqual(guard.Path(), string(p.Root)) {
		return nil, L.ErrWorkspaceIdentityConflict
	}
	return L.OpenMemberRoot(guard.Path(), path, create)
}

func decodeRendezvous(b []byte, c configuration) (rendezvous, error) {
	m, e := A.Decode[rendezvous](b)
	if e != nil || m.MetadataVersion != 1 || m.WorkspaceID != c.WorkspaceID || m.Origin != "http://127.0.0.1:"+strconv.Itoa(int(c.Port)) || T.Validate(T.WorkspaceBootstrap{APIVersion: 1, WorkspaceID: m.WorkspaceID, Epoch: m.Epoch, Mode: "human"}) != nil {
		return rendezvous{}, ErrUnavailable
	}
	token, e := base64.RawURLEncoding.DecodeString(m.AutomationToken)
	if e != nil || len(token) != 32 || base64.RawURLEncoding.EncodeToString(token) != m.AutomationToken {
		return rendezvous{}, ErrUnavailable
	}
	return m, nil
}
func (r *Runtime) retainCleanup(e error) {
	var cleanup L.WorkspaceCleanup
	if errors.As(e, &cleanup) {
		r.mu.Lock()
		r.cleanups = append(r.cleanups, cleanup)
		r.mu.Unlock()
	}
}

func (r *Runtime) retainEngine(engine *L.Runtime) {
	if engine == nil {
		return
	}
	if e := engine.Close(); e != nil {
		r.mu.Lock()
		r.orphans = append(r.orphans, engine)
		r.mu.Unlock()
	}
}
