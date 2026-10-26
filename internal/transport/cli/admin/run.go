package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/setup"
)

type adminClient interface {
	Context() evidence.Context
	Metadata() core.CredentialMetadata
	Close()
	Request(context.Context, string, string, io.Reader, string, string, int64) (int, http.Header, []byte, error)
	Dispatch(context.Context, evidence.Store, contract.ResolvedRoute, []byte, io.Writer) core.Outcome
	IssueCredential(context.Context, T.CredentialIssueRequest, func(T.CredentialSecret) error) error
	IssueCode(context.Context, T.CodeRequest, func(T.CodeSecret) error) error
	RevokeCredential(context.Context, func() error) error
}
type invocation struct {
	selected                                         core.Selectors
	json, native                                     bool
	kind, input, preview, target, incarnation, grant string
}

func parse(args []string) (q invocation, err error) {
	p, err := core.ParsePrefix(args)
	q.selected = p.Selected
	q.json = p.JSON
	if err != nil {
		return q, err
	}
	a := p.Args
	if len(a) < 2 {
		return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
	}
	q.kind = a[0] + " " + a[1]
	i := 2
	switch q.kind {
	case "grant create", "grant revoke", "credential issue", "credential revoke", "session code", "recovery preview", "recovery confirm":
	default:
		return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
	}
	if q.kind == "grant revoke" {
		if i >= len(a) || !schema.ValidUUID(a[i]) {
			return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
		}
		q.target = a[i]
		i++
	}
	seen := map[string]bool{}
	for i < len(a) {
		key := a[i]
		i++
		if seen[key] {
			return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
		}
		seen[key] = true
		if key == "--native-owner" && (q.kind == "grant create" || q.kind == "grant revoke") {
			q.native = true
			continue
		}
		if i >= len(a) {
			return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
		}
		value := a[i]
		i++
		switch {
		case key == "--input" && (strings.HasPrefix(q.kind, "grant ") || strings.HasPrefix(q.kind, "recovery ")):
			q.input = value
		case key == "--preview" && q.kind == "recovery confirm":
			q.preview = value
		case key == "--incarnation" && (strings.HasPrefix(q.kind, "credential ") || q.kind == "session code"):
			q.incarnation = value
		case key == "--grant" && (strings.HasPrefix(q.kind, "credential ") || q.kind == "session code"):
			q.grant = value
		default:
			return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
		}
	}
	special := strings.HasPrefix(q.kind, "credential ") || q.kind == "session code"
	if special {
		if err = core.ValidateSelectors(q.selected, true); err != nil {
			return q, err
		}
		if !schema.ValidUUID(q.incarnation) || !schema.ValidUUID(q.grant) {
			return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
		}
	} else {
		if err = core.ValidateSelectors(q.selected, q.native); err != nil {
			return q, err
		}
		if q.input == "" || q.kind == "recovery confirm" && q.preview == "" {
			return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
		}
		if q.kind == "recovery confirm" && q.input == "-" && q.preview == "-" {
			return q, &core.Failure{Code: "INVOCATION_OR_IO", Exit: 1}
		}
	}
	return q, nil
}
func failureClass(err error) (string, int) {
	var f *core.Failure
	var n *setup.Failure
	code, exit := "UNAVAILABLE", 3
	if errors.As(err, &f) {
		code, exit = f.Code, f.Exit
	} else if errors.As(err, &n) {
		code, exit = n.Code, n.Exit
	}
	if exit < 1 || exit > 4 {
		exit = 3
	}
	switch code {
	case "INVOCATION_OR_IO", "CONFIRMATION_REQUIRED", "CONFIRMATION_REFUSED", "UNAVAILABLE", "CREDENTIAL_SELECTION_REQUIRED", "CREDENTIAL_EXPIRED", "CREDENTIAL_DISABLED", "CREDENTIAL_CONTEXT_MISMATCH", "CREDENTIAL_UNAVAILABLE", "UNSAFE_LOCAL_MATERIAL", "INVALID_INPUT", "UNAUTHENTICATED", "FORBIDDEN", "NOT_FOUND", "INCARNATION_CONFLICT", "OUTCOME_UNKNOWN", "CREDENTIAL_OUTCOME_UNKNOWN":
		return code, exit
	}
	return "UNAVAILABLE", exit
}
func (r Runner) client(ctx context.Context, s core.Selectors, native bool) (adminClient, error) {
	if r.open != nil {
		return r.open(ctx, s, native)
	}
	if native {
		return core.OpenCurrent(ctx, r.Owner)
	}
	return core.Open(ctx, r.Provider, s)
}
func readFile(path string, stdin io.Reader) ([]byte, error) {
	reader := stdin
	if path != "-" {
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		reader = f
	}
	if reader == nil {
		return nil, errors.New("input unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(reader, 800001))
	if e != nil || len(b) > 800000 {
		return nil, errors.New("input unavailable")
	}
	return b, nil
}
func (r Runner) clock() time.Time {
	if r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}

// Run executes only the selected administrative grammar. Ordinary lookups remain
// core.Runner commands; this adapter never retries or substitutes an identity.
func (r Runner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if setupCommand(args) {
		return r.RunSetup(ctx, args, stdin, stdout, stderr)
	}
	q, err := parse(args)
	var value any
	exit := 1
	mutating := false
	if err != nil {
		code, x := failureClass(err)
		value = core.Refused(code, x).Value()
		exit = x
	} else if strings.HasPrefix(q.kind, "credential ") {
		value, exit = r.credential(ctx, q, stdin, stderr)
		mutating = true
	} else if q.kind == "session code" {
		value, exit = r.code(ctx, q, stdin, stderr)
		mutating = true
	} else {
		value, exit = r.board(ctx, q, stdin, stderr)
		mutating = q.kind != "recovery preview"
	}
	b, e := T.Readable(value)
	if e != nil {
		b, _ = T.Readable(core.Refused("UNAVAILABLE", 3).Value())
		if mutating && (exit == 0 || exit == 4) {
			exit = 4
		} else {
			exit = 3
		}
	}
	if stdout == nil {
		if mutating && (exit == 0 || exit == 4) {
			return 4
		}
		return 1
	}
	if !q.json {
		label := map[int]string{0: "confirmed", 1: "failed", 2: "refused", 3: "unavailable", 4: "unknown"}[exit]
		if q.kind == "session code" && exit == 0 {
			label = "presented; login unverified"
		}
		if _, e = fmt.Fprintf(stdout, "%s: %s\n", q.kind, label); e != nil {
			if mutating && (exit == 0 || exit == 4) {
				return 4
			}
			return 1
		}
	}
	if n, e := stdout.Write(b); e != nil || n != len(b) {
		if mutating && (exit == 0 || exit == 4) {
			return 4
		}
		return 1
	}
	return exit
}

func setupCommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" {
			continue
		}
		if strings.HasPrefix(args[i], "--") {
			i++
			continue
		}
		return args[i] == "setup"
	}
	return false
}

