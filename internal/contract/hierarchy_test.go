package contract

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestParentCanonicalOmissionNullAndExclusive(t *testing.T) {
	for _, command := range []Command{"card.create", "card.edit"} {
		id := ""
		base := `{"title":"Root","description":""}`
		if command == "card.edit" {
			id = cardC
			base = `{"title":"Root"}`
		}
		legacy, e := Freeze(sample(command, id, base), ResolvedRoute{command, board, id}, AuthenticatedActor{actor})
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(legacy.CanonicalBytes(), []byte(`"parent"`)) {
			t.Fatal("legacy bytes padded")
		}
		input := `{"parent":null}`
		if command == "card.create" {
			input = `{"title":"Root","description":"","parent":null}`
		}
		explicit, e := Freeze(sample(command, id, input), ResolvedRoute{command, board, id}, AuthenticatedActor{actor})
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Contains(explicit.CanonicalBytes(), []byte(`"parent":null`)) || explicit.Fingerprint() == legacy.Fingerprint() {
			t.Fatal("explicit null lost")
		}
		typed, e := explicit.Input()
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(typed)
		if !bytes.Contains(raw, []byte(`"parent":null`)) {
			t.Fatal("typed null lost")
		}
	}
	for _, input := range []string{`{"parent":null,"title":"mixed"}`, `{"parent":null,"ownerId":null}`, `{"parent":null,"archived":true}`, `{"parent":{"cardId":"` + cardC + `"}}`, `{"parent":"` + cardC + `"}`} {
		if _, e := Freeze(sample("card.edit", cardC, input), ResolvedRoute{"card.edit", board, cardC}, AuthenticatedActor{actor}); e == nil {
			t.Fatal("invalid parent accepted", input)
		}
	}
}
