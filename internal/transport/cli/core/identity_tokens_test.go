package core

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestRejectedIdentityTokensNeverReachProvidersOrOutput(t *testing.T) {
	type placement struct {
		name      string
		arguments []string
		index     int
		lookup    bool
	}
	var placements []placement
	for _, lookup := range []bool{false, true} {
		command := []string{"board", "read"}
		if lookup {
			command = []string{"operation", "read", "00000000-0000-4000-8000-0000000000c8", "--evidence", "synthetic-evidence"}
		}
		base := args(command...)
		for _, selector := range []string{"--installation", "--board", "--incarnation", "--grant"} {
			for i, token := range base {
				if token == selector {
					placements = append(placements, placement{fmt.Sprintf("%s_lookup_%t", selector, lookup), base, i + 1, lookup})
				}
			}
		}
	}
	placements = append(placements,
		placement{"card_id", args("card", "read", "00000000-0000-4000-8000-00000000000b"), len(prefix()) + 2, false},
		placement{"operation_id", args("operation", "read", "00000000-0000-4000-8000-0000000000c8", "--evidence", "synthetic-evidence"), len(prefix()) + 2, true},
		placement{"native_board", []string{"--json", "--board", testBoard, "operation", "read", "00000000-0000-4000-8000-0000000000c8", "--evidence", "synthetic-evidence", "--native-owner"}, 2, true},
		placement{"native_operation", []string{"--json", "--board", testBoard, "operation", "read", "00000000-0000-4000-8000-0000000000c8", "--evidence", "synthetic-evidence", "--native-owner"}, 5, true},
	)
	for _, placement := range placements {
		original := placement.arguments[placement.index]
		badTokens := []struct{ name, value string }{
			{"literal_unicode_alias", `\u0030` + original[1:]},
			{"terminal_control", "\x1b[31mforged\n"},
			{"quoted", `"` + original + `"`},
			{"raw_quote", original + `"`},
			{"trailing_space", original + " "},
			{"zero", "00000000-0000-0000-0000-000000000000"},
			{"uppercase", "00000000-0000-4000-8000-00000000000A"},
		}
		for _, bad := range badTokens {
			for _, jsonMode := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/json_%t", placement.name, bad.name, jsonMode), func(t *testing.T) {
					a := append([]string(nil), placement.arguments...)
					a[placement.index] = bad.value
					selectedBoard := testBoard
					if placement.name == "--board_lookup_false" || placement.name == "--board_lookup_true" || placement.name == "native_board" {
						selectedBoard = "unavailable"
					}
					if !jsonMode {
						a = a[1:]
					}
					p := &provider{err: &Failure{"CREDENTIAL_UNAVAILABLE", 3}}
					store := &memoryEvidence{}
					var stdout, stderr bytes.Buffer
					got := (Runner{p, store}).Run(context.Background(), a, nil, &stdout, &stderr)
					want := 1
					if placement.lookup {
						want = 4
					}
					if got != want {
						t.Fatalf("exit %d want %d", got, want)
					}
					if p.opened != 0 || p.native != 0 || store.saved != 0 || store.loaded != 0 || store.resolved != 0 {
						t.Fatal("invalid raw token reached source or dispatch")
					}
					if bytes.Contains(stdout.Bytes(), []byte(bad.value)) || bytes.Contains(stderr.Bytes(), []byte(bad.value)) || bytes.Contains(stdout.Bytes(), []byte{0x1b}) {
						t.Fatal("rejected token escaped output")
					}
					if stderr.String() != "INVOCATION_OR_IO\n" {
						t.Fatal("diagnostic was not fixed and inert")
					}
					body := stdout.Bytes()
					if !jsonMode {
						parts := bytes.SplitN(body, []byte("\n"), 2)
						label := "failed"
						if placement.lookup {
							label = "unknown"
						}
						if len(parts) != 2 || string(parts[0]) != label+" board "+selectedBoard {
							t.Fatal("unsafe human board label")
						}
						body = parts[1]
					}
					refusal, e := T.Decode[T.NonterminalRefusal](body)
					if e != nil || refusal.Error.Code != "INVOCATION_OR_IO" || refusal.Conflict != nil || refusal.OpID != nil || !bytes.HasSuffix(body, []byte("\n")) {
						t.Fatal("invalid invocation must emit one exact refusal envelope")
					}
				})
			}
		}
	}
}

func TestRawCanonicalUUIDPolicyIsUnchanged(t *testing.T) {
	// General domain IDs retain their nonzero canonical policy; only installation
	// IDs require UUIDv4. Raw spelling is never decoded or normalized.
	general := "11111111-1111-1111-1111-111111111111"
	a := args("card", "read", general)
	for i, token := range a {
		if token == "--board" || token == "--incarnation" || token == "--grant" {
			a[i+1] = general
		}
	}
	q, code, exit := parse(a)
	if code != "" || exit != 0 || q.id != general || q.selected.BoardID != general {
		t.Fatal("canonical domain UUID policy changed")
	}
	for _, bad := range []string{`\u0031` + general[1:], "\"" + general + "\"", general + "\n", strings.ToUpper("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")} {
		if uuid(bad) {
			t.Fatal("noncanonical raw UUID accepted")
		}
	}
}
