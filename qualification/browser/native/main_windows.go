//go:build windows && amd64 && v02qualification

// The V02 executable is qualification-only and requires a private root sentinel.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/assets"
	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/cli/admin"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

var unavailable = errors.New("V02 fixture unavailable")
var humanRights = []string{"comment", "create", "edit", "read", "read-own-outcome"}
var automationRights = []string{"comment", "create", "edit", "read", "read-own-outcome"}

type manifest struct {
	SchemaVersion          int      `json:"schemaVersion"`
	InstallationID         string   `json:"installationId"`
	Origin                 string   `json:"origin"`
	BoardID                string   `json:"boardId"`
	Incarnation            string   `json:"incarnation"`
	Root                   string   `json:"root"`
	Board                  string   `json:"board"`
	Profile                string   `json:"profileDirectory"`
	Evidence               string   `json:"artifactDirectory"`
	ProfileID              string   `json:"profileId"`
	FixtureBinary          string   `json:"fixtureBinary"`
	PipeName               string   `json:"pipeName"`
	HumanActor             string   `json:"humanActor"`
	AutomationActor        string   `json:"automationActor"`
	RevocableActor         string   `json:"revocableActor"`
	IssuerDER              string   `json:"issuerDER"`
	IssuerSHA256           string   `json:"issuerSha256"`
	IssuerExpiresAt        string   `json:"issuerExpiresAt"`
	FixtureBinarySHA256    string   `json:"fixtureBinarySHA256"`
	ProfileACLRecord       string   `json:"profileACLRecord"`
	ProfileACLRecordSHA256 string   `json:"profileACLRecordSHA256"`
	ChromeExecutable       string   `json:"chromeExecutable,omitempty"`
	ChromeSHA256           string   `json:"chromeSHA256,omitempty"`
	BrowserArchiveSHA256   string   `json:"browserArchiveSHA256,omitempty"`
	BrowserVersion         string   `json:"browserVersion,omitempty"`
	PlaywrightVersion      string   `json:"playwrightVersion,omitempty"`
	EvidenceID             string   `json:"evidenceId,omitempty"`
	DelegationConfirmation string   `json:"delegationConfirmation"`
	OwnerActor             string   `json:"ownerActor"`
	HumanRights            []string `json:"humanRights"`
	AutomationRights       []string `json:"automationRights"`

	HumanInputSHA256  string          `json:"humanInputSHA256"`
	TrustPlan         setup.TrustPlan `json:"trustPlan"`
	HumanInput        string          `json:"humanInput"`
	HumanConfirmation string          `json:"humanConfirmation"`
	OwnerCommands     string          `json:"ownerCommands"`
	Ready             bool            `json:"ready"`
	Status            string          `json:"status"`
}

