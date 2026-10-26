package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/setup"
)

type observedInput struct {
	source io.Reader
	reads  int
}

func (r *observedInput) Read(p []byte) (int, error) { r.reads++; return r.source.Read(p) }

// promptOutput models legal and broken writers without repairing short writes.
// Its first call is the required display; subsequent calls may be diagnostics.
type promptOutput struct {
	mode  string
	first string
	calls int
	after func()
}

func (w *promptOutput) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		w.first = string(p)
	}
	n := len(p)
	var err error
	switch w.mode {
	case "zero":
		n = 0
	case "partial":
		n = len(p) / 2
	case "partial_error":
		n = len(p) / 2
		err = errors.New("synthetic display unavailable")
	case "full_error":
		err = errors.New("synthetic display unavailable")
	case "rune_count":
		n = len([]rune(string(p)))
	}
	if w.after != nil {
		w.after()
	}
	return n, err
}

func TestConfirmationRequiresCompleteByteDisplayBeforeRead(t *testing.T) {
	// Include both multibyte Unicode and inert escaping. Character counts and
	// escape-normalized lengths cannot substitute for the exact bytes displayed.
	prompt := "Original losses " + strconv.Quote("é界\x1b[31m\n")
	expected := "confirm-recovery " + installID + " " + boardID + " " + incID + " " + strings.Repeat("a", 64)
	display := prompt + "\nType " + expected + "\n"
	for _, mode := range []string{"zero", "partial", "partial_error", "full_error", "rune_count"} {
		t.Run(mode, func(t *testing.T) {
			input := &observedInput{source: strings.NewReader(expected + "\n")}
			writer := &promptOutput{mode: mode}
			c, code := (Runner{terminal: func(io.Reader) bool { return true }}).confirmation(context.Background(), input, writer, prompt, expected)
			if code != "CONFIRMATION_REFUSED" || c.Terminal || input.reads != 0 || writer.calls != 1 || writer.first != display {
				t.Fatal("incomplete display consumed input or authorized confirmation")
			}
			if strings.ContainsRune(writer.first, '\x1b') {
				t.Fatal("inert escaped display became active terminal control")
			}
		})
	}
	for _, tc := range []struct {
		name, line string
		cancel     bool
		want       string
		reads      bool
	}{{"complete", expected + "\r\n", false, "", true}, {"eof", "", false, "CONFIRMATION_REFUSED", true}, {"cancel_after_display", expected + "\n", true, "CONFIRMATION_REFUSED", false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := &promptOutput{}
			if tc.cancel {
				writer.after = cancel
			}
			input := &observedInput{source: strings.NewReader(tc.line)}
			c, code := (Runner{terminal: func(io.Reader) bool { return true }}).confirmation(ctx, input, writer, prompt, expected)
			if code != tc.want || c.Terminal != (tc.want == "") || (input.reads > 0) != tc.reads || writer.first != display || writer.calls != 1 {
				t.Fatal("complete-display confirmation behavior changed")
			}
		})
	}
}

type effectEvidence struct{ calls int }

func (s *effectEvidence) Save(evidence.Record) (string, error) {
	s.calls++
	return "", errors.New("unexpected evidence save")
}
func (s *effectEvidence) Load(string) (evidence.Record, error) {
	s.calls++
	return evidence.Record{}, errors.New("unexpected evidence read")
}
func (s *effectEvidence) Resolve(evidence.Record, T.Terminal) error {
	s.calls++
	return errors.New("unexpected evidence resolution")
}

func TestIncompleteRequiredDisplaysFenceEveryConfirmedCommand(t *testing.T) {
	human := []byte(`{"boardId":"` + boardID + `","incarnation":"` + incID + `","opId":"00000000-0000-4000-8000-0000000000d0","target":null,"expectedRevision":5,"expectedCardRevision":null,"input":{"granteeActorId":"00000000-0000-4000-8000-00000000005c","kind":"human","rights":["read"]}}`)
	preview := fixtureBytes(t, "archive-recovery/preview-clear.json")
	confirmation := fixtureBytes(t, "archive-recovery/confirm-clear.json")
	original, e := A.Decode[A.Preview](preview)
	if e != nil {
		t.Fatal("synthetic original preview unavailable")
	}
	for _, mode := range []string{"zero", "partial", "partial_error", "full_error"} {
		for _, command := range []string{"trust_enroll", "trust_remove", "owner_renew", "native_human_grant", "ordinary_human_grant", "recovery_confirm", "credential_issue", "session_code"} {
			t.Run(mode+"/"+command, func(t *testing.T) {
				f := newFake()
				r := runnerFake(f)
				native := &setupFake{}
				r.Setup = native
				proof := &effectEvidence{}
				r.Evidence = proof
				prepareCalls := 0
				r.prepare = func(setup.CredentialSelection) (credentialLease, error) {
					prepareCalls++
					return nil, errors.New("unexpected credential preparation")
				}
				sink := &consoleFake{}
				r.console = func(io.Reader) (codeConsole, error) { return sink, nil }
				r.now = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 1, 0, time.UTC) }
				var args []string
				var challenge string
				switch command {
				case "trust_enroll", "trust_remove":
					action := "enroll"
					if command == "trust_remove" {
						action = "remove"
					}
					args = []string{"--json", "setup", "trust", action, "--installation", testID, "--issuer", testPin}
					challenge = action + " " + testID + " " + testPin
				case "owner_renew":
					args = []string{"--json", "setup", "owner", "renew", "--installation", testID, "--issuer", testPin}
					challenge = "renew-owner " + testID + " " + testPin
				case "native_human_grant":
					args = []string{"--json", "--board", boardID, "grant", "create", "--input", inputFile(t, human), "--native-owner"}
					challenge = "enroll-human " + installID + " " + boardID + " " + incID + " 00000000-0000-4000-8000-00000000005c read"
				case "ordinary_human_grant":
					args = append(ordinary(), "grant", "create", "--input", inputFile(t, human))
					challenge = "enroll-human " + installID + " " + boardID + " " + incID + " 00000000-0000-4000-8000-00000000005c read"
				case "recovery_confirm":
					args = append(ordinary(), "recovery", "confirm", "--input", inputFile(t, confirmation), "--preview", inputFile(t, preview))
					challenge = "confirm-recovery " + installID + " " + boardID + " " + incID + " " + string(original.PreviewHash)
				case "credential_issue":
					args = special("credential", "issue")
					challenge = "issue-cli " + installID + " " + boardID + " " + incID + " " + grantID
				case "session_code":
					args = special("session", "code")
					challenge = "issue-code " + installID + " " + boardID + " " + incID + " " + grantID
				}
				writer := &promptOutput{mode: mode}
				input := &observedInput{source: strings.NewReader(challenge + "\n")}
				var out bytes.Buffer
				if exit := r.Run(context.Background(), args, input, &out, writer); exit != 2 {
					t.Fatalf("incomplete required display exit %d", exit)
				}
				if writer.first == "" || !strings.Contains(writer.first, "\nType ") || input.reads != 0 || native.effects != 0 || prepareCalls != 0 || proof.calls != 0 || f.dispatches != 0 || f.requests != 0 || f.issues != 0 || f.codes != 0 || f.revokes != 0 || len(sink.codes) != 0 {
					t.Fatal("incomplete required display reached input, evidence, publication or mutation effects")
				}
			})
		}
	}
}
