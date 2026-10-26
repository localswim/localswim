// Package evidence preserves private immutable submission identity before dispatch.
package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

const MaxRecord = 1600000
const MaxRecords = 100
const MaxBytes = 8 * 1024 * 1024
const ResolutionReserve = 512

var ErrUnavailable = errors.New("private evidence unavailable")

// Context is secret-free original installation identity, never a credential.
type Context struct{ InstallationID, Origin string }

// Record owns its bytes. No exported mutable object aliases the frozen intent.
type Record struct {
	data    []byte
	intent  contract.Intent
	context Context
	route   contract.ResolvedRoute
}
type stored struct {
	Version        int                    `json:"version"`
	InstallationID string                 `json:"installationId"`
	Origin         string                 `json:"origin"`
	Route          contract.ResolvedRoute `json:"route"`
	Wire           json.RawMessage        `json:"wire"`
	Semantic       json.RawMessage        `json:"semantic"`
	Fingerprint    string                 `json:"fingerprint"`
}
type Identity struct {
	BoardID              string           `json:"boardId"`
	Incarnation          string           `json:"incarnation"`
	OpID                 string           `json:"opId"`
	ActorID              string           `json:"actorId"`
	FingerprintVersion   int              `json:"fingerprintVersion"`
	Command              contract.Command `json:"command"`
	Target               *contract.Target `json:"target"`
	ExpectedRevision     uint64           `json:"expectedRevision"`
	ExpectedCardRevision *uint64          `json:"expectedCardRevision"`
}

