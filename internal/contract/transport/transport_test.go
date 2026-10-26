package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func fixture(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("../../..", "fixtures/contracts/transport-envelopes", p))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func decoded[T any](b []byte) (any, error) { x, e := Decode[T](b); return x, e }
func fixtureValue(p, f string, b []byte) (any, error) {
	switch f {
	case "terminal":
		return decoded[Terminal](b)
	case "refused":
		return decoded[NonterminalRefusal](b)
	case "unknown":
		return decoded[KnownIdentityUnknown](b)
	case "session-read":
		return decoded[SessionRead](b)
	case "session-revoked":
		return decoded[SessionRevoked](b)
	case "code-secret":
		return decoded[CodeSecret](b)
	case "exchange-secret":
		return decoded[ExchangeSecret](b)
	case "credential-secret":
		return decoded[CredentialSecret](b)
	case "credential-cli":
		return decoded[CredentialCLI](b)
	case "export":
		return decoded[ExportStatus](b)
	case "restore":
		return decoded[RestoreStatus](b)
	case "preview":
		return decoded[Preview](b)
	case "strict-json":
		return contract.DecodeJSON(b)
	case "read":
		if strings.Contains(p, "board") {
			return decoded[BoardRead](b)
		}
		if strings.Contains(p, "card") {
			return decoded[CardRead](b)
		}
		return decoded[HistoryRead](b)
	case "request":
		switch p {
		case "code-request.json", "credential-issue-request.json":
			return decoded[CodeRequest](b)
		case "exchange-request.json":
			return decoded[ExchangeRequest](b)
		case "preview-request.json":
			return decoded[PreviewRequest](b)
		case "confirm-request.json":
			return decoded[Confirmation](b)
		case "export-request.json":
			return decoded[ExportRequest](b)
		case "request-attachment.json":
			var req struct {
				Target struct {
					CardID string `json:"cardId"`
				} `json:"target"`
			}
			json.Unmarshal(b, &req)
			_, e := FreezeMutation(b, contract.ResolvedRoute{Command: "attachment.add", BoardID: "00000000-0000-4000-8000-000000000001", TargetID: req.Target.CardID}, contract.AuthenticatedActor{ActorID: "00000000-0000-4000-8000-00000000000a"})
			if e != nil {
				return nil, e
			}
			return contract.DecodeJSON(b)
		}
	}
	return nil, fmt.Errorf("unhandled fixture family")
}
func TestIndependentDocumentaryFixtures(t *testing.T) {
	var index struct {
		Positive, Negative []struct {
			Path, Family, SHA256 string
			ByteLength           int
			SecretTransport      bool
		}
	}
	if e := json.Unmarshal(fixture(t, "cases.json"), &index); e != nil {
		t.Fatal(e)
	}
	for _, entries := range [][]struct {
		Path, Family, SHA256 string
		ByteLength           int
		SecretTransport      bool
	}{index.Positive, index.Negative} {
		for _, entry := range entries {
			raw := fixture(t, entry.Path)
			h := sha256.Sum256(raw)
			if len(raw) != entry.ByteLength || hex.EncodeToString(h[:]) != entry.SHA256 {
				t.Fatalf("fixture anchor: %s", entry.Path)
			}
		}
	}
	for _, entry := range index.Positive {
		t.Run(entry.Path, func(t *testing.T) {
			raw := fixture(t, entry.Path)
			v, e := fixtureValue(entry.Path, entry.Family, raw)
			if e != nil {
				t.Fatal(e)
			}
			var round []byte
			if entry.SecretTransport || entry.Path == "exchange-request.json" {
				round, e = MarshalEphemeral(v)
			} else if entry.Path == "request-attachment.json" {
				round, e = A.Readable(v)
			} else {
				round, e = Readable(v)
			}
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(round, raw) {
				t.Fatal("D03 R bytes changed")
			}
		})
	}
	for _, entry := range index.Negative {
		t.Run(entry.Path, func(t *testing.T) {
			if _, e := fixtureValue(entry.Path, entry.Family, fixture(t, entry.Path)); e == nil {
				t.Fatal("accepted negative")
			}
		})
	}
}

