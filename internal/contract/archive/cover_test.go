package archive

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

func TestCoverReferencesUseExactCardAndRevision(t *testing.T) {
	m := fixture[Manifest](t, "package/manifest.json")
	b := fixture[Board](t, "package/board.json")
	h := fixture[History](t, "package/history.json")
	a := fixture[Activity](t, "package/activity.json")
	o := fixture[Outcomes](t, "package/outcomes.json")
	c, s := &b.Cards[0], &h.Records[0]
	currentCover, historicalCover := c.AttachmentIDs[0], s.References.AttachmentIDs[0]
	c.CoverAttachmentID, s.State.CoverAttachmentID = &currentCover, &historicalCover
	m = refreshProjectionInventory(t, m, b, h, a, o)
	if _, err := ValidateDocuments(m, b, h, a, o); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, failure string
		change        func(*Board)
	}{
		{
			name: "historical row owned by another card", failure: "attachment reference ownership",
			change: func(board *Board) {
				board.Cards[0].AttachmentIDs = []UUID{currentCover}
				board.Attachments[1].CardID = "00000000-0000-4000-8000-000000000099"
			},
		},
		{
			name: "historical full row missing", failure: "unresolved attachment reference",
			change: func(board *Board) {
				board.Cards[0].AttachmentIDs = []UUID{currentCover}
				board.Attachments = board.Attachments[:1]
			},
		},
		{
			name: "historical cover available only as a tombstone", failure: "unresolved attachment reference",
			change: func(board *Board) {
				attachment := board.Attachments[1]
				board.Attachments = board.Attachments[:1]
				board.Cards[0].AttachmentIDs = []UUID{currentCover}
				board.Tombstones = []EntityTombstone{{
					Kind: "attachment", ID: attachment.AttachmentID, CardID: attachment.CardID,
					CreatedIn: attachment.CreatedIn, CreatedAt: &attachment.CreatedAt,
					RemovedAt: correctionTime, Reason: "expired",
				}}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			broken := b
			broken.Cards = slices.Clone(b.Cards)
			broken.Attachments = slices.Clone(b.Attachments)
			test.change(&broken)
			brokenManifest := refreshProjectionInventory(t, m, broken, h, a, o)
			if _, err := ValidateDocuments(brokenManifest, broken, h, a, o); err == nil || err.Error() != test.failure {
				t.Fatalf("expected %q, got %v", test.failure, err)
			}
		})
	}

	s.State.CoverAttachmentID = &currentCover
	if err := Validate(*s); err == nil {
		t.Fatal("current attachment substituted for historical membership")
	}
	s.State.CoverAttachmentID = &historicalCover
	c.CoverAttachmentID = new(UUID("00000000-0000-4000-8000-000000000099"))
	if Validate(*c) == nil {
		t.Fatal("absent current attachment accepted")
	}
	c.CoverAttachmentID = &currentCover

	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decode[Card](raw); err != nil {
		t.Fatal(err)
	}
	nullCover := bytes.Replace(raw, []byte(`"coverAttachmentId":"`+string(currentCover)+`"`), []byte(`"coverAttachmentId":null`), 1)
	if _, err = Decode[Card](nullCover); err == nil {
		t.Fatal("stored null cover accepted")
	}
}

func TestCoverClearRefusedOriginalRefreezes(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	for _, op := range fixture[Outcomes](t, "package/outcomes.json").Operations {
		if op.TerminalClass != "NOT_COMMITTED" || op.Full == nil {
			continue
		}
		cardID := b.Cards[0].CardID
		rev := Positive(1)
		op.Command = "card.edit"
		op.ByteFacts = []ByteFact{}
		op.Target = &Target{CardID: &cardID}
		op.ExpectedCardRevision = &rev
		wire, err := json.Marshal(map[string]any{
			"opId": op.OpID, "incarnation": op.Origin.Incarnation, "target": op.Target,
			"expectedRevision": op.ExpectedRevision, "expectedCardRevision": rev,
			"input": map[string]any{"coverAttachmentId": nil},
		})
		if err != nil {
			t.Fatal(err)
		}
		frozen, err := contract.Freeze(wire, contract.ResolvedRoute{Command: "card.edit", BoardID: string(op.Origin.BoardID), TargetID: string(cardID)}, contract.AuthenticatedActor{ActorID: string(op.ActorID)})
		if err != nil {
			t.Fatal(err)
		}
		op.Fingerprint = Digest(frozen.Fingerprint())
		op.Full.Intent = frozen.CanonicalBytes()
		raw, err := Readable(op)
		if err != nil {
			t.Fatal(err)
		}
		read, err := DecodeReadable[Operation](raw)
		if err != nil {
			t.Fatal(err)
		}
		intent, err := Canonical(read.Full.Intent)
		if err != nil || !bytes.Equal(intent, frozen.CanonicalBytes()) {
			t.Fatal("refused clear lost its exact original", err)
		}
		return
	}
	t.Fatal("fixture lacks a full refused operation")
}
