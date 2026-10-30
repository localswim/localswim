package transport

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func summaryPage() CardSearchRead {
	return CardSearchRead{APIVersion: 1, Scope: "card-search", Result: CardSearchView{
		Identity: A.Identity{BoardID: "00000000-0000-4000-8000-000000000001", Incarnation: "00000000-0000-4000-8000-000000000002"},
		Revision: 2, Query: CardSearchQuery{Archive: "active"}, Items: []CardSummary{{CardID: "00000000-0000-4000-8000-000000000003", Ticket: 1, Revision: 2, Title: "Visible", Lane: "Ready for Work", Priority: "P3"}},
	}}
}

func TestCardSummaryPageStrictBoundsAndFilters(t *testing.T) {
	valid := summaryPage()
	raw, err := Readable(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode[CardSearchRead](raw); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*CardSearchRead){
		"blank title": func(x *CardSearchRead) { x.Result.Items[0].Title = " \t" },
		"bad order": func(x *CardSearchRead) {
			second := x.Result.Items[0]
			second.CardID = "00000000-0000-4000-8000-000000000004"
			second.Ticket, second.Priority = 2, "P0"
			x.Result.Items = append(x.Result.Items, second)
		},
		"duplicate identity":   func(x *CardSearchRead) { x.Result.Items = append(x.Result.Items, x.Result.Items[0]) },
		"wrong archive mode":   func(x *CardSearchRead) { x.Result.Items[0].Archived = true },
		"unknown archive mode": func(x *CardSearchRead) { x.Result.Query.Archive = "unknown" },
		"invalid owner":        func(x *CardSearchRead) { owner := "outside"; x.Result.Query.Owner = &owner },
		"wrong owner result": func(x *CardSearchRead) {
			owner := "00000000-0000-4000-8000-000000000009"
			x.Result.Query.Owner = &owner
		},
		"query bytes":            func(x *CardSearchRead) { x.Result.Query.Q = strings.Repeat("船", 342) },
		"cursor on short page":   func(x *CardSearchRead) { cursor := "token.tag"; x.Result.NextCursor = &cursor },
		"nonempty revision zero": func(x *CardSearchRead) { x.Result.Revision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			value := summaryPage()
			change(&value)
			if Validate(value) == nil {
				t.Fatal("invalid page accepted")
			}
		})
	}
	valid.Result.Query.Q = strings.Repeat("船", 341) + "a"
	valid.Result.Items = []CardSummary{}
	valid.Result.Revision = 0
	if err := Validate(valid); err != nil {
		t.Fatal("empty source/exact query limit refused", err)
	}
}

func TestCardSummaryContinuationPageLimit(t *testing.T) {
	page := summaryPage()
	page.Result.Items = []CardSummary{}
	for i := 1; i <= 20; i++ {
		row := summaryPage().Result.Items[0]
		row.CardID = A.UUID(fmt.Sprintf("00000000-0000-4000-8000-%012d", i+10))
		row.Ticket = A.Positive(i)
		page.Result.Items = append(page.Result.Items, row)
	}
	cursor := "bounded.tag"
	page.Result.NextCursor = &cursor
	if err := Validate(page); err != nil {
		t.Fatal(err)
	}
	page.Result.Items = append(page.Result.Items, summaryPage().Result.Items[0])
	if err := Validate(page); err == nil {
		t.Fatal("21 result rows accepted")
	}
	raw, _ := json.Marshal(summaryPage())
	if _, err := Decode[BoardRead](raw); err == nil {
		t.Fatal("partial summary accepted as complete board")
	}
}
