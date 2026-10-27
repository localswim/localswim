package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestAssignmentInputPreservesOmissionNullAndActor(t *testing.T) {
	seen := map[string]bool{}
	for _, command := range []Command{"card.create", "card.edit"} {
		for _, value := range []string{"omitted", "null", actor} {
			input := map[string]any{}
			target := cardC
			if command == "card.create" {
				input["title"] = "Synthetic"
				input["description"] = ""
				target = ""
			}
			if value == "null" {
				input["ownerId"] = nil
			} else if value != "omitted" {
				input["ownerId"] = value
			}
			raw, _ := json.Marshal(input)
			i, err := Freeze(sample(command, target, string(raw)), ResolvedRoute{command, board, target}, AuthenticatedActor{actor})
			if err != nil {
				t.Fatal(err)
			}
			if seen[i.Fingerprint()] {
				t.Fatal("assignment states share fingerprint")
			}
			seen[i.Fingerprint()] = true
			typed, err := i.Input()
			if err != nil {
				t.Fatal(err)
			}
			var owner OwnerInput
			switch x := typed.(type) {
			case *CreateInput:
				owner = x.OwnerID
			case *EditInput:
				owner = x.OwnerID
			}
			if owner.Present != (value != "omitted") || (owner.Value != nil) != (value == actor) {
				t.Fatal("typed assignment loses omission/null")
			}
			raw, err = json.Marshal(typed)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(`"ownerId"`)) != owner.Present {
				t.Fatal("typed reencode loses omission")
			}
			if value == actor && *owner.Value != actor {
				t.Fatal("actor altered")
			}
		}
		target := cardC
		input := `{"ownerId":"unknown"}`
		if command == "card.create" {
			target = ""
			input = `{"title":"Synthetic","description":"","ownerId":"unknown"}`
		}
		if _, err := Freeze(sample(command, target, input), ResolvedRoute{command, board, target}, AuthenticatedActor{actor}); err == nil {
			t.Fatal(fmt.Sprint(command, " malformed owner accepted"))
		}
	}
}
