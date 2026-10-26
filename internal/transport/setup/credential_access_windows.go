//go:build windows && amd64

package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"golang.org/x/sys/windows"
)

type credentialFile struct {
	SchemaVersion  int           `json:"schemaVersion"`
	InstallationID T.UUIDv4      `json:"installationId"`
	BoardID        A.UUID        `json:"boardId"`
	Incarnation    A.UUID        `json:"incarnation"`
	GrantID        A.UUID        `json:"grantId"`
	ActorID        A.UUID        `json:"actorId"`
	Kind           A.Kind        `json:"kind"`
	Rights         T.Rights      `json:"rights"`
	CredentialID   T.UUIDv4      `json:"credentialId"`
	IssuedAt       A.Timestamp   `json:"issuedAt"`
	ExpiresAt      A.Timestamp   `json:"expiresAt"`
	Token          T.SecretToken `json:"token"`
}

var credentialDisabled = []byte("disabled\n")
var credentialLockBytes = []byte("localswim credential exclusion v1\n")

type nativeCredentialLease struct {
	mu       sync.Mutex
	manager  *Manager
	selected CredentialSelection
	ctx      ConnectionContext
	rel      string
	lock     *os.File
	unpin    func()
	closed   bool
}

func credentialFailure(e error) error {
	if e == nil {
		return nil
	}
	var f *Failure
	if errors.As(e, &f) {
		return f
	}
	if missing(e) {
		return fail("CREDENTIAL_UNAVAILABLE", 2)
	}
	return fail("CREDENTIAL_UNAVAILABLE", 3)
}

func credentialSelectionValid(s CredentialSelection) bool {
	return uuidRE.MatchString(s.InstallationID) && schema.ValidUUID(s.BoardID) && schema.ValidUUID(s.Incarnation) && schema.ValidUUID(s.GrantID)
}

