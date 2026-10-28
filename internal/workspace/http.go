package workspace

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

func respond(w http.ResponseWriter, status int, v any) {
	b, e := T.Readable(v)
	if e != nil {
		http.Error(w, "workspace unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func (r *Runtime) refusal(w http.ResponseWriter, status int, code string) {
	respond(w, status, T.WorkspaceControlError{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "workspace request refused"}, ConfigRevision: r.snapshot().ConfigRevision})
}
func body[T1 any](w http.ResponseWriter, q *http.Request) (T1, error) {
	b, e := io.ReadAll(http.MaxBytesReader(w, q.Body, ConfigLimit))
	if e != nil {
		return *new(T1), ErrUnavailable
	}
	return T.Decode[T1](b)
}
func (r *Runtime) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	if L.WorkspaceChannel(q.Context()) == "" {
		r.refusal(w, 403, "FORBIDDEN")
		return
	}
	if q.Method != "GET" && L.WorkspaceChannel(q.Context()) != "cli" {
		r.refusal(w, 403, "FORBIDDEN")
		return
	}
	path := q.URL.Path
	switch {
	case path == "/api/v1/workspace/projects" && q.Method == "GET":
		if q.URL.RawQuery != "" {
			r.refusal(w, 400, "INVALID_INPUT")
			return
		}
		c := r.snapshot()
		out := T.WorkspaceProjects{APIVersion: 1, WorkspaceID: c.WorkspaceID, ConfigRevision: c.ConfigRevision, Projects: []T.WorkspaceProject{}}
		for _, p := range c.Projects {
			out.Projects = append(out.Projects, T.WorkspaceProject{Key: p.Key, Label: p.Label})
		}
		respond(w, 200, out)
	case path == "/api/v1/workspace/boards" && q.Method == "GET":
		values, e := q.URL.Query()["projectKey"]
		if q.URL.RawQuery != "" && (len(q.URL.Query()) != 1 || !e || len(values) != 1 || T.Validate(T.WorkspaceProject{Key: T.WorkspaceKey(values[0]), Label: "validation"}) != nil) {
			r.refusal(w, 400, "INVALID_INPUT")
			return
		}
		c := r.snapshot()
		out := T.WorkspaceBoards{APIVersion: 1, WorkspaceID: c.WorkspaceID, ConfigRevision: c.ConfigRevision, Boards: []T.WorkspaceMember{}}
		for _, m := range c.Members {
			if len(values) == 0 || string(m.ProjectKey) == values[0] {
				out.Boards = append(out.Boards, r.projection(q.Context(), L.WorkspaceChannel(q.Context()), m))
			}
		}
		respond(w, 200, out)
	case strings.HasPrefix(path, "/api/v1/workspace/projects/") && q.Method == "GET":
		p := strings.Split(path, "/")
		if len(p) != 8 || p[6] != "boards" || q.URL.RawQuery != "" {
			r.refusal(w, 404, "NOT_FOUND")
			return
		}
		pk, bk := T.WorkspaceKey(p[5]), T.WorkspaceKey(p[7])
		c := r.snapshot()
		m, ok := c.member(pk, bk)
		if !ok {
			r.refusal(w, 404, "NOT_FOUND")
			return
		}
		r.activate(m)
		respond(w, 200, T.WorkspaceResolve{APIVersion: 1, WorkspaceID: c.WorkspaceID, ConfigRevision: c.ConfigRevision, Member: r.projection(q.Context(), L.WorkspaceChannel(q.Context()), m)})
	case path == "/api/v1/workspace/projects" && q.Method == "POST":
		if q.URL.RawQuery != "" {
			r.refusal(w, 400, "INVALID_INPUT")
			return
		}
		v, e := body[T.WorkspaceProjectRequest](w, q)
		if e != nil {
			r.refusal(w, 400, "INVALID_INPUT")
			return
		}
		r.createProject(w, v)
	case path == "/api/v1/workspace/boards" && q.Method == "POST":
		if q.URL.RawQuery != "" {
			r.refusal(w, 400, "INVALID_INPUT")
			return
		}
		v, e := body[T.WorkspaceBoardRequest](w, q)
		if e != nil {
			r.refusal(w, 400, "INVALID_INPUT")
			return
		}
		r.createBoard(w, q, v)
	case path == "/api/v1/workspace/boards/status" && q.Method == "POST":
		if q.URL.RawQuery != "" {
			r.refusal(w, 400, "INVALID_INPUT")
			return
		}
		v, e := body[T.WorkspaceStatusRequest](w, q)
		if e != nil {
			r.refusal(w, 400, "INVALID_INPUT")
			return
		}
		r.status(w, q, v)
	default:
		r.refusal(w, 404, "NOT_FOUND")
	}
}

