package main

import (
	"bytes"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"strings"
	"testing"
)

func TestWorkspaceCatalogParser(t *testing.T) {
	for _, args := range [][]string{{"--workspace", "private", "workspace", "catalog"}, {"--workspace", "private", "--json", "workspace", "catalog"}} {
		q, err := parseLocal(args)
		if err != nil {
			t.Fatal(err)
		}
		command, domain, err := parseWorkspaceCommand(q)
		if err != nil || domain != nil || command.family != "workspace" || command.action != "catalog" {
			t.Fatal("catalog misrouted")
		}
	}
	for _, args := range [][]string{{"workspace", "catalog"}, {"--data", "private", "workspace", "catalog"}, {"--workspace", "private", "--project", "p", "workspace", "catalog"}, {"--workspace", "private", "--board", "b", "workspace", "catalog"}, {"--workspace", "private", "workspace", "catalog", "--input", "x"}, {"--workspace", "private", "workspace", "catalog", "--json"}, {"--workspace", "private", "--data", "x", "workspace", "catalog"}} {
		q, err := parseLocal(args)
		if err == nil {
			_, _, err = parseWorkspaceCommand(q)
		}
		if err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestWorkspaceCatalogReadableStates(t *testing.T) {
	zero, age := A.Number(0), A.Number(9)
	epoch := T.UUIDv4("00000000-0000-4000-8000-000000000001")
	catalog := T.WorkspaceCatalog{WorkspaceID: epoch, ConfigRevision: 2, Entries: []T.WorkspaceCatalogEntry{{ProjectKey: "p", BoardKey: "b", ProjectLabel: "Project", Label: "Board", Freshness: "stale", AgeSeconds: &age, Identity: &T.WorkspaceIdentity{BoardID: A.UUID(epoch), Incarnation: A.UUID(epoch)}, MemberEpoch: &epoch, AttentionContext: &T.AttentionContext{PolicyVersion: 2, Source: "explicit"}, Attention: T.CatalogAttention{Blocked: T.CatalogCount{State: "stale", Value: &zero}, NeedsTerry: T.CatalogCount{State: "not_configured"}, ReadyForReview: T.CatalogCount{State: "unknown"}}}}}
	var out bytes.Buffer
	if err := writeWorkspaceText(&out, catalog); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"config revision 2", "p/b", "Project", "Board", "incarnation", "epoch", "Policy 2, explicit", "Blocked  0 (stale, age 9s)", "Needs Terry  Not configured", "Ready for Review  Unavailable"} {
		if !strings.Contains(out.String(), text) {
			t.Fatal("missing readable context", text)
		}
	}
	catalog.Entries[0].Attention.Blocked = T.CatalogCount{State: "known", Value: &zero}
	out.Reset()
	if err := writeWorkspaceText(&out, catalog); err != nil || !strings.Contains(out.String(), "Blocked  0\n") {
		t.Fatal("known zero omitted")
	}
}
