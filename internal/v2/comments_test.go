package v2

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestM05AttributedCommentAndNewestPages(t *testing.T) {
	root := t.TempDir()
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}()
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	defer server.Close()
	h := turnOneClient{t, server.URL, human, server.Client()}
	b := turnOneClient{t, server.URL, bot, server.Client()}

	raw, _ := h.call("POST", "/boards", `{"name":"Comments"}`, "", 201)
	board := turnOneDecode[board](t, raw)
	boardTag := board.ETag
	createCard := func(title string) card {
		t.Helper()
		raw, headers := h.call("POST", "/cards", fmt.Sprintf(
			`{"board_id":%q,"lane_id":%q,"expected_board_etag":%q,"title":%q}`,
			board.ID, board.LaneIDs[0], boardTag, title), "", 201)
		boardTag = headers.Get("X-Board-ETag")
		return turnOneDecode[card](t, raw)
	}
	first, second := createCard("First"), createCard("Second")
	query := "/comments?board_id=" + board.ID
	raw, _ = h.call("GET", query, "", "", 200)
	if empty := turnOnePage[comment](t, raw, "comments"); empty.Values == nil || len(empty.Values) != 0 || empty.NextCursor != nil {
		t.Fatal("expected an empty comment collection with no continuation")
	}
	id := newID()
	commentBody := func(id, cardID, cardTag, text string) string {
		t.Helper()
		value, err := marshalCanonical(map[string]string{"id": id, "card_id": cardID, "expected_card_etag": cardTag, "text": text})
		if err != nil {
			t.Fatal(err)
		}
		return string(value)
	}
	h.call("POST", "/comments", fmt.Sprintf(`{"card_id":%q,"text":"valid"}`, first.ID), "", 428)
	h.call("POST", "/comments", fmt.Sprintf(`{"card_id":%q,"expected_card_etag":%q,"text":"\ud800"}`, first.ID, first.entityTag()), "", 400)
	h.call("POST", "/comments", fmt.Sprintf(`{"card_id":%q,"expected_card_etag":%q,"text":"valid","author_id":%q}`, first.ID, first.entityTag(), newID()), "", 400)
	text := "  First line\r\n👋 e\u0301\n "
	raw, headers := h.call("POST", "/comments", commentBody(strings.ToUpper(id), first.ID, first.entityTag(), text), "", 201)
	assertImmutableCommentShape(t, raw)
	oldest := turnOneDecode[comment](t, raw)
	if oldest.ID != id || oldest.BoardID != board.ID || oldest.CardID != first.ID || oldest.Text != text ||
		oldest.AuthorID != board.UserIDs[0] || oldest.CreatedAt.IsZero() || !oldest.UpdatedAt.Equal(oldest.CreatedAt) ||
		headers.Get("Location") != Prefix+"/comments/"+id || headers.Get("ETag") != oldest.entityTag() ||
		headers.Get("X-Board-ETag") == "" || headers.Get("X-Board-ETag") == boardTag {
		t.Fatal("comment response lost exact text, attribution, entity metadata, or new board tag")
	}
	h.call("POST", "/comments", commentBody(newID(), first.ID, first.entityTag(), "stale"), "", 412)
	raw, _ = h.call("GET", "/cards/"+first.ID, "", "", 200)
	firstNext := turnOneDecode[card](t, raw)
	if firstNext.CommentCount != 1 || firstNext.Revision != 4 || firstNext.entityTag() == first.entityTag() {
		t.Fatal("parent card count and tag were not committed with the comment")
	}
	h.call("POST", "/comments", commentBody(id, first.ID, firstNext.entityTag(), "duplicate"), "", 409)
	raw, _ = b.call("POST", "/comments", commentBody(newID(), second.ID, second.entityTag(), "Newest #1"), "", 201)
	newest := turnOneDecode[comment](t, raw)
	if newest.AuthorID != board.UserIDs[1] || newest.CardID != second.ID {
		t.Fatal("Bot comment attribution did not come from its authenticated identity")
	}

	raw, _ = h.call("GET", query+"&limit=1", "", "", 200)
	page := turnOnePage[comment](t, raw, "comments")
	if len(page.Values) != 1 || page.Values[0] != newest || page.NextCursor == nil {
		t.Fatal("newest page did not include the latest comment across cards and a continuation")
	}
	raw, _ = h.call("GET", query+"&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", "", 200)
	if next := turnOnePage[comment](t, raw, "comments"); len(next.Values) != 1 || next.Values[0] != oldest || next.NextCursor != nil {
		t.Fatal("continuation did not return the older comment")
	}
	raw, _ = h.call("GET", query+"&card_id="+first.ID, "", "", 200)
	if filtered := turnOnePage[comment](t, raw, "comments"); len(filtered.Values) != 1 || filtered.Values[0] != oldest {
		t.Fatal("card filter did not return its comment")
	}
	raw, _ = h.call("GET", "/comments/"+id, "", "", 200)
	assertImmutableCommentShape(t, raw)
	if turnOneDecode[comment](t, raw) != oldest {
		t.Fatal("individual readback differs from the committed comment")
	}
	h.call("GET", query+"&cursor=invalid", "", "", 400)

	saved, err := app.store.durable(board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Comments) != 2 || len(saved.Activity) != 5 || len(saved.Relationships) != 0 ||
		saved.Board.LastCardEditAt == nil || !saved.Board.LastCardEditAt.Equal(newest.CreatedAt) {
		t.Fatal("durable state does not reflect exactly two comment transactions")
	}
	for i, value := range []comment{oldest, newest} {
		data, err := marshalCanonical(value)
		if err != nil {
			t.Fatal(err)
		}
		assertImmutableCommentShape(t, data)
		audit := saved.Activity[i+3]
		if saved.Comments[i] != value || audit.Operation != "comment" || audit.ResourceType != "comment" ||
			audit.ResourceID != value.ID || audit.ActorID != value.AuthorID || !audit.At.Equal(value.CreatedAt) || audit.Revision != uint64(i+4) {
			t.Fatal("durable comment and its audit event disagree")
		}
	}
}

func assertImmutableCommentShape(t *testing.T, data []byte) {
	t.Helper()
	fields := turnOneDecode[map[string]json.RawMessage](t, data)
	if len(fields) != 7 || fields["etag"] != nil || fields["revision"] != nil {
		t.Fatalf("unexpected immutable comment fields: %s", data)
	}
}
