package evidence

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
)

const installation = "10000000-0000-4000-8000-000000000001"
const board = "00000000-0000-4000-8000-000000000001"
const actor = "00000000-0000-4000-8000-00000000000a"
const incarnation = "00000000-0000-4000-8000-000000000002"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("../../../..", "fixtures/contracts/transport-envelopes", name))
	if e != nil {
		t.Fatal("fixture unavailable")
	}
	return b
}
func record(t *testing.T) Record {
	t.Helper()
	wire := []byte(`{"opId":"00000000-0000-4000-8000-0000000000c8","incarnation":"00000000-0000-4000-8000-000000000002","target":null,"expectedRevision":5,"expectedCardRevision":null,"input":{"title":"Example","description":"<>&\r\nΩ"}}`)
	r, e := Freeze(Context{installation, "https://synthetic.localhost:47443"}, contract.ResolvedRoute{Command: "card.create", BoardID: board}, contract.AuthenticatedActor{ActorID: actor}, wire)
	if e != nil {
		t.Fatal("freeze unavailable")
	}
	return r
}
func TestImmutableRoundtripAndStrictFormat(t *testing.T) {
	r := record(t)
	x, e := Decode(r.Bytes())
	if e != nil || !bytes.Equal(x.Semantic(), r.Semantic()) || x.Fingerprint() != r.Fingerprint() {
		t.Fatal("roundtrip")
	}
	b := r.Bytes()
	b[0] = 'x'
	s := r.Semantic()
	s[0] = 'x'
	w := r.Wire()
	w[0] = 'x'
	id := r.Identity()
	id.ActorID = "changed"
	if !r.Valid() {
		t.Fatal("caller alias changed original")
	}
	for _, bad := range [][]byte{nil, []byte(`{}`), append([]byte("\xef\xbb\xbf"), r.Bytes()...), []byte(strings.Replace(string(r.Bytes()), `"version":1`, `"version":2`, 1)), []byte(strings.Replace(string(r.Bytes()), `"fingerprint":`, `"extra":null,"fingerprint":`, 1)), []byte(strings.Replace(string(r.Bytes()), `"actorId":"`+actor+`"`, `"actorId":"00000000-0000-4000-8000-000000000099"`, 1))} {
		if _, e := Decode(bad); e == nil {
			t.Fatal("accepted invalid evidence")
		}
	}
}
func TestRouteBindingAndTerminalIdentity(t *testing.T) {
	r := record(t)
	var m map[string]any
	_ = json.Unmarshal(r.Wire(), &m)
	m["target"] = map[string]string{"cardId": "00000000-0000-4000-8000-00000000000b"}
	b, _ := json.Marshal(m)
	if _, e := Freeze(r.Context(), r.Route(), contract.AuthenticatedActor{ActorID: actor}, b); e == nil {
		t.Fatal("wrong route accepted")
	}
	// The accepted create terminal is bound to its original canonical fixture.
	semantic := fixture(t, "intent-card-create.canonical.bin")
	var intent map[string]json.RawMessage
	_ = json.Unmarshal(semantic, &intent)
	wire := map[string]json.RawMessage{}
	for _, key := range []string{"incarnation", "opId", "target", "expectedRevision", "expectedCardRevision"} {
		wire[key] = intent[key]
	}
	wire["input"] = []byte(`{"title":"Alpha","description":"First draft"}`)
	// Normalize optional payload null to omission for its original wire semantics.
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(intent["payload"], &payload)
	for k, v := range payload {
		if string(v) == "null" {
			delete(payload, k)
		}
	}
	wire["input"], _ = json.Marshal(payload)
	b, _ = json.Marshal(wire)
	r, e := Freeze(r.Context(), r.Route(), contract.AuthenticatedActor{ActorID: actor}, b)
	if e != nil {
		t.Fatal("accepted fixture freeze")
	}
	terminal, e := T.Decode[T.Terminal](fixture(t, "command-card-create.json"))
	if e != nil || !r.MatchesTerminal(terminal) {
		t.Fatal("original terminal binding")
	}
	terminal.ActorID = "00000000-0000-4000-8000-000000000099"
	if r.MatchesTerminal(terminal) {
		t.Fatal("actor rebinding")
	}
}

func TestPrivateRouteFieldsAreCompleteAndExact(t *testing.T) {
	r := record(t)
	for _, field := range []string{"Command", "BoardID", "TargetID"} {
		var root map[string]any
		_ = json.Unmarshal(r.Bytes(), &root)
		route := root["route"].(map[string]any)
		delete(route, field)
		b, _ := json.Marshal(root)
		if _, e := Decode(b); e == nil {
			t.Fatal("incomplete private route accepted")
		}
	}
	var root map[string]any
	_ = json.Unmarshal(r.Bytes(), &root)
	route := root["route"].(map[string]any)
	route["command"] = route["Command"]
	delete(route, "Command")
	b, _ := json.Marshal(root)
	if _, e := Decode(b); e == nil {
		t.Fatal("private route key alias accepted")
	}
}
