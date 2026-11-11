package v2

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestM21TicketOrderHTTP(t *testing.T) {
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
	client := turnOneClient{t, server.URL, human, server.Client()}
	raw, _ := client.call("POST", "/boards", `{"name":"Ticket preview"}`, "", 201)
	board := turnOneDecode[board](t, raw)
	raw, headers := client.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"First"}`,
		board.ID, board.ETag, board.LaneIDs[0]), "", 201)
	first := turnOneDecode[card](t, raw)
	raw, _ = client.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Second","relationships":[{"target_card_id":%q,"kind":"blocks"}]}`,
		board.ID, headers.Get("X-Board-ETag"), board.LaneIDs[0], first.ID), "", 201)
	second := turnOneDecode[card](t, raw)
	before, err := app.store.durable(board.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/ticket-orders/" + strings.ToUpper(board.LaneIDs[0])
	raw, _ = client.call("GET", path, "", "", 200)
	current := turnOneDecode[ticketOrder](t, raw)
	if current.Changed || !reflect.DeepEqual(current.Current, current.Proposed) {
		t.Fatal("default read computed a preview")
	}
	raw, headers = client.call("GET", path+"?preview=true", "", "", 200)
	preview := turnOneDecode[ticketOrder](t, raw)
	if !preview.Changed || preview.BoardRevision != before.Board.Revision ||
		!reflect.DeepEqual(preview.Proposed, []ticketOrderEntry{{second.ID, first.Ticket}, {first.ID, second.Ticket}}) ||
		headers.Get("X-Board-ETag") != before.Board.ETag || headers.Get("ETag") == "" {
		t.Fatalf("unexpected preview response: %+v", preview)
	}
	tag := headers.Get("ETag")
	for _, test := range []struct {
		method, query string
		status        int
	}{
		{"GET", "?preview=true", 304}, {"HEAD", "?preview=true", 304},
		{"GET", "?preview=true&card_id=" + first.ID, 200}, {"GET", "?preview=false", 200},
	} {
		req, err := http.NewRequest(test.method, server.URL+Prefix+path+test.query, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+human)
		req.Header.Set("If-None-Match", tag)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != test.status || response.Header.Get("X-Board-ETag") != before.Board.ETag {
			t.Fatalf("conditional %s %s failed: status %d, error %v", test.method, test.query, response.StatusCode, err)
		}
		if test.status == 304 && (len(body) != 0 || response.Header.Get("ETag") != tag) {
			t.Fatal("invalid 304 representation")
		}
		if test.status == 200 && response.Header.Get("ETag") == tag {
			t.Fatal("different selection or view reused preview validator")
		}
	}
	req, err := http.NewRequest(http.MethodHead, server.URL+Prefix+path+"?preview=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+human)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || len(body) != 0 || response.Header.Get("ETag") != tag {
		t.Fatal("HEAD did not preserve the preview headers with an empty body")
	}
	for _, query := range []string{"?preview=1", "?preview=true&preview=false", "?card_id=" + first.ID + "&card_id=" + strings.ToUpper(first.ID), "?card_id=" + newID(), "?unknown=true", "?preview=%zz"} {
		client.call("GET", path+query, "", "", 400)
	}
	client.call("GET", "/ticket-orders/"+newID(), "", "", 404)
	client.call("PUT", path, `{}`, "", 405)
	anonymous := turnOneClient{t, server.URL, "", server.Client()}
	anonymous.call("GET", path, "", "", 401)
	after, err := app.store.durable(board.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read-only ticket order changed durable state")
	}
}
