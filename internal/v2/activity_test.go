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
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

type activityTestPage[T any] struct {
	Values             []T              `json:"activities"`
	NextCursor         *string          `json:"next_cursor"`
	WriterID           string           `json:"writer_id"`
	BoardRevision      uint64           `json:"board_revision"`
	Coverage           activityCoverage `json:"coverage"`
	PaginationComplete bool             `json:"pagination_complete"`
	BotOnly            bool             `json:"bot_only"`
}

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
	first, _ := createCard(board.entityTag(), "Private original title")
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
	full := turnOneDecode[activityTestPage[activity]](t, raw)
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
	exactPage := turnOneDecode[activityTestPage[activity]](t, raw)
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
	metadata := turnOneDecode[activityTestPage[activityMetadata]](t, raw)
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
	botOnly := turnOneDecode[activityTestPage[activityMetadata]](t, raw)
	if botOnly.BotOnly || len(botOnly.Values) != 1 || botOnly.Values[0].ActorID != botID || botOnly.Values[0].Operation != "comment" {
		t.Fatal("actor filter did not isolate the Bot comment")
	}

	filters := "&ticket=1&actor_id=" + strings.ToUpper(humanID) + "&operation=patch&limit=1"
	raw, _ = c.call("GET", exact+filters, "", "", 200)
	firstEdit := turnOneDecode[activityTestPage[activityMetadata]](t, raw)
	if len(firstEdit.Values) != 1 || firstEdit.Values[0].ID != edit.ID || firstEdit.NextCursor == nil || firstEdit.PaginationComplete {
		t.Fatal("combined filters did not return the first edit and a cursor")
	}
	editCursor := "&cursor=" + url.QueryEscape(*firstEdit.NextCursor)
	raw, _ = c.call("GET", exact+filters+editCursor, "", "", 200)
	secondEdit := turnOneDecode[activityTestPage[activityMetadata]](t, raw)
	if len(secondEdit.Values) != 1 || secondEdit.Values[0].ID != description.ID || secondEdit.NextCursor != nil || !secondEdit.PaginationComplete {
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
	selectedFull := turnOneDecode[activityTestPage[activity]](t, raw)
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
		m := turnOneDecode[activityTestPage[activityMetadata]](t, raw)
		for _, row := range turnOneDecode[activityTestPage[map[string]json.RawMessage]](t, raw).Values {
			if len(row) != 9 || row["before"] != nil || row["after"] != nil || row["resource_id"] != nil || row["field"] != nil {
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
	empty := turnOneDecode[activityTestPage[activityMetadata]](t, raw)
	if len(empty.Values) != 0 || empty.NextCursor != nil || !empty.PaginationComplete || empty.Coverage.Complete || empty.BotOnly {
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
		path + "&card_id=invalid",
		path + "&writer_id=" + humanID,
		path + "&writer_id=" + app.store.activityWriterID + "&writer_id=" + app.store.activityWriterID,
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

	// Reverse the tickets after the original Human edit and Bot comment.
	_, headers = c.call("GET", "/boards/"+board.ID, "", "", 200)
	_, headers = c.call("POST", "/relationships",
		fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`,
			board.ID, headers.Get("ETag"), second.ID, first.ID), "", 201)
	c.call("PUT", "/ticket-orders/"+board.LaneIDs[0],
		ticketOrderBody(t, board.ID, headers.Get("X-Board-ETag"), []string{second.ID, first.ID}), "", 200)
	raw, _ = c.call("GET", path+"&card_id="+strings.ToUpper(first.ID)+"&ticket=1", "", "", 200)
	oldTicket := turnOneDecode[activityTestPage[activityMetadata]](t, raw)
	if !slices.ContainsFunc(oldTicket.Values, func(row activityMetadata) bool {
		return row.ID == edit.ID && slices.Equal(row.Cards, []activityCard{{CardID: first.ID, Ticket: 1}})
	}) || slices.ContainsFunc(oldTicket.Values, func(row activityMetadata) bool { return row.Operation == ticketOrderOperation }) {
		t.Fatal("historical card and ticket filter changed after renumber")
	}
	raw, _ = c.call("GET", path+"&card_id="+first.ID+"&ticket=2", "", "", 200)
	newTicket := turnOneDecode[activityTestPage[activityMetadata]](t, raw)
	if len(newTicket.Values) != 1 || newTicket.Values[0].Operation != ticketOrderOperation ||
		!slices.Equal(newTicket.Values[0].Cards, []activityCard{{CardID: first.ID, Ticket: 2}}) {
		t.Fatal("new ticket selected events before its commit or omitted the renumber")
	}
	c.call("GET", exact+filters+editCursor, "", "", 400)
}

func TestActivityCoverageHTTP(t *testing.T) {
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
	// Keep this elapsed UTC second fixed while exercising production reads.
	at := canonicalTime(time.Now()).Add(-time.Minute)
	app.store.activitySince = at
	id := newID()
	state := freshSnapshot(id, "Synthetic coverage board", defaultWorkflow(id, at, app.principals), botID, at)
	if err := app.store.create(state); err != nil {
		t.Fatal(err)
	}
	editName := func(revision uint64, actor, after string, instant time.Time) snapshot {
		t.Helper()
		value, err := app.store.update(id, revision, func(next *snapshot) error {
			before := next.Board.Name
			next.Board.Name = after
			next.Activity = append(next.Activity, activity{
				ID: newOperationID(), BoardID: id, Revision: next.Board.Revision + 1, At: instant,
				ActorID: actor, Operation: "patch", ResourceType: "board", ResourceID: id,
				Field: new("name"), Before: &before, After: &after,
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	committed := editName(state.Board.Revision, botID, "Synthetic changed board", at)

	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8810}
	handler := app.Handler(addr)
	report := func(instant time.Time, options string) activityTestPage[activityMetadata] {
		t.Helper()
		bound := instant.Format(timestampLayout)
		path := Prefix + "/activities?" + (url.Values{"board_id": {id}, "from": {bound}, "to": {bound}}).Encode()
		req := httptest.NewRequest(http.MethodGet, path+options, nil)
		req.Host = addr.String()
		req.Header.Set("Authorization", "Bearer "+human)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("activity report status %d: %s", res.Code, res.Body)
		}
		return turnOneDecode[activityTestPage[activityMetadata]](t, res.Body.Bytes())
	}
	writer := app.builds.running.ServiceInstanceID
	options := "&writer_id=" + strings.ToUpper(writer) + "&limit=1"
	first := report(at, options)
	if first.WriterID != writer || first.BoardRevision != committed.Board.Revision ||
		!first.Coverage.Complete || first.Coverage.Code != "complete" ||
		!first.Coverage.From.Equal(at) || !first.Coverage.To.Equal(at) ||
		first.PaginationComplete || first.BotOnly || first.NextCursor == nil || len(first.Values) != 1 ||
		first.Values[0].Revision != 1 {
		t.Fatal("first page did not report a covered, unfinished interval in revision order")
	}
	last := report(at, options+"&cursor="+url.QueryEscape(*first.NextCursor))
	if !last.Coverage.Complete || !last.PaginationComplete || !last.BotOnly ||
		last.NextCursor != nil || len(last.Values) != 1 || last.Values[0].Revision != 2 {
		t.Fatal("final page did not classify the complete, nonempty Bot-only interval")
	}
	empty := report(at.Add(time.Second), options)
	if !empty.Coverage.Complete || !empty.PaginationComplete || empty.BotOnly || len(empty.Values) != 0 {
		t.Fatal("valid empty interval was not distinguished from Bot-only or missing coverage")
	}
	filtered := report(at, "&actor_id="+botID)
	if !filtered.Coverage.Complete || !filtered.PaginationComplete || filtered.BotOnly || len(filtered.Values) != 2 {
		t.Fatal("filtered evidence incorrectly classified the whole interval")
	}
	mixedAt := at.Add(time.Second)
	humanEdit := editName(committed.Board.Revision, humanID, "Synthetic Human edit", mixedAt)
	editName(humanEdit.Board.Revision, botID, "Synthetic Bot edit", mixedAt)
	mixedFirst := report(mixedAt, options)
	if mixedFirst.NextCursor == nil || len(mixedFirst.Values) != 1 || mixedFirst.Values[0].ActorID != humanID {
		t.Fatal("mixed interval did not page the Human event first")
	}
	mixedLast := report(mixedAt, options+"&cursor="+url.QueryEscape(*mixedFirst.NextCursor))
	if !mixedLast.Coverage.Complete || !mixedLast.PaginationComplete || mixedLast.BotOnly ||
		len(mixedLast.Values) != 1 || mixedLast.Values[0].ActorID != botID {
		t.Fatal("terminal Bot event concealed a Human event on a previous page")
	}
	for _, test := range []struct {
		at      time.Time
		options string
		code    string
	}{
		{at.Add(-time.Second), options, "startup_gap"},
		{at, "&writer_id=" + newOperationID(), "writer_mismatch"},
		{canonicalTime(time.Now()).Add(time.Hour), options, "interval_open"},
	} {
		if got := report(test.at, test.options); got.Coverage.Complete || got.BotOnly || got.Coverage.Code != test.code {
			t.Errorf("incomplete interval did not report %s", test.code)
		}
	}
	if err := os.WriteFile(app.store.snapshotPath(id), []byte("damaged disposable snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	damaged := report(at, "")
	if damaged.Coverage.Complete || damaged.Coverage.Code != "durable_state_unavailable" ||
		damaged.BotOnly || len(damaged.Values) != 2 {
		t.Fatal("durable damage was treated as complete or hid accepted metadata")
	}
}

func TestActivityRecoveryCoverage(t *testing.T) {
	restoreAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	since := restoreAt.Add(-time.Hour)
	observation := activityObservation{
		state: snapshot{Activity: []activity{
			{ResourceType: "board", Operation: recoveryOperation, At: restoreAt},
		}},
		observedAt: restoreAt.Add(time.Minute), durable: true,
	}
	query := activityQuery{from: restoreAt.Add(-time.Second), to: restoreAt}
	if got := query.coverage(observation, "", since); got.Complete || got.Code != "recovery_gap" {
		t.Fatal("history replaced by recovery was treated as continuously covered")
	}
	query.from, query.to = restoreAt.Add(time.Second), restoreAt.Add(time.Second)
	if got := query.coverage(observation, "", since); !got.Complete {
		t.Fatal("a fully elapsed second after recovery was not covered")
	}
}
