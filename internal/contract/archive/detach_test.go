package archive

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

func TestDetachOriginalAndFullCompactReferenceRules(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	o := fixture[Outcomes](t, "package/outcomes.json").Operations[0]
	attachment := b.Attachments[0]
	o.Command = "attachment.detach"
	o.ByteFacts = []ByteFact{}
	raw, err := json.Marshal(map[string]any{"opId": o.OpID, "incarnation": o.Origin.Incarnation, "target": o.Target, "expectedRevision": o.ExpectedRevision, "expectedCardRevision": o.ExpectedCardRevision, "input": map[string]any{"attachmentId": attachment.AttachmentID}})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := contract.Freeze(raw, contract.ResolvedRoute{Command: "attachment.detach", BoardID: string(o.Origin.BoardID), TargetID: string(*o.Target.CardID)}, contract.AuthenticatedActor{ActorID: string(o.ActorID)})
	if err != nil {
		t.Fatal(err)
	}
	o.Fingerprint = Digest(frozen.Fingerprint())
	result, _ := json.Marshal(map[string]UUID{"cardId": *o.Target.CardID, "attachmentId": attachment.AttachmentID})
	o.Full = &FullOutcome{Intent: frozen.CanonicalBytes(), Result: result}
	rows := Rows{b.Actors, b.Comments, b.Links, b.Attachments, b.Approvals, b.Tombstones}
	if err = ValidateOperationReferences(o, b.Identity.BoardID, b.Cards, rows); err != nil {
		t.Fatal("full detach", err)
	}
	encoded, err := Readable(o)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReadable[Operation](encoded)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := Canonical(decoded.Full.Intent)
	if err != nil || !bytes.Equal(canonical, frozen.CanonicalBytes()) {
		t.Fatal("original changed")
	}
	wrongOriginal := o
	wrongOriginal.ResultRefs = append([]FactRef{}, o.ResultRefs...)
	wrongOriginal.ResultRefs[0].ID = correctionID
	wrongResult, _ := json.Marshal(map[string]UUID{"cardId": *o.Target.CardID, "attachmentId": correctionID})
	wrongOriginal.Full = &FullOutcome{Intent: o.Full.Intent, Result: wrongResult}
	if Validate(wrongOriginal) == nil {
		t.Fatal("detach result disagrees with original")
	}
	withBytes := o
	withBytes.ByteFacts = []ByteFact{{SHA256: attachment.SHA256, ByteLength: attachment.ByteLength}}
	if Validate(withBytes) == nil {
		t.Fatal("detach byte fact accepted")
	}
	rows.Attachments = []Attachment{}
	when := attachment.CreatedAt
	rows.Tombstones = append([]EntityTombstone{}, rows.Tombstones...)
	rows.Tombstones = append(rows.Tombstones, EntityTombstone{Kind: "attachment", ID: attachment.AttachmentID, CardID: attachment.CardID, CreatedIn: attachment.CreatedIn, CreatedAt: &when, RemovedAt: correctionTime, Reason: "expired"})
	if ValidateOperationReferences(o, b.Identity.BoardID, b.Cards, rows) == nil {
		t.Fatal("full detach accepted tombstone")
	}
	o.Full = nil
	o.Removal = &Removal{Reason: "expired", RemovedAt: correctionTime, BoardRevision: Number(*o.CommittedRevision)}
	if err = ValidateOperationReferences(o, b.Identity.BoardID, b.Cards, rows); err != nil {
		t.Fatal("compact detach tombstone", err)
	}
}
