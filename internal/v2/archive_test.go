package v2

import (
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestM06ArchiveAndRestoreHTTP(t *testing.T) {
	app, human := eventHTTPServer(t)
	bot := human
	bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	id, now := newID(), canonicalTime(time.Now())
	wf := defaultWorkflow(id, now, app.principals)
	for i := range wf.Policy.ResourceGrants {
		grant := &wf.Policy.ResourceGrants[i]
		remove := "archive_card"
		if grant.UserID == humanID {
			remove = "edit_card"
		}
		grant.Capabilities = slices.DeleteFunc(grant.Capabilities, func(value string) bool { return value == remove })
	}
	initial := freshSnapshot(id, "Archive fixture", wf, humanID, now)
	if err := app.store.create(initial); err != nil {
		t.Fatal(err)
	}

	makeCard := func(tag, parent string) (card, string) {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Fixture"%s}`, id, tag, wf.Lanes[0].ID, parent)
		data, headers := human.call("POST", "/cards", body, "", 201)
		return turnOneDecode[card](t, data), headers.Get("X-Board-ETag")
	}
	parent, tag := makeCard(initial.Board.entityTag(), "")
	child, boardTag := makeCard(tag, fmt.Sprintf(`,"parent_id":%q`, parent.ID))
	path := "/cards/" + child.ID
	for _, input := range []string{`null`, `"true"`, `0`, `{}`, `[]`} {
		human.call("PATCH", path, `{"archived":`+input+`}`, child.entityTag(), 400)
	}
	human.call("PATCH", path, `{"archived":true}`, "", 428)
	bot.call("PATCH", path, `{"archived":true}`, child.entityTag(), 403)
	bot.call("PATCH", path, `{"archived":false}`, child.entityTag(), 403)
	bot.call("PATCH", path, `{"title":"Must roll back","archived":true}`, child.entityTag(), 403)

	checkState := func(current card, archived bool) {
		t.Helper()
		expected := child
		expected.Archived = archived
		expected.UpdatedAt = current.UpdatedAt
		expected.Revision = current.Revision
		if !reflect.DeepEqual(current, expected) {
			t.Fatal("archive operation changed card content, placement, or links")
		}
	}
	raw, headers := human.call("PATCH", path, `{"archived":true}`, child.entityTag(), 200)
	hidden := turnOneDecode[card](t, raw)
	checkState(hidden, true)
	archiveBoardTag := headers.Get("X-Board-ETag")
	if headers.Get("ETag") != hidden.entityTag() || hidden.entityTag() == child.entityTag() || archiveBoardTag == "" || archiveBoardTag == boardTag {
		t.Fatal("archive did not advance both entity tags")
	}
	raw, headers = human.call("PATCH", path, `{"archived":true}`, hidden.entityTag(), 200)
	if turnOneDecode[card](t, raw).entityTag() != hidden.entityTag() || headers.Get("X-Board-ETag") != archiveBoardTag {
		t.Fatal("archive no-op advanced a version")
	}
	human.call("PATCH", path, `{"archived":false}`, child.entityTag(), 412)
	raw, _ = human.call("GET", path, "", "", 200)
	checkState(turnOneDecode[card](t, raw), true)
	raw, _ = human.call("GET", "/cards/"+parent.ID, "", "", 200)
	if got := turnOneDecode[card](t, raw); !slices.Equal(got.ChildIDs, []string{child.ID}) {
		t.Fatal("archive detached the parent relationship")
	}
	for _, tc := range []struct {
		filter string
		want   []string
	}{
		{"", []string{parent.ID}},
		{"&archived=exclude", []string{parent.ID}},
		{"&archived=include", []string{parent.ID, child.ID}},
		{"&archived=ONLY", []string{child.ID}},
		{"&ticket=2", nil},
		{"&ticket=2&archived=only", []string{child.ID}},
	} {
		raw, _ := human.call("GET", "/cards?board_id="+id+tc.filter, "", "", 200)
		var ids []string
		for _, item := range turnOnePage[card](t, raw, "cards").Values {
			ids = append(ids, item.ID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Fatalf("archive filter %q returned %v, expected %v", tc.filter, ids, tc.want)
		}
	}

	raw, _ = human.call("GET", "/snapshots/"+id, "", "", 200)
	archivedSnapshot := turnOneDecode[snapshot](t, raw)
	checkArchiveAudit(t, archivedSnapshot.Activity[len(archivedSnapshot.Activity)-1], child.ID, 4, "false", "true")
	raw, headers = human.call("PATCH", path, `{"archived":false}`, hidden.entityTag(), 200)
	restored := turnOneDecode[card](t, raw)
	checkState(restored, false)
	if headers.Get("ETag") != restored.entityTag() || restored.entityTag() == hidden.entityTag() ||
		headers.Get("X-Board-ETag") == "" || headers.Get("X-Board-ETag") == archiveBoardTag {
		t.Fatal("restore did not advance both entity tags")
	}
	raw, _ = human.call("GET", "/cards?board_id="+id+"&ticket=2", "", "", 200)
	if rows := turnOnePage[card](t, raw, "cards").Values; len(rows) != 1 || rows[0].ID != child.ID {
		t.Fatal("restored card did not reappear under its original ticket")
	}

	raw, _ = human.call("GET", "/snapshots/"+id, "", "", 200)
	durable := turnOneDecode[snapshot](t, raw)
	if durable.Board.Revision != 5 || len(durable.Activity) != 6 {
		t.Fatal("unexpected audit or revisions after archive and restore")
	}
	checkArchiveAudit(t, durable.Activity[4], child.ID, 4, "false", "true")
	checkArchiveAudit(t, durable.Activity[5], child.ID, 5, "true", "false")

	for _, test := range []struct {
		name   string
		mutate func(*snapshot)
	}{
		{"unaudited archive", func(s *snapshot) { s.Cards[0].Archived = true }},
		{"archived card with no creation audit", func(s *snapshot) {
			added := s.Cards[0]
			added.ID = newID()
			added.Ticket = 3
			added.ChildIDs = []string{}
			added.Archived = true
			s.Cards = append(s.Cards, added)
		}},
		{"non-boolean before", func(s *snapshot) {
			nonBoolean := "False"
			s.Activity[4].Before = &nonBoolean
		}},
		{"wrong initial state", func(s *snapshot) {
			s.Activity[4].Before = archiveAuditValue(true)
			s.Activity[4].After = archiveAuditValue(false)
		}},
		{"discontinuous restore", func(s *snapshot) {
			s.Activity[5].Before = archiveAuditValue(false)
			s.Activity[5].After = archiveAuditValue(true)
			s.Cards[1].Archived = true
		}},
		{"unauthorized audit actor", func(s *snapshot) { s.Activity[5].ActorID = botID }},
	} {
		t.Run(test.name, func(t *testing.T) {
			tampered := cloneSnapshot(durable)
			test.mutate(&tampered)
			if err := validateSnapshot(tampered, id); err == nil {
				t.Fatal("snapshot accepted invalid archive history")
			}
		})
	}
}

func checkArchiveAudit(t *testing.T, event activity, id string, revision uint64, before, after string) {
	t.Helper()
	if event.ActorID != humanID || event.ResourceID != id || event.Revision != revision ||
		event.ResourceType != "card" || event.Operation != "patch" || snapshotString(event.Field) != "archived" ||
		snapshotString(event.Before) != before || snapshotString(event.After) != after {
		t.Fatalf("incorrect archive audit at revision %d", revision)
	}
}

func TestM06RestoreRefusesRetiredLane(t *testing.T) {
	now := canonicalTime(time.Now())
	state := snapshot{
		Lanes: []lane{{ID: "retired", Revision: 1, Retired: true}},
		Policy: policy{ResourceGrants: []resourceGrant{
			{UserID: humanID, Capabilities: []string{"archive_card"}},
		}},
	}
	state.Board.UserIDs = []string{humanID}
	item := card{LaneID: "retired", Archived: true}
	changed, err := applyCardArchive(&state, &item, false, humanID, now)
	var field invalidField
	if changed || !errors.As(err, &field) || field.Field != "archived" || !item.Archived || len(state.Activity) != 0 {
		t.Fatal("restore into a retired lane was not refused before changing state")
	}
}
