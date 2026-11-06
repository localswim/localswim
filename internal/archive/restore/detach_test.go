package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func detachedMembers(t *testing.T) map[string][]byte {
	t.Helper()
	members := fixture(t)
	b, err := A.DecodeReadable[A.Board](members["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	h, err := A.DecodeReadable[A.History](members["history.json"])
	if err != nil {
		t.Fatal(err)
	}
	a, err := A.DecodeReadable[A.Activity](members["activity.json"])
	if err != nil {
		t.Fatal(err)
	}
	o, err := A.DecodeReadable[A.Outcomes](members["outcomes.json"])
	if err != nil {
		t.Fatal(err)
	}
	m, err := A.DecodeReadable[A.Manifest](members["manifest.json"])
	if err != nil {
		t.Fatal(err)
	}
	card := b.Cards[0]
	attachment := card.AttachmentIDs[0]
	prior := o.Operations[0]
	h.Records = append(h.Records, A.Snapshot{Origin: prior.Origin, CardID: card.CardID, CardRevision: card.Revision, CommittedAt: prior.TerminalAt, ActorID: prior.ActorID, State: &A.SnapshotState{Ticket: card.Ticket, Title: card.Title, Description: card.Description, Priority: card.Priority, Lane: card.Lane, Archived: card.Archived, CurrentApprovalID: card.CurrentApprovalID}, References: A.SnapshotReferences{CommentIDs: card.CommentIDs, LinkIDs: card.LinkIDs, AttachmentIDs: card.AttachmentIDs, ApprovalIDs: []A.UUID{}}})
	sort.Slice(h.Records, func(i, j int) bool {
		x, y := h.Records[i], h.Records[j]
		if x.Origin.BoardID != y.Origin.BoardID {
			return x.Origin.BoardID < y.Origin.BoardID
		}
		if x.Origin.Incarnation != y.Origin.Incarnation {
			return x.Origin.Incarnation < y.Origin.Incarnation
		}
		if x.CardID != y.CardID {
			return x.CardID < y.CardID
		}
		return x.CardRevision < y.CardRevision
	})
	b.Revision++
	b.Cards[0].Revision++
	b.Cards[0].AttachmentIDs = append([]A.UUID{}, card.AttachmentIDs[1:]...)
	op := prior
	op.OpID = "90000000-0000-4000-8000-000000000001"
	op.Command = "attachment.detach"
	op.ExpectedRevision = A.Number(*prior.CommittedRevision)
	op.ExpectedCardRevision = &card.Revision
	revision := A.Positive(b.Revision)
	cardRevision := b.Cards[0].Revision
	op.CommittedRevision = &revision
	op.CommittedCardRevision = &cardRevision
	op.TerminalAt = "2026-10-26T00:00:00Z"
	op.ByteFacts = []A.ByteFact{}
	cardFact := A.Number(cardRevision)
	op.ResultRefs = []A.FactRef{{Kind: "attachment", ID: attachment}, {Kind: "card", ID: card.CardID, Revision: &cardFact}}
	wire, _ := json.Marshal(map[string]any{"opId": op.OpID, "incarnation": op.Origin.Incarnation, "target": op.Target, "expectedRevision": op.ExpectedRevision, "expectedCardRevision": op.ExpectedCardRevision, "input": map[string]any{"attachmentId": attachment}})
	frozen, err := contract.Freeze(wire, contract.ResolvedRoute{Command: "attachment.detach", BoardID: string(op.Origin.BoardID), TargetID: string(card.CardID)}, contract.AuthenticatedActor{ActorID: string(op.ActorID)})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(map[string]A.UUID{"cardId": card.CardID, "attachmentId": attachment})
	op.Full = &A.FullOutcome{Intent: frozen.CanonicalBytes(), Result: result}
	op.Fingerprint = A.Digest(frozen.Fingerprint())
	o.Operations = append(o.Operations, op)
	eventID := A.Positive(1)
	for _, event := range a.Events {
		if event.EventID >= eventID {
			eventID = event.EventID + 1
		}
	}
	a.Events = append(a.Events, A.Event{Origin: op.Origin, EventID: eventID, ActorID: op.ActorID, OpID: &op.OpID, Type: "attachment.detach", BoardRevision: b.Revision, At: op.TerminalAt, Affected: op.ResultRefs})
	h.Revision = b.Revision
	a.Revision = b.Revision
	o.Revision = b.Revision
	m.Revision = b.Revision
	for name, value := range map[string]any{"board.json": b, "history.json": h, "activity.json": a, "outcomes.json": o, "manifest.json": m} {
		members[name], err = A.Readable(value)
		if err != nil {
			t.Fatal(name, err)
		}
	}
	return members
}

func TestDetachPreparedRestorePreservesFullRowsPinsAndOriginals(t *testing.T) {
	source := sourcePackage(t, detachedMembers(t))
	board, _ := decode[A.Board](source, "board.json")
	history, _ := decode[A.History](source, "history.json")
	outcomes, _ := decode[A.Outcomes](source, "outcomes.json")
	prepared, err := prepare(context.Background(), source, testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.board.Attachments, board.Attachments) || !reflect.DeepEqual(prepared.board.Cards, board.Cards) || !reflect.DeepEqual(prepared.history.Records, history.Records) {
		t.Fatal("detached rows or historical references changed")
	}
	found := false
	for _, group := range prepared.outcomes.SourceArchives {
		if group.Identity == outcomes.Identity {
			before, _ := A.Canonical(outcomes.Operations)
			after, _ := A.Canonical(group.Operations)
			found = bytes.Equal(before, after)
		}
	}
	if !found {
		t.Fatal("detach original or add byte facts changed")
	}
}