func mutate(t *testing.T, p string, fn func(map[string]any)) []byte {
	t.Helper()
	v, e := contract.DecodeJSON(fixture(t, p))
	if e != nil {
		t.Fatal(e)
	}
	fn(v.(map[string]any))
	b, e := contract.Canonical(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestTerminalBindingsAndRetention(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong command", func(m map[string]any) { m["command"] = "grant.revoke" }},
		{"wrong target", func(m map[string]any) {
			m["target"].(map[string]any)["cardId"] = "00000000-0000-4000-8000-000000000099"
		}},
		{"wrong result", func(m map[string]any) {
			m["result"].(map[string]any)["attachmentId"] = "00000000-0000-4000-8000-000000000099"
		}},
		{"wrong refs revision", func(m map[string]any) {
			refs := m["resultRefs"].([]any)
			for _, r := range refs {
				if r.(map[string]any)["kind"] == "card" {
					r.(map[string]any)["revision"] = json.Number("9")
				}
			}
		}},
		{"invented full field", func(m map[string]any) { m["result"].(map[string]any)["intent"] = map[string]any{} }},
		{"missing byte fact", func(m map[string]any) { m["byteFacts"] = []any{} }},
		{"duplicate byte fact", func(m map[string]any) { m["byteFacts"] = append(m["byteFacts"].([]any), m["byteFacts"].([]any)[0]) }},
		{"null array", func(m map[string]any) { m["resultRefs"] = nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, e := Decode[Terminal](mutate(t, "terminal-source-00.json", c.mutate)); e == nil {
				t.Fatal("binding accepted")
			}
		})
	}
	// A structurally valid matching fingerprint is deliberately not checked as
	// authenticity: the original immutable evidence is absent from this DTO.
	b := mutate(t, "terminal-source-00.json", func(m map[string]any) { m["fingerprint"] = strings.Repeat("f", 64) })
	if _, e := Decode[Terminal](b); e != nil {
		t.Fatal("validator invented authenticity")
	}
	for _, c := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"refusal code", func(m map[string]any) { m["code"] = "DIFFERENT" }},
		{"conflict half", func(m map[string]any) {
			m["result"].(map[string]any)["conflict"] = map[string]any{"incarnation": m["incarnation"], "revision": json.Number("3"), "cardRevision": nil, "cardId": "00000000-0000-4000-8000-00000000000b"}
		}},
		{"empty diagnostic", func(m map[string]any) { m["result"].(map[string]any)["error"].(map[string]any)["message"] = "" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, e := Decode[Terminal](mutate(t, "terminal-source-02.json", c.mutate)); e == nil {
				t.Fatal("refusal accepted")
			}
		})
	}
}

func TestStrictEveryRequiredFieldAndScalar(t *testing.T) {
	fixtures := []struct{ path, family string }{{"terminal-source-00.json", "terminal"}, {"unknown.json", "unknown"}, {"forbidden.json", "refused"}, {"session-read.json", "session-read"}, {"credential-cli-confirmed.json", "credential-cli"}, {"code-secret.json", "code-secret"}, {"exchange-secret.json", "exchange-secret"}, {"credential-secret.json", "credential-secret"}, {"board-read.json", "read"}, {"card-read.json", "read"}, {"history-read.json", "read"}, {"export-confirmed.json", "export"}}
	for _, f := range fixtures {
		var fields map[string]json.RawMessage
		json.Unmarshal(fixture(t, f.path), &fields)
		for key := range fields {
			if key == "opId" && f.family == "refused" {
				continue
			}
			t.Run(f.path+"/missing/"+key, func(t *testing.T) {
				b := mutate(t, f.path, func(m map[string]any) { delete(m, key) })
				if _, e := fixtureValue(f.path, f.family, b); e == nil {
					t.Fatal("missing key accepted")
				}
			})
		}
		t.Run(f.path+"/unknown", func(t *testing.T) {
			b := mutate(t, f.path, func(m map[string]any) { m["actor"] = "claimed" })
			if _, e := fixtureValue(f.path, f.family, b); e == nil {
				t.Fatal("unknown key accepted")
			}
		})
	}
	for _, bad := range []string{`{"apiVersion":1,"apiVersion":1}`, `{"apiVersion":1e0}`, `{"apiVersion":-0}`, `{"apiVersion":1.0}`, `{"apiVersion":9007199254740992}`, `{"x":"\ud800"}`, "\xef\xbb\xbf{}", string([]byte{0xff}), strings.Repeat("[", 33) + strings.Repeat("]", 33)} {
		if _, e := Decode[SessionRead]([]byte(bad)); e == nil {
			t.Fatal("strict bytes accepted")
		}
	}
}

