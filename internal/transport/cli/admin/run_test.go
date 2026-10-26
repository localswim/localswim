package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/setup"
)

const (
	boardID   = "00000000-0000-4000-8000-000000000001"
	incID     = "00000000-0000-4000-8000-000000000002"
	actorID   = "00000000-0000-4000-8000-00000000000a"
	grantID   = "00000000-0000-4000-8000-00000000000b"
	installID = "00000000-0000-4000-8000-000000000099"
)

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("../../../../fixtures/contracts/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func inputFile(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "input.json")
	if os.WriteFile(p, b, 0600) != nil {
		t.Fatal("synthetic input")
	}
	return p
}
func ordinary() []string {
	return []string{"--json", "--installation", installID, "--board", boardID, "--incarnation", incID, "--grant", grantID}
}
func special(group, action string) []string {
	return []string{"--json", "--board", boardID, group, action, "--incarnation", incID, "--grant", grantID}
}

type clientFake struct {
	ctx                                          evidence.Context
	metadata                                     core.CredentialMetadata
	dispatches, requests, issues, codes, revokes int
	request                                      func() (int, http.Header, []byte, error)
	issue                                        func(T.CredentialIssueRequest, func(T.CredentialSecret) error) error
	code                                         func(T.CodeRequest, func(T.CodeSecret) error) error
	revoke                                       func() error
	dispatch                                     func(contract.ResolvedRoute, []byte) core.Outcome
}

func newFake() *clientFake {
	return &clientFake{ctx: evidence.Context{InstallationID: installID, Origin: "https://synthetic.localhost:47443"}, metadata: core.CredentialMetadata{Selected: core.Selectors{InstallationID: installID, BoardID: boardID, Incarnation: incID, GrantID: grantID}, ActorID: actorID, ExpiresAt: time.Now().Add(time.Hour)}}
}
func (f *clientFake) Context() evidence.Context         { return f.ctx }
func (f *clientFake) Metadata() core.CredentialMetadata { return f.metadata }
func (*clientFake) Close()                              {}
func (f *clientFake) Request(context.Context, string, string, io.Reader, string, string, int64) (int, http.Header, []byte, error) {
	f.requests++
	return f.request()
}
func (f *clientFake) Dispatch(_ context.Context, _ evidence.Store, r contract.ResolvedRoute, b []byte, _ io.Writer) core.Outcome {
	f.dispatches++
	if f.dispatch != nil {
		return f.dispatch(r, b)
	}
	return core.Refused("OUTCOME_UNKNOWN", 4)
}
func (f *clientFake) IssueCredential(_ context.Context, r T.CredentialIssueRequest, c func(T.CredentialSecret) error) error {
	f.issues++
	return f.issue(r, c)
}
func (f *clientFake) IssueCode(_ context.Context, r T.CodeRequest, c func(T.CodeSecret) error) error {
	f.codes++
	return f.code(r, c)
}
func (f *clientFake) RevokeCredential(_ context.Context, before func() error) error {
	if e := before(); e != nil {
		return e
	}
	f.revokes++
	if f.revoke != nil {
		return f.revoke()
	}
	return nil
}
func runnerFake(f *clientFake) Runner {
	return Runner{open: func(context.Context, core.Selectors, bool) (adminClient, error) { return f, nil }, terminal: func(io.Reader) bool { return true }}
}

func TestAdminExactGrammarAndSelectorsBeforeProvider(t *testing.T) {
	if setupCommand(append(ordinary(), "grant", "create", "--input", "setup")) {
		t.Fatal("input filename hijacked command routing")
	}
	if !setupCommand([]string{"--port", "47443", "setup", "--json", "init"}) {
		t.Fatal("setup flag ordering lost")
	}
	for name, args := range map[string][]string{
		"missing":             {"--json", "--board", boardID, "grant", "create", "--input", "x"},
		"ordinary native mix": append(ordinary(), "grant", "create", "--input", "x", "--native-owner"),
		"duplicate":           append(ordinary(), "grant", "create", "--input", "x", "--input", "y"),
		"unknown":             append(ordinary(), "recovery", "confirm", "--input", "x", "--preview", "y", "--yes"),
		"special prefix":      append(ordinary(), "credential", "issue", "--incarnation", incID, "--grant", grantID),
		"special duplicate":   append(special("credential", "issue"), "--grant", grantID),
		"invalid incarnation": {"--json", "--board", boardID, "session", "code", "--incarnation", "bad", "--grant", grantID},
		"recovery no preview": append(ordinary(), "recovery", "confirm", "--input", "x"),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			r := Runner{open: func(context.Context, core.Selectors, bool) (adminClient, error) {
				calls++
				return nil, errors.New("should not open")
			}}
			var out bytes.Buffer
			exit := r.Run(context.Background(), args, nil, &out, io.Discard)
			if exit == 0 || calls != 0 {
				t.Fatal("invalid invocation reached provider")
			}
			if !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
				t.Fatal("missing envelope")
			}
		})
	}
}

