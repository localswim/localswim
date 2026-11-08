package transport

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	"reflect"
	"regexp"
)

const ProbeRequestLimit = 512
const ProbeControlQueryLimit = 384
const ProbeResponseLimit = 2048
const ProbeUnknownMessage = "Identity check outcome unknown"

type ProbeKind string
type ProbeState string
type ProbeCode string
type ProbeToolVersion string
type ProbeCapability string
type ProbeControlScope string
type ProbeStopScope string
type ProbeControlPhase string
type ProbeStopPhase string

type ProbeRequest struct {
	Incarnation                     A.UUID `json:"incarnation"`
	ExpectedRuntimeGeneration       UUIDv4 `json:"expectedRuntimeGeneration"`
	ExpectedConfigurationGeneration UUIDv4 `json:"expectedConfigurationGeneration"`
	ProbeGeneration                 UUIDv4 `json:"probeGeneration"`
}

type ProbeResult struct {
	Version                 A.Version         `json:"version"`
	Identity                A.Identity        `json:"identity"`
	RuntimeGeneration       UUIDv4            `json:"runtimeGeneration"`
	ConfigurationGeneration UUIDv4            `json:"configurationGeneration"`
	ProbeGeneration         UUIDv4            `json:"probeGeneration"`
	Kind                    ProbeKind         `json:"kind"`
	State                   ProbeState        `json:"state"`
	Code                    ProbeCode         `json:"code"`
	ToolVersion             *ProbeToolVersion `json:"toolVersion"`
	Capability              *ProbeCapability  `json:"capability"`
	Joined                  bool              `json:"joined"`
}

type ProbeControlView struct {
	Identity                A.Identity        `json:"identity"`
	RuntimeGeneration       UUIDv4            `json:"runtimeGeneration"`
	ConfigurationGeneration UUIDv4            `json:"configurationGeneration"`
	ProbeGeneration         UUIDv4            `json:"probeGeneration"`
	Phase                   ProbeControlPhase `json:"phase"`
	Joined                  bool              `json:"joined"`
}
type ProbeStopView struct {
	Identity                A.Identity     `json:"identity"`
	RuntimeGeneration       UUIDv4         `json:"runtimeGeneration"`
	ConfigurationGeneration UUIDv4         `json:"configurationGeneration"`
	ProbeGeneration         UUIDv4         `json:"probeGeneration"`
	Phase                   ProbeStopPhase `json:"phase"`
	Joined                  bool           `json:"joined"`
}
type ProbeControlResponse struct {
	APIVersion A.Version         `json:"apiVersion"`
	Scope      ProbeControlScope `json:"scope"`
	Result     ProbeControlView  `json:"result"`
}
type ProbeStopResponse struct {
	APIVersion A.Version      `json:"apiVersion"`
	Scope      ProbeStopScope `json:"scope"`
	Result     ProbeStopView  `json:"result"`
}
type ProbeUnknownError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ProbeOutcomeUnknown struct {
	APIVersion A.Version         `json:"apiVersion"`
	Outcome    UnknownOutcome    `json:"outcome"`
	Error      ProbeUnknownError `json:"error"`
}

// ASCII SemVer 2.0.0. Numeric prerelease identifiers cannot have leading zeros.
var probeSemverRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

var probePairs = map[ProbeState][]string{
	"supported":   {"OK"},
	"unavailable": {"TOOL_MISSING", "PLATFORM_UNQUALIFIED"},
	"unsupported": {"TOOL_UNSUPPORTED", "PARSE_UNSUPPORTED"},
	"unknown":     {"EXIT_FAILED", "PARSE_ERROR", "OUTPUT_LIMIT", "TIMED_OUT", "CANCELLED"},
	"incomplete":  {"DRAIN_INCOMPLETE"},
}

