package v2

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func searchSnapshot() snapshot {
	s := snapshot{Priorities: []priority{{entity: entity{ID: "normal"}, Rank: 1}, {entity: entity{ID: "urgent"}, Rank: 0}}}
	for ticket := uint64(1); ticket <= 8; ticket++ {
		s.Cards = append(s.Cards, laneTestCard(ticket, "normal", "a"))
	}
	s.Cards[1].Archived = true
	s.Cards[2].LaneID = "b"
	now := time.Now()
	s.Cards[5].CompletedAt = &now
	for _, edge := range [][2]uint64{{1, 2}, {1, 3}, {2, 4}, {3, 4}, {4, 5}, {5, 4}, {1, 6}, {6, 7}, {8, 1}} {
		s.Relationships = append(s.Relationships, laneTestLink(edge[0], edge[1], "blocks"))
	}
	s.Relationships = append(s.Relationships, laneTestLink(1, 7, "references"))
	return s
}

func TestCardTriageWholeGraph(t *testing.T) {
	s := searchSnapshot()
	before, _ := marshalCanonical(s)
	derived := cardsWithTriage(s)
	wantCounts := []int{4, 2, 2, 1, 1, 0, 0, 5}
	wantDepths := []int{2, 1, 1, 0, 0, 1, 0, 3}
	for i, item := range derived {
		if item.BlockedCardCount != wantCounts[i] || item.TriageScore != 200-wantCounts[i] || item.BlockingDepth != wantDepths[i] {
			t.Fatalf("ticket %d metrics = %d/%d/%d", item.Ticket, item.BlockedCardCount, item.TriageScore, item.BlockingDepth)
		}
	}
	filters := cardFilters{laneIDs: map[string]bool{"a": true}, archived: "exclude", sort: "triage"}
	rows := selectCards(s, filters)
	if !slices.Equal(laneTickets(rows), []uint64{8, 1, 4, 5, 6, 7}) || rows[1].BlockedCardCount != 4 {
		t.Fatalf("triage order/filter = %v", laneTickets(rows))
	}
	filters.archived = "only"
	if rows := selectCards(s, filters); len(rows) != 1 || rows[0].Ticket != 2 || rows[0].BlockedCardCount != 2 {
		t.Fatal("archived-only filtering lost whole-board counts")
	}
	s.Cards[4].PriorityID = "urgent"
	if item := cardsWithTriage(s)[0]; item.PriorityID != "normal" || item.TriageScore != 96 {
		t.Fatal("derived score did not use inherited urgency")
	}
	s.Cards[4].PriorityID = "normal"
	after, _ := marshalCanonical(s)
	if string(before) != string(after) {
		t.Fatal("reads persisted derived metrics or mutated native cards")
	}
}

func TestCardTriageNegativeScoresAndLongChains(t *testing.T) {
	s := snapshot{Priorities: []priority{{entity: entity{ID: "p"}, Rank: 0}}}
	for ticket := uint64(1); ticket <= 1200; ticket++ {
		s.Cards = append(s.Cards, laneTestCard(ticket, "p", "a"))
		if ticket > 1 {
			s.Relationships = append(s.Relationships, laneTestLink(ticket-1, ticket, "blocks"))
		}
	}
	rows := cardsWithTriage(s)
	if rows[0].BlockedCardCount != 1199 || rows[0].TriageScore != -1099 || rows[0].BlockingDepth != 1199 {
		t.Fatal("long chain was truncated, double-counted, or clamped")
	}
}

func TestCardTriageSparseGraphAllocation(t *testing.T) {
	const size = 16000
	for _, withEdges := range []bool{false, true} {
		edges := make([][]int, size)
		if withEdges {
			edges[size-3] = []int{size - 2, size - 1}
			edges[size-2] = []int{size - 1}
		}
		counts, depths := blockingMetrics(edges, true)
		if withEdges && (counts[size-3] != 2 || depths[size-3] != 2) {
			t.Fatal("sparse diamond traversal returned incorrect metrics")
		}
		measured := testing.Benchmark(func(b *testing.B) {
			for b.Loop() {
				blockingMetrics(edges, true)
			}
		})
		// Leave room for linear SCC bookkeeping and slice growth. Allocating
		// a dense prefix bitset for each isolated component exceeds this cap.
		if got := measured.AllocedBytesPerOp(); got > size*512 {
			t.Fatalf("sparse graph with edges=%t allocated %d bytes for %d cards", withEdges, got, size)
		}
	}
}

