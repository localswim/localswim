package boardapi

import (
	T "github.com/localswim/localswim/internal/contract/transport"
	"net/url"
	"strings"
	"testing"
)

func TestSearchLiteralAndIdentity(t *testing.T) {
	c := T.CardSummary{CardID: testCard, Ticket: 42, Title: "CAFÉ Straße <b>.* 日本語"}
	for _, tc := range []struct {
		q    string
		want bool
	}{
		{"café", true}, {"strasse", false}, {"straße", true}, {"cafe\u0301", false}, {".*", true}, {"<b>", true}, {"日本", true}, {"CAFÉ", true}, {"42", true}, {"#42", true}, {"042", false}, {"+42", false}, {"#042", false}, {"4", false}, {" 42", false}, {" ", true}, {"#9007199254740992", false}, {strings.ToUpper(testCard), true}, {testCard[:20], false}, {"desc needle", true}, {"retained needle", true}, {"old needle", false},
	} {
		if got := searchMatches(strings.ToLower(tc.q), c, "Desc Needle", []string{"Retained Needle"}); got != tc.want {
			t.Errorf("literal case %q=%v", tc.q, got)
		}
	}
}
func TestSearchQueryValidationAndExactDigest(t *testing.T) {
	for _, q := range []string{"", "  ", strings.Repeat("é", 512)} {
		r, e := parseSearch("q=" + url.QueryEscape(q) + "&lane=In+Progress&owner=unassigned&priority=P0&archive=all")
		if e != nil || r.q != q {
			t.Fatal("valid exact query")
		}
	}
	for _, q := range []string{"q=" + url.QueryEscape(strings.Repeat("é", 513)), "owner=", "owner=INVALID", "lane=ready", "priority=P6", "priority=p0", "q=%ff", "lane=In+Progress&lane=Completed"} {
		if _, e := parseSearch(q); e == nil {
			t.Fatal("invalid typed query accepted")
		}
	}
	a, _ := parseSearch("q=CAFÉ")
	b, _ := parseSearch("q=café")
	if filterDigest(a) == filterDigest(b) {
		t.Fatal("case-only query cursor binding lost")
	}
}
