package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"unicode"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

// Runner has no default credentials, evidence storage or implicit board source.
// Composition owns provider/storage lifetimes and the selected private root.
type Runner struct {
	Provider Provider
	Evidence evidence.Store
}

type result struct {
	value any
	exit  int
}

func refused(code string, exit int, original *evidence.Record) result {
	e := T.Error{Code: safeCode(code), Message: "Request unavailable or refused"}
	if original != nil && original.Valid() {
		i := original.Identity()
		return result{T.KnownIdentityUnknown{APIVersion: 1, Outcome: "unknown", BoardID: A.UUID(i.BoardID), Incarnation: A.UUID(i.Incarnation), OpID: A.UUID(i.OpID), ActorID: A.UUID(i.ActorID), FingerprintVersion: 1, Fingerprint: A.Digest(original.Fingerprint()), Error: e}, 4}
	}
	return result{T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: e}, exit}
}
func safeCode(code string) string {
	switch code {
	case "INVOCATION_OR_IO", "CREDENTIAL_SELECTION_REQUIRED", "CREDENTIAL_EXPIRED", "CREDENTIAL_DISABLED", "CREDENTIAL_CONTEXT_MISMATCH", "CREDENTIAL_UNAVAILABLE", "UNSAFE_LOCAL_MATERIAL", "UNAVAILABLE", "INVALID_INPUT", "INPUT_TOO_LARGE", "UNAUTHENTICATED", "FORBIDDEN", "NOT_FOUND", "CURSOR_STALE", "REVISION_CONFLICT", "INCARNATION_CONFLICT", "OPERATION_MISMATCH", "OUTCOME_UNKNOWN", "SOURCE_AUTHORITY_UNAVAILABLE", "CONFIRMATION_REQUIRED", "CONFIRMATION_REFUSED":
		return code
	}
	return "UNAVAILABLE"
}
func failure(err error) (string, int) {
	var f *Failure
	if errors.As(err, &f) {
		code := safeCode(f.Code)
		if f.Exit >= 1 && f.Exit <= 3 {
			return code, f.Exit
		}
	}
	return "CREDENTIAL_UNAVAILABLE", 3
}
func readInput(path string, stdin io.Reader) ([]byte, error) {
	var r io.Reader = stdin
	if path != "-" {
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		r = f
	}
	if r == nil {
		return nil, errors.New("input unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(r, 800001))
	if e != nil || len(b) > 800000 {
		return nil, errors.New("input unavailable")
	}
	return b, nil
}

// Run emits exactly one D03 envelope in JSON mode. Diagnostics are fixed inert
// categories on stderr. No received invalid bytes/paths/secrets are rendered.
// Output failure after a possible mutation cannot downgrade the outcome to IO.
func (r Runner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	q, code, exit := parse(args)
	var out result
	if code != "" {
		if q.lookup {
			exit = 4
		}
		out = refused(code, exit, nil)
	} else {
		out = r.execute(ctx, q, stdin, stderr)
	}
	b, e := T.Readable(out.value)
	if e != nil {
		out = refused("UNAVAILABLE", out.exit, nil)
		if out.exit == 0 {
			out.exit = 3
		}
		b, _ = T.Readable(out.value)
	}
	if stdout == nil {
		return outputFailure(out.exit, q)
	}
	if !q.json {
		label := "confirmed"
		if out.exit != 0 {
			label = map[int]string{1: "failed", 2: "refused", 3: "unavailable", 4: "unknown"}[out.exit]
		}
		board := "unavailable"
		if uuid(q.selected.BoardID) {
			board = q.selected.BoardID
		}
		if _, e = fmt.Fprintf(stdout, "%s board %s\n", label, board); e != nil {
			return outputFailure(out.exit, q)
		}
		if text, ok := focusedRead(out.value); out.exit == 0 && ok {
			if n, err := io.WriteString(stdout, text); err != nil || n != len(text) {
				return outputFailure(out.exit, q)
			}
			return out.exit
		}
	}
	if n, err := stdout.Write(b); err != nil || n != len(b) {
		return outputFailure(out.exit, q)
	}
	if !q.json && out.exit == 0 {
		if board, ok := out.value.(T.BoardRead); ok {
			text := boardAdvisory(board.Result)
			if n, err := io.WriteString(stdout, text); err != nil || n != len(text) {
				return outputFailure(out.exit, q)
			}
		}
	}
	if stderr != nil && out.exit != 0 {
		_, _ = fmt.Fprintf(stderr, "%s\n", diagnostic(out.value))
	}
	return out.exit
}
func outputFailure(exit int, q invocation) int {
	if q.lookup || q.command != "" && (exit == 0 || exit == 4) {
		return 4
	}
	return 1
}
func diagnostic(v any) string {
	code := "UNAVAILABLE"
	switch x := v.(type) {
	case T.NonterminalRefusal:
		code = x.Error.Code
	case T.KnownIdentityUnknown:
		code = x.Error.Code
	case T.Terminal:
		code = x.Code
	}
	for _, r := range code {
		if !unicode.IsPrint(r) {
			return strconv.Quote(code)
		}
	}
	return code
}

func (r Runner) execute(ctx context.Context, q invocation, stdin io.Reader, stderr io.Writer) result {
	var original *evidence.Record
	if q.lookup {
		if r.Evidence == nil {
			return refused("UNSAFE_LOCAL_MATERIAL", 4, nil)
		}
		x, e := r.Evidence.Load(q.file)
		if e != nil || !x.Valid() {
			return refused("UNSAFE_LOCAL_MATERIAL", 4, nil)
		}
		original = &x
		i := x.Identity()
		if i.BoardID != q.selected.BoardID || i.OpID != q.id {
			return refused("OPERATION_MISMATCH", 4, original)
		}
		if q.native && i.Command != "grant.create" && i.Command != "grant.revoke" {
			return refused("OPERATION_MISMATCH", 4, original)
		}
	}
	var wire []byte
	if q.command != "" {
		var e error
		wire, e = readInput(q.input, stdin)
		if e != nil {
			return refused("INVOCATION_OR_IO", 1, nil)
		}
		if (q.kind == "card archive" || q.kind == "card unarchive") && !archiveAlias(wire, q.kind == "card archive") {
			return refused("INVALID_INPUT", 2, nil)
		}
		if _, e = contract.DecodeJSON(wire); e != nil {
			return refused("INVALID_INPUT", 2, nil)
		}
	}
	if ctx == nil || ctx.Err() != nil {
		return refused("UNAVAILABLE", 3, original)
	}
	if r.Provider == nil {
		return refused("CREDENTIAL_UNAVAILABLE", 3, original)
	}
	var c *Connection
	var client *Client
	var e error
	if q.native {
		c, e = r.Provider.NativeOwner(ctx, *original)
		client = &Client{c: c}
		if e == nil && (c == nil || c.do == nil || !c.native) {
			e = &Failure{"CREDENTIAL_CONTEXT_MISMATCH", 2}
		}
	} else {
		client, e = Open(ctx, r.Provider, q.selected)
		if client != nil {
			c = client.c
		}
	}
	if e != nil {
		if client != nil {
			client.Close()
		}
		code, exit := failure(e)
		return refused(code, exit, original)
	}
	defer client.Close()
	if original != nil {
		return client.lookupRecord(ctx, r.Evidence, *original, stderr).r
	}
	if q.command != "" {
		return client.Dispatch(ctx, r.Evidence, contract.ResolvedRoute{Command: q.command, BoardID: q.selected.BoardID, TargetID: q.id}, wire, stderr).r
	}
	status, b, err := c.request(ctx, q.method, q.path, nil)
	if err != nil {
		return refused("UNAVAILABLE", 3, nil)
	}
	if status != 200 {
		return classifyReadFailure(status, b, nil)
	}
	var v any
	var identity A.Identity
	switch q.kind {
	case "card list", "card search":
		x, err := T.Decode[T.CardSearchRead](b)
		if err != nil || x.Result.Query.Q != q.query || !searchEcho(x.Result.Query.Lane, q.lane) || !searchEcho(x.Result.Query.Owner, q.owner) || !searchEcho(x.Result.Query.Priority, q.priority) || string(x.Result.Query.Archive) != q.archive {
			return refused("UNAVAILABLE", 3, nil)
		}
		v = x
		identity = x.Result.Identity
	case "board read":
		x, err := T.Decode[T.BoardRead](b)
		if err != nil {
			return refused("UNAVAILABLE", 3, nil)
		}
		v = x
		identity = x.Result.Identity
	case "card read":
		x, err := T.Decode[T.CardRead](b)
		if err != nil || string(x.Result.Card.CardID) != q.id {
			return refused("UNAVAILABLE", 3, nil)
		}
		v = x
		identity = x.Result.Identity
	case "card history":
		x, err := T.Decode[T.HistoryRead](b)
		if err != nil || string(x.Result.CardID) != q.id {
			return refused("UNAVAILABLE", 3, nil)
		}
		v = x
		identity = x.Result.Identity
	default:
		return refused("INVOCATION_OR_IO", 1, nil)
	}
	if string(identity.BoardID) != q.selected.BoardID || string(identity.Incarnation) != q.selected.Incarnation {
		return refused("INCARNATION_CONFLICT", 2, nil)
	}
	return result{v, 0}
}

func (r Runner) mark(out result, original evidence.Record, stderr io.Writer) result {
	if t, ok := out.value.(T.Terminal); ok && r.Evidence.Resolve(original, t) != nil && stderr != nil {
		_, _ = io.WriteString(stderr, "EVIDENCE_STATUS_UNAVAILABLE\n")
	}
	return out
}

func classifyReadFailure(status int, b []byte, original *evidence.Record) result {
	x, e := T.Decode[T.NonterminalRefusal](b)
	if e != nil {
		return refused("UNAVAILABLE", 3, original)
	}
	exit := 3
	switch status {
	case 400, 401, 403, 404, 409, 413:
		exit = 2
	case 503:
		exit = 3
	default:
		return refused("UNAVAILABLE", 3, original)
	}
	x.Error.Code = safeCode(x.Error.Code)
	x.Error.Message = "Request unavailable or refused"
	if original != nil {
		return refused(x.Error.Code, 4, original)
	}
	return result{x, exit}
}

func classifyMutation(status int, b []byte, original evidence.Record, lookup bool) result {
	if status == 200 || (!lookup && status == 201) {
		t, e := T.Decode[T.Terminal](b)
		if e == nil && original.MatchesTerminal(t) {
			if status == 201 && (t.Outcome != "committed" || (t.Command != "card.create" && t.Command != "comment.add" && t.Command != "link.add" && t.Command != "attachment.add" && t.Command != "grant.create")) {
				return refused("OUTCOME_UNKNOWN", 4, &original)
			}
			if t.Outcome == "committed" {
				return result{t, 0}
			}
			return result{t, 2}
		}
		if e == nil {
			return refused("OPERATION_MISMATCH", 4, &original)
		}
	}
	if x, e := T.Decode[T.NonterminalRefusal](b); e == nil {
		return refused(x.Error.Code, 4, &original)
	}
	if x, e := T.Decode[T.KnownIdentityUnknown](b); e == nil {
		return refused(x.Error.Code, 4, &original)
	}
	return refused("OUTCOME_UNKNOWN", 4, &original)
}

// The alias checks only explicit intent; normal dispatch still validates the full
// envelope and freezes the same card.edit original before any send.
func archiveAlias(wire []byte, want bool) bool {
	if _, err := contract.DecodeJSON(wire); err != nil {
		return false
	}
	var envelope struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if json.Unmarshal(wire, &envelope) != nil || len(envelope.Input) != 1 {
		return false
	}
	flag, ok := envelope.Input["archived"]
	if !ok || string(flag) == "null" {
		return false
	}
	var got bool
	return json.Unmarshal(flag, &got) == nil && got == want
}

func searchEcho[S ~string](got *S, want string) bool {
	if got == nil {
		return want == ""
	}
	return want != "" && string(*got) == want
}
