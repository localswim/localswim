package v2

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestActivityHTTP(t *testing.T) {
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
	botClient := turnOneClient{t, server.URL, bot, server.Client()}
	raw, _ := c.call("POST", "/boards", `{"name":"Audit Board"}`, "", 201)
	board := turnOneDecode[board](t, raw)

	createCard := func(tag, title string) (card, http.Header) {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q}`,
			board.ID, tag, board.LaneIDs[0], title)
		raw, headers := c.call("POST", "/cards", body, "", 201)
		return turnOneDecode[card](t, raw), headers
	}
	first, _ := createCard(board.ETag, "Private original title")
	_, headers := c.call("PATCH", "/cards/"+first.ID, `{"title":"Private revised title"}`, first.entityTag(), 200)
	_, headers = botClient.call("POST", "/comments",
		fmt.Sprintf(`{"card_id":%q,"expected_card_etag":%q,"text":"Private comment"}`, first.ID, headers.Get("ETag")), "", 201)
	second, headers := createCard(headers.Get("X-Board-ETag"), "Other card")
	raw, headers = c.call("POST", "/relationships",
		fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`,
			board.ID, headers.Get("X-Board-ETag"), first.ID, second.ID), "", 201)
	link := turnOneDecode[relationship](t, raw)

	do := func(method, path string, headers http.Header, want int) http.Header {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+Prefix+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header = headers.Clone()
		req.Header.Set("Authorization", "Bearer "+human)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || len(data) != 0 || res.StatusCode != want {
			t.Fatalf("%s response: status %d, body %s, read error %v", method, res.StatusCode, data, err)
		}
		return res.Header
	}
	do("DELETE", "/relationships/"+link.ID, http.Header{
		"If-Match": {link.entityTag()}, "X-Expected-Board-ETag": {headers.Get("X-Board-ETag")},
	}, 204)

	activityPath := func(from, to string) string {
		return "/activities?" + (url.Values{
			"board_id": {strings.ToUpper(board.ID)}, "from": {from}, "to": {to},
		}).Encode()
	}
	path := activityPath("0001-01-01 00:00:00+00:00", "9999-12-31 23:59:59+00:00")
	raw, headers = c.call("GET", path, "", "", 200)
	full := turnOnePage[activity](t, raw, "activities")
	if len(full.Values) != 7 || full.NextCursor != nil {
		t.Fatalf("expected seven audit records, got %d", len(full.Values))
	}
	edit := full.Values[2]
	if edit.ActorID != humanID || edit.ResourceID != first.ID || edit.Field == nil || *edit.Field != "title" ||
		edit.Before == nil || *edit.Before != "Private original title" || edit.After == nil || *edit.After != "Private revised title" {
		t.Fatalf("title audit lacks attributed values: %+v", edit)
	}
	if full.Values[3].ActorID != botID || full.Values[3].ResourceType != "comment" {
		t.Fatal("comment audit lacks Bot attribution")
	}
	head := do("HEAD", path, http.Header{}, 200)
	if tag := headers.Get("ETag"); tag == "" || tag != head.Get("ETag") {
		t.Fatal("HEAD and GET do not share an ETag")
	}
	do("GET", path, http.Header{"If-None-Match": {headers.Get("ETag")}}, 304)

	at := edit.At.Format(timestampLayout)
	exact := activityPath(at, at)
	raw, _ = c.call("GET", exact+"&ticket=1", "", "", 200)
	exactPage := turnOnePage[activity](t, raw, "activities")
	if !slices.ContainsFunc(exactPage.Values, func(row activity) bool { return row.ID == edit.ID }) {
		t.Fatal("exact interval did not include both endpoints")
	}
	for _, row := range exactPage.Values {
		if row.At.Format(timestampLayout) != at {
			t.Fatal("exact interval returned a row outside the bounds")
		}
	}

	monitorPath := path + "&view=MoNiToR&ticket=1&limit=2"
	var monitor []activityMetadata
	requestPath := monitorPath
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		raw, _ := c.call("GET", requestPath, "", "", 200)
		if strings.Contains(string(raw), "Private") {
			t.Fatal("monitor leaked card or comment text")
		}
		m := turnOnePage[activityMetadata](t, raw, "activities")
		for _, row := range turnOnePage[map[string]json.RawMessage](t, raw, "activities").Values {
			if len(row) != 8 || row["before"] != nil || row["after"] != nil || row["resource_id"] != nil || row["field"] != nil {
				t.Fatal("monitor contains fields outside the metadata contract")
			}
		}
		monitor = append(monitor, m.Values...)
		if m.NextCursor == nil {
			break
		}
		cursor := "&cursor=" + url.QueryEscape(*m.NextCursor)
		if pageNumber == 0 {
			changedBounds := activityPath("0001-01-02 00:00:00+00:00", "9999-12-31 23:59:59+00:00")
			c.call("GET", changedBounds+"&view=monitor&ticket=1&limit=2"+cursor, "", "", 400)
		}
		requestPath = monitorPath + cursor
	}
	if len(monitor) != 5 || monitor[0].ID != full.Values[1].ID || monitor[1].ID != edit.ID ||
		monitor[2].ActorKind != "bot" || monitor[2].Operation != "comment" ||
		monitor[3].ActorKind != "human" || !slices.Equal(monitor[3].Tickets, []uint64{1, 2}) ||
		monitor[4].Operation != "remove" || !slices.Equal(monitor[4].Tickets, []uint64{1, 2}) {
		t.Fatalf("wrong paginated monitor metadata: %+v", monitor)
	}
	raw, _ = c.call("GET", path+"&ticket=99", "", "", 200)
	empty := turnOnePage[activity](t, raw, "activities")
	if len(empty.Values) != 0 || empty.NextCursor != nil {
		t.Fatal("no matching ticket should return an empty page")
	}

	for _, invalidPath := range []string{
		"/activities?board_id=" + board.ID,
		activityPath("2026-11-10T02:00:00Z", "9999-12-31 23:59:59+00:00"),
		activityPath("2026-11-10 02:00:00.5+00:00", "9999-12-31 23:59:59+00:00"),
		activityPath("2026-02-30 02:00:00+00:00", "9999-12-31 23:59:59+00:00"),
		activityPath("2026-11-10 02:00:00+01:00", "9999-12-31 23:59:59+00:00"),
		activityPath(at, edit.At.Add(-time.Second).Format(timestampLayout)),
		path + "&from=" + url.QueryEscape(at),
		path + "&ticket=0",
		path + "&ticket=9007199254740992",
		path + "&view=other",
		path + "&limit=201",
		path + "&cursor=",
		path + "&cursor=invalid",
	} {
		c.call("GET", invalidPath, "", "", 400)
	}
}
