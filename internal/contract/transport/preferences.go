package transport

import (
	"fmt"
	"reflect"

	A "github.com/localswim/localswim/internal/contract/archive"
)

type WorkspacePreferenceAction string

// Preferences expose only registered references. Durable tuple binding remains
// private and conveys no board grant or selected-context authority.
type WorkspacePinReference struct {
	ProjectKey WorkspaceKey `json:"projectKey"`
	BoardKey   WorkspaceKey `json:"boardKey"`
}
type WorkspacePreferences struct {
	APIVersion          A.Version               `json:"apiVersion"`
	WorkspaceID         UUIDv4                  `json:"workspaceId"`
	PreferencesRevision A.Positive              `json:"preferencesRevision"`
	Pins                []WorkspacePinReference `json:"pins"`
}
type WorkspacePreferencesRequest struct {
	APIVersion                  A.Version                 `json:"apiVersion"`
	ExpectedPreferencesRevision A.Positive                `json:"expectedPreferencesRevision"`
	Action                      WorkspacePreferenceAction `json:"action"`
	Pin                         *WorkspacePinReference    `json:"pin"`
	Order                       []WorkspacePinReference   `json:"order"`
}
type WorkspacePreferencesError struct {
	APIVersion  A.Version             `json:"apiVersion"`
	Outcome     RefusedOutcome        `json:"outcome"`
	Error       Error                 `json:"error"`
	Preferences *WorkspacePreferences `json:"preferences"`
}

func preferenceReferences(pins []WorkspacePinReference) error {
	if len(pins) > 256 {
		return fmt.Errorf("preference pin limit")
	}
	seen := map[WorkspacePinReference]bool{}
	for _, p := range pins {
		if seen[p] {
			return fmt.Errorf("duplicate preference reference")
		}
		seen[p] = true
	}
	return nil
}
func preferenceRequest(x WorkspacePreferencesRequest) error {
	if x.Action == "reorder" {
		if x.Pin != nil {
			return fmt.Errorf("reorder pin must be null")
		}
	} else if x.Pin == nil || len(x.Order) != 0 {
		return fmt.Errorf("pin action requires reference and empty order")
	}
	return preferenceReferences(x.Order)
}
func preferencesSchema(t reflect.Type, m map[string]any) {
	if t == reflect.TypeFor[WorkspacePreferences]() {
		p := m["properties"].(map[string]any)["pins"].(map[string]any)
		p["maxItems"], p["uniqueItems"] = 256, true
	}
	if t == reflect.TypeFor[WorkspacePreferencesRequest]() {
		m["properties"].(map[string]any)["order"].(map[string]any)["maxItems"] = 256
		m["properties"].(map[string]any)["order"].(map[string]any)["uniqueItems"] = true
		m["oneOf"] = []any{
			map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"enum": []string{"pin", "unpin"}}, "pin": schema(reflect.TypeFor[WorkspacePinReference](), false), "order": map[string]any{"type": "array", "maxItems": 0}}},
			map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"const": "reorder"}, "pin": map[string]any{"type": "null"}}},
		}
	}
}
