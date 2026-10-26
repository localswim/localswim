//go:build windows && amd64

package setup

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// Manager always uses Windows' LocalAppData known folder in production. The
// alternate root, clock, random, bind and interruption seams are package-private
// and exist solely for synthetic tests; no environment or CLI override exists.
type Manager struct {
	fs         *nativeFS
	now        func() time.Time
	random     io.Reader
	bind       func(int) error
	store      TrustStore
	checkpoint func(string) error
}

// New resolves native setup storage. A nil store makes trust membership explicitly
// unavailable; it never assumes absence and never accesses a live store implicitly.
func New(store TrustStore) (out *Manager, err error) {
	defer func() {
		if err != nil {
			err = failure(err)
		}
	}()
	root, e := nativeRoot()
	if e != nil {
		return nil, e
	}
	return managerAt(root, store)
}
func managerAt(root string, store TrustStore) (*Manager, error) {
	fs, e := newNativeFS(root)
	if e != nil {
		return nil, e
	}
	return &Manager{fs: fs, now: time.Now, random: productionRandom, bind: bindBoth, store: store, checkpoint: func(string) error { return nil }}, nil
}
func (m *Manager) time() time.Time { return m.now().UTC().Truncate(time.Second) }
func (m *Manager) checkRootLayout() error {
	entries, e := m.fs.list("")
	if e != nil {
		return e
	}
	allowed := map[string]bool{"install.lock": false, "active.json": false, "active.json.next": false, "generations": true, "pending": true, "cli": true}
	for _, entry := range entries {
		directory, ok := allowed[entry.Name()]
		if !ok || entry.IsDir() != directory {
			return fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
		if directory {
			pin, e := m.fs.pinChain(m.fs.path(entry.Name()), false)
			if e != nil {
				return e
			}
			pin()
		} else if e = m.fs.validateFile(entry.Name()); e != nil {
			return e
		}
	}
	return nil
}
func missing(e error) bool {
	return errors.Is(e, os.ErrNotExist) || errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND)
}
func (m *Manager) readSelector() (*selector, []byte, error) {
	b, e := m.fs.read("active.json")
	if missing(e) {
		return nil, nil, nil
	}
	if e != nil {
		return nil, nil, e
	}
	var s selector
	if e = jsonDecode(b, &s); e != nil {
		return nil, nil, e
	}
	if s.SchemaVersion != 1 {
		return nil, nil, fail("UNSUPPORTED_VERSION", 2)
	}
	if !uuidRE.MatchString(s.Generation) || !hashRE.MatchString(s.ManifestSHA256) {
		return nil, nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return &s, b, nil
}
func (m *Manager) readGeneration(rel, gen, digest string, public bool) (*material, error) {
	entries, e := m.fs.list(rel)
	if e != nil {
		return nil, e
	}
	allowed := map[string]bool{"manifest.json": true}
	for _, s := range materialNames {
		allowed[s] = true
	}
	if !public && len(entries) != len(allowed) {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	for _, entry := range entries {
		if entry.IsDir() || !allowed[entry.Name()] {
			return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
	}
	b := map[string][]byte{}
	names := append([]string{"manifest.json"}, materialNames...)
	if public {
		names = []string{"manifest.json", "issuer.pem"}
	}
	for _, s := range names {
		b[s], e = m.fs.read(filepath.Join(rel, s))
		if e != nil {
			return nil, e
		}
	}
	return validateMaterial(b, gen, digest, public)
}
func (m *Manager) coherent(public bool) (*material, error) {
	for attempt := 0; attempt < 2; attempt++ {
		s, a, e := m.readSelector()
		if e != nil {
			return nil, e
		}
		if s == nil {
			return nil, nil
		}
		mat, e := m.readGeneration(filepath.Join("generations", s.Generation), s.Generation, s.ManifestSHA256, public)
		_, b, re := m.readSelector()
		if re != nil {
			return nil, re
		}
		if !bytes.Equal(a, b) {
			continue
		}
		return mat, e
	}
	return nil, fail("STATUS_UNAVAILABLE", 3)
}
func (m *Manager) pending() (*transaction, error) {
	entries, e := m.fs.list("pending")
	if missing(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) != 1 || !entries[0].IsDir() || !uuidRE.MatchString(entries[0].Name()) {
		return nil, fail("SETUP_RECOVERY_REQUIRED", 4)
	}
	id := entries[0].Name()
	b, e := m.fs.read(filepath.Join("pending", id, "transaction.json"))
	if e != nil {
		return nil, fail("SETUP_RECOVERY_REQUIRED", 4)
	}
	var tx transaction
	if jsonDecode(b, &tx) != nil || tx.SchemaVersion != 1 || tx.TransactionID != id || !uuidRE.MatchString(tx.CandidateGeneration) || (tx.Operation != "init" && tx.Operation != "certificate.renew" && tx.Operation != "owner.renew") || (tx.Operation == "init") != (tx.PriorGeneration == nil) || (tx.PriorGeneration != nil && !uuidRE.MatchString(*tx.PriorGeneration)) || (tx.ManifestSHA256 != nil && !hashRE.MatchString(*tx.ManifestSHA256)) {
		return nil, fail("SETUP_RECOVERY_REQUIRED", 4)
	}
	return &tx, nil
}
func (m *Manager) result(mat *material, tx *transaction, now time.Time) Result {
	r := emptyResult()
	r.ExclusiveLock = m.fs.busy()
	if mat != nil {
		x := mat.manifest
		o := origin(x)
		r.State = "initialized"
		r.LocalMaterial = "valid"
		r.InstallationID = &x.InstallationID
		r.Origin = &o
		r.IssuerSHA256 = &x.IssuerSHA256
		r.IssuerExpiresAt = &x.IssuerNotAfter
		r.LeafExpiresAt = &x.LeafNotAfter
		r.OwnerExpiresAt = &x.OwnerExpiresAt
		r.Port.Value = &x.Port
		r.NextAction = "none"
		if m.store == nil {
			r.CurrentUserRoot = "unavailable"
			r.NextAction = "inspect_trust_store"
		} else if yes, e := m.store.Membership(mat.issuer.Raw); e != nil {
			r.CurrentUserRoot = "unavailable"
			r.NextAction = "inspect_trust_store"
		} else if yes {
			r.CurrentUserRoot = "present"
		} else {
			r.CurrentUserRoot = "absent"
			r.NextAction = "owner_trust_enrollment"
		}
		owner, _ := parseTime(x.OwnerExpiresAt)
		issued, _ := parseTime(x.OwnerIssuedAt)
		if !now.Before(owner) {
			r.NextAction = "renew_owner"
		}
		if mat.leaf != nil && mat.leaf.NotAfter.Sub(now) <= 7*day {
			r.NextAction = "renew_leaf"
		}
		if mat.issuer.NotAfter.Sub(now) < day {
			r.NextAction = "issuer_replacement_decision"
		}
		if now.Before(mat.issuer.NotBefore) || (mat.leaf != nil && now.Before(mat.leaf.NotBefore)) || now.Before(issued) {
			r.NextAction = "repair_decision"
		}
	}
	if tx != nil {
		r.State = "pending"
		r.PendingTransaction = &tx.TransactionID
		r.NextAction = "investigate_pending"
	}
	return r
}
func invalidResult(r Result) Result {
	r.State = "invalid"
	r.LocalMaterial = "invalid"
	r.NextAction = "repair_decision"
	r.InstallationID = nil
	r.Origin = nil
	r.IssuerSHA256 = nil
	r.IssuerExpiresAt = nil
	r.LeafExpiresAt = nil
	r.OwnerExpiresAt = nil
	r.Port.Value = nil
	return r
}

// Status is read-only: no directory creation, repair, lock creation, bind, DNS or
// renewal. Invalid/pending is a confirmed observation when coherently readable.
func (m *Manager) Status() Response {
	r := emptyResult()
	closeRoot, e := m.fs.rootPin(false)
	if missing(e) {
		return response("status", r, nil)
	}
	if e != nil {
		return response("status", invalidResult(r), e)
	}
	defer closeRoot()
	r.ExclusiveLock = m.fs.busy()
	if e = m.checkRootLayout(); e != nil {
		return response("status", invalidResult(r), nil)
	}
	mat, e := m.coherent(false)
	if e != nil {
		if failure(e).Code == "STATUS_UNAVAILABLE" {
			return response("status", r, e)
		}
		return response("status", invalidResult(r), nil)
	}
	tx, pe := m.pending()
	r = m.result(mat, tx, m.time())
	if pe != nil {
		r.State = "pending"
		r.NextAction = "investigate_pending"
		return response("status", r, nil)
	}
	if mat == nil && tx == nil {
		entries, e := m.fs.list("")
		if e != nil {
			return response("status", r, fail("STATUS_UNAVAILABLE", 3))
		}
		if len(entries) > 0 {
			r = invalidResult(r)
		}
	}
	return response("status", r, nil)
}
func (m *Manager) Init(port *int) Response { return m.mutate("init", port, "", "", Confirmation{}) }
func (m *Manager) RenewCertificate(installationID, issuerSHA256 string) Response {
	return m.mutate("certificate.renew", nil, installationID, issuerSHA256, Confirmation{})
}
func (m *Manager) RenewOwner(installationID, issuerSHA256 string, c Confirmation) Response {
	return m.mutate("owner.renew", nil, installationID, issuerSHA256, c)
}
func identity(mat *material, id, pin string) error {
	if !uuidRE.MatchString(id) || !hashRE.MatchString(pin) || mat == nil || mat.manifest.InstallationID != id || mat.manifest.IssuerSHA256 != pin {
		return fail("IDENTITY_MISMATCH", 2)
	}
	return nil
}
func (m *Manager) mutate(command string, port *int, id, pin string, c Confirmation) Response {
	r := emptyResult()
	if port != nil && (*port < 1024 || *port > 65535) {
		return response(command, r, fail("INVOCATION_OR_IO", 1))
	}
	if command != "init" && (!uuidRE.MatchString(id) || !hashRE.MatchString(pin)) {
		return response(command, r, fail("IDENTITY_MISMATCH", 2))
	}
	closeRoot, e := m.fs.rootPin(command == "init")
	if e != nil {
		return response(command, r, e)
	}
	defer closeRoot()
	if e = m.checkRootLayout(); e != nil {
		return response(command, invalidResult(r), e)
	}
	mat, e := m.coherent(false)
	if e != nil {
		if pending, pe := m.pending(); pending != nil || pe != nil {
			return response(command, invalidResult(r), fail("SETUP_RECOVERY_REQUIRED", 4))
		}
		return response(command, invalidResult(r), e)
	}
	tx, e := m.pending()
	if e != nil {
		return response(command, m.result(mat, nil, m.time()), e)
	}
	r = m.result(mat, tx, m.time())
	if command == "init" && mat != nil && port != nil && mat.manifest.Port != *port {
		return response(command, r, fail("CONFIG_CONFLICT", 2))
	}
	if command == "init" && mat != nil && tx == nil {
		return response(command, r, nil)
	} // valid busy no-op
	if command != "init" {
		if e = identity(mat, id, pin); e != nil {
			return response(command, r, e)
		}
		if command == "owner.renew" {
			if e = confirm(c, "renew-owner "+id+" "+pin); e != nil {
				return response(command, r, e)
			}
		}
	}
	// Refuse all unrecognized ROOT content before creating install.lock.
	if mat == nil && tx == nil {
		entries, e := m.fs.list("")
		if e != nil {
			return response(command, r, e)
		}
		if len(entries) != 0 {
			return response(command, invalidResult(r), fail("SETUP_RECOVERY_REQUIRED", 2))
		}
	}
	unlock, e := m.fs.lock(true)
	if e != nil {
		if failure(e).Code == "INSTALLATION_BUSY" {
			r.ExclusiveLock = "busy"
		}
		return response(command, r, e)
	}
	defer unlock()
	r.ExclusiveLock = "free"
	// Re-read under exclusion, including a racing first publication.
	current, e := m.coherent(false)
	if e != nil {
		return response(command, invalidResult(r), e)
	}
	pending, e := m.pending()
	if e != nil {
		return response(command, r, e)
	}
	if pending != nil {
		return m.reconcile(command, port, id, pin, current, pending)
	}
	if command == "init" && current != nil {
		if port != nil && current.manifest.Port != *port {
			return response(command, m.result(current, nil, m.time()), fail("CONFIG_CONFLICT", 2))
		}
		return response(command, m.result(current, nil, m.time()), nil)
	}
	if command != "init" {
		if e = identity(current, id, pin); e != nil {
			return response(command, r, e)
		}
		now := m.time()
		if now.Before(current.issuer.NotBefore) || now.Before(current.leaf.NotBefore) {
			return response(command, m.result(current, nil, now), fail("UNSAFE_LOCAL_MATERIAL", 2))
		}
		if current.issuer.NotAfter.Sub(now) < day {
			return response(command, m.result(current, nil, now), fail("ISSUER_REPLACEMENT_REQUIRED", 2))
		}
	}
	txID, e := uuid(m.random)
	if e != nil {
		return response(command, r, e)
	}
	gen, e := uuid(m.random)
	if e != nil {
		return response(command, r, e)
	}
	tx = &transaction{SchemaVersion: 1, TransactionID: txID, Operation: command, CandidateGeneration: gen}
	if current != nil {
		prior := current.manifest.Generation
		tx.PriorGeneration = &prior
	}
	for _, rel := range []string{"pending", "generations", filepath.Join("pending", txID)} {
		if e = m.fs.mkdir(rel); e != nil {
			return response(command, r, e)
		}
	}
	if e = m.fs.write(filepath.Join("pending", txID, "transaction.json"), jsonBytes(tx)); e != nil {
		return response(command, r, e)
	}
	r = m.result(current, tx, m.time())
	if e = m.checkpoint("transaction"); e != nil {
		return response(command, r, e)
	}
	now := m.time()
	var candidate *material
	if command == "init" {
		p := DefaultPort
		if port != nil {
			p = *port
		}
		candidate, e = freshMaterial(m.random, now, gen, p)
	} else {
		candidate = &material{manifest: current.manifest, bytes: map[string][]byte{}, owner: current.owner, issuer: current.issuer, leaf: current.leaf, issuerKey: current.issuerKey, leafKey: current.leafKey}
		for k, b := range current.bytes {
			candidate.bytes[k] = append([]byte(nil), b...)
		}
		candidate.manifest.Generation = gen
		candidate.manifest.GenerationCreatedAt = stamp(now)
		if command == "certificate.renew" {
			e = issueLeaf(m.random, candidate, now)
		} else {
			var o ownerFile
			o, e = newOwner(m.random, candidate.manifest, now)
			if e == nil {
				candidate.setOwner(o)
			}
		}
		if e == nil {
			candidate.finish()
		}
	}
	if e != nil {
		return response(command, r, e)
	}
	rel := filepath.Join("pending", txID, gen)
	if e = m.fs.mkdir(rel); e != nil {
		return response(command, r, e)
	}
	for _, s := range append(append([]string{}, materialNames...), "manifest.json") {
		if e = m.fs.write(filepath.Join(rel, s), candidate.bytes[s]); e != nil {
			return response(command, r, e)
		}
		if e = m.checkpoint("file:" + s); e != nil {
			return response(command, r, e)
		}
	}
	digest := hash(candidate.bytes["manifest.json"])
	if _, e = m.readGeneration(rel, gen, digest, false); e != nil {
		return response(command, r, e)
	}
	tx.ManifestSHA256 = &digest
	if e = m.fs.replace(filepath.Join("pending", txID, "transaction.json"), jsonBytes(tx)); e != nil {
		return response(command, r, e)
	}
	if e = m.checkpoint("complete"); e != nil {
		return response(command, r, e)
	}
	return m.publish(command, current, tx, candidate, rel)
}
func (m *Manager) publish(command string, prior *material, tx *transaction, candidate *material, rel string) Response {
	r := m.result(prior, tx, m.time())
	if command == "init" {
		if e := m.bind(candidate.manifest.Port); e != nil {
			return response(command, r, e)
		}
		r.Port.Observation = "both_bound_then_released"
	}
	final := filepath.Join("generations", tx.CandidateGeneration)
	if rel != final {
		if e := m.fs.move(rel, final, false); e != nil {
			return response(command, r, e)
		}
	}
	if e := m.checkpoint("before-selector"); e != nil {
		return response(command, r, e)
	}
	sel := jsonBytes(selector{1, tx.CandidateGeneration, *tx.ManifestSHA256})
	// Any failure once final selector replacement is attempted is conservatively
	// unknown. Complete pending candidates are retained for exact reconciliation.
	if e := m.fs.write("active.json.next", sel); e != nil {
		return response(command, r, e)
	}
	if e := m.checkpoint("selector-flushed"); e != nil {
		return response(command, r, e)
	}
	if e := m.fs.move("active.json.next", "active.json", true); e != nil {
		return response(command, r, fail("SETUP_OUTCOME_UNKNOWN", 4))
	}
	if e := m.checkpoint("after-selector"); e != nil {
		return response(command, r, fail("SETUP_OUTCOME_UNKNOWN", 4))
	}
	active, e := m.coherent(false)
	if e != nil || active == nil || active.manifest.Generation != tx.CandidateGeneration {
		return response(command, r, fail("SETUP_OUTCOME_UNKNOWN", 4))
	}
	r = m.result(active, tx, m.time())
	r.Changed = true
	if command == "init" {
		r.Port.Observation = "both_bound_then_released"
	}
	if e = m.checkpoint("cleanup"); e != nil {
		return response(command, r, fail("SETUP_OUTCOME_UNKNOWN", 4))
	}
	if e = m.fs.cleanup(tx.TransactionID); e != nil {
		return response(command, r, fail("SETUP_OUTCOME_UNKNOWN", 4))
	}
	r.State = "initialized"
	r.PendingTransaction = nil
	r.NextAction = m.result(active, nil, m.time()).NextAction
	return response(command, r, nil)
}
func (m *Manager) reconcile(command string, port *int, id, pin string, active *material, tx *transaction) Response {
	r := m.result(active, tx, m.time())
	stop := func() Response { return response(command, r, fail("SETUP_RECOVERY_REQUIRED", 4)) }
	if tx.Operation != command || tx.ManifestSHA256 == nil {
		return stop()
	}
	if _, e := m.fs.read("active.json.next"); e == nil || !missing(e) {
		return stop()
	}
	gen := tx.CandidateGeneration
	rel := filepath.Join("generations", gen)
	candidate, e := m.readGeneration(rel, gen, *tx.ManifestSHA256, false)
	if missing(e) {
		rel = filepath.Join("pending", tx.TransactionID, gen)
		candidate, e = m.readGeneration(rel, gen, *tx.ManifestSHA256, false)
	}
	if e != nil {
		return stop()
	}
	if command == "init" {
		requestedPort := DefaultPort
		if active != nil {
			requestedPort = active.manifest.Port
		}
		if port != nil {
			requestedPort = *port
		}
		if candidate.manifest.Port != requestedPort {
			return response(command, r, fail("CONFIG_CONFLICT", 2))
		}
	} else if identity(candidate, id, pin) != nil {
		return response(command, r, fail("IDENTITY_MISMATCH", 2))
	}
	if active != nil && active.manifest.Generation == gen {
		if rel != filepath.Join("generations", gen) {
			return stop()
		}
		if e = m.fs.cleanup(tx.TransactionID); e != nil {
			return response(command, r, fail("SETUP_OUTCOME_UNKNOWN", 4))
		}
		r = m.result(active, nil, m.time())
		r.Changed = false
		return response(command, r, nil)
	}
	if (active == nil) != (tx.PriorGeneration == nil) || (active != nil && active.manifest.Generation != *tx.PriorGeneration) {
		return stop()
	}
	if active != nil {
		a, b := active.manifest, candidate.manifest
		if a.InstallationID != b.InstallationID || a.OwnerActorID != b.OwnerActorID || a.Hostname != b.Hostname || a.Port != b.Port || a.IssuerSHA256 != b.IssuerSHA256 || a.CreatedAt != b.CreatedAt || !bytes.Equal(active.bytes["issuer-key.pem"], candidate.bytes["issuer-key.pem"]) {
			return stop()
		}
		if command == "certificate.renew" && !bytes.Equal(active.bytes["owner.json"], candidate.bytes["owner.json"]) {
			return stop()
		}
		if command == "owner.renew" && (!bytes.Equal(active.bytes["server.pem"], candidate.bytes["server.pem"]) || !bytes.Equal(active.bytes["server-key.pem"], candidate.bytes["server-key.pem"])) {
			return stop()
		}
	}
	return m.publish(command, active, tx, candidate, rel)
}

// ReadContext returns validated local authority only. It never adopts or rewrites
// submitted operation evidence and never establishes any board outcome.
func (m *Manager) ReadContext() (out ConnectionContext, err error) {
	defer func() {
		if err != nil {
			err = failure(err)
		}
	}()
	c, e := m.fs.rootPin(false)
	if e != nil {
		return ConnectionContext{}, e
	}
	defer c()
	mat, e := m.coherent(false)
	if e != nil {
		return ConnectionContext{}, e
	}
	if mat == nil {
		return ConnectionContext{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	now := m.time()
	if now.Before(mat.issuer.NotBefore) || !now.Before(mat.issuer.NotAfter) || now.Before(mat.leaf.NotBefore) || !now.Before(mat.leaf.NotAfter) {
		return ConnectionContext{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return mat.context(), nil
}
func (m *Manager) LoadNativeOwner() (out NativeOwner, err error) {
	defer func() {
		if err != nil {
			err = failure(err)
		}
	}()
	c, e := m.fs.rootPin(false)
	if e != nil {
		return NativeOwner{}, e
	}
	defer c()
	mat, e := m.coherent(false)
	if e != nil {
		return NativeOwner{}, e
	}
	if mat == nil {
		return NativeOwner{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	now := m.time()
	expiry, _ := parseTime(mat.owner.ExpiresAt)
	issued, _ := parseTime(mat.owner.IssuedAt)
	if now.Before(issued) || !now.Before(expiry) || now.Before(mat.issuer.NotBefore) || !now.Before(mat.issuer.NotAfter) || now.Before(mat.leaf.NotBefore) || !now.Before(mat.leaf.NotAfter) {
		return NativeOwner{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	token, _ := base64.RawURLEncoding.DecodeString(mat.owner.Token)
	return NativeOwner{Context: mat.context(), IssuedAt: issued, ExpiresAt: expiry, token: mat.owner.Token, tokenHash: sha256.Sum256(token)}, nil
}

// ServerLease is the stopped/exclusive handoff to the future service. Holding it
// fences every setup mutation until Close; no PID or lock-file existence is used.
type ServerLease struct {
	Context             ConnectionContext
	certificate         tls.Certificate
	release             func()
	now                 func() time.Time
	notBefore, notAfter time.Time
}

func (s *ServerLease) Close() {
	if s.release != nil {
		s.release()
		s.release = nil
	}
}

// AcquireServer performs the same stopped/exclusive leaf renewal when needed,
// then acquires the service lifetime lock. Every stage rereads validated state;
// a racing server can win exclusion and cause INSTALLATION_BUSY, never bypass it.
func (m *Manager) AcquireServer() (out *ServerLease, err error) {
	defer func() {
		if err != nil {
			err = failure(err)
		}
	}()
	initialPin, e := m.fs.rootPin(false)
	if e != nil {
		return nil, e
	}
	initial, e := m.coherent(false)
	initialPin()
	if e != nil {
		return nil, e
	}
	if initial == nil {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	if initial.leaf.NotAfter.Sub(m.time()) <= 7*day {
		renewed := m.RenewCertificate(initial.manifest.InstallationID, initial.manifest.IssuerSHA256)
		if renewed.Exit != 0 {
			return nil, fail(renewed.Envelope.Error.Code, renewed.Exit)
		}
	}
	c, e := m.fs.rootPin(false)
	if e != nil {
		return nil, e
	}
	unlock, e := m.fs.lock(false)
	if e != nil {
		c()
		return nil, e
	}
	release := func() { unlock(); c() }
	mat, e := m.coherent(false)
	tx, pe := m.pending()
	if e != nil || pe != nil || mat == nil || tx != nil {
		release()
		return nil, fail("SETUP_RECOVERY_REQUIRED", 4)
	}
	now := m.time()
	if now.Before(mat.issuer.NotBefore) || now.Before(mat.leaf.NotBefore) {
		release()
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	if mat.issuer.NotAfter.Sub(now) < day {
		release()
		return nil, fail("ISSUER_REPLACEMENT_REQUIRED", 2)
	}
	if mat.leaf.NotAfter.Sub(now) <= 7*day {
		release()
		return nil, fail("STATUS_UNAVAILABLE", 3)
	}
	cert, e := tls.X509KeyPair(mat.bytes["server.pem"], mat.bytes["server-key.pem"])
	if e != nil {
		release()
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return &ServerLease{Context: mat.context(), certificate: cert, release: release, now: m.time, notBefore: mat.leaf.NotBefore, notAfter: mat.leaf.NotAfter}, nil
}