func TestSecretBoundaryAndExpiry(t *testing.T) {
	for _, f := range []struct{ path, family string }{{"code-secret.json", "code-secret"}, {"exchange-secret.json", "exchange-secret"}, {"credential-secret.json", "credential-secret"}, {"exchange-request.json", "request"}} {
		v, e := fixtureValue(f.path, f.family, fixture(t, f.path))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = json.Marshal(v); e == nil {
			t.Fatal("implicit secret serialization")
		}
		if _, e = Readable(v); e == nil {
			t.Fatal("secret public serialization")
		}
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if !strings.Contains(fmt.Sprintf(format, v), "REDACTED") {
				t.Fatal("secret formatting unredacted")
			}
		}
	}
	for _, f := range []struct{ path, family string }{{"code-secret.json", "code-secret"}, {"exchange-secret.json", "exchange-secret"}, {"credential-secret.json", "credential-secret"}} {
		b := mutate(t, f.path, func(m map[string]any) { m["expiresAt"] = m["issuedAt"] })
		if _, e := fixtureValue(f.path, f.family, b); e == nil {
			t.Fatal("expiry accepted")
		}
	}
	for _, s := range []string{"AAAAAAAAAAAAAAAAAAAAAB", "AAAAAAAAAAAAAAAAAAAAAA=", "AAAAAAAAAAAAAAAAAAAAAA\n", "AAAAAAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, e := Decode[ExchangeRequest]([]byte(`{"code":"` + s + `"}`)); e == nil {
			t.Fatal("noncanonical code accepted")
		}
	}
	b := mutate(t, "code-request.json", func(m map[string]any) { m["installationId"] = "00000000-0000-1000-8000-000000000001" })
	if _, e := Decode[CodeRequest](b); e == nil {
		t.Fatal("non-v4 installation accepted")
	}
	b = mutate(t, "credential-secret.json", func(m map[string]any) { m["credentialId"] = "00000000-0000-4000-7000-000000000001" })
	if _, e := Decode[CredentialSecret](b); e == nil {
		t.Fatal("non-v4 credential accepted")
	}
}

func TestReadReferences(t *testing.T) {
	for _, c := range []struct {
		name, path string
		fn         func(map[string]any)
	}{
		{"missing attachment", "card-read.json", func(m map[string]any) { m["result"].(map[string]any)["attachments"] = []any{} }},
		{"wrong owner", "board-read.json", func(m map[string]any) {
			m["result"].(map[string]any)["comments"].([]any)[0].(map[string]any)["cardId"] = "00000000-0000-4000-8000-000000000099"
		}},
		{"missing actor", "card-read.json", func(m map[string]any) { m["result"].(map[string]any)["actors"] = []any{} }},
		{"current history", "card-read.json", func(m map[string]any) {
			m["result"].(map[string]any)["historyReferences"].([]any)[0].(map[string]any)["cardRevision"] = json.Number("4")
		}},
		{"wrong scope", "history-read.json", func(m map[string]any) {
			m["result"].(map[string]any)["cardId"] = "00000000-0000-4000-8000-000000000099"
		}},
		{"foreign coverage", "board-read.json", func(m map[string]any) {
			m["result"].(map[string]any)["coverage"].(map[string]any)["gaps"].([]any)[0].(map[string]any)["origin"].(map[string]any)["boardId"] = "00000000-0000-4000-8000-000000000099"
		}},
		{"nonempty revision zero", "card-read.json", func(m map[string]any) { m["result"].(map[string]any)["revision"] = json.Number("0") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, e := fixtureValue(c.path, "read", mutate(t, c.path, c.fn)); e == nil {
				t.Fatal("bad reference accepted")
			}
		})
	}
}

