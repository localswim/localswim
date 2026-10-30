//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"fmt"
	T "github.com/localswim/localswim/internal/contract/transport"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestSearchNativeHTTPMatchesFiltersPaginationAndConservation(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Skip("requires explicit private native test parent")
	}
	r, root := startTest(t)
	info, e := ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	rev := 0
	create := func(title, description string) string {
		out := term(t, request(t, r, "POST", r.boardPath()+"cards", mutationWire(t, r, "", rev, 0, map[string]any{"title": title, "description": description}), info.Token, nil, nil), "committed")
		rev++
		for _, ref := range out.ResultRefs {
			if ref.Kind == "card" {
				return string(ref.ID)
			}
		}
		t.Fatal("card ref")
		return ""
	}
	card := create("CAFÉ Straße <b>.* 日本語", "current description")
	term(t, request(t, r, "POST", r.boardPath()+"cards/"+card+"/comments", mutationWire(t, r, card, rev, 1, map[string]any{"text": "retained comment"}), info.Token, nil, nil), "committed")
	rev++
	page := func(values url.Values) T.CardSearchRead {
		t.Helper()
		w := request(t, r, "GET", r.boardPath()+"cards?"+values.Encode(), nil, info.Token, nil, nil)
		out, e := T.Decode[T.CardSearchRead](w.b)
		if w.status != 200 || e != nil {
			t.Fatalf("search status=%d decode=%v", w.status, e)
		}
		return out
	}
	for _, tc := range []struct {
		q string
		n int
	}{{"café", 1}, {"strasse", 0}, {"straße", 1}, {"cafe\u0301", 0}, {".*", 1}, {"current description", 1}, {"retained comment", 1}, {"#1", 1}, {"01", 0}, {strings.ToUpper(card), 1}, {card[:20], 0}, {"no match", 0}} {
		if p := page(url.Values{"q": {tc.q}}); len(p.Result.Items) != tc.n || p.Result.Query.Q != tc.q {
			t.Fatalf("literal match %q", tc.q)
		}
	}
	for n := 0; n < 25; n++ {
		create(fmt.Sprintf("unmatched %d", n), "")
	}
	for n := 0; n < 20; n++ {
		create(fmt.Sprintf("needle %d", n), "")
	}
	first20 := page(url.Values{"q": {"needle"}})
	if len(first20.Result.Items) != 20 || first20.Result.NextCursor != nil {
		t.Fatal("exact20")
	}
	create("needle 21", "")
	before := request(t, r, "GET", strings.TrimSuffix(r.boardPath(), "/"), nil, info.Token, nil, nil)
	first := page(url.Values{"q": {"needle"}, "lane": {"Ready for Work"}, "owner": {"unassigned"}, "priority": {"P3"}})
	if len(first.Result.Items) != 20 || first.Result.NextCursor == nil {
		t.Fatal("21-match first page")
	}
	second := page(url.Values{"q": {"needle"}, "lane": {"Ready for Work"}, "owner": {"unassigned"}, "priority": {"P3"}, "cursor": {*first.Result.NextCursor}})
	if len(second.Result.Items) != 1 || second.Result.NextCursor != nil || second.Result.Items[0].Ticket <= first.Result.Items[19].Ticket {
		t.Fatal("continuation order")
	}
	stale := request(t, r, "GET", r.boardPath()+"cards?"+url.Values{"q": {"NEEDLE"}, "lane": {"Ready for Work"}, "owner": {"unassigned"}, "priority": {"P3"}, "cursor": {*first.Result.NextCursor}}.Encode(), nil, info.Token, nil, nil)
	if stale.status != 409 {
		t.Fatal("case-only cursor reuse")
	}
	for _, v := range []url.Values{{"owner": {"00000000-0000-4000-8000-000000000099"}}, {"lane": {"In Progress"}}, {"priority": {"P0"}}} {
		if len(page(v).Result.Items) != 0 {
			t.Fatal("AND typed filter")
		}
	}
	after := request(t, r, "GET", strings.TrimSuffix(r.boardPath(), "/"), nil, info.Token, nil, nil)
	if !bytes.Equal(before.b, after.b) {
		t.Fatal("search changed full board facts")
	}
	bad := request(t, r, "GET", r.boardPath()+"cards?q="+url.QueryEscape(strings.Repeat("é", 513)), nil, info.Token, nil, nil)
	if bad.status != 400 {
		t.Fatal("byte bound")
	}
	// Handler restart invalidates its ephemeral cursor but preserves committed matches.
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	restarted, e := Start(context.Background(), Options{DataDir: root, Port: info.Port})
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	r = restarted
	info, e = ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	stale = request(t, r, "GET", r.boardPath()+"cards?"+url.Values{"q": {"needle"}, "lane": {"Ready for Work"}, "owner": {"unassigned"}, "priority": {"P3"}, "cursor": {*first.Result.NextCursor}}.Encode(), nil, info.Token, nil, nil)
	if stale.status != 409 {
		t.Fatal("restart reused cursor")
	}
	if len(page(url.Values{"q": {"needle"}}).Result.Items) != 20 {
		t.Fatal("restart lost committed matches")
	}
	configuredGrantSQL(t, r, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", r.id.Incarnation, r.id.BotGrant)
	denied := request(t, r, "GET", r.boardPath()+"cards?q=needle", nil, info.Token, nil, nil)
	if denied.status != 403 {
		t.Fatal("search reused revoked authority")
	}

}

// Controlled reference-removal fixture proves retained history is not current
// search text; ordinary mutations seed the prose and revisions first.
func TestSearchNativeCurrentReferencesAndArchive(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Skip("requires explicit private native test parent")
	}
	r, root := startTest(t)
	info, e := ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	card := catalogCreate(t, r, info.Token)
	term(t, request(t, r, "POST", r.boardPath()+"cards/"+card+"/comments", mutationWire(t, r, card, 1, 1, map[string]any{"text": "removed unique phrase"}), info.Token, nil, nil), "committed")
	count := func(q, mode string) int {
		t.Helper()
		w := request(t, r, "GET", r.boardPath()+"cards?"+url.Values{"q": {q}, "archive": {mode}}.Encode(), nil, info.Token, nil, nil)
		p, e := T.Decode[T.CardSearchRead](w.b)
		if w.status != 200 || e != nil {
			t.Fatal("current search decode")
		}
		return len(p.Result.Items)
	}
	if count("removed unique phrase", "active") != 1 {
		t.Fatal("current comment not matched")
	}
	configuredGrantSQL(t, r, "DELETE FROM current_reference WHERE incarnation=? AND card_id=? AND kind='comment'", r.id.Incarnation, card)
	if count("removed unique phrase", "active") != 0 {
		t.Fatal("noncurrent comment matched")
	}
	term(t, request(t, r, "PATCH", r.boardPath()+"cards/"+card, mutationWire(t, r, card, 2, 2, map[string]any{"description": "replacement prose"}), info.Token, nil, nil), "committed")
	if count("Synthetic", "active") != 0 || count("replacement prose", "active") != 1 {
		t.Fatal("historical description matched")
	}
	term(t, request(t, r, "PATCH", r.boardPath()+"cards/"+card, mutationWire(t, r, card, 3, 3, map[string]any{"archived": true}), info.Token, nil, nil), "committed")
	if count("replacement prose", "active") != 0 || count("replacement prose", "archived") != 1 || count("replacement prose", "all") != 1 {
		t.Fatal("archive search projection")
	}
}