// refresh is serialized with control mutations, but performs native IO outside
// the short snapshot mutex. A failed publication is never assumed absent.
func (r *Runtime) refresh() (configuration, error) {
	raw, e := r.files.Read("workspace.json", ConfigLimit)
	if e != nil {
		r.retainCleanup(e)
		return configuration{}, ErrUnavailable
	}
	c, e := decodeConfig(raw)
	if e != nil {
		return configuration{}, e
	}
	r.control.Lock()
	defer r.control.Unlock()
	old := r.snapshot()
	if c.WorkspaceID != old.WorkspaceID || c.Port != old.Port {
		return configuration{}, ErrUnavailable
	}
	if c.ConfigRevision < old.ConfigRevision {
		return old, nil
	}
	// The configuration is append-only: existing immutable keys cannot rebind.
	for _, p := range old.Projects {
		np, ok := c.project(p.Key)
		if !ok || np != p {
			return configuration{}, ErrUnavailable
		}
	}
	for _, m := range old.Members {
		nm, ok := c.member(m.ProjectKey, m.BoardKey)
		if !ok || nm != m {
			return configuration{}, ErrUnavailable
		}
	}
	r.mu.Lock()
	r.config = c
	r.raw = raw
	r.mu.Unlock()
	return c, nil
}
func (r *Runtime) publish(c configuration) error {
	if uint64(c.ConfigRevision) >= contract.MaxInteger-1 {
		return ErrUnavailable
	}
	c.ConfigRevision++
	b, e := configBytes(c)
	if e != nil {
		return e
	}
	if r.publishFault != nil {
		if e = r.publishFault("before-config-publication"); e != nil {
			return e
		}
	}
	r.control.Lock()
	defer r.control.Unlock()
	if r.snapshot().ConfigRevision+1 != c.ConfigRevision {
		return L.ErrWorkspaceChanged
	}
	r.mu.Lock()
	old := append([]byte(nil), r.raw...)
	r.mu.Unlock()
	if e = r.files.CompareAndSwap("workspace.json", old, b, ConfigLimit); e != nil {
		r.retainCleanup(e)
		return e
	}
	if r.publishFault != nil {
		if e = r.publishFault("after-config-publication"); e != nil {
			return e
		}
	}
	r.mu.Lock()
	r.config = c
	r.raw = b
	r.mu.Unlock()
	return nil
}
func (r *Runtime) result(w http.ResponseWriter, c configuration, status T.WorkspaceRegistrationStatus, p T.WorkspaceKey, b *T.WorkspaceKey, id *T.WorkspaceIdentity) {
	respond(w, 200, T.WorkspaceControlResult{APIVersion: 1, WorkspaceID: c.WorkspaceID, ConfigRevision: c.ConfigRevision, Status: status, ProjectKey: p, BoardKey: b, Identity: id})
}
func (r *Runtime) begin(w http.ResponseWriter, expected A.Positive) (configuration, bool) {
	c, e := r.refresh()
	if e != nil {
		r.refusal(w, 503, "UNAVAILABLE")
		return c, false
	}
	if c.ConfigRevision != expected {
		r.refusal(w, 412, "CONFIG_CHANGED")
		return c, false
	}
	r.mu.Lock()
	closing := r.closing
	r.mu.Unlock()
	if closing {
		r.refusal(w, 503, "UNAVAILABLE")
		return c, false
	}
	return c, true
}
func (r *Runtime) createProject(w http.ResponseWriter, v T.WorkspaceProjectRequest) {
	c, ok := r.begin(w, v.ExpectedConfigRevision)
	if !ok {
		return
	}
	if _, ok = c.project(v.Key); ok || len(c.Projects) >= MemberLimit {
		r.refusal(w, 409, "INVALID_INPUT")
		return
	}
	g, e := L.OpenProjectRoot(string(v.Root), true)
	if e != nil {
		r.retain(g)
		r.refusal(w, 400, "INVALID_INPUT")
		return
	}
	defer r.retain(g)
	for _, p := range c.Projects {
		if p.PhysicalKey == g.PhysicalKey() || pathEqual(string(p.Root), g.Path()) {
			r.refusal(w, 409, "INVALID_INPUT")
			return
		}
	}
	c.Projects = append(append([]project(nil), c.Projects...), project{v.Key, v.Label, T.WorkspacePath(g.Path()), g.PhysicalKey()})
	if e = r.publish(c); e != nil {
		if errors.Is(e, L.ErrWorkspaceChanged) {
			r.refusal(w, 412, "CONFIG_CHANGED")
			return
		}
		r.result(w, r.snapshot(), "UNKNOWN", v.Key, nil, nil)
		return
	}
	r.result(w, r.snapshot(), "registered", v.Key, nil, nil)
}
func (r *Runtime) createBoard(w http.ResponseWriter, q *http.Request, v T.WorkspaceBoardRequest) {
	c, ok := r.begin(w, v.ExpectedConfigRevision)
	if !ok {
		return
	}
	p, ok := c.project(v.ProjectKey)
	_, exists := c.member(v.ProjectKey, v.BoardKey)
	if !ok || exists || len(c.Members) >= MemberLimit || v.Action == "create" && v.ExpectedIdentity != nil {
		r.refusal(w, 409, "INVALID_INPUT")
		return
	}
	g, e := r.openMember(p, string(v.DataDir), v.Action == "create")
	if e != nil {
		r.retain(g)
		r.refusal(w, 400, "INVALID_INPUT")
		return
	}
	defer r.retain(g)
	for _, m := range c.Members {
		if m.PhysicalKey == g.PhysicalKey() || pathEqual(string(m.DataDir), g.Path()) {
			r.refusal(w, 409, "INVALID_INPUT")
			return
		}
	}
	var id T.WorkspaceIdentity
	var engine *L.Runtime
	if v.Action == "create" {
		engine, e = L.OpenEngine(r.ctx, L.EngineOptions{DataDir: g.Path(), Origin: r.Origin(), WorkspaceID: string(c.WorkspaceID)})
		if e != nil {
			r.retainEngine(engine)
			r.result(w, c, "UNKNOWN", v.ProjectKey, &v.BoardKey, nil)
			return
		}
		id = wireIdentity(engine.Identity())
	} else {
		var native L.BoardIdentity
		var expected *L.BoardIdentity
		if v.ExpectedIdentity != nil {
			expected = localIdentity(*v.ExpectedIdentity)
		}
		native, e = L.InspectBoard(q.Context(), g.Path(), expected)
		if e != nil {
			r.retainCleanup(e)
			r.refusal(w, 409, "UNAVAILABLE")
			return
		}
		id = wireIdentity(native)
	}
	owned := engine != nil
	defer func() {
		if owned {
			if e := engine.Close(); e != nil {
				r.mu.Lock()
				r.orphans = append(r.orphans, engine)
				r.mu.Unlock()
			}
		}
	}()
	for _, m := range c.Members {
		if m.Identity.BoardID == id.BoardID || m.Identity == id {
			r.refusal(w, 409, "INVALID_INPUT")
			return
		}
	}
	m := member{v.ProjectKey, v.BoardKey, v.Label, T.WorkspacePath(g.Path()), g.PhysicalKey(), id}
	c.Members = append(append([]member(nil), c.Members...), m)
	if e = r.publish(c); e != nil {
		if v.Action == "register" && errors.Is(e, L.ErrWorkspaceChanged) {
			r.refusal(w, 412, "CONFIG_CHANGED")
			return
		}
		r.result(w, r.snapshot(), "UNKNOWN", v.ProjectKey, &v.BoardKey, &id)
		return
	}
	if engine != nil {
		if e = r.host.Attach(engine); e == nil {
			owned = false
			r.mu.Lock()
			r.states[memberKey(m.ProjectKey, m.BoardKey)] = &activation{availability: "ready", engine: engine}
			r.mu.Unlock()
		}
	} else {
		r.activate(m)
	}
	r.result(w, r.snapshot(), "registered", v.ProjectKey, &v.BoardKey, &id)
}
func (r *Runtime) status(w http.ResponseWriter, q *http.Request, v T.WorkspaceStatusRequest) {
	c, e := r.refresh()
	if e != nil {
		r.result(w, r.snapshot(), "UNKNOWN", v.ProjectKey, &v.BoardKey, nil)
		return
	}
	m, registered := c.member(v.ProjectKey, v.BoardKey)
	if registered {
		if v.DataDir != nil && !pathEqual(string(*v.DataDir), string(m.DataDir)) || v.ExpectedIdentity != nil && *v.ExpectedIdentity != m.Identity {
			r.result(w, c, "UNKNOWN", v.ProjectKey, &v.BoardKey, nil)
			return
		}
		id := m.Identity
		r.result(w, c, "registered", v.ProjectKey, &v.BoardKey, &id)
		return
	}
	p, ok := c.project(v.ProjectKey)
	if !ok || v.DataDir == nil {
		r.result(w, c, "unavailable", v.ProjectKey, &v.BoardKey, nil)
		return
	}
	g, e := r.openMember(p, string(*v.DataDir), false)
	if e != nil {
		r.retain(g)
		r.result(w, c, "unavailable", v.ProjectKey, &v.BoardKey, nil)
		return
	}
	defer r.retain(g)
	var expected *L.BoardIdentity
	if v.ExpectedIdentity != nil {
		expected = localIdentity(*v.ExpectedIdentity)
	}
	native, e := L.InspectBoard(q.Context(), g.Path(), expected)
	if e != nil {
		r.retainCleanup(e)
		r.result(w, c, "UNKNOWN", v.ProjectKey, &v.BoardKey, nil)
		return
	}
	id := wireIdentity(native)
	for _, other := range c.Members {
		if other.Identity.BoardID == id.BoardID || other.PhysicalKey == g.PhysicalKey() {
			r.result(w, c, "UNKNOWN", v.ProjectKey, &v.BoardKey, nil)
			return
		}
	}
	r.result(w, c, "initialized-unregistered", v.ProjectKey, &v.BoardKey, &id)
}
