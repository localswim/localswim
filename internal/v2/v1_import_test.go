package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const v1FixturePNG = "\x89PNG\r\n\x1a\n"
const v1FixtureText = "Text attachment\n"
const v1FixturePNGID = "01519f3c-5683-4bc2-a970-854f6013e20d"
const v1FixtureTextID = "b180a00f-4580-4ce5-aa1c-39f2ec5cf207"
const v1FixtureDetachedID = "16dfd35a-e940-48d9-b4ee-4e906b855a51"

func v1ReplayFixture() v1BoardExport {
	const created = "2025-11-01T12:00:00.123456-04:00"
	const completed = "2025-11-03T12:00:00.765432-05:00"
	return v1BoardExport{Schema: 6, Revision: 14, Project: " Réplay ", Port: 8792, NextTicket: 50,
		BrowserUser: "terry", CLIUser: "agent", DefaultOwner: "agent",
		Users: []v1User{{"terry", "Terry", "human", "#2266AA"}, {"agent", "Bot", "bot", "#AA6622"}, {"observer", "Reviewer", "human", "#CC1155"}},
		Policy: v1Policy{Schema: 7, Note: "V1 policy note", DefaultPriority: new("P3"), Priorities: []v1Priority{{"P1", "Urgent"}, {"P3", "Normal"}},
			Lanes: []v1Lane{
				{ID: "backlog", Label: "Backlog", Note: "  Intake ", Create: []string{"terry", "agent", "agent"}, Reorder: true},
				{ID: "ready_for_review", Label: "Review", Create: []string{}, ApprovalEnabled: true, ApprovalMessage: "  Approved from Review.  "},
				{ID: "old", Label: "Former lane", Create: []string{}},
				{ID: "completed", Label: "Done", Create: []string{}},
			}, RetiredLanes: []string{"old"}, Edges: map[string][]v1Edge{
				"terry": {{From: "backlog", To: "ready_for_review"}, {From: "ready_for_review", To: "completed"}},
				"agent": {{From: "backlog", To: "ready_for_review", Description: "V1 grant annotation"}},
			}},
		Items: []v1Item{
			{ID: "last", Ticket: 1, State: "backlog", Priority: "P3", Subject: "Last", Detail: new(""), Owner: "agent"},
			{ID: "middle", Ticket: 2, State: "backlog", Priority: "P3", Subject: "Middle", Detail: new("Description\n"), Owner: "observer", Parent: "free",
				Comments: []v1Comment{{created, "agent", "  Garder #0002 exactement. \n"}, {completed, "observer", "Reviewed"}}},
			{ID: "free", Ticket: 5, OriginTicket: 42, State: "backlog", Priority: "P3", Subject: "  Free #0005  ", Detail: new("Résumé\n"), Owner: "terry",
				CoverAttachment: v1FixturePNGID, Attachments: []v1Attachment{
					{ID: v1FixturePNGID, Name: "Image.PNG", Size: int64(len(v1FixturePNG)), MediaType: "image/png",
						SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(v1FixturePNG))), At: created, By: "agent"},
					{ID: v1FixtureTextID, Name: "file.İ", Size: int64(len(v1FixtureText)), MediaType: "application/octet-stream",
						SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(v1FixtureText))), At: completed, By: "terry"},
				}, AttachmentHistory: []v1AttachmentEvent{
					{At: created, By: "agent", Action: "attached", AttachmentID: v1FixturePNGID, Extension: ".png"},
					{At: created, By: "agent", Action: "cover_set", AttachmentID: v1FixturePNGID, Extension: ".png"},
					{At: completed, By: "terry", Action: "attached", AttachmentID: v1FixtureTextID, Extension: ".bin"},
					{At: completed, By: "terry", Action: "attached", AttachmentID: v1FixtureDetachedID, Extension: ".zip"},
					{At: completed, By: "terry", Action: "attachment_removed", AttachmentID: v1FixtureDetachedID, Extension: ".zip"},
				}},
			{ID: "retired", Ticket: 25, State: "old", Priority: "P3", Subject: "Archived in retired lane", Detail: new(""), Owner: "agent", Archived: true},
			{ID: "root", Ticket: 30, State: "completed", Priority: "P3", Subject: "Completed blocker", Detail: new(""), Owner: "terry", Archived: true,
				History: []v1CardHistory{{At: created, By: "terry", To: "backlog"},
					{At: completed, By: "terry", From: new("backlog"), To: "ready_for_review"},
					{At: completed, By: "terry", From: new("ready_for_review"), To: "completed"}}},
		}, Links: []v1Link{{From: "root", To: "middle", Kind: "blocks"}, {From: "middle", To: "last", Kind: "blocks"},
			{From: "free", To: "last", Kind: "references"}},
	}
}

