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
	ProjectKey     WorkspaceKey          `json:"projectKey"`
	BoardKey       WorkspaceKey          `json:"boardKey"`
	ProjectLabel   WorkspaceLabel        `json:"projectLabel"`
	Label          WorkspaceLabel        `json:"label"`
	Identity       *WorkspaceIdentity    `json:"identity"`
	MemberEpoch    *UUIDv4               `json:"memberEpoch"`
	Availability   WorkspaceAvailability `json:"availability"`
	Freshness      CatalogFreshness      `json:"freshness"`
	AgeSeconds     *A.Number             `json:"ageSeconds"`
	LastObservedAt *A.Timestamp          `json:"lastObservedAt"`
	BoardRevision  *A.Number             `json:"boardRevision"`
	Activity       CatalogActivity       `json:"activity"`
	Attention      CatalogAttention      `json:"attention"`
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
	if x.Attention.Blocked.State != "not_configured" || x.Attention.NeedsTerry.State != "not_configured" {
		return fmt.Errorf("unsupported catalog attention")
	}
	if (x.Identity == nil) != (x.MemberEpoch == nil) {
		return fmt.Errorf("catalog identity pair")
	}
	if x.Freshness == "never" {
		if x.LastObservedAt != nil || x.AgeSeconds != nil || x.BoardRevision != nil || x.Identity != nil || x.Activity.State != "unknown" || x.Attention.ReadyForReview.State != "unknown" {
			return fmt.Errorf("unobserved catalog facts")
		}
	} else {
		if x.LastObservedAt == nil || x.AgeSeconds == nil || x.BoardRevision == nil || x.Identity == nil {
			return fmt.Errorf("observed catalog facts")
		}
		want := CatalogFactState("known")
		if x.Freshness == "stale" {
			want = "stale"
		}
		if x.Activity.State != want || x.Attention.ReadyForReview.State != want {
			return fmt.Errorf("catalog freshness facts")
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
	if t == reflect.TypeFor[CatalogAttention]() {
		for _, n := range []string{"blocked", "needsTerry"} {
			m["properties"].(map[string]any)[n] = map[string]any{"allOf": []any{schema(reflect.TypeFor[CatalogCount](), false), props(map[string]any{"state": map[string]any{"const": "not_configured"}, "value": null})}}
		}
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
				for _, name := range []string{"identity", "memberEpoch", "ageSeconds", "lastObservedAt", "boardRevision"} {
					p[name] = null
				}
			} else {
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
			p["attention"] = props(map[string]any{"readyForReview": props(map[string]any{"state": map[string]any{"const": fact}})})
			variants = append(variants, props(p))
		}
		m["oneOf"] = variants
	}
}
