package archive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

func relationshipOperation(t *testing.T, command Command, input string) Operation {
	t.Helper()
	o := fixture[Outcomes](t, "package/outcomes.json").Operations[0]
	o.Command = command
	o.ByteFacts = []ByteFact{}
	owner := *o.Target.CardID
	targetID := string(owner)
	if command == "card.create" {
		o.Target = nil
		o.ExpectedCardRevision = nil
		targetID = ""
	}
	wire, err := json.Marshal(map[string]any{"opId": o.OpID, "incarnation": o.Origin.Incarnation, "target": o.Target, "expectedRevision": o.ExpectedRevision, "expectedCardRevision": o.ExpectedCardRevision, "input": json.RawMessage(input)})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := contract.Freeze(wire, contract.ResolvedRoute{Command: contract.Command(command), BoardID: string(o.Origin.BoardID), TargetID: targetID}, contract.AuthenticatedActor{ActorID: string(o.ActorID)})
	if err != nil {
		t.Fatal(err)
	}
	rev := Number(*o.CommittedCardRevision)
	o.ResultRefs = []FactRef{{Kind: "card", ID: owner, Revision: &rev}}
	result := fmt.Sprintf(`{"cardId":%q}`, owner)
	if command == "link.add" {
		o.ResultRefs = append(o.ResultRefs, FactRef{Kind: "link", ID: correctionID})
		result = fmt.Sprintf(`{"cardId":%q,"linkId":%q}`, owner, correctionID)
	}
	o.Fingerprint = Digest(frozen.Fingerprint())
	o.Full = &FullOutcome{Intent: frozen.CanonicalBytes(), Result: json.RawMessage(result)}
	return o
}

func TestRelationshipOriginalRefreezeAndCardOnlyReceipts(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	ref := fmt.Sprintf(`{"boardId":%q,"incarnation":%q,"cardId":%q}`, b.Identity.BoardID, b.Identity.Incarnation, correctionID)
	for _, item := range []struct {
		command Command
		input   string
	}{
		{"card.create", `{"title":"Old","description":""}`},
		{"card.edit", `{"title":"Old"}`},
		{"card.create", `{"title":"Batch","description":"","relationships":[]}`},
		{"card.create", `{"title":"Batch","description":"","relationships":[{"kind":"reference","target":` + ref + `}]}`},
		{"card.edit", `{"relationships":[]}`},
		{"card.edit", `{"relationships":[{"kind":"related","target":` + ref + `},{"kind":"duplicate","target":` + ref + `}]}`},
		{"link.add", fmt.Sprintf(`{"kind":"informational","targetCardId":%q}`, correctionID)},
		{"link.add", `{"kind":"reference","target":` + ref + `}`},
	} {
		t.Run(string(item.command)+item.input, func(t *testing.T) {
			o := relationshipOperation(t, item.command, item.input)
			original := bytes.Clone(o.Full.Intent)
			raw, err := Readable(o)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeReadable[Operation](raw)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := Canonical(decoded.Full.Intent)
			if err != nil || !bytes.Equal(original, canonical) || decoded.Fingerprint != o.Fingerprint {
				t.Fatal("original refreeze changed", err)
			}
			if item.command == "card.create" || item.command == "card.edit" {
				if len(o.ResultRefs) != 1 {
					t.Fatal("batch receipt widened")
				}
				wide := o
				wide.ResultRefs = append(append([]FactRef{}, o.ResultRefs...), FactRef{Kind: "link", ID: correctionID})
				if Validate(wide) == nil {
					t.Fatal("extra link result ref accepted")
				}
			}
			o.Full = nil
			o.Removal = &Removal{Reason: "expired", RemovedAt: correctionTime, BoardRevision: 5}
			if err := Validate(o); err != nil {
				t.Fatal("compact original", err)
			}
		})
	}
}