func TestHumanGrantConfirmationBeforeSharedDispatch(t *testing.T) {
	wire := []byte(`{"boardId":"` + boardID + `","incarnation":"` + incID + `","opId":"00000000-0000-4000-8000-0000000000d0","target":null,"expectedRevision":5,"expectedCardRevision":null,"input":{"granteeActorId":"00000000-0000-4000-8000-00000000005c","kind":"human","rights":["read"]}}`)
	// Fixture grantee/kind/rights are shared C01 synthetic semantic input.
	v, e := contract.DecodeJSON(wire)
	if e != nil {
		t.Fatal(e)
	}
	m := v.(map[string]any)
	payload := m["input"].(map[string]any)
	var rights []string
	for _, v := range payload["rights"].([]any) {
		rights = append(rights, v.(string))
	}
	expected := "enroll-human " + installID + " " + boardID + " " + m["incarnation"].(string) + " " + payload["granteeActorId"].(string) + " " + strings.Join(rights, ",")
	for _, tc := range []struct {
		name, line string
		terminal   bool
		want       int
	}{{"correct", expected + "\n", true, 4}, {"wrong", expected + " \n", true, 2}, {"eof", expected, true, 2}, {"nonterminal", expected + "\n", false, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			r := runnerFake(f)
			r.terminal = func(io.Reader) bool { return tc.terminal }
			var out, diag bytes.Buffer
			args := []string{"--json", "--board", boardID, "grant", "create", "--input", inputFile(t, wire), "--native-owner"}
			exit := r.Run(context.Background(), args, strings.NewReader(tc.line), &out, &diag)
			if exit != tc.want {
				t.Fatalf("exit %d", exit)
			}
			if (f.dispatches == 1) != (tc.want == 4) {
				t.Fatal("confirmation gate dispatch mismatch")
			}
			if tc.terminal && !strings.Contains(diag.String(), "Operator") {
				t.Fatal("missing authority display")
			}
		})
	}
}

func TestRecoveryOriginalPreviewNoRefreshOrRetry(t *testing.T) {
	p := fixtureBytes(t, "archive-recovery/preview-clear.json")
	c := fixtureBytes(t, "archive-recovery/confirm-clear.json")
	decoded, e := A.Decode[A.Preview](p)
	if e != nil {
		t.Fatal(e)
	}
	now, _ := time.Parse("2006-01-02T15:04:05Z", string(decoded.PreviewedAt))
	expected := "confirm-recovery " + installID + " " + string(decoded.BoardID) + " " + string(decoded.Incarnation) + " " + string(decoded.PreviewHash)
	for _, tc := range []struct {
		name, line string
		now        time.Time
		preview    []byte
		want       int
	}{{"correct", expected + "\n", now.Add(time.Second), p, 4}, {"eof", expected, now.Add(time.Second), p, 2}, {"expired", expected + "\n", now.Add(5 * time.Minute), p, 2}, {"tamper", expected + "\n", now.Add(time.Second), bytes.Replace(p, []byte("cb3b9"), []byte("ab3b9"), 1), 2}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			r := runnerFake(f)
			r.now = func() time.Time { return tc.now }
			var out bytes.Buffer
			args := append(ordinary(), "recovery", "confirm", "--input", inputFile(t, c), "--preview", inputFile(t, tc.preview))
			exit := r.Run(context.Background(), args, strings.NewReader(tc.line), &out, io.Discard)
			if exit != tc.want {
				t.Fatalf("exit %d", exit)
			}
			if f.requests != 0 || f.dispatches > 1 || (f.dispatches == 1) != (tc.want == 4) {
				t.Fatal("refreshed preview or gate bypass")
			}
		})
	}
}

