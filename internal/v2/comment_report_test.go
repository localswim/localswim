package v2

import (
	"fmt"
	"net/url"
	"slices"
	"testing"
)

func TestM069NewestCommentTicketsAndArchiveSelection(t *testing.T) {
	_, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"M069 comment report"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	active := attachmentHTTPCard(t, editor, b)
	raw, _ = editor.call("GET", "/boards/"+b.ID, "", "", 200)
	b = turnOneDecode[board](t, raw)
	archived := attachmentHTTPCard(t, editor, b)

	addComment := func(parent card, text string) comment {
		t.Helper()
		body := fmt.Sprintf(`{"card_id":%q,"expected_card_etag":%q,"text":%q}`, parent.ID, parent.entityTag(), text)
		raw, _ := editor.call("POST", "/comments", body, "", 201)
		assertImmutableCommentShape(t, raw)
		return turnOneDecode[comment](t, raw)
	}
	oldest := addComment(active, "First active comment.")
	raw, _ = editor.call("GET", "/cards/"+active.ID, "", "", 200)
	active = turnOneDecode[card](t, raw)
	newestActive := addComment(active, "  Latest active comment.\n ")
	newestArchived := addComment(archived, "Latest comment on work later archived.")
	raw, _ = editor.call("GET", "/cards/"+archived.ID, "", "", 200)
	archived = turnOneDecode[card](t, raw)
	editor.call("PATCH", "/cards/"+archived.ID, `{"archived":true}`, archived.entityTag(), 200)

	older := commentListEntry{comment: oldest, Ticket: active.Ticket}
	activeEntry := commentListEntry{comment: newestActive, Ticket: active.Ticket}
	archiveEntry := commentListEntry{comment: newestArchived, Ticket: archived.Ticket}
	path := "/comments?board_id=" + b.ID
	for _, tc := range []struct {
		name, query string
		want        []commentListEntry
		continues   bool
	}{
		{"default active limit", "&limit=1", []commentListEntry{activeEntry}, true},
		{"default active full", "", []commentListEntry{activeEntry, older}, false},
		{"include archived limit", "&archived=include&limit=1", []commentListEntry{archiveEntry}, true},
		{"include archived full", "&archived=INCLUDE", []commentListEntry{archiveEntry, activeEntry, older}, false},
		{"only archived", "&archived=only&limit=1", []commentListEntry{archiveEntry}, false},
		{"focused archived", "&card_id=" + archived.ID, []commentListEntry{archiveEntry}, false},
		{"explicitly exclude focused archived", "&card_id=" + archived.ID + "&archived=exclude", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := editor.call("GET", path+tc.query, "", "", 200)
			page := turnOnePage[commentListEntry](t, raw, "comments")
			if !slices.Equal(page.Values, tc.want) || (page.NextCursor != nil) != tc.continues {
				t.Fatalf("unexpected report: %s", raw)
			}
			for _, entry := range page.Values {
				t.Logf("ticket #%04d, exact text %q", entry.Ticket, entry.Text)
			}
		})
	}

	raw, _ = editor.call("GET", path+"&limit=1", "", "", 200)
	page := turnOnePage[commentListEntry](t, raw, "comments")
	if page.NextCursor == nil {
		t.Fatal("expected a continuation after the first active comment")
	}
	continuation := "&limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
	raw, _ = editor.call("GET", path+continuation+"&archived=exclude", "", "", 200)
	if next := turnOnePage[commentListEntry](t, raw, "comments"); !slices.Equal(next.Values, []commentListEntry{older}) || next.NextCursor != nil {
		t.Fatalf("unexpected continuation: %s", raw)
	}
	editor.call("GET", path+continuation+"&archived=include", "", "", 400)
	for _, query := range []string{"archived=", "archived=bad", "archived=only&archived=include", "limit=0", "limit=201"} {
		editor.call("GET", path+"&"+query, "", "", 400)
	}
	raw, _ = editor.call("GET", "/comments/"+newestArchived.ID, "", "", 200)
	assertImmutableCommentShape(t, raw)
}
