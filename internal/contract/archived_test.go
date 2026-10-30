package contract

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestArchivedInputPreservesLegacyAndExplicitFalse(t *testing.T) {
	legacy, err := Freeze(sample("card.edit", cardC, `{"title":"Retained"}`), ResolvedRoute{"card.edit", board, cardC}, AuthenticatedActor{actor})
	if err != nil || bytes.Contains(legacy.CanonicalBytes(), []byte(`"archived"`)) {
		t.Fatal("legacy edit changed", err)
	}
	seen := map[string]bool{legacy.Fingerprint(): true}
	for _, flag := range []string{"false", "true"} {
		intent, err := Freeze(sample("card.edit", cardC, `{"archived":`+flag+`}`), ResolvedRoute{"card.edit", board, cardC}, AuthenticatedActor{actor})
		if err != nil {
			t.Fatal(err)
		}
		if seen[intent.Fingerprint()] || !bytes.Contains(intent.CanonicalBytes(), []byte(`"archived":`+flag)) {
			t.Fatal("archive value lost or shared fingerprint")
		}
		seen[intent.Fingerprint()] = true
		typed, err := intent.Input()
		if err != nil {
			t.Fatal(err)
		}
		edit := typed.(*EditInput)
		if edit.Archived == nil || *edit.Archived != (flag == "true") {
			t.Fatal("typed explicit false became omission")
		}
		raw, err := json.Marshal(edit)
		if err != nil || !bytes.Contains(raw, []byte(`"archived":`+flag)) {
			t.Fatal("typed archive reencode", err)
		}
	}
	for _, input := range []string{`{"archived":null}`, `{"archived":"false"}`, `{"archived":0}`, `{"archived":false,"title":"Mixed"}`, `{"archived":true,"ownerId":null}`, `{"archived":false,"lane":"In Progress"}`} {
		if _, err := Freeze(sample("card.edit", cardC, input), ResolvedRoute{"card.edit", board, cardC}, AuthenticatedActor{actor}); err == nil {
			t.Fatal("invalid archive wire accepted", input)
		}
	}
}