func v1FixtureJSON(t *testing.T, fixture v1BoardExport) *bytes.Reader {
	t.Helper()
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(data)
}

func v1FixtureFiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "0042")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{v1FixturePNGID + ".png": v1FixturePNG, v1FixtureTextID + ".bin": v1FixtureText} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestV1ImportPreservesContentAndContinuesAllocation(t *testing.T) {
	root, attachments := t.TempDir(), v1FixtureFiles(t)
	fixture := v1ReplayFixture()
	result, err := ImportV1(root, attachments, "", v1FixtureJSON(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if !validSnapshotID(result.BoardID) || result.DetachedAttachmentsOmitted != 1 || result.CompletedDatesDefaulted != 0 || result.PolicyAnnotationsOmitted != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Cards) != 5 {
		t.Fatalf("wrong number of mappings: %v", result.Cards)
	}
	for index, oldTicket := range []uint64{5, 25, 30, 2, 1} {
		item := result.Cards[index]
		if item.OldTicket != oldTicket || item.NewTicket != uint64(index+1) || !validSnapshotID(item.NewCardID) {
			t.Fatalf("incorrect dependency replay order: %v", result.Cards)
		}
	}
	path := filepath.Join(root, "boards", result.BoardID+".json")
	state, err := readSnapshot(path, result.BoardID)
	if err != nil {
		t.Fatal(err)
	}
	assertV1ReplayContent(t, state, fixture, result, root)
	assertV1ReplayAudit(t, state)
	testV1ReplayContinues(t, root, state)
}

func assertV1ReplayContent(t *testing.T, state snapshot, fixture v1BoardExport, result V1ImportResult, root string) {
	t.Helper()
	if state.Board.Revision != result.Revision || state.Board.Name != fixture.Project || state.Board.DefaultOwnerID != botID ||
		state.Board.ActorPresentation.Human.Label != "Terry" || state.Policy.DefaultPriorityID != state.Priorities[1].ID ||
		state.Lanes[0].Notes != "  Intake " || !state.Lanes[0].ReorderEnabled || !state.Lanes[1].ApprovalEnabled ||
		state.Lanes[1].BulkApprovalComment != "Approved from Review." || !state.Lanes[2].Retired || len(state.Policy.MoveGrants) != 3 ||
		len(state.Policy.CreateGrants) != 2 {
		t.Fatal("board and workflow content was not preserved")
	}
	free, retired, completed, middle := state.Cards[0], state.Cards[1], state.Cards[2], state.Cards[3]
	if free.Title != fixture.Items[2].Subject || free.Description != *fixture.Items[2].Detail || free.OwnerID != humanID ||
		!retired.Archived || retired.LaneID != state.Lanes[2].ID || !completed.Archived || completed.LaneID != state.Lanes[3].ID ||
		completed.CompletedAt == nil || completed.CompletedAt.Format(timestampLayout) != "2025-11-03 17:00:00+00:00" ||
		middle.ParentID == nil || *middle.ParentID != free.ID || !slices.Equal(free.ChildIDs, []string{middle.ID}) {
		t.Fatal("card text, archive, completion, or hierarchy was not preserved")
	}
	if len(state.Comments) != 2 || state.Comments[0].AuthorID != botID || state.Comments[0].Text != fixture.Items[1].Comments[0].Text ||
		state.Comments[0].CreatedAt.Format(timestampLayout) != "2025-11-01 16:00:00+00:00" || state.Comments[1].AuthorID != middle.OwnerID {
		t.Fatal("comment text, source timestamp or attribution was not preserved")
	}
	if len(state.Relationships) != 3 || state.Relationships[0].SourceCardID != completed.ID ||
		state.Relationships[0].TargetCardID != middle.ID || state.Relationships[2].Kind != "references" {
		t.Fatal("relationship content was not preserved")
	}
	if len(state.Attachments) != 2 || free.CoverAttachmentID == nil || *free.CoverAttachmentID != state.Attachments[0].ID ||
		state.Attachments[0].AddedBy != botID || state.Attachments[0].OriginalName != "Image.PNG" || state.Attachments[1].OriginalName != "file.İ" ||
		state.Attachments[1].Previewable {
		t.Fatal("attachment metadata or cover was not preserved")
	}
	for index, want := range []string{v1FixturePNG, v1FixtureText} {
		if data, err := readVerifiedAttachment(root, state.Attachments[index]); err != nil || string(data) != want {
			t.Fatalf("attachment %d bytes did not survive: %v", index, err)
		}
	}
}

func assertV1ReplayAudit(t *testing.T, state snapshot) {
	t.Helper()
	changed := cloneSnapshot(state)
	link := &changed.Relationships[0]
	link.SourceCardID, link.TargetCardID = link.TargetCardID, link.SourceCardID
	for index, event := range changed.Activity {
		if event.ResourceID == link.ID {
			changed.Activity[index].After = new(relationshipAuditValue(link.relationshipLink))
		}
	}
	if err := validateSnapshot(changed, state.Board.ID); err == nil || !strings.Contains(err.Error(), "earlier ticket allocation") {
		t.Fatalf("verifier accepted reversed imported blocker order: %v", err)
	}
	if _, err := freshRecoverySnapshot(state); err != nil {
		t.Fatalf("explicit fresh recovery could not remap imported lineage: %v", err)
	}
}

func testV1ReplayContinues(t *testing.T, root string, state snapshot) {
	t.Helper()
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)
	client := turnOneClient{t, server.URL, human, server.Client()}
	free := state.Cards[0]
	laneID := state.Lanes[0].ID
	create := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"After replay","relationships":[{"target_card_id":%q,"kind":"blocks"}]}`,
		state.Board.ID, state.Board.entityTag(), laneID, free.ID)
	raw, _ := client.call("POST", "/cards", create, "", 201)
	created := turnOneDecode[card](t, raw)
	if created.Ticket != 6 {
		t.Fatalf("next ticket is %d, want 6", created.Ticket)
	}
	path := "/ticket-orders/" + laneID
	raw, headers := client.call("GET", path+"?preview=true&card_id="+free.ID+"&card_id="+created.ID, "", "", 200)
	preview := turnOneDecode[ticketOrder](t, raw)
	if !preview.Changed || len(preview.Proposed) != 2 || preview.Proposed[0] != (ticketOrderEntry{created.ID, 1}) {
		t.Fatal("native preview did not place the new blocker first")
	}
	reorder := ticketOrderBody(t, state.Board.ID, headers.Get("X-Board-ETag"), []string{preview.Proposed[0].CardID, preview.Proposed[1].CardID})
	raw, _ = client.call("PUT", path, reorder, "", 200)
	ordered := turnOneDecode[ticketOrder](t, raw)
	if !ordered.Changed || ordered.Proposed[0] != (ticketOrderEntry{created.ID, 1}) {
		t.Fatal("native reorder could not use imported allocation history")
	}
	newer, ok := app.store.accepted(state.Board.ID)
	if !ok {
		t.Fatal("newer board state was not accepted")
	}
	assertV1RestoreProvenance(t, app.store, newer, state)
}

func assertV1RestoreProvenance(t *testing.T, s *store, current, original snapshot) {
	t.Helper()
	changed := cloneSnapshot(original)
	completed := &changed.Cards[2]
	completed.CompletedAt = new(completed.CompletedAt.Add(time.Hour))
	for index, event := range changed.Activity {
		if event.ResourceID != completed.ID || event.Operation != "create" {
			continue
		}
		dates, err := marshalCanonical(v1ImportedDates{CreatedAt: completed.CreatedAt, CompletedAt: completed.CompletedAt})
		if err != nil {
			t.Fatal(err)
		}
		changed.Activity[index].After = new(string(dates))
	}
	if err := validateSnapshot(changed, original.Board.ID); err != nil {
		t.Fatalf("changed candidate should be self-consistent before checking trusted lineage: %v", err)
	}
	tag := matchingBoardTag(current.Board.entityTag())
	if _, err := s.recoverSnapshot(original.Board.ID, humanID, newOperationID(), tag, changed, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "accepted V1 import history") {
		t.Fatalf("same-ID restore accepted a change to imported provenance: %v", err)
	}
	changed = cloneSnapshot(original)
	changed.Cards[0].LaneID = original.Lanes[1].ID
	changed.Board.Attention, changed.Board.LastCardEditAt = deriveAttention(changed)
	if err := validateSnapshot(changed, original.Board.ID); err != nil {
		t.Fatalf("changed lane candidate should be self-consistent before checking trusted lineage: %v", err)
	}
	if _, err := s.recoverSnapshot(original.Board.ID, humanID, newOperationID(), tag, changed, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "accepted V1 import history") {
		t.Fatalf("same-ID restore accepted a change to an imported card's original lane: %v", err)
	}
	rolledBack, err := s.recoverSnapshot(original.Board.ID, humanID, newOperationID(), tag, original, nil, nil)
	if err != nil || len(rolledBack.Cards) != len(original.Cards) || rolledBack.Board.Revision <= current.Board.Revision {
		t.Fatalf("restore of an earlier captured imported revision failed: %v", err)
	}
}

func TestV1ImportRefusesBadInputBeforeDestination(t *testing.T) {
	attachments := v1FixtureFiles(t)
	for _, test := range []struct {
		name string
		edit func(*v1BoardExport)
		want string
	}{
		{"cycle", func(board *v1BoardExport) { board.Links = append(board.Links, v1Link{"last", "root", "blocks"}) }, "blocking cycle"},
		{"dangling", func(board *v1BoardExport) { board.Links[0].To = "missing" }, "invalid V1"},
		{"parent cycle", func(board *v1BoardExport) { board.Items[2].Parent = "middle" }, "parent links contain a cycle"},
		{"digest", func(board *v1BoardExport) { board.Items[2].Attachments[1].SHA256 = strings.Repeat("0", 64) }, "SHA-256"},
		{"bad recorded extension", func(board *v1BoardExport) { board.Items[2].AttachmentHistory[2].Extension = "../file" }, "valid extension"},
		{"future attachment", func(board *v1BoardExport) { board.Items[2].Attachments[1].At = "9999-01-01T00:00:00Z" }, "after the import clock"},
		{"future creation", func(board *v1BoardExport) { board.Items[4].History[0].At = "9999-01-01T00:00:00Z" }, "after the import clock"},
		{"future completion", func(board *v1BoardExport) { board.Items[4].History[2].At = "9999-01-01T00:00:00Z" }, "after the import clock"},
		{"long approval message", func(board *v1BoardExport) { board.Policy.Lanes[1].ApprovalMessage = strings.Repeat("a", 1025) }, "Ready for Review messages"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "uncreated")
			fixture := v1ReplayFixture()
			test.edit(&fixture)
			if _, err := ImportV1(root, attachments, newID(), v1FixtureJSON(t, fixture)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("wanted %q, got %v", test.want, err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("preflight touched destination: %v", err)
			}
		})
	}
}

func TestV1ImportRespectsWriterLeaseAndFreshIdentity(t *testing.T) {
	root, attachments := t.TempDir(), v1FixtureFiles(t)
	fixture := v1ReplayFixture()
	id := newID()
	owner, err := openStore(root, workspacePrincipals(canonicalTime(time.Now()))...)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ImportV1(root, attachments, id, v1FixtureJSON(t, fixture))
	closeErr := owner.close()
	if !errors.Is(err, errStoreBusy) || closeErr != nil {
		t.Fatalf("writer exclusion: %v, %v", err, closeErr)
	}
	if _, err := ImportV1(root, attachments, id, v1FixtureJSON(t, fixture)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "boards", id+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImportV1(root, attachments, id, v1FixtureJSON(t, fixture)); !errors.Is(err, errBoardExists) {
		t.Fatalf("existing board refusal: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("repeat import changed original snapshot: %v", err)
	}
}

func TestV1ImportDefaultsToMiddlePriorityWhenOmitted(t *testing.T) {
	root, attachments := t.TempDir(), v1FixtureFiles(t)
	fixture := v1ReplayFixture()
	fixture.Policy.DefaultPriority = nil
	fixture.Policy.Priorities = []v1Priority{{"P1", "Urgent"}, {"P2", "Middle"}, {"P3", "Normal"}}
	result, err := ImportV1(root, attachments, "", v1FixtureJSON(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	state, err := readSnapshot(filepath.Join(root, "boards", result.BoardID+".json"), result.BoardID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Policy.DefaultPriorityID != state.Priorities[1].ID || state.Priorities[1].Key != "P2" {
		t.Fatal("import did not retain V1's inferred middle priority")
	}
}

func TestV1ImportLegacyTimestampsUseSourceHostZone(t *testing.T) {
	want := canonicalTime(time.Date(2025, time.July, 3, 14, 20, 0, 0, time.Local))
	for _, text := range []string{"2025-07-03 14:20", "2025-07-03 14:20 EDT", " 2025-07-03T14:20:00.9 "} {
		got, err := v1Timestamp(text)
		if err != nil || !got.Equal(want) {
			t.Fatalf("parse %q = %s, %v; want %s", text, got, err, want)
		}
	}
}

func TestV1ImportFreshSnapshotAndReferenceAreExclusive(t *testing.T) {
	const competitor = "competing publication"
	path := filepath.Join(t.TempDir(), "initial.json")
	err := writeAtomicNew(path, []byte("import"), func() error { return os.WriteFile(path, []byte(competitor), 0o600) })
	if !errors.Is(err, os.ErrExist) || errors.Is(err, errWriteUncertain) {
		t.Fatalf("exclusive snapshot result: %v", err)
	}
	assertFileContent(t, path, competitor)

	root, attachments := t.TempDir(), v1FixtureFiles(t)
	id := newID()
	now := canonicalTime(time.Now())
	principals := workspacePrincipals(now)
	files, err := os.OpenRoot(attachments)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	replay, err := planV1Replay(v1ReplayFixture(), id, principals, now, files)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openStore(root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	s.beforeRecoverySettlement = func(id string) error {
		return os.WriteFile(s.recoveryReferencePath(id), []byte(competitor), 0o600)
	}
	if err := s.commitV1Replay(replay); !errors.Is(err, os.ErrExist) || !errors.Is(err, errWriteUncertain) {
		t.Fatalf("competing recovery reference did not produce an uncertain publication: %v", err)
	}
	assertFileContent(t, s.recoveryReferencePath(id), competitor)
	if _, exists := s.accepted(id); exists {
		t.Fatal("unsettled import was admitted")
	}
	state, err := readSnapshot(s.snapshotPath(id), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readVerifiedAttachment(root, state.Attachments[0]); err != nil {
		t.Fatalf("uncertain publication discarded referenced attachment: %v", err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("competing file changed: %v", err)
	}
}