type leaseFake struct {
	ctx                                  setup.ConnectionContext
	meta                                 setup.CredentialMetadata
	events                               *[]string
	disabled                             bool
	failDisable, failPublish, failRemove bool
}

func (l *leaseFake) Context() setup.ConnectionContext { return l.ctx }
func (l *leaseFake) Close()                           {}
func (l *leaseFake) Disable() error {
	*l.events = append(*l.events, "disable")
	if l.failDisable {
		return &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
	}
	l.disabled = true
	return nil
}
func (l *leaseFake) Publish(s T.CredentialSecret) (setup.CredentialMetadata, error) {
	*l.events = append(*l.events, "publish")
	if !l.disabled || l.failPublish {
		return setup.CredentialMetadata{}, errors.New("synthetic publication failed")
	}
	l.meta = metadataFromSecret(s)
	l.disabled = false
	return l.meta, nil
}
func (l *leaseFake) Remove() error {
	*l.events = append(*l.events, "remove")
	if l.failRemove {
		return errors.New("synthetic removal failed")
	}
	return nil
}
func (l *leaseFake) LoadForRevocation() (credentialMaterial, error) {
	*l.events = append(*l.events, "load")
	if l.disabled {
		return nil, &core.Failure{Code: "CREDENTIAL_DISABLED", Exit: 2}
	}
	return &materialFake{l: l}, nil
}

type materialFake struct{ l *leaseFake }

func (m *materialFake) Context() setup.ConnectionContext   { return m.l.ctx }
func (m *materialFake) Metadata() setup.CredentialMetadata { return m.l.meta }
func (m *materialFake) WithToken(f func(T.SecretToken) error) error {
	return f(T.SecretToken(strings.Repeat("A", 43)))
}
func (*materialFake) Close() {}
func secretFixture(t *testing.T) T.CredentialSecret {
	t.Helper()
	s, e := T.Decode[T.CredentialSecret](fixtureBytes(t, "transport-envelopes/credential-secret.json"))
	if e != nil {
		t.Fatal(e)
	}
	s.InstallationID = T.UUIDv4(installID)
	s.BoardID = A.UUID(boardID)
	s.Incarnation = A.UUID(incID)
	s.GrantID = A.UUID(grantID)
	s.ActorID = A.UUID(actorID)
	return s
}

func metadataFromSecret(s T.CredentialSecret) setup.CredentialMetadata {
	i, _ := time.Parse("2006-01-02T15:04:05Z", string(s.IssuedAt))
	e, _ := time.Parse("2006-01-02T15:04:05Z", string(s.ExpiresAt))
	return setup.CredentialMetadata{Selected: setup.CredentialSelection{InstallationID: string(s.InstallationID), BoardID: string(s.BoardID), Incarnation: string(s.Incarnation), GrantID: string(s.GrantID)}, ActorID: string(s.ActorID), Kind: string(s.Kind), CredentialID: string(s.CredentialID), Rights: append([]string(nil), s.Rights...), IssuedAt: i, ExpiresAt: e}
}

func TestCredentialIssueDisablesBeforeDispatchAndNeverRetries(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		disable, publish, lost bool
		want                   int
	}{{"success", false, false, false, 0}, {"marker failure", true, false, false, 1}, {"publication loss", false, true, false, 4}, {"response loss", false, false, true, 4}} {
		t.Run(tc.name, func(t *testing.T) {
			var events []string
			f := newFake()
			l := &leaseFake{ctx: setup.ConnectionContext{InstallationID: installID, Origin: f.ctx.Origin}, events: &events, failDisable: tc.disable, failPublish: tc.publish}
			secret := secretFixture(t)
			f.issue = func(_ T.CredentialIssueRequest, consume func(T.CredentialSecret) error) error {
				events = append(events, "request")
				if !l.disabled {
					t.Fatal("request before marker")
				}
				if tc.lost {
					return &core.Failure{Code: "OUTCOME_UNKNOWN", Exit: 4}
				}
				if consume(secret) != nil {
					return &core.Failure{Code: "OUTCOME_UNKNOWN", Exit: 4}
				}
				return nil
			}
			r := runnerFake(f)
			r.prepare = func(setup.CredentialSelection) (credentialLease, error) {
				events = append(events, "prepare")
				return l, nil
			}
			var out, diag bytes.Buffer
			exit := r.Run(context.Background(), special("credential", "issue"), strings.NewReader("issue-cli "+installID+" "+boardID+" "+incID+" "+grantID+"\n"), &out, &diag)
			if exit != tc.want || f.issues > 1 {
				t.Fatalf("exit %d requests %d", exit, f.issues)
			}
			if tc.disable && f.issues != 0 {
				t.Fatal("marker failure dispatched")
			}
			if tc.want == 4 && !l.disabled {
				t.Fatal("unknown reopened local credential")
			}
			if strings.Contains(out.String(), string(secret.Token)) || strings.Contains(diag.String(), string(secret.Token)) {
				t.Fatal("secret output")
			}
		})
	}
}

func TestCredentialSelfRevokeMarkerAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lost, remove bool
		want         int
	}{{"confirmed", false, false, 0}, {"lost", true, false, 4}, {"remove failure", false, true, 4}} {
		t.Run(tc.name, func(t *testing.T) {
			var events []string
			f := newFake()
			l := &leaseFake{ctx: setup.ConnectionContext{InstallationID: installID, Origin: f.ctx.Origin}, meta: metadataFromSecret(secretFixture(t)), events: &events, failRemove: tc.remove}
			f.revoke = func() error {
				events = append(events, "request")
				if !l.disabled {
					t.Fatal("revoke before marker")
				}
				if tc.lost {
					return &core.Failure{Code: "OUTCOME_UNKNOWN", Exit: 4}
				}
				return nil
			}
			r := runnerFake(f)
			r.prepare = func(setup.CredentialSelection) (credentialLease, error) { return l, nil }
			var out bytes.Buffer
			exit := r.Run(context.Background(), special("credential", "revoke"), nil, &out, io.Discard)
			if exit != tc.want || f.revokes != 1 || !l.disabled {
				t.Fatal("self revoke sequence")
			}
			if tc.lost && strings.Contains(strings.Join(events, ","), "remove") {
				t.Fatal("unknown removed credential")
			}
		})
	}
}

func TestAlreadyDisabledRevokeRetainsOriginalUncertaintyWithoutSession(t *testing.T) {
	var events []string
	f := newFake()
	l := &leaseFake{ctx: setup.ConnectionContext{InstallationID: installID}, meta: metadataFromSecret(secretFixture(t)), events: &events, disabled: true}
	r := runnerFake(f)
	opens := 0
	r.open = func(context.Context, core.Selectors, bool) (adminClient, error) { opens++; return f, nil }
	r.prepare = func(setup.CredentialSelection) (credentialLease, error) { return l, nil }
	var out bytes.Buffer
	if r.Run(context.Background(), special("credential", "revoke"), nil, &out, io.Discard) != 4 || opens != 0 || f.revokes != 0 {
		t.Fatal("disabled revocation reused credential or lost uncertainty")
	}
	v, e := T.Decode[T.CredentialCLI](out.Bytes())
	if e != nil || v.Outcome != "unknown" || v.Result != nil {
		t.Fatal("disabled revocation claimed verified facts")
	}
}

type consoleFake struct {
	codes []string
	fail  bool
}