// PrepareCredential pins the exact path and takes a separate per-credential OS
// lock. It never takes install.lock, so a running server does not block CLI use.
// Existing malformed files and unknown staging material are never adopted.
func (m *Manager) PrepareCredential(s CredentialSelection) (out *CredentialLease, err error) {
	defer func() { err = credentialFailure(err) }()
	if !credentialSelectionValid(s) {
		return nil, fail("CREDENTIAL_SELECTION_REQUIRED", 2)
	}
	if m == nil || m.fs == nil {
		return nil, fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	rootPin, e := m.fs.rootPin(false)
	if e != nil {
		return nil, e
	}
	defer rootPin()
	ctx, e := m.ReadContext()
	if e != nil {
		return nil, e
	}
	if ctx.InstallationID != s.InstallationID {
		return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	dir := filepath.Join("cli", s.InstallationID, s.BoardID, s.Incarnation)
	unpin, e := m.fs.pinChain(m.fs.path(dir), true)
	if e != nil {
		return nil, e
	}
	l := &nativeCredentialLease{manager: m, selected: s, ctx: ctx, rel: filepath.Join(dir, s.GrantID), unpin: unpin}
	defer func() {
		if err != nil {
			l.close()
		}
	}()
	lockRel := l.rel + ".lock"
	f, e := m.fs.open(lockRel, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.CREATE_NEW)
	if e == nil {
		_, e = f.Write(credentialLockBytes)
		if e == nil {
			e = f.Sync()
		}
		if e != nil {
			f.Close()
			return nil, e
		}
	} else if errors.Is(e, windows.ERROR_FILE_EXISTS) || errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		f, e = m.fs.open(lockRel, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_EXISTING)
	}
	if errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
		return nil, fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	if e != nil {
		return nil, e
	}
	l.lock = f
	b := make([]byte, len(credentialLockBytes)+1)
	n, re := f.ReadAt(b, 0)
	if n != len(credentialLockBytes) || !bytes.Equal(b[:n], credentialLockBytes) || (re != nil && n == 0) {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	// Fixed staging names may indicate an interrupted publication. Preserve them.
	for _, suffix := range []string{".json.next", ".disabled.next", ".disabled.removed"} {
		if _, e = m.fs.read(l.rel + suffix); !missing(e) {
			return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
	}
	if _, e = l.readFile(false); e != nil && !missing(e) {
		return nil, e
	}
	if _, e = l.marker(); e != nil {
		return nil, e
	}
	return &CredentialLease{state: l}, nil
}

// OpenCredential does no discovery or fallback and never loads native owner
// authority. The opaque material retains exclusion until the caller closes it.
func (m *Manager) OpenCredential(s CredentialSelection) (*CredentialMaterial, error) {
	// Ordinary reads must not create directories/locks for an absent credential.
	if !credentialSelectionValid(s) {
		return nil, fail("CREDENTIAL_SELECTION_REQUIRED", 2)
	}
	if m == nil || m.fs == nil {
		return nil, fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	ctx, e := m.ReadContext()
	if e != nil {
		return nil, credentialFailure(e)
	}
	if ctx.InstallationID != s.InstallationID {
		return nil, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	rootPin, e := m.fs.rootPin(false)
	if e != nil {
		return nil, credentialFailure(e)
	}
	markerRel := filepath.Join("cli", s.InstallationID, s.BoardID, s.Incarnation, s.GrantID+".disabled")
	marker, me := m.fs.read(markerRel)
	if me == nil {
		rootPin()
		if !bytes.Equal(marker, credentialDisabled) {
			return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
		return nil, fail("CREDENTIAL_DISABLED", 2)
	}
	if !missing(me) {
		rootPin()
		return nil, credentialFailure(me)
	}
	rel := filepath.Join("cli", s.InstallationID, s.BoardID, s.Incarnation, s.GrantID+".json")
	_, e = m.fs.read(rel)
	rootPin()
	if e != nil {
		return nil, credentialFailure(e)
	}
	lease, e := m.PrepareCredential(s)
	if e != nil {
		return nil, e
	}
	material, e := lease.state.load(false)
	if e != nil {
		lease.Close()
		return nil, e
	}
	material.close = lease.Close
	return material, nil
}

func (l *nativeCredentialLease) context() ConnectionContext {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ConnectionContext{}
	}
	return l.ctx
}
func (l *nativeCredentialLease) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	if l.lock != nil {
		l.lock.Close()
	}
	if l.unpin != nil {
		l.unpin()
	}
}

func (l *nativeCredentialLease) marker() (bool, error) {
	b, e := l.manager.fs.read(l.rel + ".disabled")
	if missing(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if !bytes.Equal(b, credentialDisabled) {
		return false, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return true, nil
}

func (l *nativeCredentialLease) readFile(checkExpiry bool) (T.CredentialSecret, error) {
	b, e := l.manager.fs.read(l.rel + ".json")
	if e != nil {
		return T.CredentialSecret{}, e
	}
	var disk credentialFile
	if e = jsonDecode(b, &disk); e != nil || disk.SchemaVersion != 1 {
		return T.CredentialSecret{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	s := T.CredentialSecret{APIVersion: 1, InstallationID: disk.InstallationID, BoardID: disk.BoardID, Incarnation: disk.Incarnation, GrantID: disk.GrantID, ActorID: disk.ActorID, Kind: disk.Kind, Rights: disk.Rights, CredentialID: disk.CredentialID, IssuedAt: disk.IssuedAt, ExpiresAt: disk.ExpiresAt, Token: disk.Token}
	// apiVersion is forbidden in the disk schema, even though it is required on
	// the wire. The disk DTO is never returned or sent through public encoders.
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || len(fields) != 12 {
		return T.CredentialSecret{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	if _, ok := fields["apiVersion"]; ok {
		return T.CredentialSecret{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	if e = T.Validate(s); e != nil {
		return T.CredentialSecret{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	if !l.matches(s) {
		return T.CredentialSecret{}, fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	issued, _ := parseTime(string(s.IssuedAt))
	expires, _ := parseTime(string(s.ExpiresAt))
	if checkExpiry && (l.manager.time().Before(issued) || !l.manager.time().Before(expires)) {
		return T.CredentialSecret{}, fail("CREDENTIAL_EXPIRED", 2)
	}
	return s, nil
}
func (l *nativeCredentialLease) matches(s T.CredentialSecret) bool {
	x := l.selected
	return string(s.InstallationID) == x.InstallationID && string(s.BoardID) == x.BoardID && string(s.Incarnation) == x.Incarnation && string(s.GrantID) == x.GrantID
}
func credentialMetadata(s T.CredentialSecret) CredentialMetadata {
	i, _ := parseTime(string(s.IssuedAt))
	e, _ := parseTime(string(s.ExpiresAt))
	return CredentialMetadata{Selected: CredentialSelection{string(s.InstallationID), string(s.BoardID), string(s.Incarnation), string(s.GrantID)}, ActorID: string(s.ActorID), Kind: string(s.Kind), CredentialID: string(s.CredentialID), Rights: append([]string(nil), s.Rights...), IssuedAt: i, ExpiresAt: e}
}

func (l *nativeCredentialLease) load(revocation bool) (*CredentialMaterial, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	disabled, e := l.marker()
	if e != nil {
		return nil, credentialFailure(e)
	}
	if disabled {
		if revocation {
			return nil, fail("CREDENTIAL_OUTCOME_UNKNOWN", 4)
		}
		return nil, fail("CREDENTIAL_DISABLED", 2)
	}
	s, e := l.readFile(true)
	if e != nil {
		return nil, credentialFailure(e)
	}
	closed := false
	captured := false
	mat := &CredentialMaterial{context: l.ctx, metadata: credentialMetadata(s)}
	mat.use = func(f func(T.SecretToken) error) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		if closed || l.closed {
			return fail("CREDENTIAL_UNAVAILABLE", 3)
		}
		disabled, e := l.marker()
		if e != nil {
			return credentialFailure(e)
		}
		if disabled {
			if revocation {
				return fail("CREDENTIAL_OUTCOME_UNKNOWN", 4)
			}
			return fail("CREDENTIAL_DISABLED", 2)
		}
		// Revocation captures the bearer once, before the session preflight and
		// Disable. The consuming client then owns the single DELETE-only spend
		// guard; a disabled bearer can never construct another connection here.
		if revocation && captured {
			return fail("CREDENTIAL_UNAVAILABLE", 3)
		}
		current, e := l.readFile(true)
		if e != nil {
			return credentialFailure(e)
		}
		if current.CredentialID != s.CredentialID || current.Token != s.Token {
			return fail("CREDENTIAL_CONTEXT_MISMATCH", 2)
		}
		if revocation {
			captured = true
		}
		return f(s.Token)
	}
	mat.close = func() { l.mu.Lock(); defer l.mu.Unlock(); closed = true; s.Token = "" }
	return mat, nil
}

func (l *nativeCredentialLease) disable() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return fail("CREDENTIAL_UNAVAILABLE", 3)
	}
	exists, e := l.marker()
	if e != nil {
		return credentialFailure(e)
	}
	if exists {
		return nil
	}
	if e = l.manager.fs.write(l.rel+".disabled", credentialDisabled); e != nil {
		return credentialFailure(e)
	}
	return credentialFailure(l.manager.checkpoint("credential:disabled"))
}

func (l *nativeCredentialLease) publish(s T.CredentialSecret) (CredentialMetadata, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	unknown := func() (CredentialMetadata, error) { return CredentialMetadata{}, fail("CREDENTIAL_OUTCOME_UNKNOWN", 4) }
	if l.closed || T.Validate(s) != nil || !l.matches(s) {
		return unknown()
	}
	issued, e := parseTime(string(s.IssuedAt))
	expiry, ee := parseTime(string(s.ExpiresAt))
	if e != nil || ee != nil || l.manager.time().Before(issued) || !l.manager.time().Before(expiry) {
		return unknown()
	}
	disabled, e := l.marker()
	if e != nil || !disabled {
		return unknown()
	}
	// The exact schema has no apiVersion field. Avoid any public output encoder.
	raw, e := T.MarshalEphemeral(s)
	if e != nil {
		return unknown()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return unknown()
	}
	delete(fields, "apiVersion")
	fields["schemaVersion"] = json.RawMessage("1")
	b, e := json.Marshal(fields)
	if e != nil {
		return unknown()
	}
	b = append(b, '\n')
	if e = l.manager.fs.write(l.rel+".json.next", b); e != nil {
		return unknown()
	}
	if e = l.manager.checkpoint("credential:flushed"); e != nil {
		return unknown()
	}
	if _, e = l.readFile(false); e != nil && !missing(e) {
		return unknown()
	}
	if e = l.manager.fs.move(l.rel+".json.next", l.rel+".json", true); e != nil {
		return unknown()
	}
	if e = l.manager.checkpoint("credential:published"); e != nil {
		return unknown()
	}
	got, e := l.readFile(true)
	if e != nil || got.Token != s.Token || got.CredentialID != s.CredentialID {
		return unknown()
	}
	if e = l.manager.fs.move(l.rel+".disabled", l.rel+".disabled.removed", false); e != nil {
		return unknown()
	}
	if e = l.manager.checkpoint("credential:marker-removed"); e != nil {
		return unknown()
	}
	if e = windows.DeleteFile(ptr(l.manager.fs.path(l.rel + ".disabled.removed"))); e != nil {
		return unknown()
	}
	if exists, e := l.marker(); e != nil || exists {
		return unknown()
	}
	return credentialMetadata(got), nil
}

func (l *nativeCredentialLease) remove() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	unknown := fail("CREDENTIAL_OUTCOME_UNKNOWN", 4)
	if l.closed {
		return unknown
	}
	if yes, e := l.marker(); e != nil || !yes {
		return unknown
	}
	if _, e := l.readFile(false); e != nil {
		return unknown
	}
	if e := windows.DeleteFile(ptr(l.manager.fs.path(l.rel + ".json"))); e != nil {
		return unknown
	}
	if _, e := l.manager.fs.read(l.rel + ".json"); !missing(e) {
		return unknown
	}
	return nil
}