func TestRelationshipCommittedExactTargetReferences(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	ref := fmt.Sprintf(`{"boardId":%q,"incarnation":%q,"cardId":%q}`, b.Identity.BoardID, b.Identity.Incarnation, correctionID)
	for _, command := range []Command{"card.create", "card.edit", "link.add"} {
		input := `{"relationships":[{"kind":"reference","target":` + ref + `}]}`
		if command == "card.create" {
			input = `{"title":"Batch","description":"",` + input[1:]
		} else if command == "link.add" {
			input = `{"kind":"reference","target":` + ref + `}`
		}
		o := relationshipOperation(t, command, input)
		rows := emptyRows()
		rows.Actors = b.Actors
		if command == "link.add" {
			rows.Links = []Link{{LinkID: correctionID, CardID: b.Cards[0].CardID, Kind: "reference", Target: CardRef{b.Identity.BoardID, b.Identity.Incarnation, correctionID}, Active: true, CreatedAt: correctionTime, CreatedIn: b.Identity}}
		}
		requireError(t, ValidateOperationReferences(o, b.Identity.BoardID, b.Cards, rows), "COMMITTED_REFERENCE")
		peer := b.Cards[0]
		peer.CardID, peer.Ticket = correctionID, 2
		peer.CommentIDs, peer.LinkIDs, peer.AttachmentIDs = []UUID{}, []UUID{}, []UUID{}
		if err := ValidateOperationReferences(o, b.Identity.BoardID, append(b.Cards, peer), rows); err != nil {
			t.Fatal(command, err)
		}
		// After restore the original incarnation remains source-bound while the
		// target card ID continues to resolve, without modifying canonical bytes.
		if err := Validate(o); err != nil {
			t.Fatal("source original", err)
		}
	}
}

func TestRelationshipArchiveRejectsOptionalNullCounterparts(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	ref := fmt.Sprintf(`{"boardId":%q,"incarnation":%q,"cardId":%q}`, b.Identity.BoardID, b.Identity.Incarnation, correctionID)
	for _, item := range []struct {
		command       Command
		input, padded string
	}{
		{"card.create", `{"title":"Old","description":""}`, "relationships"},
		{"link.add", fmt.Sprintf(`{"kind":"blocking","targetCardId":%q}`, correctionID), "target"},
		{"link.add", `{"kind":"reference","target":` + ref + `}`, "targetCardId"},
	} {
		o := relationshipOperation(t, item.command, item.input)
		value, err := contract.DecodeJSON(o.Full.Intent)
		if err != nil {
			t.Fatal(err)
		}
		value.(map[string]any)["payload"].(map[string]any)[item.padded] = nil
		o.Full.Intent, err = contract.Canonical(value)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, _ := contract.Hash(value)
		o.Fingerprint = Digest(fingerprint)
		if Validate(o) == nil {
			t.Fatal("padded counterpart accepted", item.padded)
		}
	}
}

func TestRelationshipArchiveCurrentScopeAndHistoricalEvidence(t *testing.T) {
	for _, kind := range []LinkKind{"blocking", "informational", "related", "reference", "duplicate"} {
		b := fixture[Board](t, "package/board.json")
		h := fixture[History](t, "package/history.json")
		a := fixture[Activity](t, "package/activity.json")
		o := fixture[Outcomes](t, "package/outcomes.json")
		m := fixture[Manifest](t, "package/manifest.json")
		b.Links = []Link{{LinkID: correctionID, CardID: b.Cards[0].CardID, Kind: kind, Target: CardRef{correctionID, b.Identity.Incarnation, correctionID}, Active: true, CreatedAt: correctionTime, CreatedIn: b.Identity}}
		b.Cards[0].LinkIDs = []UUID{correctionID}
		mm := refreshProjectionInventory(t, m, b, h, a, o)
		_, err := ValidateDocuments(mm, b, h, a, o)
		if (err == nil) != !IsModernLinkKind(kind) {
			t.Fatal("current target scope", kind, err)
		}
		b.Cards[0].LinkIDs = []UUID{}
		b.Links[0].Active = false
		mm = refreshProjectionInventory(t, m, b, h, a, o)
		if _, err := ValidateDocuments(mm, b, h, a, o); err != nil {
			t.Fatal("historical unavailable target", kind, err)
		}
		b.Links[0].Kind = "unknown"
		if Validate(b) == nil {
			t.Fatal("unknown kind accepted")
		}
	}
}
