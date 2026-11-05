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
	entry := WorkspaceCatalogEntry{ProjectKey: "p", BoardKey: "b", ProjectLabel: "P", Label: "B", Availability: "unavailable", Freshness: "never", Activity: CatalogActivity{State: "unknown"}, Attention: CatalogAttention{ReadyForReview: CatalogCount{State: "unknown"}, Blocked: CatalogCount{State: "unknown"}, NeedsTerry: CatalogCount{State: "unknown"}}}
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

func TestCatalogContextAndCountCoherence(t *testing.T) {
	zero := A.Number(0)
	stamp := A.Timestamp("2026-11-05T12:00:00Z")
	epoch := UUIDv4("00000000-0000-4000-8000-000000000003")
	context := AttentionContext{PolicyVersion: 1, Profile: "default", Source: "implicit", Assignments: A.ResolveWorkflowAttention(nil), Lanes: []AttentionLane{{ID: "Ready for Review", Label: "Ready for Review", Role: "review"}}}
	entry := WorkspaceCatalogEntry{ProjectKey: "p", BoardKey: "b", ProjectLabel: "P", Label: "B", Identity: &WorkspaceIdentity{InstallationID: epoch, BoardID: "00000000-0000-4000-8000-000000000001", Incarnation: "00000000-0000-4000-8000-000000000002"}, MemberEpoch: &epoch, Availability: "ready", Freshness: "fresh", AgeSeconds: &zero, LastObservedAt: &stamp, BoardRevision: &zero, Activity: CatalogActivity{State: "known", Coverage: &A.Coverage{Gaps: []A.Gap{}}}, AttentionContext: &context, Attention: CatalogAttention{Blocked: CatalogCount{State: "not_configured"}, NeedsTerry: CatalogCount{State: "not_configured"}, ReadyForReview: CatalogCount{State: "known", Value: &zero}}}
	if err := Validate(entry); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*WorkspaceCatalogEntry){
		"missing context": func(e *WorkspaceCatalogEntry) { e.AttentionContext = nil },
		"mapped disabled": func(e *WorkspaceCatalogEntry) { e.Attention.ReadyForReview = CatalogCount{State: "not_configured"} },
		"unmapped zero":   func(e *WorkspaceCatalogEntry) { e.Attention.Blocked = CatalogCount{State: "known", Value: &zero} },
		"mixed freshness": func(e *WorkspaceCatalogEntry) { e.Attention.ReadyForReview.State = "stale" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := entry
			change(&changed)
			if Validate(changed) == nil {
				t.Fatal("incoherent catalog accepted")
			}
		})
	}
	entry.Freshness = "stale"
	entry.Availability = "unavailable"
	entry.Activity.State = "stale"
	entry.Attention.ReadyForReview.State = "stale"
	if err := Validate(entry); err != nil {
		t.Fatal(err)
	}
	entry.Freshness = "fresh"
	entry.Availability = "ready"
	entry.Activity.State = "known"
	id := A.UUID(epoch)
	context.WorkflowID = &id
	context.PolicyVersion = 2
	context.Source = "explicit"
	context.Assignments = A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{}}
	context.Lanes = []AttentionLane{}
	entry.Attention.ReadyForReview = CatalogCount{State: "not_configured"}
	if err := Validate(entry); err != nil {
		t.Fatal(err)
	}
}
