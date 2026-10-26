package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestDiagnosticPresentationPreservesSafeCodes(t *testing.T) {
	const unsafe = "REFUSED\x1b[31m\nforged"
	const escaped = `"REFUSED\x1b[31m\nforged"`
	for _, value := range []any{
		T.NonterminalRefusal{Error: T.Error{Code: unsafe}},
		T.KnownIdentityUnknown{Error: T.Error{Code: unsafe}},
		T.Terminal{Code: unsafe},
	} {
		if diagnostic(value) != escaped {
			t.Fatal("nonprinting diagnostic code was not escaped")
		}
	}
	if diagnostic(T.Terminal{Code: "REVISION_CONFLICT"}) != "REVISION_CONFLICT" {
		t.Fatal("ordinary diagnostic readability changed")
	}
}

func TestRemoteDiagnosticControlsNeverAlterJSONOrOutcome(t *testing.T) {
	original, wire := fixtureRecord(t, "card-create")
	const code = "REFUSED\x1b[31m\nforged"
	terminal, e := T.Decode[T.Terminal](fixture(t, "command-card-create.json"))
	if e != nil {
		t.Fatal("terminal fixture")
	}
	terminal.Outcome = "not_committed"
	terminal.Code = code
	terminal.Revision, terminal.CardRevision = nil, nil
	terminal.ResultRefs = []A.FactRef{}
	terminal.ByteFacts = []A.ByteFact{}
	refusalResult, _ := json.Marshal(T.RefusalResult{Error: T.Error{Code: code, Message: "Request refused"}})
	raw := json.RawMessage(refusalResult)
	terminal.Result = &raw
	terminalBytes, e := T.Readable(terminal)
	if e != nil {
		t.Fatal("valid terminal with nonprinting code required")
	}
	refusalBytes, e := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}})
	if e != nil {
		t.Fatal("valid nonterminal refusal required")
	}
	unknown := refused("OUTCOME_UNKNOWN", 4, &original).value.(T.KnownIdentityUnknown)
	unknown.Error.Code = code
	unknownBytes, e := T.Readable(unknown)
	if e != nil {
		t.Fatal("valid unknown required")
	}
	for _, family := range []string{"read-refusal", "mutation-refusal", "unknown", "terminal", "terminal-lookup"} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(family+map[bool]string{false: "/human", true: "/json"}[jsonMode], func(t *testing.T) {
				store := &memoryEvidence{record: original}
				remote := refusalBytes
				status, want := 403, 4
				a := args("card", "create", "--input", "-")
				input := wire
				var expected []byte
				switch family {
				case "read-refusal":
					want = 2
					a = args("board", "read")
					input = nil
					expected, _ = T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "UNAVAILABLE", Message: "Request unavailable or refused"}})
				case "unknown":
					status, remote = 503, unknownBytes
				case "terminal", "terminal-lookup":
					status, want, remote, expected = 200, 2, terminalBytes, terminalBytes
					if family == "terminal-lookup" {
						a = args("operation", "read", original.Identity().OpID, "--evidence", "synthetic-evidence")
						input = nil
					}
				}
				if expected == nil {
					expected, _ = T.Readable(refused("UNAVAILABLE", 4, &original).value)
				}
				if !jsonMode {
					a = a[1:]
				}
				c := connection(t, func(*http.Request) (int, []byte, error) { return status, remote, nil })
				var stdout, stderr bytes.Buffer
				exit := (Runner{&provider{conn: c}, store}).Run(context.Background(), a, bytes.NewReader(input), &stdout, &stderr)
				if exit != want || bytes.Contains(stderr.Bytes(), []byte{0x1b}) || strings.Count(stderr.String(), "\n") != 1 || strings.Contains(stderr.String(), "\nforged") {
					t.Fatal("remote code forged diagnostics or changed outcome")
				}
				body := stdout.Bytes()
				if !jsonMode {
					parts := bytes.SplitN(body, []byte("\n"), 2)
					if len(parts) != 2 {
						t.Fatal("human output missing envelope")
					}
					body = parts[1]
				}
				if !bytes.Equal(body, expected) {
					t.Fatal("diagnostic escaping altered exact JSON or original proof")
				}
				if family == "terminal" || family == "terminal-lookup" {
					if stderr.String() != `"REFUSED\x1b[31m\nforged"`+"\n" || store.resolved != 1 {
						t.Fatal("terminal diagnostic or definitive noncommit changed")
					}
				}
			})
		}
	}
}
