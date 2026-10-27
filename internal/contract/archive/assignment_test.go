package archive

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAssignmentArchiveLegacyAndNullCompatibility(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	c := b.Cards[0]
	c.OwnerID = nil
	legacy, err := Canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacy, []byte(`"ownerId"`)) {
		t.Fatal("unassigned changes legacy bytes")
	}
	decoded, err := Decode[Card](legacy)
	if err != nil || decoded.OwnerID != nil {
		t.Fatal("legacy decode", err)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(legacy, &fields)
	fields["ownerId"] = json.RawMessage("null")
	explicit, _ := json.Marshal(fields)
	decoded, err = Decode[Card](explicit)
	if err != nil || decoded.OwnerID != nil {
		t.Fatal("explicit null decode", err)
	}
	owner := b.Actors[0].ActorID
	c.OwnerID = &owner
	assigned, err := Canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = Decode[Card](assigned)
	if err != nil || decoded.OwnerID == nil || *decoded.OwnerID != owner {
		t.Fatal("assigned decode", err)
	}
	state := SnapshotState{Ticket: c.Ticket, Title: c.Title, Description: c.Description, Priority: c.Priority, Lane: c.Lane, Archived: c.Archived, OwnerID: &owner}
	raw, err := Canonical(state)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Decode[SnapshotState](raw)
	if err != nil || restored.OwnerID == nil || *restored.OwnerID != owner {
		t.Fatal("history assignment decode", err)
	}
}

func TestAssignmentCurrentAndHistoryReferences(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	rows := Rows{Actors: b.Actors, Comments: []Comment{}, Links: []Link{}, Attachments: []Attachment{}, Approvals: []Approval{}, Tombstones: []EntityTombstone{}}
	owner := b.Actors[0].ActorID
	state := &SnapshotState{Ticket: 1, Title: "Synthetic", Description: "", Priority: "P3", Lane: "Ready for Work", OwnerID: &owner}
	s := Snapshot{Origin: b.Identity, CardID: b.Cards[0].CardID, CardRevision: 1, CommittedAt: "2026-10-25T00:00:00Z", ActorID: owner, State: state, References: SnapshotReferences{CommentIDs: []UUID{}, LinkIDs: []UUID{}, AttachmentIDs: []UUID{}, ApprovalIDs: []UUID{}}}
	if err := ValidateHistoryReferences(b.Identity, s.CardID, 2, []Snapshot{s}, rows); err != nil {
		t.Fatal(err)
	}
	missing := correctionID
	state.OwnerID = &missing
	if err := ValidateHistoryReferences(b.Identity, s.CardID, 2, []Snapshot{s}, rows); err == nil {
		t.Fatal("unresolved historical assignee accepted")
	}
}
