package v2

import (
	"errors"
	"slices"
	"testing"
	"time"
)

const (
	parentTestA = "10000000-0000-4000-8000-00000000000a"
	parentTestB = "10000000-0000-4000-8000-00000000000b"
	parentTestC = "10000000-0000-4000-8000-00000000000c"
	parentTestD = "10000000-0000-4000-8000-00000000000d"
)

func parentTestRef(id string) *string {
	return &id
}

func TestSetParentUpdatesDirectChildViews(t *testing.T) {
	state := &snapshot{Cards: []card{
		{ID: parentTestA},
		{ID: parentTestB, ParentID: parentTestRef(parentTestA), ChildIDs: []string{parentTestC}},
		{ID: parentTestC, ParentID: parentTestRef(parentTestB)},
		{ID: parentTestD, ParentID: parentTestRef(parentTestA), Archived: true},
	}}
	state.Cards[0].ChildIDs = childrenOf(state, parentTestA)

	parent := "10000000-0000-4000-8000-00000000000A"
	changed, err := setParent(state, parentTestC, &parent)
	if err != nil || !changed {
		t.Fatalf("setParent returned (%t, %v)", changed, err)
	}
	parent = parentTestB
	if got := state.Cards[2].ParentID; got == nil || *got != parentTestA {
		t.Fatalf("stored parent = %v, want %s", got, parentTestA)
	}
	if got := state.Cards[0].ChildIDs; !slices.Equal(got, []string{parentTestB, parentTestC, parentTestD}) {
		t.Fatalf("new parent's children = %v", got)
	}
	if got := state.Cards[1].ChildIDs; got == nil || len(got) != 0 {
		t.Fatalf("former parent's children = %v, want a non-nil empty list", got)
	}

	changed, err = setParent(state, parentTestC, parentTestRef(parentTestA))
	if err != nil || changed {
		t.Fatalf("repeating parent returned (%t, %v)", changed, err)
	}

	changed, err = setParent(state, parentTestC, nil)
	if err != nil || !changed || state.Cards[2].ParentID != nil {
		t.Fatalf("clearing parent returned (%t, %v), stored %v", changed, err, state.Cards[2].ParentID)
	}
	if got := state.Cards[0].ChildIDs; !slices.Equal(got, []string{parentTestB, parentTestD}) {
		t.Fatalf("remaining children = %v", got)
	}
}

func TestSetParentRefusesInvalidLinks(t *testing.T) {
	for _, test := range []struct {
		name     string
		parentID string
	}{
		{"self", parentTestA},
		{"descendant", parentTestC},
		{"missing", parentTestD},
		{"whitespace", " " + parentTestB},
		{"braces", "{" + parentTestB + "}"},
		{"wrong version", "10000000-0000-5000-8000-00000000000b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &snapshot{Cards: []card{
				{ID: parentTestA, ChildIDs: []string{parentTestB}},
				{ID: parentTestB, ParentID: parentTestRef(parentTestA), ChildIDs: []string{parentTestC}},
				{ID: parentTestC, ParentID: parentTestRef(parentTestB)},
			}}
			changed, err := setParent(state, parentTestA, &test.parentID)
			if err == nil || changed || state.Cards[0].ParentID != nil ||
				!slices.Equal(state.Cards[0].ChildIDs, []string{parentTestB}) {
				t.Fatalf("invalid parent returned (%t, %v) or changed the original link", changed, err)
			}
		})
	}
}

func TestSetParentRefusesExistingCycle(t *testing.T) {
	state := &snapshot{Cards: []card{
		{ID: parentTestA, ParentID: parentTestRef(parentTestB)},
		{ID: parentTestB, ParentID: parentTestRef(parentTestA)},
		{ID: parentTestC},
	}}
	if changed, err := setParent(state, parentTestC, parentTestRef(parentTestB)); err == nil || changed {
		t.Fatalf("joining an existing cycle returned (%t, %v)", changed, err)
	}
	if changed, err := setParent(state, parentTestA, nil); err == nil || changed {
		t.Fatalf("clearing a link in an existing cycle returned (%t, %v)", changed, err)
	}
}

