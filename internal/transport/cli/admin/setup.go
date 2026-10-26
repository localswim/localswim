// Package admin adapts explicit administrative commands without executable wiring.
package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/setup"
)

// SetupOperations is implemented by the native setup manager. There is no
// implicit initialization, trust enrollment or alternate installation root.
type SetupOperations interface {
	Status() setup.Response
	Init(*int) setup.Response
	RenewCertificate(string, string) setup.Response
	RenewOwner(string, string, setup.Confirmation) setup.Response
	PlanTrust(string, string, string) (setup.TrustPlan, error)
	ApplyTrust(setup.TrustPlan, setup.Confirmation) setup.Response
}

type setupInvocation struct {
	command, installation, issuer string
	port                          *int
	json                          bool
}

var canonicalV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var canonicalDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func parseSetup(args []string) (q setupInvocation, ok bool) {
	// Preserve machine output even when a preceding invalid flag stops parsing.
	for _, arg := range args {
		if arg == "--json" {
			q.json = true
		}
	}
	seen := map[string]bool{}
	var words []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			words = append(words, arg)
			continue
		}
		if seen[arg] {
			return q, false
		}
		seen[arg] = true
		if arg == "--json" {
			q.json = true
			continue
		}
		if arg != "--port" && arg != "--installation" && arg != "--issuer" || i+1 == len(args) {
			return q, false
		}
		i++
		value := args[i]
		switch arg {
		case "--installation":
			q.installation = value
		case "--issuer":
			q.issuer = value
		case "--port":
			if value == "" || value[0] == '0' {
				return q, false
			}
			for _, c := range value {
				if c < '0' || c > '9' {
					return q, false
				}
			}
			n, e := strconv.Atoi(value)
			if e != nil || n < 1024 || n > 65535 {
				return q, false
			}
			q.port = &n
		}
	}
	if len(words) < 2 || words[0] != "setup" {
		return q, false
	}
	switch strings.Join(words[1:], " ") {
	case "init":
		q.command = "init"
	case "status":
		q.command = "status"
	case "certificate renew":
		q.command = "certificate.renew"
	case "owner renew":
		q.command = "owner.renew"
	case "trust enroll":
		q.command = "trust.enroll"
	case "trust remove":
		q.command = "trust.remove"
	default:
		return q, false
	}
	if q.command == "init" || q.command == "status" {
		return q, q.installation == "" && q.issuer == "" && (q.command == "init" || q.port == nil)
	}
	return q, q.port == nil && canonicalV4.MatchString(q.installation) && canonicalDigest.MatchString(q.issuer)
}

// Runner must be composed explicitly with the native manager. Its terminal
// recognition accepts only an actual console file handle, never an input flag.
type Runner struct {
	Setup    SetupOperations
	Provider core.Provider
	Owner    core.NativeCurrentProvider
	Manager  *setup.Manager
	Evidence evidence.Store
	terminal func(io.Reader) bool
	open     func(context.Context, core.Selectors, bool) (adminClient, error)
	prepare  func(setup.CredentialSelection) (credentialLease, error)
	console  func(io.Reader) (codeConsole, error)
	now      func() time.Time
}

