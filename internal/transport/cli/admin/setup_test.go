package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/transport/setup"
)

const testID = "00000000-0000-4000-8000-000000000001"

var testPin = strings.Repeat("a", 64)

type setupFake struct {
	called  string
	confirm setup.Confirmation
	effects int
}

func (s *setupFake) result(command string) setup.Response {
	s.called = command
	return setup.Response{Envelope: setup.Envelope{APIVersion: 1, Scope: "setup", Command: &command, Outcome: "confirmed"}}
}
func (s *setupFake) Status() setup.Response {
	q := s.result("status")
	origin := "https://example.localhost:47443"
	expiry := "2026-12-01T00:00:00Z"
	id, pin := testID, testPin
	q.Envelope.Result = &setup.Result{InstallationID: &id, IssuerSHA256: &pin, Origin: &origin, OwnerExpiresAt: &expiry}
	return q
}
func (s *setupFake) Init(*int) setup.Response { return s.result("init") }
func (s *setupFake) RenewCertificate(string, string) setup.Response {
	return s.result("certificate.renew")
}
func (s *setupFake) RenewOwner(_, _ string, c setup.Confirmation) setup.Response {
	s.effects++
	s.confirm = c
	return s.result("owner.renew")
}
func (s *setupFake) PlanTrust(op, id, pin string) (setup.TrustPlan, error) {
	s.called = "plan"
	return setup.TrustPlan{InstallationID: id, IssuerSHA256: pin, Authority: "example.localhost:47443", ExpiresAt: "2030-01-01T00:00:00Z", StoreScope: "CurrentUser Root", Consequence: "Root trust is broad", ConfirmationLine: op + " " + id + " " + pin}, nil
}
func (s *setupFake) ApplyTrust(_ setup.TrustPlan, c setup.Confirmation) setup.Response {
	s.effects++
	s.confirm = c
	return s.result("trust")
}

func TestSetupGrammarExactBeforeEffects(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "init", "--yes"}, {"setup", "init", "--port", "047443"}, {"setup", "init", "--port", "+47443"}, {"setup", "status", "--port", "47443"},
		{"setup", "init", "--json", "--json"}, {"setup", "status", "operand"}, {"setup", "init", "--installation", testID}, {"setup", "owner", "renew", "--installation", testID, "--issuer", strings.ToUpper(testPin)},
	} {
		s := &setupFake{}
		var out, diag bytes.Buffer
		if exit := (Runner{Setup: s}).RunSetup(context.Background(), args, nil, &out, &diag); exit != 1 || s.called != "" {
			t.Fatalf("invalid invocation had effects: %v", args)
		}
		if strings.Contains(out.String(), strings.ToUpper(testPin)) {
			t.Fatal("echoed invalid input")
		}
	}
	q, ok := parseSetup([]string{"--port", "47443", "setup", "--json", "init"})
	if !ok || q.port == nil || *q.port != 47443 || !q.json {
		t.Fatal("allowed flag ordering")
	}
}

func TestSetupTerminalGateAndExactChallenge(t *testing.T) {
	args := []string{"--json", "setup", "trust", "enroll", "--installation", testID, "--issuer", testPin}
	for _, tc := range []struct {
		name, line string
		terminal   bool
		exit       int
	}{
		{"nonterminal", "enroll " + testID + " " + testPin + "\n", false, 2},
		{"wrong", "enroll " + testID + " " + testPin + " \n", true, 2},
		{"eof", "enroll " + testID + " " + testPin, true, 2},
		{"correct", "enroll " + testID + " " + testPin + "\r\n", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &setupFake{}
			var out, diag bytes.Buffer
			r := Runner{Setup: s, terminal: func(io.Reader) bool { return tc.terminal }}
			if exit := r.RunSetup(context.Background(), args, strings.NewReader(tc.line), &out, &diag); exit != tc.exit {
				t.Fatalf("exit %d", exit)
			}
			if (s.called == "trust") != (tc.exit == 0) {
				t.Fatal("trust gate bypass")
			}
			var e setup.Envelope
			if json.Unmarshal(out.Bytes(), &e) != nil || bytes.Count(out.Bytes(), []byte("\n")) != 1 {
				t.Fatal("not exactly one envelope")
			}
			if tc.terminal && !strings.Contains(diag.String(), "Root trust is broad") {
				t.Fatal("consequence not displayed")
			}
		})
	}
	// A reader with a matching line never grants production terminal consent.
	s := &setupFake{}
	var out, diag bytes.Buffer
	if exit := (Runner{Setup: s}).RunSetup(context.Background(), args, strings.NewReader("enroll "+testID+" "+testPin+"\n"), &out, &diag); exit != 2 || s.called == "trust" {
		t.Fatal("reader asserted production terminal")
	}
}

func TestOwnerRenewRequiresExplicitIdentityAndWarning(t *testing.T) {
	s := &setupFake{}
	var out, diag bytes.Buffer
	r := Runner{Setup: s, terminal: func(io.Reader) bool { return true }}
	args := []string{"--json", "setup", "owner", "renew", "--installation", testID, "--issuer", testPin}
	if r.RunSetup(context.Background(), args, strings.NewReader("renew-owner "+testID+" "+testPin+"\n"), &out, &diag) != 0 || !s.confirm.Terminal {
		t.Fatal("owner confirmation failed")
	}
	if !strings.Contains(diag.String(), "Old native owner bearer will cease working") {
		t.Fatal("missing owner warning")
	}
	args[len(args)-1] = strings.Repeat("b", 64)
	s.called = ""
	out.Reset()
	diag.Reset()
	if r.RunSetup(context.Background(), args, strings.NewReader(""), &out, &diag) != 2 || s.called == "owner.renew" {
		t.Fatal("mismatched identity renewed")
	}
}

func TestSetupCanceledNeverMutates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &setupFake{}
	var out bytes.Buffer
	if (Runner{Setup: s}).RunSetup(ctx, []string{"--json", "setup", "init"}, nil, &out, io.Discard) != 3 || s.called != "" {
		t.Fatal("canceled invocation mutated")
	}
}

type failedOutput struct{}

func (failedOutput) Write([]byte) (int, error) { return 0, errors.New("synthetic output unavailable") }

func TestSetupOutputFailurePreservesPossibleEffect(t *testing.T) {
	for _, tc := range []struct {
		command string
		exit    int
	}{{"init", 4}, {"status", 1}} {
		s := &setupFake{}
		if exit := (Runner{Setup: s}).RunSetup(context.Background(), []string{"--json", "setup", tc.command}, nil, failedOutput{}, io.Discard); exit != tc.exit {
			t.Fatalf("%s output loss exit %d", tc.command, exit)
		}
	}
}
