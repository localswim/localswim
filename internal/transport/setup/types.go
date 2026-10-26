// Package setup implements the accepted native setup material lifecycle.
// It does not start services, select boards or issue board credentials.
// Trust changes require explicit provider composition and confirmed ApplyTrust.
package setup

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const DefaultPort = 47443
const day = 24 * time.Hour

// Failure contains only a fixed diagnostic; underlying OS errors and material are
// deliberately not included. Possible publication effects always use exit 4.
type Failure struct {
	Code string
	Exit int
}

func (e *Failure) Error() string       { return e.Code }
func fail(code string, exit int) error { return &Failure{code, exit} }
func failure(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	return &Failure{"INVOCATION_OR_IO", 1}
}

type PortObservation struct {
	Value       *int   `json:"value"`
	Observation string `json:"observation"`
}

// Result is intentionally secret-free and implements every accepted status field.
type Result struct {
	State              string          `json:"state"`
	Changed            bool            `json:"changed"`
	InstallationID     *string         `json:"installationId"`
	Origin             *string         `json:"origin"`
	IssuerSHA256       *string         `json:"issuerSha256"`
	IssuerExpiresAt    *string         `json:"issuerExpiresAt"`
	LeafExpiresAt      *string         `json:"leafExpiresAt"`
	OwnerExpiresAt     *string         `json:"ownerExpiresAt"`
	LocalMaterial      string          `json:"localMaterial"`
	PendingTransaction *string         `json:"pendingTransaction"`
	ExclusiveLock      string          `json:"exclusiveLock"`
	Port               PortObservation `json:"port"`
	CurrentUserRoot    string          `json:"currentUserRoot"`
	BrowserTrust       string          `json:"browserTrust"`
	NextAction         string          `json:"nextAction"`
}
type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Envelope struct {
	APIVersion int         `json:"apiVersion"`
	Scope      string      `json:"scope"`
	Command    *string     `json:"command"`
	Outcome    string      `json:"outcome"`
	Result     *Result     `json:"result"`
	Error      *Diagnostic `json:"error"`
}

// Response has a process-independent exit classification. The command adapter
// serializes Envelope once and sends terminal prompts only to stderr.
type Response struct {
	Envelope Envelope
	Exit     int
}

func response(command string, r Result, err error) Response {
	e := Envelope{APIVersion: 1, Scope: "setup", Command: &command, Outcome: "confirmed", Result: &r}
	x := 0
	if err != nil {
		f := failure(err)
		x = f.Exit
		e.Outcome = map[int]string{1: "failed", 2: "refused", 3: "unavailable", 4: "unknown"}[x]
		e.Error = &Diagnostic{f.Code, f.Code}
	}
	return Response{e, x}
}
func emptyResult() Result {
	return Result{State: "absent", LocalMaterial: "absent", ExclusiveLock: "unavailable", Port: PortObservation{Observation: "unchecked"}, CurrentUserRoot: "unchecked", BrowserTrust: "unqualified", NextAction: "initialize"}
}

type selector struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Generation     string `json:"generation"`
	ManifestSHA256 string `json:"manifestSha256"`
}
type manifest struct {
	SchemaVersion       int               `json:"schemaVersion"`
	Generation          string            `json:"generation"`
	InstallationID      string            `json:"installationId"`
	OwnerActorID        string            `json:"ownerActorId"`
	Hostname            string            `json:"hostname"`
	Port                int               `json:"port"`
	CreatedAt           string            `json:"createdAt"`
	GenerationCreatedAt string            `json:"generationCreatedAt"`
	IssuerSHA256        string            `json:"issuerSha256"`
	IssuerNotBefore     string            `json:"issuerNotBefore"`
	IssuerNotAfter      string            `json:"issuerNotAfter"`
	LeafSHA256          string            `json:"leafSha256"`
	LeafNotBefore       string            `json:"leafNotBefore"`
	LeafNotAfter        string            `json:"leafNotAfter"`
	OwnerTokenSHA256    string            `json:"ownerTokenSha256"`
	OwnerIssuedAt       string            `json:"ownerIssuedAt"`
	OwnerExpiresAt      string            `json:"ownerExpiresAt"`
	Files               map[string]string `json:"files"`
}
type ownerFile struct {
	SchemaVersion  int    `json:"schemaVersion"`
	InstallationID string `json:"installationId"`
	ActorID        string `json:"actorId"`
	Token          string `json:"token"`
	IssuedAt       string `json:"issuedAt"`
	ExpiresAt      string `json:"expiresAt"`
}
type transaction struct {
	SchemaVersion       int     `json:"schemaVersion"`
	TransactionID       string  `json:"transactionId"`
	Operation           string  `json:"operation"`
	PriorGeneration     *string `json:"priorGeneration"`
	CandidateGeneration string  `json:"candidateGeneration"`
	ManifestSHA256      *string `json:"manifestSha256"`
}
type material struct {
	manifest           manifest
	bytes              map[string][]byte
	owner              ownerFile
	issuer, leaf       *x509.Certificate
	issuerKey, leafKey any
}

