//go:build nativecompilerqualification

package transport

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

// These native checks compile actual independent D03 fixtures against generated
// declarations. No browser execution, auth, multipart or native IO is qualified.
func TestNativeDeclarations(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" || runtime.Version() != "go1.27.3" {
		t.Fatal("requires pinned Windows amd64 Go1.27.3")
	}
	node, tsc, work := os.Getenv("C02_NODE"), os.Getenv("C02_TSC"), os.Getenv("C02_WORK")
	if !filepath.IsAbs(node) || !filepath.IsAbs(tsc) || !filepath.IsAbs(work) {
		t.Fatal("absolute C02_NODE/C02_TSC/new private C02_WORK required")
	}
	if _, e := os.Stat(work); !os.IsNotExist(e) {
		t.Fatal("C02_WORK must be new")
	}
	if e := os.MkdirAll(work, 0700); e != nil {
		t.Fatal(e)
	}
	run := func(args ...string) []byte {
		t.Helper()
		c := exec.Command(node, args...)
		c.Dir = work
		b, e := c.CombinedOutput()
		if e != nil {
			os.WriteFile(filepath.Join(work, "failure.log"), b, 0600)
			t.Fatalf("native tool failed: %v; diagnostics private", e)
		}
		return b
	}
	if strings.TrimSpace(string(run("--version"))) != "v26.12.1" || !strings.Contains(string(run(tsc, "--version")), "7.0.2") {
		t.Fatal("native pins")
	}
	for _, generate := range []func() (map[string][]byte, error){contract.GeneratedFiles, A.GeneratedFiles, GeneratedFiles} {
		files, e := generate()
		if e != nil {
			t.Fatal(e)
		}
		for p, b := range files {
			if strings.HasPrefix(p, "web/") {
				target := filepath.Join(work, filepath.FromSlash(strings.TrimPrefix(p, "web/")))
				if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(target, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	var index struct {
		Positive []struct{ Path, Family string }
	}
	if e := json.Unmarshal(fixture(t, "cases.json"), &index); e != nil {
		t.Fatal(e)
	}
	var source strings.Builder
	source.WriteString("import type * as T from './generated/transport/contracts';\nimport type * as M from './generated/contracts';\n")
	for i, f := range index.Positive {
		typ := map[string]string{"terminal": "Terminal", "refused": "NonterminalRefusal", "unknown": "KnownIdentityUnknown", "session-read": "SessionRead", "session-revoked": "SessionRevoked", "code-secret": "CodeSecret", "exchange-secret": "ExchangeSecret", "credential-secret": "CredentialSecret", "credential-cli": "CredentialCLI", "export": "ExportStatus", "restore": "RestoreStatus", "preview": "Preview"}[f.Family]
		if f.Family == "read" {
			typ = "HistoryRead"
			if f.Path == "board-read.json" {
				typ = "BoardRead"
			}
			if f.Path == "card-read.json" {
				typ = "CardRead"
			}
		}
		if f.Family == "request" {
			typ = map[string]string{"code-request.json": "CodeRequest", "credential-issue-request.json": "CredentialIssueRequest", "exchange-request.json": "ExchangeRequest", "preview-request.json": "PreviewRequest", "confirm-request.json": "Confirmation", "export-request.json": "ExportRequest", "request-attachment.json": "M.RequestAttachmentAdd"}[f.Path]
		}
		if typ == "" {
			t.Fatalf("unhandled TS fixture %s", f.Path)
		}
		if !strings.HasPrefix(typ, "M.") {
			typ = "T." + typ
		}
		fmt.Fprintf(&source, "const fixture%d = %s satisfies %s;\n", i, strings.TrimSuffix(string(fixture(t, f.Path)), "\n"), typ)
	}
	source.WriteString(`
declare const terminal: T.Terminal;
if (terminal.retention === 'compact') {
 const absent: null = terminal.result;
 // @ts-expect-error compact data cannot expose a pruned result
 const pruned = terminal.result.cardId;
}
if (terminal.outcome === 'not_committed') {
 const absentRevision: null = terminal.revision;
 const noEffects: readonly [] = terminal.resultRefs;
}
if (terminal.outcome === 'committed' && terminal.retention === 'full' && terminal.command === 'attachment.add') {
 const id: string = terminal.result.attachmentId;
 // @ts-expect-error command-specific full result
 const wrong = terminal.result.grantId;
}
declare const full: T.TerminalAttachmentAddCommittedFull;
// @ts-expect-error full result cannot contain token
const leaked: T.TerminalAttachmentAddCommittedFull = {...full,result:{...full.result,token:'synthetic'}};
// @ts-expect-error target selected by command
const wrongTarget: T.TerminalAttachmentAddCommittedFull = {...full,target:{grantId:'synthetic'}};
// @ts-expect-error no revision in known identity unknown
const unknown: T.KnownIdentityUnknown = {apiVersion:1,outcome:'unknown',boardId:'x',incarnation:'x',opId:'x',actorId:'x',fingerprintVersion:1,fingerprint:'x',error:{code:'x',message:'x',field:null},revision:1};
// @ts-expect-error absent-or-UUID is never null
const op: T.NonterminalRefusal = {apiVersion:1,outcome:'refused',error:{code:'x',message:'x',field:null},conflict:null,opId:null};
// @ts-expect-error optional opId cannot be explicitly undefined
const undefinedOp: T.NonterminalRefusal = {apiVersion:1,outcome:'refused',error:{code:'x',message:'x',field:null},conflict:null,opId:undefined};
// @ts-expect-error required nullable conflict
const missing: T.NonterminalRefusal = {apiVersion:1,outcome:'refused',error:{code:'x',message:'x',field:null}};
// @ts-expect-error credential error excludes field
const credentialError: T.CredentialError = {code:'x',message:'x',field:null};
// @ts-expect-error local export path never enters public envelope
const exportPath: T.ExportResult = {sourceIdentity:null,sourceRevision:null,destination:'private/path'};
// @ts-expect-error exact D03 create wire has required null card precondition
const omittedCreate: T.RequestCardCreate = {opId:'x',incarnation:'x',target:null,expectedRevision:0,input:{title:'x',description:''}};
// @ts-expect-error exact D03 grant wire has required null card precondition
const omittedGrant: T.RequestGrantRevoke = {boardId:'x',opId:'x',incarnation:'x',target:{grantId:'x'},expectedRevision:0,input:{}};
// @ts-expect-error successful export requires validated source facts
const unverifiedExport: T.ExportStatus = {apiVersion:1,scope:'export',outcome:'offered',result:{sourceIdentity:null,sourceRevision:null,destination:null}};
declare const credential: T.CredentialCLI;
if (credential.outcome === 'confirmed' && credential.command === 'issue') {
 const id: string = credential.result.credentialId;
 const available: true = credential.result.locallyAvailable;
 const noError: null = credential.error;
}
`)
	if e := os.WriteFile(filepath.Join(work, "check.ts"), []byte(source.String()), 0600); e != nil {
		t.Fatal(e)
	}
	config := `{"compilerOptions":{"strict":true,"exactOptionalPropertyTypes":true,"noEmitOnError":true,"target":"ES2022","module":"CommonJS"},"files":["check.ts"]}` + "\n"
	if e := os.WriteFile(filepath.Join(work, "tsconfig.json"), []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	log := run(tsc, "-p", filepath.Join(work, "tsconfig.json"), "--noEmit")
	if e := os.WriteFile(filepath.Join(work, "check.log"), log, 0600); e != nil {
		t.Fatal(e)
	}
}