func TestCompleteBoardCurrentLinkTargetClosure(t *testing.T) {
	const linkID A.UUID = "00000000-0000-4000-8000-000000000090"
	const absentCard A.UUID = "00000000-0000-4000-8000-000000000091"
	board, e := Decode[BoardRead](fixture(t, "board-read.json"))
	if e != nil {
		t.Fatal(e)
	}
	link := A.Link{LinkID: linkID, CardID: board.Result.Cards[0].CardID, Kind: "informational", Target: A.CardRef{BoardID: board.Result.Identity.BoardID, Incarnation: board.Result.Identity.Incarnation, CardID: absentCard}, Active: true, CreatedAt: "2026-10-01T00:00:00Z", CreatedIn: board.Result.Identity}
	board.Result.Cards[0].LinkIDs = []A.UUID{linkID}
	board.Result.Links = []A.Link{link}
	if e := Validate(board); e == nil {
		t.Fatal("complete board accepted absent current internal target")
	}
	// Marshal directly to exercise the received-wire decoder independently of
	// Readable's constructed-value validation.
	b, e := json.Marshal(board)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := Decode[BoardRead](b); e == nil {
		t.Fatal("wire board accepted absent current internal target")
	}
	for _, test := range []struct {
		name   string
		target A.CardRef
	}{
		{"present current", A.CardRef{BoardID: board.Result.Identity.BoardID, Incarnation: board.Result.Identity.Incarnation, CardID: board.Result.Cards[0].CardID}},
		{"inherited identity", A.CardRef{BoardID: board.Result.Identity.BoardID, Incarnation: "00000000-0000-4000-8000-000000000092", CardID: absentCard}},
		{"foreign board", A.CardRef{BoardID: "00000000-0000-4000-8000-000000000093", Incarnation: board.Result.Identity.Incarnation, CardID: absentCard}},
	} {
		t.Run(test.name, func(t *testing.T) {
			board.Result.Links[0].Target = test.target
			b, e := Readable(board)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = Decode[BoardRead](b); e != nil {
				t.Fatal(e)
			}
		})
	}
	card, e := Decode[CardRead](fixture(t, "card-read.json"))
	if e != nil {
		t.Fatal(e)
	}
	card.Result.Card.LinkIDs = []A.UUID{linkID}
	card.Result.Links = []A.Link{link}
	b, e = Readable(card)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Decode[CardRead](b); e != nil {
		t.Fatal("single-card view incorrectly required an unavailable complete target-card set")
	}
}

func TestGeneratedDeterminismAndCheckedIn(t *testing.T) {
	a, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	b, e := GeneratedFiles()
	if e != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("nondeterministic generator")
	}
	if len(a) != 2 {
		t.Fatal("artifact scope")
	}
	for p, raw := range a {
		if bytes.Contains(raw, []byte{'\r'}) || !bytes.HasSuffix(raw, []byte{'\n'}) || bytes.HasSuffix(raw, []byte("\n\n")) {
			t.Fatal("artifact text convention")
		}
		stored, e := os.ReadFile(filepath.Join("../../..", filepath.FromSlash(p)))
		if e != nil || !bytes.Equal(stored, raw) {
			t.Fatalf("generated drift: %s", p)
		}
	}
}