func TestCardSearchSelection(t *testing.T) {
	s := searchSnapshot()
	s.Cards[0].Title = "  HELLO café 007 "
	s.Cards[1].Title = "Hello archived"
	s.Cards[2].Description = "hidden needle"
	s.Comments = []comment{{CardID: s.Cards[3].ID, Text: "Comment NEEDLE"}}
	tests := []struct {
		query   string
		content bool
		want    []uint64
	}{
		{query: "  hElLo ", want: []uint64{1}},
		{query: "\x1cHELLO\x1f", want: []uint64{1}},
		{query: "CAFÉ", want: []uint64{1}},
		{query: "#0007", want: []uint64{7}},
		{query: "007", want: []uint64{7}},
		{query: "#٠٠٧", want: []uint64{7}},
		{query: "００７", want: []uint64{7}},
		{query: "999999999999999999999999999999999", want: []uint64{}},
		{query: "0", want: []uint64{}},
		{query: "needle", want: []uint64{}},
		{query: "needle", content: true, want: []uint64{3, 4}},
		{query: s.Cards[7].ID, want: []uint64{8}},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("%s/%t", test.query, test.content), func(t *testing.T) {
			request := httptest.NewRequest("GET", "/?q="+url.QueryEscape(test.query)+"&search_content="+fmt.Sprint(test.content), nil)
			filters, err := readCardFilters(request)
			if err != nil {
				t.Fatal(err)
			}
			if got := laneTickets(selectCards(s, filters)); !slices.Equal(got, test.want) {
				t.Fatalf("tickets = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSearchFullUnicodeFolding(t *testing.T) {
	for _, test := range []struct{ source, want string }{
		{"Straße ẞ", "strasse ss"},
		{"ﬃ ﬀ ﬁ ﬂ", "ffi ff fi fl"},
		{"Σ σ ς", "σ σ σ"},
		{"İ I ı", "i\u0307 i ı"},
		{"Ꭰ ꭰ", "Ꭰ Ꭰ"},
		{"Café cafe\u0301", "café cafe\u0301"},
	} {
		if got := foldSearchText(test.source); got != test.want {
			t.Errorf("fold %q = %q, want %q", test.source, got, test.want)
		}
	}
	s := snapshot{Cards: []card{{ID: "one", Ticket: 1, Title: "Straße", Description: "ﬃ"}, {ID: "two", Ticket: 2, Title: "other"}}, Comments: []comment{{CardID: "two", Text: "Σ"}}}
	for _, query := range []string{"STRASSE", "ffi", "ς"} {
		filters, err := readCardFilters(httptest.NewRequest("GET", "/?q="+url.QueryEscape(query)+"&search_content=true", nil))
		if err != nil {
			t.Fatal(err)
		}
		if rows := selectCards(s, filters); len(rows) != 1 {
			t.Errorf("full fold search %q matched %d cards", query, len(rows))
		}
	}
}

func TestSearchLaneAndNumericLookupOrders(t *testing.T) {
	s := snapshot{
		Lanes:         []lane{{ID: "a", Position: 1}, {ID: "b", Position: 0}},
		Priorities:    []priority{{entity: entity{ID: "normal"}, Rank: 1}},
		Cards:         []card{laneTestCard(1, "normal", "a"), laneTestCard(2, "normal", "a"), laneTestCard(3, "normal", "b")},
		Relationships: []relationship{laneTestLink(2, 1, "blocks")},
	}
	if got := laneTickets(selectCards(s, cardFilters{sort: "lane", archived: "exclude"})); !slices.Equal(got, []uint64{3, 2, 1}) {
		t.Fatalf("lane ordering = %v", got)
	}
	if got := laneTickets(selectCards(s, cardFilters{sort: "ticket", archived: "include"})); !slices.Equal(got, []uint64{1, 2, 3}) {
		t.Fatalf("numeric lookup ordering = %v", got)
	}
	s.Cards[1].Archived = true
	if got := laneTickets(selectCards(s, cardFilters{sort: "lane", archived: "exclude"})); !slices.Equal(got, []uint64{3, 1}) {
		t.Fatalf("archived lane ordering = %v", got)
	}
	if got := selectCards(s, cardFilters{sort: "lane", archived: "only"}); len(got) != 1 || got[0].Ticket != 2 || !got[0].Archived {
		t.Fatal("explicit archived lane selection lost its archived flag")
	}
}

func TestCardSearchQueryValidation(t *testing.T) {
	for _, query := range []string{"q=%C2%B2", "q=%232%C2%B2", "q=", "q=+%09", "q=%1C%1F", "q=" + strings.Repeat("x", 1025), "q=%FF", "q=a&q=b", "search_content=1", "search_content=TRUE", "search_content=", "sort=t%C4%B0cket", "sort=tr%C4%B0age", "archived=%C4%B0nclude", "sort=unknown", "sort=", "ticket=9007199254740992", "ticket=0", "lane_id=no"} {
		if _, err := readCardFilters(httptest.NewRequest("GET", "/?"+query, nil)); err == nil {
			t.Errorf("accepted %q", query)
		}
	}
	for _, query := range []string{"sort=TRIAGE", "sort=LaNe", "sort=TICKET", "q=" + strings.Repeat("é", 1024), "search_content=false"} {
		if _, err := readCardFilters(httptest.NewRequest("GET", "/?"+query, nil)); err != nil {
			t.Errorf("rejected valid query %q", query)
		}
	}
}

func TestCardSearchHTTPPaginationAndDurability(t *testing.T) {
	app, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Search"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	create := func(title, description string) card {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q,"description":%q}`, b.ID, b.entityTag(), b.LaneIDs[0], title, description)
		raw, _ := client.call("POST", "/cards", body, "", 201)
		b.Revision++
		return turnOneDecode[card](t, raw)
	}
	a, z := create("Alpha", "secret concept"), create("Zulu", "secret concept")
	body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`, b.ID, b.entityTag(), z.ID, a.ID)
	client.call("POST", "/relationships", body, "", 201)
	before, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	query := "/cards?board_id=" + b.ID
	raw, _ = client.call("GET", query+"&sort=triage&limit=1", "", "", 200)
	first := turnOnePage[card](t, raw, "cards")
	if len(first.Values) != 1 || first.Values[0].ID != z.ID || first.Values[0].BlockedCardCount != 1 || first.NextCursor == nil {
		t.Fatal("triage was not calculated before limit")
	}
	cursor := url.QueryEscape(*first.NextCursor)
	raw, _ = client.call("GET", query+"&sort=triage&limit=1&cursor="+cursor, "", "", 200)
	if page := turnOnePage[card](t, raw, "cards"); len(page.Values) != 1 || page.Values[0].ID != a.ID || page.NextCursor != nil {
		t.Fatal("triage continuation is incorrect")
	}
	client.call("GET", query+"&sort=ticket&limit=1&cursor="+cursor, "", "", 400)
	raw, _ = client.call("GET", query+"&q=concept&search_content=true&limit=1", "", "", 200)
	searched := turnOnePage[card](t, raw, "cards")
	if searched.NextCursor == nil || searched.Values[0].ID != a.ID {
		t.Fatal("relationship lookup lost ascending numeric order")
	}
	client.call("GET", query+"&q=secret&search_content=true&cursor="+url.QueryEscape(*searched.NextCursor), "", "", 400)
	raw, _ = client.call("GET", query+"&q=concept", "", "", 200)
	if len(turnOnePage[card](t, raw, "cards").Values) != 0 {
		t.Fatal("description search was implicitly enabled")
	}
	raw, _ = client.call("GET", query+"&q=%230002", "", "", 200)
	if rows := turnOnePage[card](t, raw, "cards").Values; len(rows) != 1 || rows[0].ID != z.ID {
		t.Fatal("ticket search failed")
	}
	after, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := marshalCanonical(before)
	right, _ := marshalCanonical(after)
	if string(left) != string(right) {
		t.Fatal("search changed board revision or persisted derived metrics")
	}
}

func TestM067CardTriageAcrossLanes(t *testing.T) {
	_, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"M067 disposable"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	backlog, ready, inProgress, review, completed := b.LaneIDs[0], b.LaneIDs[1], b.LaneIDs[2], b.LaneIDs[3], b.LaneIDs[4]
	create := func(title, laneID, priorityID string) card {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":%q,"priority_id":%q}`,
			b.ID, b.entityTag(), laneID, title, priorityID)
		raw, _ := client.call("POST", "/cards", body, "", 201)
		b.Revision++
		return turnOneDecode[card](t, raw)
	}
	patch := func(item card, body string) card {
		t.Helper()
		raw, _ := client.call("PATCH", "/cards/"+item.ID, body, item.entityTag(), 200)
		b.Revision++
		return turnOneDecode[card](t, raw)
	}
	blocks := func(source, target card) {
		t.Helper()
		body := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"source_card_id":%q,"target_card_id":%q,"kind":"blocks"}`,
			b.ID, b.entityTag(), source.ID, target.ID)
		_, _ = client.call("POST", "/relationships", body, "", 201)
		b.Revision++
	}
	normal := b.PriorityIDs[1]
	first := create("Backlog", backlog, normal)
	second := create("Ready", ready, normal)
	archived := create("Archived", backlog, normal)
	create("Another ready", ready, normal)
	outside := create("Outside", backlog, normal)
	closed := create("Completed urgent", backlog, b.PriorityIDs[0])
	patch(outside, fmt.Sprintf(`{"lane_id":%q}`, inProgress))
	for _, laneID := range []string{inProgress, review, completed} {
		closed = patch(closed, fmt.Sprintf(`{"lane_id":%q}`, laneID))
	}
	blocks(second, outside)
	blocks(archived, first)
	blocks(archived, second)
	raw, _ = client.call("GET", "/cards/"+archived.ID, "", "", 200)
	patch(turnOneDecode[card](t, raw), `{"archived":true}`)

	base := "/cards?board_id=" + b.ID + "&sort=triage"
	lanes := "&lane_id=" + backlog + "&lane_id=" + ready
	for _, tc := range []struct {
		name, query string
		tickets     []uint64
	}{
		{"top three across both lanes", lanes + "&limit=3", []uint64{2, 1, 4}},
		{"include archived", lanes + "&archived=include&limit=3", []uint64{3, 2, 1}},
		{"only archived", lanes + "&archived=only&limit=3", []uint64{3}},
		{"unscoped next", "&unfinished_only=true&limit=1", []uint64{2}},
		{"unscoped search includes Completed", "&limit=1", []uint64{6}},
		{"explicit Completed lane", "&lane_id=" + completed + "&limit=1", []uint64{6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := client.call("GET", base+tc.query, "", "", 200)
			got := turnOnePage[card](t, raw, "cards").Values
			if !slices.Equal(laneTickets(got), tc.tickets) {
				t.Fatalf("tickets = %v, want %v", laneTickets(got), tc.tickets)
			}
			if got[0].Ticket == 2 && got[0].BlockedCardCount != 1 {
				t.Fatal("score missed the blocker target outside the selected lanes")
			}
		})
	}

	raw, _ = client.call("GET", base+lanes+"&limit=1", "", "", 200)
	page := turnOnePage[card](t, raw, "cards")
	if page.NextCursor == nil {
		t.Fatal("first page missing cursor")
	}
	cursor := "&limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
	reordered := "&lane_id=" + strings.ToUpper(ready) + "&lane_id=" + backlog + "&lane_id=" + ready
	raw, _ = client.call("GET", base+reordered+cursor, "", "", 200)
	if rows := turnOnePage[card](t, raw, "cards").Values; !slices.Equal(laneTickets(rows), []uint64{1}) {
		t.Fatal("the equivalent lane set did not continue the same selection")
	}
	client.call("GET", base+lanes+"&unfinished_only=true"+cursor, "", "", 400)
	client.call("GET", base+lanes+"&lane_id=invalid", "", "", 400)
	client.call("GET", base+"&unfinished_only=true&unfinished_only=false", "", "", 400)
}