func (c *consoleFake) Present(s T.SecretCode) error {
	c.codes = append(c.codes, string(s))
	if c.fail {
		return errors.New("synthetic partial console write")
	}
	return nil
}
func (*consoleFake) Close() error { return nil }
func TestCodePrivatePresentationIsolationAndUnknown(t *testing.T) {
	secret, e := T.Decode[T.CodeSecret](fixtureBytes(t, "transport-envelopes/code-secret.json"))
	if e != nil {
		t.Fatal(e)
	}
	secret.InstallationID = T.UUIDv4(installID)
	secret.BoardID = A.UUID(boardID)
	secret.Incarnation = A.UUID(incID)
	secret.GrantID = A.UUID(grantID)
	for _, tc := range []struct {
		name                               string
		sinkFail, openFail, terminal, lost bool
		line                               string
		want                               int
	}{{"presented", false, false, true, false, "exact", 0}, {"partial", true, false, true, false, "exact", 4}, {"preflight", false, true, true, false, "exact", 1}, {"nonterminal", false, false, false, false, "exact", 2}, {"eof", false, false, true, false, "", 2}, {"lost", false, false, true, true, "exact", 4}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			sink := &consoleFake{fail: tc.sinkFail}
			f.code = func(_ T.CodeRequest, consume func(T.CodeSecret) error) error {
				if tc.lost {
					return &core.Failure{Code: "OUTCOME_UNKNOWN", Exit: 4}
				}
				if consume(secret) != nil {
					return &core.Failure{Code: "OUTCOME_UNKNOWN", Exit: 4}
				}
				return nil
			}
			r := runnerFake(f)
			r.terminal = func(io.Reader) bool { return tc.terminal }
			issued, _ := time.Parse("2006-01-02T15:04:05Z", string(secret.IssuedAt))
			r.now = func() time.Time { return issued.Add(time.Second) }
			r.console = func(io.Reader) (codeConsole, error) {
				if tc.openFail {
					return nil, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
				}
				return sink, nil
			}
			line := ""
			if tc.line == "exact" {
				line = "issue-code " + installID + " " + boardID + " " + incID + " " + grantID + "\n"
			}
			var out, diag bytes.Buffer
			exit := r.Run(context.Background(), special("session", "code"), strings.NewReader(line), &out, &diag)
			if exit != tc.want || f.codes > 1 || len(sink.codes) > 1 {
				t.Fatalf("exit %d", exit)
			}
			if (tc.openFail || !tc.terminal || tc.line == "") && f.codes != 0 {
				t.Fatal("code issued before gate")
			}
			if strings.Contains(out.String(), string(secret.Code)) || strings.Contains(diag.String(), string(secret.Code)) {
				t.Fatal("code leaked outside private sink")
			}
			if tc.want == 0 {
				v, e := T.Decode[T.SessionCodeCLI](out.Bytes())
				if e != nil || v.Outcome != "presented" || !v.Result.Presented {
					t.Fatal("invalid secret-free presentation status")
				}
			}
		})
	}
}

func TestCodeExpiredCanceledAndRedirectedOutput(t *testing.T) {
	secret, e := T.Decode[T.CodeSecret](fixtureBytes(t, "transport-envelopes/code-secret.json"))
	if e != nil {
		t.Fatal(e)
	}
	secret.InstallationID = T.UUIDv4(installID)
	secret.BoardID = A.UUID(boardID)
	secret.Incarnation = A.UUID(incID)
	secret.GrantID = A.UUID(grantID)
	issued, _ := time.Parse("2006-01-02T15:04:05Z", string(secret.IssuedAt))
	line := "issue-code " + installID + " " + boardID + " " + incID + " " + grantID + "\n"
	for _, tc := range []struct {
		name    string
		offset  time.Duration
		cancel  bool
		want    int
		present int
	}{{"expired", time.Minute, false, 4, 0}, {"future", -time.Second, false, 4, 0}, {"canceled", time.Second, true, 2, 0}, {"redirect", time.Second, false, 0, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			sink := &consoleFake{}
			f.code = func(_ T.CodeRequest, consume func(T.CodeSecret) error) error {
				if consume(secret) != nil {
					return &core.Failure{Code: "OUTCOME_UNKNOWN", Exit: 4}
				}
				return nil
			}
			r := runnerFake(f)
			r.now = func() time.Time { return issued.Add(tc.offset) }
			r.console = func(io.Reader) (codeConsole, error) { return sink, nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			path := filepath.Join(t.TempDir(), "redirected-output.json")
			stdout, e := os.Create(path)
			if e != nil {
				t.Fatal("synthetic redirected output unavailable")
			}
			var stderr bytes.Buffer
			exit := r.Run(ctx, special("session", "code"), strings.NewReader(line), stdout, &stderr)
			stdout.Close()
			if exit != tc.want || len(sink.codes) != tc.present || f.codes > 1 {
				t.Fatalf("exit %d presentations %d", exit, len(sink.codes))
			}
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal("redirect readback unavailable")
			}
			if bytes.Contains(b, []byte(string(secret.Code))) || bytes.Contains(stderr.Bytes(), []byte(string(secret.Code))) {
				t.Fatal("private code reached redirected public output")
			}
			if tc.cancel && f.codes != 0 {
				t.Fatal("canceled code dispatched")
			}
		})
	}
}