func (r Runner) confirmation(ctx context.Context, stdin io.Reader, stderr io.Writer, prompt, expected string) (setup.Confirmation, string) {
	check := r.terminal
	if check == nil {
		check = consoleInput
	}
	if ctx == nil || ctx.Err() != nil {
		return setup.Confirmation{}, "CONFIRMATION_REFUSED"
	}
	if !check(stdin) {
		return setup.Confirmation{}, "CONFIRMATION_REQUIRED"
	}
	if stderr == nil {
		return setup.Confirmation{}, "CONFIRMATION_REFUSED"
	}
	// Required scope/loss information and the complete challenge form one display.
	// A short writer may report no error; accepting input after that would turn an
	// unseen consequence into consent. Never retry a partial prompt or read ahead.
	display := prompt + "\nType " + expected + "\n"
	if n, e := io.WriteString(stderr, display); e != nil || n != len(display) || ctx.Err() != nil {
		return setup.Confirmation{}, "CONFIRMATION_REFUSED"
	}
	line, e := bufio.NewReader(io.LimitReader(stdin, 2049)).ReadString('\n')
	if e != nil || len(line) > 2048 || ctx.Err() != nil {
		return setup.Confirmation{}, "CONFIRMATION_REFUSED"
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line != expected {
		return setup.Confirmation{}, "CONFIRMATION_REFUSED"
	}
	return setup.Confirmation{Terminal: true, Line: line}, ""
}

func setupError(command *string, code string, exit int) setup.Response {
	return setup.Response{Envelope: setup.Envelope{APIVersion: 1, Scope: "setup", Command: command, Outcome: map[int]string{1: "failed", 2: "refused", 3: "unavailable", 4: "unknown"}[exit], Error: &setup.Diagnostic{Code: code, Message: code}}, Exit: exit}
}

func setupFailure(command string, err error) setup.Response {
	f, ok := err.(*setup.Failure)
	if !ok || f.Exit < 1 || f.Exit > 4 {
		return setupError(&command, "STATUS_UNAVAILABLE", 3)
	}
	// Only the native provider's fixed categories are rendered, never OS errors.
	switch f.Code {
	case "INVOCATION_OR_IO", "CONFIG_CONFLICT", "IDENTITY_MISMATCH", "UNSAFE_LOCAL_MATERIAL", "UNSUPPORTED_VERSION", "CONFIRMATION_REQUIRED", "CONFIRMATION_REFUSED", "ISSUER_REPLACEMENT_REQUIRED", "INSTALLATION_BUSY", "PORT_UNAVAILABLE", "PLATFORM_UNQUALIFIED", "TRUST_STORE_UNAVAILABLE", "STATUS_UNAVAILABLE", "SETUP_OUTCOME_UNKNOWN", "SETUP_RECOVERY_REQUIRED":
		return setupError(&command, f.Code, f.Exit)
	}
	return setupError(&command, "STATUS_UNAVAILABLE", 3)
}

func setupOutputFailure(command string, exit int) int {
	if command != "status" && (exit == 0 || exit == 4) {
		return 4
	}
	return 1
}

// RunSetup implements only D01's exact setup grammar. It never starts a server.
func (r Runner) RunSetup(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	q, ok := parseSetup(args)
	out := setupError(nil, "INVOCATION_OR_IO", 1)
	if ok {
		out = setupError(&q.command, "STATUS_UNAVAILABLE", 3)
		if r.Setup != nil && ctx != nil && ctx.Err() == nil {
			out = r.executeSetup(ctx, q, stdin, stderr)
		}
	}
	b, err := json.Marshal(out.Envelope)
	if err != nil || stdout == nil {
		return setupOutputFailure(q.command, out.Exit)
	}
	b = append(b, '\n')
	if !q.json {
		if _, err = fmt.Fprintf(stdout, "setup %s: %s\n", q.command, out.Envelope.Outcome); err != nil {
			return setupOutputFailure(q.command, out.Exit)
		}
	}
	if n, e := stdout.Write(b); e != nil || n != len(b) {
		return setupOutputFailure(q.command, out.Exit)
	}
	if out.Envelope.Error != nil && stderr != nil {
		_, _ = fmt.Fprintln(stderr, out.Envelope.Error.Code)
	}
	return out.Exit
}

func (r Runner) executeSetup(ctx context.Context, q setupInvocation, stdin io.Reader, stderr io.Writer) setup.Response {
	switch q.command {
	case "init":
		return r.Setup.Init(q.port)
	case "status":
		return r.Setup.Status()
	case "certificate.renew":
		return r.Setup.RenewCertificate(q.installation, q.issuer)
	case "owner.renew":
		observed := r.Setup.Status()
		v := observed.Envelope.Result
		if observed.Exit != 0 || v == nil || v.InstallationID == nil || v.IssuerSHA256 == nil || *v.InstallationID != q.installation || *v.IssuerSHA256 != q.issuer {
			return setupError(&q.command, "IDENTITY_MISMATCH", 2)
		}
		origin, expiry := "unavailable", "unavailable"
		if v.Origin != nil {
			origin = *v.Origin
		}
		if v.OwnerExpiresAt != nil {
			expiry = *v.OwnerExpiresAt
		}
		c, code := r.confirmation(ctx, stdin, stderr, "Installation "+q.installation+" origin "+origin+" issuer "+q.issuer+" owner expiry "+expiry+". Old native owner bearer will cease working.", "renew-owner "+q.installation+" "+q.issuer)
		if code != "" {
			return setupError(&q.command, code, 2)
		}
		return r.Setup.RenewOwner(q.installation, q.issuer, c)
	case "trust.enroll", "trust.remove":
		operation := strings.TrimPrefix(q.command, "trust.")
		plan, err := r.Setup.PlanTrust(operation, q.installation, q.issuer)
		if err != nil {
			return setupFailure(q.command, err)
		}
		prompt := "Installation " + plan.InstallationID + " authority " + plan.Authority + " issuer " + plan.IssuerSHA256 + " expiry " + plan.ExpiresAt + " store " + plan.StoreScope + ". " + plan.Consequence
		c, code := r.confirmation(ctx, stdin, stderr, prompt, operation+" "+q.installation+" "+q.issuer)
		if code != "" {
			return setupError(&q.command, code, 2)
		}
		return r.Setup.ApplyTrust(plan, c)
	}
	return setupError(nil, "INVOCATION_OR_IO", 1)
}

// Keep os in this file's API boundary: only *os.File may be a production console.
func consoleFile(r io.Reader) (*os.File, bool) { f, ok := r.(*os.File); return f, ok && f != nil }
