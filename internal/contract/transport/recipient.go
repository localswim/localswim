package transport

import (
	"encoding/json"
	"fmt"

	A "github.com/localswim/localswim/internal/contract/archive"
	"reflect"
)

const RecipientRequestLimit = 2048
const RecipientResponseLimit = 4096

type RecipientScope string
type RecipientState string
type RecipientReason string
type RecipientKind string
type RecipientPolicy string
type RecipientThreadID string
type RecipientReference struct {
	Kind     RecipientKind     `json:"kind"`
	ThreadID RecipientThreadID `json:"threadId"`
}
type RecipientInput struct {
	Version   A.Version          `json:"version"`
	Recipient RecipientReference `json:"recipient"`
	Policy    RecipientPolicy    `json:"policy"`
}
type RecipientConfigureRequest struct {
	Incarnation                   A.UUID         `json:"incarnation"`
	ExpectedRuntimeGeneration     UUIDv4         `json:"expectedRuntimeGeneration"`
	ExpectedConfigurationRevision A.Number       `json:"expectedConfigurationRevision"`
	RequestID                     UUIDv4         `json:"requestId"`
	Input                         RecipientInput `json:"input"`
}
type RecipientView struct {
	Identity                A.Identity       `json:"identity"`
	RuntimeGeneration       UUIDv4           `json:"runtimeGeneration"`
	State                   RecipientState   `json:"state"`
	Reason                  *RecipientReason `json:"reason"`
	ConfigurationRevision   A.Number         `json:"configurationRevision"`
	ConfigurationGeneration *UUIDv4          `json:"configurationGeneration"`
	RequestID               *UUIDv4          `json:"requestId"`
	Input                   *RecipientInput  `json:"input"`
}
type RecipientResponse struct {
	APIVersion A.Version      `json:"apiVersion"`
	Scope      RecipientScope `json:"scope"`
	Result     RecipientView  `json:"result"`
}
type RecipientUnknownError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type RecipientOutcomeUnknown struct {
	APIVersion A.Version             `json:"apiVersion"`
	Outcome    UnknownOutcome        `json:"outcome"`
	Error      RecipientUnknownError `json:"error"`
}

const RecipientUnknownMessage = "Recipient request outcome is unknown. Inspect status; do not submit again."

func recipientValid(v RecipientView) error {
	configured := v.State == "configured"
	if configured {
		if v.Reason != nil || v.ConfigurationRevision != 1 || v.ConfigurationGeneration == nil || v.RequestID == nil || v.Input == nil {
			return fmt.Errorf("invalid recipient configured view")
		}
	} else if v.ConfigurationRevision != 0 || v.ConfigurationGeneration != nil || v.RequestID != nil || v.Input != nil || v.State == "unconfigured" && v.Reason != nil || v.State != "unconfigured" && v.Reason == nil {
		return fmt.Errorf("invalid recipient redaction")
	}
	return nil
}
func recipientByteLimit(t reflect.Type) int {
	switch t {
	case reflect.TypeFor[RecipientInput]():
		return 1024
	case reflect.TypeFor[RecipientConfigureRequest]():
		return RecipientRequestLimit
	case reflect.TypeFor[RecipientResponse](), reflect.TypeFor[RecipientOutcomeUnknown]():
		return RecipientResponseLimit
	}
	return 0
}
func recipientSchema(t reflect.Type, m map[string]any) {
	p := m["properties"].(map[string]any)
	if n := recipientByteLimit(t); n > 0 {
		m["x-max-utf8-bytes"] = n
	}
	switch t {
	case reflect.TypeFor[RecipientConfigureRequest]():
		p["expectedConfigurationRevision"].(map[string]any)["const"] = 0
	case reflect.TypeFor[RecipientUnknownError]():
		p["code"].(map[string]any)["const"] = "OUTCOME_UNKNOWN"
		p["message"].(map[string]any)["const"] = RecipientUnknownMessage
	case reflect.TypeFor[RecipientView]():
		m["oneOf"] = []any{map[string]any{"type": "object", "properties": map[string]any{"state": map[string]any{"const": "configured"}, "reason": map[string]any{"type": "null"}, "configurationRevision": map[string]any{"const": 1}, "configurationGeneration": schema(reflect.TypeFor[UUIDv4](), false), "requestId": schema(reflect.TypeFor[UUIDv4](), false), "input": schema(reflect.TypeFor[RecipientInput](), false)}}, map[string]any{"type": "object", "properties": map[string]any{"state": map[string]any{"const": "unconfigured"}, "reason": map[string]any{"type": "null"}, "configurationRevision": map[string]any{"const": 0}, "configurationGeneration": map[string]any{"type": "null"}, "requestId": map[string]any{"type": "null"}, "input": map[string]any{"type": "null"}}}, map[string]any{"type": "object", "properties": map[string]any{"state": map[string]any{"enum": []string{"unavailable", "unknown"}}, "reason": map[string]any{"enum": []string{"storage", "foreign", "unknown"}}, "configurationRevision": map[string]any{"const": 0}, "configurationGeneration": map[string]any{"type": "null"}, "requestId": map[string]any{"type": "null"}, "input": map[string]any{"type": "null"}}}}
	}
}
func recipientInputBytes(v RecipientInput) bool {
	b, e := json.Marshal(v)
	return e == nil && len(b) <= 1024 && v.Version == 1 && v.Recipient.Kind == "codex-thread" && recipientThreadRE.MatchString(string(v.Recipient.ThreadID)) && v.Policy == "conservative-metadata-v1"
}
