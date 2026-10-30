package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func relationship(kind, target string) string {
	return fmt.Sprintf(`{"kind":%q,"target":{"boardId":%q,"incarnation":%q,"cardId":%q}}`, kind, board, incarnation, target)
}

func TestRelationshipShapesAndCanonicalConservation(t *testing.T) {
	freeze := func(command Command, input string) (Intent, error) {
		target := cardC
		if command == "card.create" {
			target = ""
		}
		return Freeze(sample(command, target, input), ResolvedRoute{command, board, target}, AuthenticatedActor{actor})
	}
	for _, command := range []Command{"card.create", "card.edit"} {
		base := `{"title":"Old"}`
		if command == "card.create" {
			base = `{"title":"Old","description":""}`
		}
		legacy, err := freeze(command, base)
		if err != nil || bytes.Contains(legacy.CanonicalBytes(), []byte(`"relationships"`)) {
			t.Fatal("legacy omission changed", command, err)
		}
		explicit := strings.TrimSuffix(base, "}") + `,"relationships":[]}`
		if command == "card.edit" {
			explicit = `{"relationships":[]}`
		}
		cleared, err := freeze(command, explicit)
		if err != nil || !bytes.Contains(cleared.CanonicalBytes(), []byte(`"relationships":[]`)) || legacy.Fingerprint() == cleared.Fingerprint() {
			t.Fatal("explicit empty lost", command, err)
		}
		typed, err := cleared.Input()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(typed)
		if !bytes.Contains(raw, []byte(`"relationships":[]`)) {
			t.Fatal("typed empty lost")
		}
	}
	for _, kind := range []string{"blocking", "informational", "related", "reference", "duplicate"} {
		input := `{"relationships":[` + relationship(kind, cardD) + `]}`
		i, err := freeze("card.edit", input)
		if err != nil {
			t.Fatal(kind, err)
		}
		typed, _ := i.Input()
		if got := typed.(*EditInput).Relationships; len(got) != 1 || got[0].Kind != kind || got[0].Target.CardID != cardD {
			t.Fatal("typed relationship changed")
		}
	}
	first := relationship("reference", cardD)
	second := relationship("related", cardD)
	ordered, err := freeze("card.edit", `{"relationships":[`+first+`,`+second+`]}`)
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := freeze("card.edit", `{"relationships":[`+second+`,`+first+`]}`)
	if err != nil || ordered.Fingerprint() == reversed.Fingerprint() {
		t.Fatal("submitted array order lost", err)
	}
	for _, input := range []string{
		`{"relationships":null}`, `{"relationships":{}}`,
		`{"relationships":[],"title":"mixed"}`, `{"relationships":[],"parent":null}`, `{"relationships":[],"ownerId":null}`, `{"relationships":[],"archived":false}`,
		`{"relationships":[` + first + `,` + first + `]}`,
		`{"relationships":[{"kind":"blocks","target":{"boardId":"` + board + `","incarnation":"` + incarnation + `","cardId":"` + cardD + `"}}]}`,
		`{"relationships":[{"kind":"reference","targetCardId":"` + cardD + `"}]}`,
		`{"relationships":[{"kind":"reference","target":{"cardId":"` + cardD + `"}}]}`,
		`{"relationships":[` + strings.TrimSuffix(first, "}") + `,"label":"hidden"}]}`,
	} {
		if _, err := freeze("card.edit", input); err == nil {
			t.Fatal("invalid relationship shape accepted", input)
		}
	}
	for _, count := range []int{100, 101} {
		members := make([]string, count)
		for i := range members {
			members[i] = relationship("reference", fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1))
		}
		for _, command := range []Command{"card.create", "card.edit"} {
			input := `{"relationships":[` + strings.Join(members, ",") + `]}`
			if command == "card.create" {
				input = `{"title":"Batch","description":"","parent":null,` + strings.TrimPrefix(input, "{")
			}
			_, err := freeze(command, input)
			if (err == nil) != (count == 100) {
				t.Fatal("array bound", command, count, err)
			}
		}
	}
}

func TestLinkAddModernLegacyExclusiveTarget(t *testing.T) {
	freeze := func(input string) (Intent, error) {
		return Freeze(sample("link.add", cardC, input), ResolvedRoute{"link.add", board, cardC}, AuthenticatedActor{actor})
	}
	for _, kind := range []string{"blocking", "informational", "related", "reference", "duplicate"} {
		modern, err := freeze(relationship(kind, cardD))
		if err != nil || bytes.Contains(modern.CanonicalBytes(), []byte(`"targetCardId"`)) {
			t.Fatal("modern target padded", kind, err)
		}
		typed, _ := modern.Input()
		raw, _ := json.Marshal(typed)
		if bytes.Contains(raw, []byte(`"targetCardId"`)) || typed.(*LinkAddInput).Target == nil {
			t.Fatal("modern typed target changed")
		}
		legacyInput := fmt.Sprintf(`{"kind":%q,"targetCardId":%q}`, kind, cardD)
		legacy, err := freeze(legacyInput)
		legacyKind := kind == "blocking" || kind == "informational"
		if (err == nil) != legacyKind {
			t.Fatal("legacy kind boundary", kind, err)
		}
		if legacyKind && bytes.Contains(legacy.CanonicalBytes(), []byte(`"payload":{"kind":"`+kind+`","target":`)) {
			t.Fatal("legacy modern target padded")
		}
	}
	for _, input := range []string{
		`{"kind":"reference"}`, `{"kind":"reference","target":null}`,
		`{"kind":"blocking","targetCardId":null}`,
		strings.TrimSuffix(relationship("blocking", cardD), "}") + `,"targetCardId":"` + cardD + `"}`,
		strings.TrimSuffix(relationship("blocking", cardD), "}") + `,"targetCardId":null}`,
	} {
		if _, err := freeze(input); err == nil {
			t.Fatal("invalid target combination accepted", input)
		}
	}
}