func uuid() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(unavailable)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func privateDir(path string) error {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return unavailable
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		return unavailable
	}
	return windows.CreateDirectory(windows.StringToUTF16Ptr(path), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd})
}
func save(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return unavailable
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
func pinned() (string, error) {
	p, e := setup.V02QualificationRoot()
	if e != nil || p == "" {
		return "", unavailable
	}
	return p, nil
}
func load() (manifest, *setup.Manager, error) {
	var f manifest
	root, e := pinned()
	if e != nil {
		return f, nil, e
	}
	b, e := os.ReadFile(filepath.Join(filepath.Dir(root), "run-manifest.json"))
	if e != nil {
		return f, nil, unavailable
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil || f.SchemaVersion != 1 || f.Root != root || f.Board != filepath.Join(filepath.Dir(root), "board") || f.Profile != filepath.Join(filepath.Dir(root), "profile") || f.Evidence != filepath.Join(filepath.Dir(root), "evidence") || f.HumanInput != filepath.Join(filepath.Dir(root), "human-grant.json") || !schema.ValidUUID(f.HumanActor) || !schema.ValidUUID(f.AutomationActor) || !schema.ValidUUID(f.RevocableActor) {
		return f, nil, unavailable
	}
	binaryPath, e := os.Executable()
	if e != nil || f.FixtureBinary != binaryPath || binaryPath != filepath.Join(filepath.Dir(root), "fixture.exe") {
		return f, nil, unavailable
	}
	binaryBytes, e := os.ReadFile(binaryPath)
	if e != nil {
		return f, nil, unavailable
	}
	digest := sha256.Sum256(binaryBytes)
	if hex.EncodeToString(digest[:]) != f.FixtureBinarySHA256 || f.ProfileACLRecord != filepath.Join(filepath.Dir(root), "profile-acl.json") || verifyPrivateDirectory(f.Profile) != nil {
		return f, nil, unavailable
	}
	m, e := setup.NewWithWindowsCurrentUserRoot()
	if e != nil {
		return f, nil, unavailable
	}
	c, e := m.ReadContext()
	if e != nil || c.InstallationID != f.InstallationID || c.Origin != f.Origin || c.IssuerSHA256 != f.IssuerSHA256 {
		return f, nil, unavailable
	}
	return f, m, nil
}
func grantWire(f manifest, actor, kind string, rights []string, revision uint64, target string) []byte {
	var t any
	if target != "" {
		t = map[string]any{"grantId": target}
	}
	input := map[string]any{}
	if target == "" {
		input = map[string]any{"granteeActorId": actor, "kind": kind, "rights": rights}
	}
	b, _ := json.Marshal(map[string]any{"boardId": f.BoardID, "incarnation": f.Incarnation, "opId": uuid(), "target": t, "expectedRevision": revision, "expectedCardRevision": nil, "input": input})
	return b
}
func prepare() error {
	root, e := pinned()
	if e != nil {
		return e
	}
	base := filepath.Dir(root)
	for _, s := range []string{"board", "profile", "evidence"} {
		if privateDir(filepath.Join(base, s)) != nil {
			return unavailable
		}
	}
	m, e := setup.NewWithWindowsCurrentUserRoot()
	if e != nil {
		return unavailable
	}
	port := 47443
	r := m.Init(&port)
	if r.Exit != 0 {
		return unavailable
	}
	c, e := m.ReadContext()
	if e != nil {
		return unavailable
	}
	f := manifest{SchemaVersion: 1, InstallationID: c.InstallationID, Origin: c.Origin, BoardID: uuid(), Incarnation: uuid(), Root: root, Board: filepath.Join(base, "board"), Profile: filepath.Join(base, "profile"), Evidence: filepath.Join(base, "evidence"), ProfileID: uuid(), HumanActor: uuid(), AutomationActor: uuid(), RevocableActor: uuid(), PipeName: `\\.\pipe\localswim-v02-` + uuid(), IssuerSHA256: c.IssuerSHA256, Status: "prepared; owner trust/human/delegation absent"}
	f.OwnerActor = c.OwnerActorID
	f.HumanRights = append([]string(nil), humanRights...)
	f.AutomationRights = append([]string(nil), automationRights...)
	f.DelegationConfirmation = delegationChallenge(f)
	f.FixtureBinary, _ = os.Executable()
	binaryBytes, e := os.ReadFile(f.FixtureBinary)
	if e != nil {
		return unavailable
	}
	binaryDigest := sha256.Sum256(binaryBytes)
	f.FixtureBinarySHA256 = hex.EncodeToString(binaryDigest[:])
	s, e := S.Create(context.Background(), f.Board, schema.Identity{BoardID: f.BoardID, Incarnation: f.Incarnation})
	if e != nil {
		return unavailable
	}
	if s.Close() != nil {
		return unavailable
	}
	f.TrustPlan, e = m.PlanTrust("enroll", f.InstallationID, f.IssuerSHA256)
	if e != nil {
		return unavailable
	}
	f.IssuerExpiresAt = f.TrustPlan.ExpiresAt
	// Read only the exact validated public issuer. Never read or serialize owner/key material.
	var selected struct {
		Generation string `json:"generation"`
	}
	b, e := os.ReadFile(filepath.Join(root, "active.json"))
	if e != nil || json.Unmarshal(b, &selected) != nil || !schema.ValidUUID(selected.Generation) {
		return unavailable
	}
	b, e = os.ReadFile(filepath.Join(root, "generations", selected.Generation, "issuer.pem"))
	if e != nil {
		return unavailable
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return unavailable
	}
	cert, e := x509.ParseCertificate(block.Bytes)
	h := sha256.Sum256(block.Bytes)
	if e != nil || hex.EncodeToString(h[:]) != f.IssuerSHA256 || !cert.IsCA {
		return unavailable
	}
	f.IssuerDER = filepath.Join(base, "issuer.der")
	if os.WriteFile(f.IssuerDER, cert.Raw, 0600) != nil {
		return unavailable
	}
	f.ProfileACLRecord = filepath.Join(base, "profile-acl.json")
	if verifyPrivateDirectory(f.Profile) != nil {
		return unavailable
	}
	if save(f.ProfileACLRecord, map[string]any{"schemaVersion": 1, "profileId": f.ProfileID, "path": f.Profile, "filesystem": "NTFS", "owner": "current-user", "protectedDACL": true, "allowPrincipals": []string{"current-user", "SYSTEM"}, "verified": true}) != nil {
		return unavailable
	}
	aclBytes, e := os.ReadFile(f.ProfileACLRecord)
	if e != nil {
		return unavailable
	}
	aclDigest := sha256.Sum256(aclBytes)
	f.ProfileACLRecordSHA256 = hex.EncodeToString(aclDigest[:])
	f.HumanInput = filepath.Join(base, "human-grant.json")
	humanWire := grantWire(f, f.HumanActor, "human", humanRights, 0, "")
	if _, e := T.FreezeMutation(humanWire, contract.ResolvedRoute{Command: "grant.create", BoardID: f.BoardID}, contract.AuthenticatedActor{ActorID: c.OwnerActorID}); e != nil {
		return unavailable
	}
	humanDigest := sha256.Sum256(append(humanWire, '\n'))
	f.HumanInputSHA256 = hex.EncodeToString(humanDigest[:])
	if os.WriteFile(f.HumanInput, append(humanWire, '\n'), 0600) != nil {
		return unavailable
	}
	f.HumanConfirmation = "enroll-human " + f.InstallationID + " " + f.BoardID + " " + f.Incarnation + " " + f.HumanActor + " " + strings.Join(humanRights, ",")
	f.OwnerCommands = filepath.Join(base, "owner-commands.txt")
	commands := fmt.Sprintf("V02 PREPARED ONLY. No owner actions have been executed.\n\n1. Review issuer DER SHA256 %s expiry %s. CurrentUser Root trust is broad browser trust.\n2. In a real Windows console, after explicit owner approval:\n  & '%s' trust-enroll\nType exactly: %s\n3. Only after actual trust enrollment, separately approve starting the bootstrap server:\n  & '%s' serve-bootstrap\n4. In a second real console:\n  & '%s' human-enroll\nType exactly: %s\nRights: %s. No approve/history-admin/grant-admin.\n5. Stop bootstrap with Ctrl+C. Separate owner approval is needed for delegated test control.\n  & '%s' serve-qualified\n%s\nType exactly: %s\nCodes travel only in the protected memory pipe.\n6. Stop service with Ctrl+C or pipe stop before trust removal:\n  & '%s' trust-remove\nType exactly: remove %s %s\n7. Preserve evidence. Manual cleanup may remove only this frozen private anchor after review; do not remove other issuers/profiles.\n", f.IssuerSHA256, f.IssuerExpiresAt, f.FixtureBinary, f.TrustPlan.ConfirmationLine, f.FixtureBinary, f.FixtureBinary, f.HumanConfirmation, strings.Join(humanRights, ","), f.FixtureBinary, delegationPrompt(f), f.DelegationConfirmation, f.FixtureBinary, f.InstallationID, f.IssuerSHA256)
	if os.WriteFile(f.OwnerCommands, []byte(commands), 0600) != nil {
		return unavailable
	}
	return save(filepath.Join(base, "run-manifest.json"), f)
}
func terminal(expected, prompt string) error {
	var mode uint32
	if windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) != nil {
		return fixtureFailure("V02_DELEGATION_CONSOLE_REQUIRED")
	}
	display := prompt + "\nType " + expected + "\n"
	if n, e := io.WriteString(os.Stderr, display); e != nil || n != len(display) {
		return fixtureFailure("V02_DELEGATION_DISPLAY_UNAVAILABLE")
	}
	line, e := bufio.NewReader(io.LimitReader(os.Stdin, 2049)).ReadString('\n')
	return validateDelegationLine(expected, line, e)
}

// Pure parser permits synthetic validation, never a terminal or consent substitute.
func validateDelegationLine(expected, line string, e error) error {
	if e != nil || len(line) > 2048 {
		return fixtureFailure("V02_DELEGATION_INPUT_UNAVAILABLE")
	}
	if strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r") != expected {
		return fixtureFailure("V02_DELEGATION_LINE_MISMATCH")
	}
	return nil
}
func owner(action string) int {
	f, m, e := load()
	if e != nil {
		return 3
	}
	p := admin.Provider{Manager: m}
	st, e := evidence.NewWindowsStore(f.Evidence)
	if e != nil {
		return 3
	}
	r := admin.Runner{Setup: m, Manager: m, Provider: p, Owner: p, Evidence: st}
	switch action {
	case "trust-enroll", "trust-remove":
		op := strings.TrimPrefix(action, "trust-")
		return r.RunSetup(context.Background(), []string{"setup", "trust", op, "--installation", f.InstallationID, "--issuer", f.IssuerSHA256, "--json"}, os.Stdin, os.Stdout, os.Stderr)
	case "human-enroll":
		input, e := os.ReadFile(f.HumanInput)
		if e != nil {
			return 3
		}
		digest := sha256.Sum256(input)
		if hex.EncodeToString(digest[:]) != f.HumanInputSHA256 {
			return 3
		}
		return r.Run(context.Background(), []string{"--json", "--board", f.BoardID, "grant", "create", "--native-owner", "--input", f.HumanInput}, os.Stdin, os.Stdout, os.Stderr)
	}
	return 1
}

