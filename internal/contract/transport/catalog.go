package transport

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	"reflect"
)

type CatalogFreshness string
type CatalogFactState string

type CatalogCount struct {
	State CatalogFactState `json:"state"`
	Value *A.Number        `json:"value"`
}
type CatalogAttention struct {
	ReadyForReview CatalogCount `json:"readyForReview"`
	Blocked        CatalogCount `json:"blocked"`
	NeedsTerry     CatalogCount `json:"needsTerry"`
}
type CatalogActivity struct {
	State           CatalogFactState `json:"state"`
	LastCommittedAt *A.Timestamp     `json:"lastCommittedAt"`
	Coverage        *A.Coverage      `json:"coverage"`
}
type WorkspaceCatalogEntry struct {
	ProjectKey       WorkspaceKey          `json:"projectKey"`
	BoardKey         WorkspaceKey          `json:"boardKey"`
	ProjectLabel     WorkspaceLabel        `json:"projectLabel"`
	Label            WorkspaceLabel        `json:"label"`
	Identity         *WorkspaceIdentity    `json:"identity"`
	MemberEpoch      *UUIDv4               `json:"memberEpoch"`
	Availability     WorkspaceAvailability `json:"availability"`
	Freshness        CatalogFreshness      `json:"freshness"`
	AgeSeconds       *A.Number             `json:"ageSeconds"`
	LastObservedAt   *A.Timestamp          `json:"lastObservedAt"`
	BoardRevision    *A.Number             `json:"boardRevision"`
	Activity         CatalogActivity       `json:"activity"`
	Attention        CatalogAttention      `json:"attention"`
	AttentionContext *AttentionContext     `json:"attentionContext"`
}
type WorkspaceCatalog struct {
	APIVersion     A.Version               `json:"apiVersion"`
	WorkspaceID    UUIDv4                  `json:"workspaceId"`
	ConfigRevision A.Positive              `json:"configRevision"`
	ObservedAt     A.Timestamp             `json:"observedAt"`
	Entries        []WorkspaceCatalogEntry `json:"entries"`
}

func catalogEntry(x WorkspaceCatalogEntry) error {
	if x.Availability == "pending" {
		return fmt.Errorf("catalog pending availability")
	}

	if (x.Identity == nil) != (x.MemberEpoch == nil) {
		return fmt.Errorf("catalog identity pair")
	}
	if x.Freshness == "never" {
		if x.LastObservedAt != nil || x.AgeSeconds != nil || x.BoardRevision != nil || x.Identity != nil || x.Activity.State != "unknown" || x.AttentionContext != nil || x.Attention.ReadyForReview.State != "unknown" || x.Attention.Blocked.State != "unknown" || x.Attention.NeedsTerry.State != "unknown" {
			return fmt.Errorf("unobserved catalog facts")
		}
	} else {
		if x.LastObservedAt == nil || x.AgeSeconds == nil || x.BoardRevision == nil || x.Identity == nil || x.AttentionContext == nil {
			return fmt.Errorf("observed catalog facts")
		}
		want := CatalogFactState("known")
		if x.Freshness == "stale" {
			want = "stale"
		}
		if x.Activity.State != want {
			return fmt.Errorf("catalog freshness facts")
		}
		counts := []CatalogCount{x.Attention.Blocked, x.Attention.NeedsTerry, x.Attention.ReadyForReview}
		a := x.AttentionContext.Assignments
		for i, lanes := range [][]A.Lane{a.Blocked, a.NeedsTerry, a.ReadyForReview} {
			expected := want
			if len(lanes) == 0 {
				expected = "not_configured"
			}
			if counts[i].State != expected {
				return fmt.Errorf("catalog attention mapping mismatch")
			}
		}
		if x.Freshness == "fresh" && x.Availability != "ready" {
			return fmt.Errorf("catalog fresh availability")
		}
	}
	return nil
}

func catalogSchema(t reflect.Type, m map[string]any) {
	null := map[string]any{"type": "null"}
	props := func(p map[string]any) map[string]any { return map[string]any{"type": "object", "properties": p} }
	if t == reflect.TypeFor[CatalogCount]() {
		m["oneOf"] = []any{props(map[string]any{"state": map[string]any{"enum": []string{"known", "stale"}}, "value": schema(reflect.TypeFor[A.Number](), false)}), props(map[string]any{"state": map[string]any{"enum": []string{"unknown", "not_configured"}}, "value": null})}
	}
	if t == reflect.TypeFor[CatalogActivity]() {
		m["oneOf"] = []any{props(map[string]any{"state": map[string]any{"enum": []string{"known", "stale"}}, "coverage": schema(reflect.TypeFor[A.Coverage](), false)}), props(map[string]any{"state": map[string]any{"const": "unknown"}, "coverage": null, "lastCommittedAt": null})}
	}
	if t == reflect.TypeFor[WorkspaceCatalog]() {
		m["properties"].(map[string]any)["entries"].(map[string]any)["maxItems"] = 256
	}
	if t == reflect.TypeFor[WorkspaceCatalogEntry]() {
		allowed := []string{}
		for _, state := range enums["WorkspaceAvailability"] {
			if state != "pending" {
				allowed = append(allowed, state)
			}
		}
		m["properties"].(map[string]any)["availability"] = map[string]any{"type": "string", "enum": allowed}
		variants := []any{}
		for _, state := range []string{"never", "fresh", "stale"} {
			p := map[string]any{"freshness": map[string]any{"const": state}}
			fact := "known"
			if state == "never" {
				fact = "unknown"
				for _, name := range []string{"identity", "memberEpoch", "ageSeconds", "lastObservedAt", "boardRevision", "attentionContext"} {
					p[name] = null
				}
			} else {
				p["attentionContext"] = schema(reflect.TypeFor[AttentionContext](), false)
				p["identity"] = schema(reflect.TypeFor[WorkspaceIdentity](), false)
				p["memberEpoch"] = schema(reflect.TypeFor[UUIDv4](), false)
				p["ageSeconds"] = schema(reflect.TypeFor[A.Number](), false)
				p["lastObservedAt"] = schema(reflect.TypeFor[A.Timestamp](), false)
				p["boardRevision"] = schema(reflect.TypeFor[A.Number](), false)
				if state == "fresh" {
					p["availability"] = map[string]any{"const": "ready"}
				} else {
					fact = "stale"
				}
			}
			p["activity"] = props(map[string]any{"state": map[string]any{"const": fact}})
			attentionProps := map[string]any{}
			for _, category := range []string{"blocked", "needsTerry", "readyForReview"} {
				states := []string{fact, "not_configured"}
				if state == "never" {
					states = []string{"unknown"}
				}
				attentionProps[category] = props(map[string]any{"state": map[string]any{"enum": states}})
			}
			p["attention"] = props(attentionProps)
			variants = append(variants, props(p))
		}
		m["oneOf"] = variants
	}
}