// The fixture supplies cards without implying that the card HTTP routes exist.
func TestPatchParentChecksTagPermissionAndRecordsAudit(t *testing.T) {
	boardID := newID()
	actorID := newID()
	viewerID := newID()
	created := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
	now := created.Add(time.Second)
	state := &snapshot{
		Board: board{entity: newEntity(boardID, created), Revision: 4, UserIDs: []string{actorID, viewerID}},
		Policy: policy{ResourceGrants: []resourceGrant{
			{UserID: actorID, Capabilities: []string{"manage_relationships"}},
			{UserID: viewerID, Capabilities: []string{"read", "edit_card"}},
		}},
		Cards: []card{
			{ID: parentTestA, Revision: 1, CreatedAt: created, UpdatedAt: created, ChildIDs: []string{parentTestB}},
			{ID: parentTestB, Revision: 1, CreatedAt: created, UpdatedAt: created, ParentID: parentTestRef(parentTestA)},
			{ID: parentTestC, Revision: 1, CreatedAt: created, UpdatedAt: created, ChildIDs: []string{}},
			{ID: parentTestD, Revision: 1, CreatedAt: created, UpdatedAt: created, ChildIDs: []string{}},
		},
	}
	tag := state.Cards[1].entityTag()
	if changed, err := patchParent(state, parentTestB, tag, parentTestRef(parentTestC), viewerID, now); changed || !errors.Is(err, errForbidden) {
		t.Fatalf("unauthorized patch returned (%t, %v)", changed, err)
	}
	if changed, err := patchParent(state, parentTestB, etag(parentTestB, 2), parentTestRef(parentTestC), actorID, now); changed || !errors.Is(err, errStale) {
		t.Fatalf("stale patch returned (%t, %v)", changed, err)
	}
	if len(state.Activity) != 0 || *state.Cards[1].ParentID != parentTestA {
		t.Fatal("refused patch changed the relationship or audit")
	}
	if changed, err := patchParent(state, parentTestB, "W/"+etag(parentTestB, 2)+", "+tag, parentTestRef(parentTestC), actorID, now); !changed || err != nil {
		t.Fatalf("authorized patch returned (%t, %v)", changed, err)
	}
	for _, c := range state.Cards[:3] {
		if c.Revision != 5 || !c.UpdatedAt.Equal(now) {
			t.Fatalf("affected card %s has an incorrect tag or timestamp", c.ID)
		}
	}
	if got := state.Cards[3]; got.Revision != 1 || !got.UpdatedAt.Equal(created) {
		t.Fatal("unrelated card's version changed")
	}
	if len(state.Activity) != 1 {
		t.Fatalf("recorded %d activity events, want 1", len(state.Activity))
	}
	event := state.Activity[0]
	if event.BoardID != boardID || event.ActorID != actorID || event.ResourceID != parentTestB ||
		event.Operation != "patch" || event.ResourceType != "card" || event.Revision != 5 || !event.At.Equal(now) ||
		event.Field == nil || *event.Field != "parent_id" || event.Before == nil || *event.Before != parentTestA ||
		event.After == nil || *event.After != parentTestC {
		t.Fatal("parent patch did not record the expected audit event")
	}
	if changed, err := patchParent(state, parentTestB, state.Cards[1].entityTag(), parentTestRef(parentTestC), actorID, now); changed || err != nil || len(state.Activity) != 1 {
		t.Fatalf("no-op patch returned (%t, %v), activity count %d", changed, err, len(state.Activity))
	}
}

// Seed the transaction directly until M02 provides the public card resources.
func TestPatchParentCommitsAndClearsWithAudit(t *testing.T) {
	now := time.Now().UTC()
	actor := user{entity: newEntity(newID(), now), Label: "Parent editor", Kind: "human", Color: "#245083"}
	st, err := openStore(t.TempDir(), actor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.close(); err != nil {
			t.Error(err)
		}
	})

	boardID := newID()
	wf := defaultWorkflow(boardID, now, []user{actor})
	initial := freshSnapshot(boardID, "Parent test board", wf, actor.ID, now)
	initial.Board.DefaultOwnerID = actor.ID
	for i, id := range []string{parentTestA, parentTestB} {
		initial.Cards = append(initial.Cards, card{
			ID: id, Revision: 1, CreatedAt: canonicalTime(now), UpdatedAt: canonicalTime(now), BoardID: boardID, Ticket: uint64(i + 1), Title: id,
			OwnerID: actor.ID, LaneID: wf.Lanes[0].ID, PriorityID: wf.Policy.DefaultPriorityID, ChildIDs: []string{},
		})
	}
	if err := st.create(initial); err != nil {
		t.Fatal(err)
	}

	update := func(boardTag, cardTag string, parentID *string) (snapshot, error) {
		return st.update(boardID, boardTag, func(candidate *snapshot) error {
			_, err := patchParent(candidate, parentTestB, cardTag, parentID, actor.ID, now)
			return err
		})
	}
	updated, err := update(initial.Board.ETag, initial.Cards[1].entityTag(), parentTestRef(parentTestA))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Board.Revision != 2 || !slices.Equal(updated.Cards[0].ChildIDs, []string{parentTestB}) {
		t.Fatal("parent relation and board revision did not commit together")
	}
	if _, err := update(updated.Board.ETag, initial.Cards[1].entityTag(), nil); !errors.Is(err, errStale) {
		t.Fatalf("stale clear returned %v", err)
	}
	if _, err := update(updated.Board.ETag, updated.Cards[1].entityTag(), nil); err != nil {
		t.Fatal(err)
	}
	durable, err := st.durable(boardID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Board.Revision != 3 || durable.Cards[1].ParentID != nil || len(durable.Cards[0].ChildIDs) != 0 {
		t.Fatal("cleared relation did not commit")
	}
	if len(durable.Activity) != 3 || durable.Activity[1].Before != nil ||
		durable.Activity[1].After == nil || *durable.Activity[1].After != parentTestA ||
		durable.Activity[2].Before == nil || *durable.Activity[2].Before != parentTestA ||
		durable.Activity[2].After != nil {
		t.Fatal("durable activity does not contain both parent changes")
	}
}