type grantState struct {
	Actor   string   `json:"actor"`
	Grant   string   `json:"grant"`
	Kind    string   `json:"kind"`
	Rights  []string `json:"rights"`
	Enabled bool     `json:"enabled"`
}
type runtimeFixture struct {
	mu        sync.Mutex
	f         manifest
	m         *setup.Manager
	store     *S.Store
	native    *credentials.Service
	sessions  *session.Service
	server    *H.Server
	delivery  *delivery
	ctx       context.Context
	cancel    context.CancelFunc
	mutations uint64
	lookups   uint64
	qualified bool
	stopping  bool
}

func (x *runtimeFixture) snapshot(fn func(*sql.Conn) error) error {
	l, e := x.store.Reader(x.ctx)
	if e != nil {
		return unavailable
	}
	defer l.Close()
	return l.Do(x.ctx, fn)
}
func (x *runtimeFixture) state(actor string) (grantState, error) {
	v := grantState{Actor: actor, Rights: []string{}}
	e := x.snapshot(func(c *sql.Conn) error {
		e := c.QueryRowContext(x.ctx, "SELECT g.grant_id,a.kind,g.enabled FROM grant_record g JOIN actor a ON a.incarnation=g.incarnation AND a.actor_id=g.actor_id WHERE g.incarnation=? AND g.actor_id=? ORDER BY g.grant_id", x.f.Incarnation, actor).Scan(&v.Grant, &v.Kind, &v.Enabled)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return unavailable
		}
		rows, e := c.QueryContext(x.ctx, "SELECT right_name FROM grant_right WHERE incarnation=? AND grant_id=? ORDER BY right_name", x.f.Incarnation, v.Grant)
		if e != nil {
			return unavailable
		}
		defer rows.Close()
		for rows.Next() {
			var r string
			if rows.Scan(&r) != nil {
				return unavailable
			}
			v.Rights = append(v.Rights, r)
		}
		return rows.Err()
	})
	return v, e
}
func equal(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }
func (x *runtimeFixture) humanReady() bool {
	v, e := x.state(x.f.HumanActor)
	return e == nil && v.Enabled && v.Kind == "human" && equal(v.Rights, humanRights)
}
func (x *runtimeFixture) trusted() bool {
	r := x.m.Status()
	return r.Exit == 0 && r.Envelope.Result != nil && r.Envelope.Result.CurrentUserRoot == "present" && r.Envelope.Result.InstallationID != nil && *r.Envelope.Result.InstallationID == x.f.InstallationID && r.Envelope.Result.IssuerSHA256 != nil && *r.Envelope.Result.IssuerSHA256 == x.f.IssuerSHA256
}
func (x *runtimeFixture) revision() (uint64, error) {
	var n uint64
	e := x.snapshot(func(c *sql.Conn) error {
		return c.QueryRowContext(x.ctx, "SELECT revision FROM board WHERE singleton=1").Scan(&n)
	})
	return n, e
}
func (x *runtimeFixture) open() error {
	s, e := S.Existing(x.ctx, x.f.Board, schema.Identity{BoardID: x.f.BoardID, Incarnation: x.f.Incarnation})
	if e != nil {
		return fixtureFailure("V02_SOURCE_OPEN_UNAVAILABLE")
	}
	x.store = s
	n, e := credentials.SourceRoot(s)
	if e != nil {
		return fixtureFailure("V02_NATIVE_SOURCE_UNAVAILABLE")
	}
	x.native = n
	ss, e := session.New(s, n)
	if e != nil {
		return fixtureFailure("V02_SESSION_UNAVAILABLE")
	}
	x.sessions = ss
	a, e := assets.Handler()
	if e != nil {
		return fixtureFailure("V02_ASSETS_UNAVAILABLE")
	}
	h, e := routes(s, n, ss, a)
	if e != nil {
		return fixtureFailure("V02_ROUTES_UNAVAILABLE")
	}
	x.delivery = &delivery{next: h}
	x.server, e = H.Listen(x.ctx, n, x.delivery)
	if e != nil {
		return fixtureFailure("V02_HTTPS_UNAVAILABLE")
	}
	return nil
}
func (x *runtimeFixture) close() {
	if x.delivery != nil {
		x.delivery.release()
	}
	if x.server != nil {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = x.server.Shutdown(c)
		cancel()
	}
	if x.delivery != nil {
		x.delivery.active.Wait()
	}
	if x.sessions != nil {
		x.sessions.Close()
	}
	if x.native != nil {
		x.native.Close()
	}
	if x.store != nil {
		_ = x.store.Close()
	}
}
func serve(qualified bool) error {
	f, m, e := load()
	if e != nil {
		return fixtureFailure("V02_SETUP_CONTEXT_UNAVAILABLE")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x := &runtimeFixture{f: f, m: m, ctx: ctx, cancel: cancel, qualified: qualified}
	if !x.trusted() {
		return fixtureFailure("V02_TRUST_UNAVAILABLE")
	}
	if qualified { // inspect actual grant before opening any listener; no file approval boolean.
		s, e := S.Existing(ctx, f.Board, schema.Identity{BoardID: f.BoardID, Incarnation: f.Incarnation})
		if e != nil {
			return fixtureFailure("V02_HUMAN_SOURCE_UNAVAILABLE")
		}
		x.store = s
		ready := x.humanReady()
		_ = s.Close()
		x.store = nil
		if !ready {
			return fixtureFailure("V02_HUMAN_GRANT_UNAVAILABLE")
		}
		expected := delegationChallenge(f)
		if f.DelegationConfirmation != expected {
			return fixtureFailure("V02_DELEGATION_CONTEXT_MISMATCH")
		}
		if e := terminal(expected, delegationPrompt(f)); e != nil {
			return e
		}
	}
	if e = x.open(); e != nil {
		x.close()
		return e
	}
	defer x.close()
	if qualified {
		return x.pipe()
	}
	fmt.Fprintln(os.Stdout, "V02 bootstrap server active; no test control or code broker")
	ch := make(chan os.Signal, 1)
	signalNotify(ch)
	<-ch
	return nil
}
func (x *runtimeFixture) nativeToken() (T.SecretToken, error) {
	n, e := x.m.LoadNativeOwner()
	if e != nil {
		return "", unavailable
	}
	return T.SecretToken(strings.TrimPrefix(n.Authorization(), "Bearer ")), nil
}
func (x *runtimeFixture) submit(actor, kind string, rights []string, target string) error {
	rev, e := x.revision()
	if e != nil {
		return unavailable
	}
	cmd := "grant.create"
	if target != "" {
		cmd = "grant.revoke"
	}
	token, e := x.nativeToken()
	if e != nil {
		return e
	}
	i, e := T.FreezeMutation(grantWire(x.f, actor, kind, rights, rev, target), contract.ResolvedRoute{Command: contract.Command(cmd), BoardID: x.f.BoardID, TargetID: target}, contract.AuthenticatedActor{ActorID: x.native.Context().OwnerActorID})
	if e != nil {
		return unavailable
	}
	r := x.native.SubmitNative(x.ctx, token, i, grants.Route{BoardID: x.f.BoardID, Incarnation: x.f.Incarnation, GrantID: target})
	if r.Preparation != credentials.Confirmed || r.Result == nil || r.Result.Terminal == nil || r.Result.Terminal.Outcome != "committed" {
		return unavailable
	}
	return nil
}
func main() {
	if len(os.Args) != 2 {
		os.Exit(1)
	}
	var e error
	switch os.Args[1] {
	case "prepare":
		e = prepare()
	case "trust-enroll", "trust-remove", "human-enroll":
		os.Exit(owner(os.Args[1]))
	case "serve-bootstrap":
		e = serve(false)
	case "serve-qualified":
		e = serve(true)
	default:
		e = unavailable
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, fixtureDiagnostic(e))
		os.Exit(3)
	}
}