func TestD03MutationRefinementAndOriginalIntent(t *testing.T) {
	for _, d := range contract.Commands() {
		t.Run(string(d.Command), func(t *testing.T) {
			stem := "intent-" + strings.ReplaceAll(string(d.Command), ".", "-") + ".canonical.bin"
			raw := fixture(t, stem)
			v, e := contract.DecodeJSON(raw)
			if e != nil {
				t.Fatal(e)
			}
			m := v.(map[string]any)
			wire := map[string]any{}
			for _, key := range []string{"opId", "incarnation", "target", "expectedRevision", "expectedCardRevision"} {
				wire[key] = m[key]
			}
			payload := m["payload"].(map[string]any)
			if d.Recovery {
				wire["input"] = payload["input"]
				wire["previewHash"] = payload["previewHash"]
				wire["command"] = m["command"]
			} else {
				input := map[string]any{}
				for key, val := range payload {
					if f, ok := d.Input.Fields[key]; ok && f.Optional && val == nil {
						continue
					}
					input[key] = val
				}
				wire["input"] = input
			}
			if strings.HasPrefix(string(d.Command), "grant.") {
				wire["boardId"] = m["boardId"]
			}
			route := contract.ResolvedRoute{Command: d.Command, BoardID: m["boardId"].(string)}
			if target, ok := m["target"].(map[string]any); ok {
				for _, id := range target {
					route.TargetID = id.(string)
				}
			}
			actor := contract.AuthenticatedActor{ActorID: m["actorId"].(string)}
			b, e := contract.Canonical(wire)
			if e != nil {
				t.Fatal(e)
			}
			intent, e := FreezeMutation(b, route, actor)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(intent.CanonicalBytes(), raw) {
				t.Fatal("original semantic bytes changed")
			}
			terminal, e := Decode[Terminal](fixture(t, "command-"+strings.ReplaceAll(string(d.Command), ".", "-")+".json"))
			if e != nil {
				t.Fatal(e)
			}
			if intent.Fingerprint() != string(terminal.Fingerprint) {
				t.Fatal("original fingerprint changed")
			}
			for _, bad := range []any{"omitted", json.Number("0"), json.Number("1")} {
				if bad == json.Number("1") && d.Target == "card" {
					continue
				}
				if bad == json.Number("0") && d.Target != "card" {
					continue
				}
				saved := wire["expectedCardRevision"]
				if bad == "omitted" {
					delete(wire, "expectedCardRevision")
				} else {
					wire["expectedCardRevision"] = bad
				}
				badBytes, _ := contract.Canonical(wire)
				if _, e := FreezeMutation(badBytes, route, actor); e == nil {
					t.Fatal("bad D03 precondition accepted")
				}
				wire["expectedCardRevision"] = saved
			}
			wire["actorId"] = m["actorId"]
			b, _ = contract.Canonical(wire)
			if _, e := FreezeMutation(b, route, actor); e == nil {
				t.Fatal("transport actor claim accepted")
			}
		})
	}
}

func TestInMemoryValidationAndPublicOutput(t *testing.T) {
	refusal, e := Decode[NonterminalRefusal](fixture(t, "forbidden.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = Validate(&refusal); e != nil {
		t.Fatal(e)
	}
	refusal.Error.Message = string([]byte{0xff})
	if e = Validate(refusal); e == nil {
		t.Fatal("Go encoder repaired invalid Unicode")
	}
	if _, e = Readable(json.RawMessage(`{"token":"synthetic"}`)); e == nil {
		t.Fatal("untyped unsafe output accepted")
	}
	if _, e = Decode[struct {
		Token string `json:"token"`
	}]([]byte(`{"token":"synthetic"}`)); e == nil {
		t.Fatal("unsupported root accepted")
	}
	terminal, e := Decode[Terminal](fixture(t, "terminal-source-00.json"))
	if e != nil {
		t.Fatal(e)
	}
	terminal.ByteFacts = nil
	if e = Validate(terminal); e == nil {
		t.Fatal("required nil array accepted")
	}
}