func hash(b []byte) string     { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
func parseTime(s string) (time.Time, error) {
	t, e := time.Parse("2006-01-02T15:04:05Z", s)
	if e != nil || stamp(t) != s {
		return time.Time{}, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return t, nil
}
func origin(m manifest) string { return fmt.Sprintf("https://%s:%d", m.Hostname, m.Port) }

// ConnectionContext preserves exact installation/origin/issuer identity. It is
// independent of board, incarnation and immutable submitted evidence.
type ConnectionContext struct {
	InstallationID, OwnerActorID, Hostname, Origin, Authority, IssuerSHA256 string
	Port                                                                    int
	issuerDER                                                               []byte
}

func (m *material) context() ConnectionContext {
	x := m.manifest
	return ConnectionContext{x.InstallationID, x.OwnerActorID, x.Hostname, origin(x), fmt.Sprintf("%s:%d", x.Hostname, x.Port), x.IssuerSHA256, x.Port, append([]byte(nil), m.issuer.Raw...)}
}

// TLSConfig trusts only the validated local issuer, never the system trust pool.
func (c ConnectionContext) TLSConfig() (*tls.Config, error) {
	issuer, err := x509.ParseCertificate(c.issuerDER)
	if err != nil || hash(c.issuerDER) != c.IssuerSHA256 {
		return nil, fail("IDENTITY_MISMATCH", 2)
	}
	pool := x509.NewCertPool()
	pool.AddCert(issuer)
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.Hostname, RootCAs: pool}, nil
}

// NativeOwner is a private client credential provider. It has no board rights.
// Never put Authorization in output, evidence, URLs or logs.
type NativeOwner struct {
	Context             ConnectionContext
	IssuedAt, ExpiresAt time.Time
	token               string
	tokenHash           [32]byte
}

func (n NativeOwner) String() string   { return "native owner [redacted]" }
func (n NativeOwner) GoString() string { return n.String() }
func (n NativeOwner) MarshalJSON() ([]byte, error) {
	return nil, errors.New("native owner is not serializable")
}
func (n NativeOwner) Authorization() string { return "Bearer " + n.token }

// ActiveTokenMatches checks only the current native token and expiry. Grant and
// narrow own-grant-outcome admission remain the later grant component's duty.
func (n NativeOwner) ActiveTokenMatches(token string, installationID string, now time.Time) bool {
	decoded, e := base64.RawURLEncoding.Strict().DecodeString(token)
	x := sha256.Sum256(decoded)
	return e == nil && len(decoded) == 32 && installationID == n.Context.InstallationID && !now.Before(n.IssuedAt) && now.Before(n.ExpiresAt) && subtle.ConstantTimeCompare(x[:], n.tokenHash[:]) == 1
}

// Confirmation represents the terminal adapter's exact line, never a claimed
// human identity. Native library checks both terminal presence and exact text.
type Confirmation struct {
	Terminal bool
	Line     string
}

func confirm(c Confirmation, expected string) error {
	if !c.Terminal {
		return fail("CONFIRMATION_REQUIRED", 2)
	}
	if c.Line != expected {
		return fail("CONFIRMATION_REFUSED", 2)
	}
	return nil
}

// TrustStore is deliberately injected. New(nil) has no store; the explicit
// NewWithWindowsCurrentUserRoot constructor supplies the fixed Windows provider
// without opening it. Manager.ApplyTrust retains the plan/confirmation gate.
// Actual CurrentUser Root owner actions remain separate qualification work.
// Membership must inspect the writable local physical current-user Root store.
type TrustStore interface {
	Membership(der []byte) (bool, error)
	Enroll(der []byte) error
	Remove(der []byte) error
}
type TrustPlan struct {
	InstallationID, Authority, IssuerSHA256, ExpiresAt, StoreScope, Consequence, ConfirmationLine string
	issuerDER                                                                                     []byte
	operation                                                                                     string
}

func (p TrustPlan) String() string {
	return p.operation + " " + p.InstallationID + " " + p.IssuerSHA256
}