func probeValid(v ProbeResult) error {
	found := false
	for _, code := range probePairs[v.State] {
		found = found || string(v.Code) == code
	}
	if !found || v.Kind != "identity" || v.Capability != nil || v.Joined != (v.State != "incomplete") || (v.ToolVersion != nil) != (v.State == "supported") {
		return fmt.Errorf("invalid identity probe result")
	}
	return nil
}
func probeByteLimit(t reflect.Type) int {
	switch t {
	case reflect.TypeFor[ProbeRequest]():
		return ProbeRequestLimit
	case reflect.TypeFor[ProbeResult](), reflect.TypeFor[ProbeControlResponse](), reflect.TypeFor[ProbeStopResponse](), reflect.TypeFor[ProbeOutcomeUnknown]():
		return ProbeResponseLimit
	}
	return 0
}
func probeSchema(t reflect.Type, m map[string]any) {
	p := m["properties"].(map[string]any)
	if n := probeByteLimit(t); n > 0 {
		m["x-max-utf8-bytes"] = n
	}
	switch t {
	case reflect.TypeFor[ProbeResult]():
		p["capability"] = map[string]any{"type": "null"}
		variants := []any{}
		// Stable order keeps generation deterministic.
		for _, state := range []ProbeState{"supported", "unavailable", "unsupported", "unknown", "incomplete"} {
			version := map[string]any{"type": "null"}
			if state == "supported" {
				version = schema(reflect.TypeFor[ProbeToolVersion](), false)
			}
			variants = append(variants, map[string]any{"type": "object", "properties": map[string]any{"state": map[string]any{"const": state}, "code": map[string]any{"enum": probePairs[state]}, "joined": map[string]any{"const": state != "incomplete"}, "toolVersion": version}})
		}
		m["oneOf"] = variants
	case reflect.TypeFor[ProbeControlView]():
		m["oneOf"] = []any{map[string]any{"type": "object", "properties": map[string]any{"phase": map[string]any{"const": "ready"}, "joined": map[string]any{"const": true}}}, map[string]any{"type": "object", "properties": map[string]any{"phase": map[string]any{"enum": []string{"running", "draining"}}, "joined": map[string]any{"const": false}}}}
	case reflect.TypeFor[ProbeStopView]():
		m["oneOf"] = []any{map[string]any{"type": "object", "properties": map[string]any{"phase": map[string]any{"const": "joined"}, "joined": map[string]any{"const": true}}}, map[string]any{"type": "object", "properties": map[string]any{"phase": map[string]any{"const": "incomplete"}, "joined": map[string]any{"const": false}}}}
	case reflect.TypeFor[ProbeUnknownError]():
		p["code"] = map[string]any{"const": "OUTCOME_UNKNOWN"}
		p["message"] = map[string]any{"const": ProbeUnknownMessage}
	}
}

// ValidateProbeResultContext binds an already received result to the selected
// operation. Boundary validity alone never establishes current authority.
func ValidateProbeResultContext(v ProbeResult, identity A.Identity, runtimeGeneration, configurationGeneration, probeGeneration UUIDv4) error {
	if err := Validate(v); err != nil {
		return err
	}
	if err := probeContext(v.Identity, v.RuntimeGeneration, v.ConfigurationGeneration, identity, runtimeGeneration, configurationGeneration); err != nil {
		return err
	}
	if !v4RE.MatchString(string(probeGeneration)) || v.ProbeGeneration != probeGeneration {
		return fmt.Errorf("identity probe operation mismatch")
	}
	return nil
}
func ValidateProbeControlContext(v ProbeControlResponse, identity A.Identity, runtimeGeneration, configurationGeneration UUIDv4) error {
	if err := Validate(v); err != nil {
		return err
	}
	return probeContext(v.Result.Identity, v.Result.RuntimeGeneration, v.Result.ConfigurationGeneration, identity, runtimeGeneration, configurationGeneration)
}
func ValidateProbeStopContext(v ProbeStopResponse, identity A.Identity, runtimeGeneration, configurationGeneration, probeGeneration UUIDv4) error {
	if err := Validate(v); err != nil {
		return err
	}
	if err := probeContext(v.Result.Identity, v.Result.RuntimeGeneration, v.Result.ConfigurationGeneration, identity, runtimeGeneration, configurationGeneration); err != nil {
		return err
	}
	if !v4RE.MatchString(string(probeGeneration)) || v.Result.ProbeGeneration != probeGeneration {
		return fmt.Errorf("identity stop operation mismatch")
	}
	return nil
}
func probeContext(got A.Identity, gotRuntime, gotConfiguration UUIDv4, expected A.Identity, expectedRuntime, expectedConfiguration UUIDv4) error {
	if A.Validate(expected) != nil || !v4RE.MatchString(string(expectedRuntime)) || !v4RE.MatchString(string(expectedConfiguration)) || got != expected || gotRuntime != expectedRuntime || gotConfiguration != expectedConfiguration {
		return fmt.Errorf("identity probe context mismatch")
	}
	return nil
}
