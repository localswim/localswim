package transport

import (
	"encoding/json"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestCatalogStrictCountsAndUnknownFacts(t *testing.T) {
	zero := A.Number(0)
	for _, value := range []CatalogCount{{State: "known", Value: &zero}, {State: "unknown"}, {State: "not_configured"}, {State: "stale", Value: &zero}} {
		b, _ := json.Marshal(value)
		if _, e := Decode[CatalogCount](b); e != nil {
			t.Fatal(e)
		}
	}
	for _, body := range []string{`{"state":"known","value":null}`, `{"state":"unknown","value":0}`, `{"state":"known","value":-1}`, `{"state":"known","value":0,"path":"private"}`, `{"state":"known","value":0,"value":1}`} {
		if _, e := Decode[CatalogCount]([]byte(body)); e == nil {
			t.Fatal("invalid catalog count accepted")
		}
	}
	entry := WorkspaceCatalogEntry{ProjectKey: "p", BoardKey: "b", ProjectLabel: "P", Label: "B", Availability: "unavailable", Freshness: "never", Activity: CatalogActivity{State: "unknown"}, Attention: CatalogAttention{ReadyForReview: CatalogCount{State: "unknown"}, Blocked: CatalogCount{State: "not_configured"}, NeedsTerry: CatalogCount{State: "not_configured"}}}
	b, _ := json.Marshal(entry)
	if _, e := Decode[WorkspaceCatalogEntry](b); e != nil {
		t.Fatal(e)
	}
	if _, e := Decode[WorkspaceCatalogEntry]([]byte(strings.Replace(string(b), `"boardRevision":null`, `"boardRevision":0`, 1))); e == nil {
		t.Fatal("unobserved zero accepted")
	}
	entry.Attention.Blocked = CatalogCount{State: "known", Value: &zero}
	if Validate(entry) == nil {
		t.Fatal("unsupported configured count accepted")
	}
	files, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(files["web/generated/transport/contracts.d.ts"]), "export type WorkspaceCatalog =") {
		t.Fatal("catalog missing from aggregate declarations")
	}
}