func validContext(c Context) bool {
	ok := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(c.InstallationID)
	u, err := url.Parse(c.Origin)
	if !ok || err != nil {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	host := u.Hostname()
	return err == nil && port >= 1024 && port <= 65535 && u.Port() == strconv.Itoa(port) && u.Scheme == "https" && u.Opaque == "" && u.User == nil && u.Path == "" && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.localhost$`).MatchString(host) && u.Host == host+":"+strconv.Itoa(port) && c.Origin == "https://"+u.Host
}

// Freeze uses the shared D03/C01 validator; caller supplies an authenticated actor.
func Freeze(c Context, route contract.ResolvedRoute, actor contract.AuthenticatedActor, wire []byte) (Record, error) {
	if !validContext(c) {
		return Record{}, ErrUnavailable
	}
	i, e := T.FreezeMutation(bytes.Clone(wire), route, actor)
	if e != nil {
		return Record{}, ErrUnavailable
	}
	v, e := contract.DecodeJSON(wire)
	if e != nil {
		return Record{}, ErrUnavailable
	}
	normalized, e := contract.Canonical(v)
	if e != nil {
		return Record{}, ErrUnavailable
	}
	s := stored{1, c.InstallationID, c.Origin, route, normalized, i.CanonicalBytes(), i.Fingerprint()}
	b, e := json.Marshal(s)
	if e != nil || len(b) > MaxRecord {
		return Record{}, ErrUnavailable
	}
	decoded, e := contract.DecodeJSON(b)
	if e != nil {
		return Record{}, ErrUnavailable
	}
	b, e = contract.Canonical(decoded)
	if e != nil || len(b)+1 > MaxRecord {
		return Record{}, ErrUnavailable
	}
	return Record{append(b, '\n'), i, c, route}, nil
}

// Decode validates a complete existing record; it never supplies absent identity.
func Decode(data []byte) (Record, error) {
	if len(data) > MaxRecord {
		return Record{}, ErrUnavailable
	}
	v, e := contract.DecodeJSON(data)
	if e != nil {
		return Record{}, ErrUnavailable
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 7 {
		return Record{}, ErrUnavailable
	}
	for _, k := range []string{"version", "installationId", "origin", "route", "wire", "semantic", "fingerprint"} {
		if _, ok = m[k]; !ok {
			return Record{}, ErrUnavailable
		}
	}
	route, ok := m["route"].(map[string]any)
	if !ok || len(route) != 3 {
		return Record{}, ErrUnavailable
	}
	for _, key := range []string{"Command", "BoardID", "TargetID"} {
		if _, ok := route[key].(string); !ok {
			return Record{}, ErrUnavailable
		}
	}
	var s stored
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || s.Version != 1 {
		return Record{}, ErrUnavailable
	}
	var id Identity
	if json.Unmarshal(s.Semantic, &id) != nil {
		return Record{}, ErrUnavailable
	}
	r, e := Freeze(Context{s.InstallationID, s.Origin}, s.Route, contract.AuthenticatedActor{ActorID: id.ActorID}, s.Wire)
	if e != nil || !bytes.Equal(r.intent.CanonicalBytes(), s.Semantic) || r.intent.Fingerprint() != s.Fingerprint {
		return Record{}, ErrUnavailable
	}
	return r, nil
}
func (r Record) Bytes() []byte { return bytes.Clone(r.data) }
func (r Record) Wire() []byte {
	var s stored
	_ = json.Unmarshal(r.data, &s)
	return bytes.Clone(s.Wire)
}
func (r Record) Semantic() []byte              { return r.intent.CanonicalBytes() }
func (r Record) Fingerprint() string           { return r.intent.Fingerprint() }
func (r Record) Context() Context              { return r.context }
func (r Record) Route() contract.ResolvedRoute { return r.route }
func (r Record) Identity() Identity {
	var x Identity
	_ = json.Unmarshal(r.intent.CanonicalBytes(), &x)
	return x
}
func (r Record) Valid() bool { _, e := Decode(r.data); return e == nil }

// MatchesTerminal requires every original semantic identity/precondition field.
func (r Record) MatchesTerminal(t T.Terminal) bool {
	i := r.Identity()
	target, _ := json.Marshal(i.Target)
	other, _ := json.Marshal(t.Target)
	rev := func(p *uint64, q *A.Positive) bool {
		if p == nil || q == nil {
			return p == nil && q == nil
		}
		return *p == uint64(*q)
	}
	return r.Valid() && string(t.BoardID) == i.BoardID && string(t.Incarnation) == i.Incarnation && string(t.OpID) == i.OpID && string(t.ActorID) == i.ActorID && int(t.FingerprintVersion) == i.FingerprintVersion && string(t.Fingerprint) == r.Fingerprint() && string(t.Command) == string(i.Command) && bytes.Equal(target, other) && uint64(t.ExpectedRevision) == i.ExpectedRevision && rev(i.ExpectedCardRevision, t.ExpectedCardRevision)
}

// Store implementations must exclusively create, flush, verify private ACLs and
// read back the identical record before Save succeeds. Load never creates files.
// Failures retain incomplete files and MUST prevent dispatch. No overwrite API.
type Store interface {
	Save(Record) (string, error)
	Load(string) (Record, error)
	Resolve(Record, T.Terminal) error
}

type resolution struct {
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Outcome     string `json:"outcome"`
	ResolvedAt  string `json:"resolvedAt"`
}

func resolutionBytes(r Record, t T.Terminal, now time.Time) ([]byte, error) {
	if T.Validate(t) != nil || !r.MatchesTerminal(t) {
		return nil, ErrUnavailable
	}
	b, e := json.Marshal(resolution{1, r.Fingerprint(), string(t.Outcome), now.UTC().Format("2006-01-02T15:04:05Z")})
	if e != nil || len(b)+1 > ResolutionReserve {
		return nil, ErrUnavailable
	}
	return append(b, '\n'), nil
}
func readResolution(b []byte, r Record) (resolution, error) {
	var out resolution
	v, e := contract.DecodeJSON(b)
	if e != nil {
		return out, ErrUnavailable
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 4 || len(b) > ResolutionReserve {
		return out, ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || out.Version != 1 || out.Fingerprint != r.Fingerprint() || (out.Outcome != "committed" && out.Outcome != "not_committed") {
		return out, ErrUnavailable
	}
	t, e := time.Parse("2006-01-02T15:04:05Z", out.ResolvedAt)
	if e != nil || t.UTC().Format("2006-01-02T15:04:05Z") != out.ResolvedAt {
		return out, ErrUnavailable
	}
	return out, nil
}
