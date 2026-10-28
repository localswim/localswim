package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

const PreferencesLimit int64 = 64 << 10

type preferencePin struct {
	ProjectKey T.WorkspaceKey      `json:"projectKey"`
	BoardKey   T.WorkspaceKey      `json:"boardKey"`
	Identity   T.WorkspaceIdentity `json:"identity"`
}
type preferences struct {
	SchemaVersion       A.Version       `json:"schemaVersion"`
	WorkspaceID         T.UUIDv4        `json:"workspaceId"`
	PreferencesRevision A.Positive      `json:"preferencesRevision"`
	Pins                []preferencePin `json:"pins"`
}

func (p preferences) public() T.WorkspacePreferences {
	out := T.WorkspacePreferences{APIVersion: 1, WorkspaceID: p.WorkspaceID, PreferencesRevision: p.PreferencesRevision, Pins: []T.WorkspacePinReference{}}
	for _, pin := range p.Pins {
		out.Pins = append(out.Pins, T.WorkspacePinReference{ProjectKey: pin.ProjectKey, BoardKey: pin.BoardKey})
	}
	return out
}
func validatePreferences(p preferences, c configuration) error {
	if p.SchemaVersion != 1 || p.WorkspaceID != c.WorkspaceID || p.Pins == nil || uint64(p.PreferencesRevision) >= contract.MaxInteger || T.Validate(p.public()) != nil {
		return ErrUnavailable
	}
	for _, pin := range p.Pins {
		m, ok := c.member(pin.ProjectKey, pin.BoardKey)
		if !ok || m.Identity != pin.Identity {
			return ErrUnavailable
		}
	}
	return nil
}
func decodePreferences(b []byte, c configuration) (preferences, error) {
	if int64(len(b)) > PreferencesLimit {
		return preferences{}, ErrUnavailable
	}
	p, e := A.Decode[preferences](b)
	if e != nil || validatePreferences(p, c) != nil {
		return preferences{}, ErrUnavailable
	}
	return p, nil
}
func preferencesBytes(p preferences, c configuration) ([]byte, error) {
	if validatePreferences(p, c) != nil {
		return nil, ErrUnavailable
	}
	b, e := json.Marshal(p)
	if e != nil || int64(len(b)) > PreferencesLimit {
		return nil, ErrUnavailable
	}
	return b, nil
}

// The preference publication lock is independent of registry bookkeeping and
// board writers. Saved bytes are re-read before every request; invalid bytes,
// deletion after observation, downgrades and same-revision replacements refuse.
// An uncertain CAS is reconciled only by a later explicit read, never replayed.
func (r *Runtime) readPreferences(c configuration) (preferences, []byte, error) {
	raw, e := r.files.Read("workspace-preferences.json", PreferencesLimit)
	if errors.Is(e, os.ErrNotExist) && !r.preferencesObserved {
		return preferences{SchemaVersion: 1, WorkspaceID: c.WorkspaceID, PreferencesRevision: 1, Pins: []preferencePin{}}, nil, nil
	}
	if e != nil {
		r.retainCleanup(e)
		return preferences{}, nil, ErrUnavailable
	}
	p, e := decodePreferences(raw, c)
	if e != nil {
		return preferences{}, nil, e
	}
	if r.preferencesRaw != nil {
		old, e := decodePreferences(r.preferencesRaw, c)
		if e != nil || p.PreferencesRevision < old.PreferencesRevision || p.PreferencesRevision == old.PreferencesRevision && !bytes.Equal(raw, r.preferencesRaw) {
			return preferences{}, nil, ErrUnavailable
		}
	}
	r.preferencesRaw = append([]byte(nil), raw...)
	r.preferencesObserved = true
	return p, raw, nil
}
func preferenceRefusal(w http.ResponseWriter, status int, code string, p *preferences) {
	var current *T.WorkspacePreferences
	if p != nil {
		v := p.public()
		current = &v
	}
	respond(w, status, T.WorkspacePreferencesError{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "workspace preferences request refused"}, Preferences: current})
}