// Keep the archive type binding explicit for the IssueCode protocol.
var _ A.UUID

func verifyPrivateDirectory(path string) error { return verifyPrivatePath(path, true) }
func verifyPrivatePath(path string, protected bool) error {
	h, e := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return unavailable
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return unavailable
	}
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return unavailable
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return unavailable
	}
	owner, _, e := sd.Owner()
	if e != nil || !owner.Equals(u.User.Sid) {
		return unavailable
	}
	control, _, e := sd.Control()
	if e != nil || protected && control&windows.SE_DACL_PROTECTED == 0 {
		return unavailable
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		return unavailable
	}
	seenUser, seenSystem := false, false
	system, _ := windows.StringToSid("S-1-5-18")
	for i := uint32(0); i < 2; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x001f01ff || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			return unavailable
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(u.User.Sid) {
			seenUser = true
		} else if sid.Equals(system) {
			seenSystem = true
		} else {
			return unavailable
		}
	}
	if !seenUser || !seenSystem {
		return unavailable
	}
	return nil
}

func auditProfile(f manifest) any { return auditBrowserProfile(f) }

const delegationScope = "code-human,code-automation,code-revocable,provisionAutomation,revoke-revocable,dropNext,delayNext,release,restart,status,auditProfile,stop"

func delegationChallenge(f manifest) string {
	return "delegate-v02 " + f.InstallationID + " " + f.BoardID + " " + f.Incarnation + " " + f.HumanActor + " " + f.AutomationActor + " " + f.RevocableActor + " rights=" + strings.Join(automationRights, ",") + " " + delegationScope
}
func delegationPrompt(f manifest) string {
	return "Installation " + f.InstallationID + " origin " + f.Origin + " board " + f.BoardID + " incarnation " + f.Incarnation + " issuer " + f.IssuerSHA256 + " expires " + f.IssuerExpiresAt + ". Delegates native-owner test code issuance only for synthetic human " + f.HumanActor + ", automation " + f.AutomationActor + " and revocable automation " + f.RevocableActor + ". Automation provisioning is limited to those two automation actors with exact rights " + strings.Join(automationRights, ",") + "; revocation is limited to the revocable actor's grant. Delivery controls drop or delay the actual product response only after its handler returns; release resumes a delayed response. Restart drains and reopens the same validated store/TLS and invalidates all browser sessions and unexchanged codes. Also permits read-only status/profile ACL audit and stopping the fixture. No human grant creation, approve action, history-admin or grant-admin right is delegated. Codes travel only in protected pipe memory. Exact control scope " + delegationScope + "."
}