func (r Runner) board(ctx context.Context, q invocation, stdin io.Reader, stderr io.Writer) (any, int) {
	wire, err := readFile(q.input, stdin)
	if err != nil {
		o := core.Refused("INVOCATION_OR_IO", 1)
		return o.Value(), o.Exit()
	}
	c, err := r.client(ctx, q.selected, q.native)
	if err != nil {
		code, exit := failureClass(err)
		o := core.Refused(code, exit)
		return o.Value(), o.Exit()
	}
	defer c.Close()
	refuse := func(code string, exit int) (any, int) { o := core.Refused(code, exit); return o.Value(), o.Exit() }
	if q.kind == "recovery preview" {
		p, e := A.Decode[A.PreviewRequest](wire)
		if e != nil || string(p.Incarnation) != q.selected.Incarnation {
			return refuse("INVALID_INPUT", 2)
		}
		status, _, body, e := c.Request(ctx, "POST", "/api/v1/boards/"+q.selected.BoardID+"/recovery/preview", bytes.NewReader(wire), "application/json", "application/json", 32*1024*1024)
		if e != nil {
			return refuse("UNAVAILABLE", 3)
		}
		if status != 200 {
			o := core.ReadFailure(status, body)
			return o.Value(), o.Exit()
		}
		v, e := A.Decode[A.Preview](body)
		if e != nil || string(v.BoardID) != q.selected.BoardID || string(v.Incarnation) != q.selected.Incarnation || string(v.ActorID) != c.Metadata().ActorID || v.ExpectedRevision != p.ExpectedRevision || v.Command != p.Command || !sameJSON(v.Input, p.Input) {
			return refuse("UNAVAILABLE", 3)
		}
		return v, 0
	}
	command := contract.Command(strings.ReplaceAll(q.kind, " ", "."))
	if q.kind == "recovery confirm" {
		preview, e := readFile(q.preview, stdin)
		if e != nil {
			return refuse("INVOCATION_OR_IO", 1)
		}
		p, confirmation, e := matchPreview(preview, wire, q.selected.BoardID, q.selected.Incarnation, c.Metadata().ActorID, r.clock())
		if e != nil {
			return refuse("INVALID_INPUT", 2)
		}
		shown, e := A.Readable(p)
		if e != nil {
			return refuse("INVALID_INPUT", 2)
		}
		expected := "confirm-recovery " + c.Context().InstallationID + " " + q.selected.BoardID + " " + q.selected.Incarnation + " " + string(p.PreviewHash)
		_, code := r.confirmation(ctx, stdin, stderr, "Original recovery losses, inventory, hash and expiry: "+strconv.Quote(string(shown)), expected)
		if code != "" {
			return refuse(code, 2)
		}
		command = contract.Command(confirmation.Command)
	} else {
		route := contract.ResolvedRoute{Command: command, BoardID: q.selected.BoardID, TargetID: q.target}
		intent, e := T.FreezeMutation(wire, route, contract.AuthenticatedActor{ActorID: c.Metadata().ActorID})
		if e != nil {
			return refuse("INVALID_INPUT", 2)
		}
		semantic, e := contract.DecodeJSON(intent.CanonicalBytes())
		if e != nil {
			return refuse("INVALID_INPUT", 2)
		}
		m := semantic.(map[string]any)
		if !q.native && m["incarnation"] != q.selected.Incarnation {
			return refuse("CREDENTIAL_CONTEXT_MISMATCH", 2)
		}
		if command == "grant.create" {
			payload := m["payload"].(map[string]any)
			kind := payload["kind"].(string)
			rights := payload["rights"].([]any)
			var names []string
			for _, v := range rights {
				names = append(names, v.(string))
			}
			if kind == "automation" {
				for _, right := range names {
					if right == "approve" || right == "history-admin" || right == "grant-admin" {
						return refuse("FORBIDDEN", 2)
					}
				}
			}
			if kind == "human" {
				actor := payload["granteeActorId"].(string)
				inc := m["incarnation"].(string)
				expected := "enroll-human " + c.Context().InstallationID + " " + q.selected.BoardID + " " + inc + " " + actor + " " + strings.Join(names, ",")
				prompt := "Operator " + strconv.Quote(c.Metadata().ActorID) + " installation " + strconv.Quote(c.Context().InstallationID) + " origin " + strconv.Quote(c.Context().Origin) + " board " + strconv.Quote(q.selected.BoardID) + " incarnation " + strconv.Quote(inc) + " grantee " + strconv.Quote(actor) + " kind human rights " + strconv.Quote(strings.Join(names, ","))
				_, code := r.confirmation(ctx, stdin, stderr, prompt, expected)
				if code != "" {
					return refuse(code, 2)
				}
			}
		}
	}
	o := c.Dispatch(ctx, r.Evidence, contract.ResolvedRoute{Command: command, BoardID: q.selected.BoardID, TargetID: q.target}, wire, stderr)
	return o.Value(), o.Exit()
}
func sameJSON(a, b []byte) bool {
	x, e := contract.DecodeJSON(a)
	y, f := contract.DecodeJSON(b)
	if e != nil || f != nil {
		return false
	}
	left, e := contract.Canonical(x)
	right, f := contract.Canonical(y)
	return e == nil && f == nil && bytes.Equal(left, right)
}