func (r *Runtime) servePreferences(w http.ResponseWriter, q *http.Request) {
	if q.URL.RawQuery != "" {
		preferenceRefusal(w, 400, "INVALID_INPUT", nil)
		return
	}
	channel := L.WorkspaceChannel(q.Context())
	if channel != "browser" && channel != "cli" || q.Method == "POST" && channel != "browser" {
		preferenceRefusal(w, 403, "FORBIDDEN", nil)
		return
	}
	if q.Method != "GET" && q.Method != "POST" {
		preferenceRefusal(w, 405, "METHOD_NOT_ALLOWED", nil)
		return
	}
	var request T.WorkspacePreferencesRequest
	if q.Method == "POST" {
		b, e := readPreferenceBody(w, q)
		if e != nil {
			preferenceRefusal(w, 400, "INVALID_INPUT", nil)
			return
		}
		request = b
	}
	c, e := r.refresh()
	if e != nil {
		preferenceRefusal(w, 503, "UNAVAILABLE", nil)
		return
	}
	r.preferencesMu.Lock()
	defer r.preferencesMu.Unlock()
	r.mu.Lock()
	closing := r.closing
	r.mu.Unlock()
	if closing {
		preferenceRefusal(w, 503, "UNAVAILABLE", nil)
		return
	}
	p, raw, e := r.readPreferences(c)
	if e != nil {
		preferenceRefusal(w, 503, "UNAVAILABLE", nil)
		return
	}
	if q.Method == "GET" {
		respond(w, 200, p.public())
		return
	}
	if request.ExpectedPreferencesRevision != p.PreferencesRevision {
		preferenceRefusal(w, 412, "PREFERENCES_CHANGED", &p)
		return
	}
	next, code := preferenceChange(p, c, request)
	if code != "" {
		preferenceRefusal(w, 400, code, &p)
		return
	}
	// Native path/tuple checks grant no board rights. Hold the checked root pins
	// through publication, without the registry mutex or any SQL writer.
	refs := []preferencePin{}
	if request.Action == "reorder" {
		refs = append(refs, next.Pins...)
	}
	if request.Action == "pin" {
		m, _ := c.member(request.Pin.ProjectKey, request.Pin.BoardKey)
		refs = append(refs, preferencePin{m.ProjectKey, m.BoardKey, m.Identity})
	}
	guards := []*L.WorkspacePath{}
	defer func() {
		for _, g := range guards {
			r.retain(g)
		}
	}()
	seen := map[string]bool{}
	for _, pin := range refs {
		key := memberKey(pin.ProjectKey, pin.BoardKey)
		if seen[key] {
			continue
		}
		seen[key] = true
		m, _ := c.member(pin.ProjectKey, pin.BoardKey)
		g, e := r.preferenceMember(q.Context(), c, m)
		if g != nil {
			guards = append(guards, g)
		}
		if e != nil {
			preferenceRefusal(w, 503, "UNAVAILABLE", &p)
			return
		}
	}
	if uint64(p.PreferencesRevision) >= contract.MaxInteger-1 {
		preferenceRefusal(w, 503, "UNAVAILABLE", &p)
		return
	}
	next.PreferencesRevision++
	b, e := preferencesBytes(next, c)
	if e == nil && r.preferencesFault != nil {
		e = r.preferencesFault("before-preferences-publication")
	}
	if e == nil {
		r.preferencesObserved = true
		e = r.files.CompareAndSwap("workspace-preferences.json", raw, b, PreferencesLimit)
	}
	if e == nil && r.preferencesFault != nil {
		e = r.preferencesFault("after-preferences-publication")
	}
	if e != nil {
		r.retainCleanup(e)
		if errors.Is(e, L.ErrWorkspaceChanged) {
			if current, _, er := r.readPreferences(c); er == nil {
				preferenceRefusal(w, 412, "PREFERENCES_CHANGED", &current)
				return
			}
		}
		// No persisted-success claim, retry or byte removal after uncertainty.
		preferenceRefusal(w, 503, "UNAVAILABLE", nil)
		return
	}
	r.preferencesRaw = append([]byte(nil), b...)
	respond(w, 200, next.public())
}

func preferenceChange(p preferences, c configuration, q T.WorkspacePreferencesRequest) (preferences, string) {
	next := p
	next.Pins = append([]preferencePin{}, p.Pins...)
	index := -1
	if q.Pin != nil {
		if _, ok := c.member(q.Pin.ProjectKey, q.Pin.BoardKey); !ok {
			return p, "INVALID_INPUT"
		}
		for i, pin := range p.Pins {
			if pin.ProjectKey == q.Pin.ProjectKey && pin.BoardKey == q.Pin.BoardKey {
				index = i
			}
		}
	}
	switch q.Action {
	case "pin":
		if index != -1 || len(p.Pins) >= MemberLimit {
			return p, "INVALID_INPUT"
		}
		m, _ := c.member(q.Pin.ProjectKey, q.Pin.BoardKey)
		next.Pins = append(next.Pins, preferencePin{m.ProjectKey, m.BoardKey, m.Identity})
	case "unpin":
		if index == -1 {
			return p, "INVALID_INPUT"
		}
		next.Pins = append(next.Pins[:index], next.Pins[index+1:]...)
	case "reorder":
		if len(q.Order) != len(p.Pins) {
			return p, "INVALID_INPUT"
		}
		pins := map[T.WorkspacePinReference]preferencePin{}
		for _, pin := range p.Pins {
			pins[T.WorkspacePinReference{ProjectKey: pin.ProjectKey, BoardKey: pin.BoardKey}] = pin
		}
		for i, ref := range q.Order {
			pin, ok := pins[ref]
			if !ok {
				return p, "INVALID_INPUT"
			}
			next.Pins[i] = pin
			delete(pins, ref)
		}
	default:
		return p, "INVALID_INPUT"
	}
	return next, ""
}

func (r *Runtime) preferenceMember(ctx context.Context, c configuration, m member) (*L.WorkspacePath, error) {
	p, ok := c.project(m.ProjectKey)
	if !ok {
		return nil, ErrUnavailable
	}
	g, e := r.openMember(p, string(m.DataDir), false)
	if e != nil {
		return g, e
	}
	if g.PhysicalKey() != m.PhysicalKey || !pathEqual(g.Path(), string(m.DataDir)) {
		return g, ErrUnavailable
	}
	r.mu.Lock()
	s := r.states[memberKey(m.ProjectKey, m.BoardKey)]
	var engine *L.Runtime
	if s != nil {
		engine = s.engine
	}
	r.mu.Unlock()
	if engine != nil {
		if !engine.WorkspaceIdentityCurrent(*localIdentity(m.Identity)) {
			return g, ErrUnavailable
		}
		return g, nil
	}
	attempt, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, e = L.InspectBoard(attempt, g.Path(), localIdentity(m.Identity))
	r.retainCleanup(e)
	return g, e
}
