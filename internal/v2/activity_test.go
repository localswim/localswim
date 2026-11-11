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
	_, headers := c.call("PATCH", "/cards/"+first.ID, `{"title":"Private revised title","description":"Private revised description"}`, first.entityTag(), 200)
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
	fullPath := path + "&view=FuLL"
	raw, headers = c.call("GET", fullPath, "", "", 200)
	full := turnOnePage[activity](t, raw, "activities")
	if len(full.Values) != 8 || full.NextCursor != nil {
		t.Fatalf("expected eight audit records, got %d", len(full.Values))
	}
	edit := full.Values[2]
	if edit.ActorID != humanID || edit.ResourceID != first.ID || edit.Field == nil || *edit.Field != "title" ||
		edit.Before == nil || *edit.Before != "Private original title" || edit.After == nil || *edit.After != "Private revised title" {
		t.Fatalf("title audit lacks attributed values: %+v", edit)
	}
	description := full.Values[3]
	if description.Field == nil || *description.Field != "description" ||
		description.After == nil || *description.After != "Private revised description" {
		t.Fatal("full view did not retain description audit")
	}
	if full.Values[4].ActorID != botID || full.Values[4].ResourceType != "comment" {
		t.Fatal("comment audit lacks Bot attribution")
	}
	head := do("HEAD", fullPath, http.Header{}, 200)
	if tag := headers.Get("ETag"); tag == "" || tag != head.Get("ETag") {
		t.Fatal("HEAD and GET do not share an ETag")
	}
	do("GET", fullPath, http.Header{"If-None-Match": {headers.Get("ETag")}}, 304)

	at := edit.At.Format(timestampLayout)
	exact := activityPath(at, at)
	raw, _ = c.call("GET", exact+"&ticket=1&view=full", "", "", 200)
	exactPage := turnOnePage[activity](t, raw, "activities")
	if !slices.ContainsFunc(exactPage.Values, func(row activity) bool { return row.ID == edit.ID }) {
		t.Fatal("exact interval did not include both endpoints")
	}
	for _, row := range exactPage.Values {
		if row.At.Format(timestampLayout) != at {
			t.Fatal("exact interval returned a row outside the bounds")
		}
	}

	raw, defaultHeaders := c.call("GET", path, "", "", 200)
	if strings.Contains(string(raw), "Private") {
		t.Fatal("default activity leaked card or comment text")
	}
	metadata := turnOnePage[activityMetadata](t, raw, "activities")
	if len(metadata.Values) != len(full.Values) ||
		metadata.Values[2].ActorID != humanID || metadata.Values[2].Operation != "patch" ||
		metadata.Values[2].Revision != edit.Revision || metadata.Values[3].Revision != description.Revision {
		t.Fatalf("default activity lacks title and description edit metadata: %+v", metadata)
	}
	defaultHead := do("HEAD", path, http.Header{}, 200)
	if tag := defaultHeaders.Get("ETag"); tag == "" || tag != defaultHead.Get("ETag") || tag == headers.Get("ETag") {
		t.Fatal("default activity does not have a distinct and consistent validator")
	}
	_, monitorHeaders := c.call("GET", path+"&view=MoNiToR", "", "", 200)
	if monitorHeaders.Get("ETag") != defaultHeaders.Get("ETag") {
		t.Fatal("default activity and explicit monitor differ")
	}

	raw, _ = c.call("GET", path+"&actor_id="+botID, "", "", 200)
	botOnly := turnOnePage[activityMetadata](t, raw, "activities")
	if len(botOnly.Values) != 1 || botOnly.Values[0].ActorID != botID || botOnly.Values[0].Operation != "comment" {
		t.Fatal("actor filter did not isolate the Bot comment")
	}

	filters := "&ticket=1&actor_id=" + strings.ToUpper(humanID) + "&operation=patch&limit=1"
	raw, _ = c.call("GET", exact+filters, "", "", 200)
	firstEdit := turnOnePage[activityMetadata](t, raw, "activities")
	if len(firstEdit.Values) != 1 || firstEdit.Values[0].ID != edit.ID || firstEdit.NextCursor == nil {
		t.Fatal("combined filters did not return the first edit and a cursor")
	}
	editCursor := "&cursor=" + url.QueryEscape(*firstEdit.NextCursor)
	raw, _ = c.call("GET", exact+filters+editCursor, "", "", 200)
	secondEdit := turnOnePage[activityMetadata](t, raw, "activities")
	if len(secondEdit.Values) != 1 || secondEdit.Values[0].ID != description.ID || secondEdit.NextCursor != nil {
		t.Fatal("combined filters did not return the second edit")
	}
	for _, changedFilters := range []string{
		strings.Replace(filters, "&actor_id="+strings.ToUpper(humanID), "", 1),
		strings.Replace(filters, "&operation=patch", "", 1),
		filters + "&view=full",
	} {
		c.call("GET", exact+changedFilters+editCursor, "", "", 400)
	}
	raw, _ = c.call("GET", exact+filters+"&view=full", "", "", 200)
	selectedFull := turnOnePage[activity](t, raw, "activities")
	if len(selectedFull.Values) != 1 || selectedFull.Values[0].ID != edit.ID || selectedFull.Values[0].After == nil {
		t.Fatal("full view did not preserve the selected audit values")
	}

	monitorPath := path + "&ticket=1&limit=2"
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
	if len(monitor) != 6 || monitor[0].ID != full.Values[1].ID || monitor[1].ID != edit.ID ||
		monitor[2].ID != description.ID || monitor[3].ActorKind != "bot" || monitor[3].Operation != "comment" ||
		monitor[4].ActorKind != "human" || !slices.Equal(monitor[4].Tickets, []uint64{1, 2}) ||
		monitor[5].Operation != "remove" || !slices.Equal(monitor[5].Tickets, []uint64{1, 2}) {
		t.Fatalf("wrong paginated monitor metadata: %+v", monitor)
	}
	raw, _ = c.call("GET", path+"&operation=unmatched", "", "", 200)
	empty := turnOnePage[activityMetadata](t, raw, "activities")
	if len(empty.Values) != 0 || empty.NextCursor != nil {
		t.Fatal("no matching operation should return an empty page")
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
		path + "&actor_id=",
		path + "&actor_id=invalid",
		path + "&actor_id=" + humanID + "&actor_id=" + botID,
		path + "&operation=",
		path + "&operation=Patch",
		path + "&operation=" + strings.Repeat("a", 81),
		path + "&operation=patch&operation=create",
		path + "&limit=201",
		path + "&cursor=",
		path + "&cursor=invalid",
	} {
		c.call("GET", invalidPath, "", "", 400)
	}
}
