package v2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestM68InspectTicket(t *testing.T) {
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)
	c := turnOneClient{t, server.URL, human, server.Client()}
	raw, _ := c.call("POST", "/boards", `{"name":"Ticket context"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	tag := b.entityTag()
	createCard := func(title, description, parent, laneID string) card {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q,"description":%q`,
			b.ID, tag, laneID, title, description)
		if parent != "" {
			body += fmt.Sprintf(`,"parent_id":%q`, parent)
		}
		raw, headers := c.call("POST", "/cards", body+"}", "", 201)
		tag = headers.Get("X-Board-ETag")
		return turnOneDecode[card](t, raw)
	}
	parent := createCard("Parent", "parent-private", "", b.LaneIDs[0])
	focus := createCard("Focus", "  selected-private\n  ", parent.ID, b.LaneIDs[0])
	child := createCard("Child", "child-private", focus.ID, b.LaneIDs[0])
	dependency := createCard("Dependency", "dependency-private", "", b.LaneIDs[1])
	other := createCard("Other", "other-private", "", b.LaneIDs[0])
	for _, link := range []relationshipLink{
		{focus.ID, child.ID, "references"},
		{focus.ID, parent.ID, "blocks"},
		{dependency.ID, focus.ID, "blocks"},
		{other.ID, focus.ID, "relates_to"},
		{other.ID, dependency.ID, "references"},
	} {
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":%q}`,
			b.ID, tag, link.SourceCardID, link.TargetCardID, link.Kind)
		_, headers := c.call("POST", "/relationships", body, "", 201)
		tag = headers.Get("X-Board-ETag")
	}
	commentOn := func(cardID, text string) {
		t.Helper()
		raw, headers := c.call("GET", "/cards/"+cardID, "", "", 200)
		if turnOneDecode[card](t, raw).ID != cardID {
			t.Fatal("card read returned the wrong card")
		}
		body := fmt.Sprintf(`{"card_id":%q,"expected_card_etag":%q,"text":%q}`, cardID, headers.Get("ETag"), text)
		_, headers = c.call("POST", "/comments", body, "", 201)
		tag = headers.Get("X-Board-ETag")
	}
	commentOn(focus.ID, "older-comment-private")
	commentOn(focus.ID, "newer-comment-private")
	commentOn(child.ID, "child-comment-private")
	focusRaw, _ := c.call("GET", "/cards/"+focus.ID, "", "", 200)
	current := turnOneDecode[card](t, focusRaw)
	before, _ := c.call("GET", "/snapshots/"+b.ID, "", "", 200)
	path := "/boards/" + b.ID + "?view=ticket&ticket=0002"
	raw, headers := c.call("GET", path, "", "", 200)
	value := turnOneDecode[TicketInspection](t, raw)
	if value.BoardID != b.ID || value.BoardRevision <= value.Card.Revision || value.Card.ID != focus.ID ||
		value.Card.Ticket != 2 || value.Card.Revision != current.Revision ||
		headers.Get("X-Board-ETag") != tag || value.Card.Lane.ID != current.LaneID ||
		value.Card.Owner.ID != current.OwnerID || value.Card.Priority.ID != current.PriorityID {
		t.Fatalf("wrong focused ticket identity, workflow, or revisions: %+v", value)
	}
	if value.Parent == nil || value.Parent.ID != parent.ID || len(value.Children) != 1 || value.Children[0].ID != child.ID {
		t.Fatalf("wrong hierarchy: %+v", value)
	}
	if len(value.Relationships) != 4 ||
		value.Relationships[0].Card.ID != parent.ID || value.Relationships[0].Direction != "outgoing" || value.Relationships[0].Kind != "blocks" ||
		value.Relationships[1].Card.ID != child.ID || value.Relationships[1].Direction != "outgoing" ||
		value.Relationships[2].Card.ID != dependency.ID || value.Relationships[2].Direction != "incoming" ||
		value.Relationships[2].Card.Lane.ID != dependency.LaneID || value.Relationships[2].Card.Lane.Label == "" ||
		value.Relationships[2].Card.Owner.Label == "" || value.Relationships[2].Card.Priority.Label == "" ||
		value.Relationships[3].Card.ID != other.ID || value.Relationships[3].Direction != "symmetric" {
		t.Fatalf("wrong direct link context: %+v", value.Relationships)
	}
	if value.Prose != nil || bytes.Contains(raw, []byte("private")) || bytes.Contains(raw, []byte(`"prose"`)) || bytes.Contains(raw, []byte(`"etag"`)) {
		t.Fatal("default inspection exposed descriptions or comments")
	}
	withProse, proseHeaders := c.call("GET", path+"&include_prose=true", "", "", 200)
	value = turnOneDecode[TicketInspection](t, withProse)
	if value.Prose == nil || value.Prose.Description != "  selected-private\n  " || len(value.Prose.Comments) != 2 ||
		value.Prose.Comments[0].Text != "newer-comment-private" || value.Prose.Comments[1].Text != "older-comment-private" ||
		value.Prose.Comments[0].AuthorID != humanID || bytes.Contains(withProse, []byte("child-comment-private")) ||
		bytes.Contains(withProse, []byte("dependency-private")) || bytes.Contains(withProse, []byte(`"etag"`)) || proseHeaders.Get("ETag") == headers.Get("ETag") {
		t.Fatal("prose inspection lost exact focused text or included linked-card text")
	}
	withoutProse, _ := c.call("GET", path+"&include_prose=false", "", "", 200)
	if !bytes.Equal(raw, withoutProse) {
		t.Fatal("explicit false changed the metadata representation")
	}
	head := boardRequest(c, "HEAD", path, "", "")
	if head.err != nil || head.status != 200 || len(head.data) != 0 ||
		head.header.Get("ETag") != headers.Get("ETag") || head.header.Get("X-Board-ETag") != tag {
		t.Fatal("HEAD did not retain the ticket view validators")
	}
	for _, query := range []string{"", "&ticket=", "&ticket=0", "&ticket=9007199254740992", "&ticket=2&ticket=2",
		"&ticket=2&include_prose=", "&ticket=2&include_prose=TRUE", "&ticket=2&include_prose=false&include_prose=true"} {
		c.call("GET", "/boards/"+b.ID+"?view=ticket"+query, "", "", 400)
	}
	c.call("GET", "/boards/"+b.ID+"?view=ticket&ticket=200", "", "", 404)
	after, _ := c.call("GET", "/snapshots/"+b.ID, "", "", 200)
	if !bytes.Equal(before, after) {
		t.Fatal("ticket inspection changed durable state")
	}

	t.Logf("default inspection: ticket %d, lane %s, owner %s, priority %s, %d directed/symmetric links, parent %d, child %d",
		value.Card.Ticket, value.Card.Lane.Label, value.Card.Owner.Label, value.Card.Priority.Label,
		len(value.Relationships), value.Parent.Ticket, value.Children[0].Ticket)
	t.Logf("explicit prose inspection: exact selected description and %d attributed comments", len(value.Prose.Comments))
}
