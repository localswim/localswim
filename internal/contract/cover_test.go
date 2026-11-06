package contract

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCoverEditIntent(t *testing.T) {
	route := ResolvedRoute{"card.edit", board, cardC}
	identity := AuthenticatedActor{actor}
	seen := map[string]bool{}
	for _, test := range []struct {
		name, wire string
		value      *string
	}{
		{name: "omitted", wire: `{"title":"Existing edit"}`},
		{name: "clear", wire: `{"coverAttachmentId":null}`},
		{name: "set", wire: `{"coverAttachmentId":"` + cardD + `"}`, value: new(cardD)},
	} {
		t.Run(test.name, func(t *testing.T) {
			frozen, err := Freeze(sample(route.Command, route.TargetID, test.wire), route, identity)
			if err != nil {
				t.Fatal(err)
			}
			if seen[frozen.Fingerprint()] {
				t.Fatal("distinct cover inputs shared a fingerprint")
			}
			seen[frozen.Fingerprint()] = true
			typed, err := frozen.Input()
			if err != nil {
				t.Fatal(err)
			}
			present := test.name != "omitted"
			cover := typed.(*EditInput).CoverAttachmentID
			if cover.Present != present || (cover.Value == nil) != (test.value == nil) ||
				cover.Value != nil && *cover.Value != *test.value {
				t.Fatal("cover value or presence changed")
			}
			raw, err := json.Marshal(typed)
			if err != nil || bytes.Contains(raw, []byte(`"coverAttachmentId"`)) != present {
				t.Fatal("typed cover re-encode", err)
			}
		})
	}

	for _, input := range []string{
		`{"coverAttachmentId":false}`,
		`{"coverAttachmentId":"invalid"}`,
		`{"coverAttachmentId":null,"title":null}`,
		`{"coverAttachmentId":null,"archived":false}`,
		`{"coverAttachmentId":null,"coverAttachmentId":null}`,
	} {
		if _, err := Freeze(sample(route.Command, route.TargetID, input), route, identity); err == nil {
			t.Fatal("invalid cover edit accepted", input)
		}
	}
	if _, err := Freeze(sample("card.create", "", `{"title":"New","description":"","coverAttachmentId":null}`), ResolvedRoute{"card.create", board, ""}, identity); err == nil {
		t.Fatal("cover input accepted on create")
	}
}
