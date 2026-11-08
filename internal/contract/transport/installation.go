package transport

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	"reflect"
)

const InstallationInspectionResponseLimit = 2048

type InstallationInspectionScope string
type InstallationInspectionState string
type InstallationInspectionCode string
type InstallationInspectionResult struct {
	Identity          A.Identity                  `json:"identity"`
	RuntimeGeneration UUIDv4                      `json:"runtimeGeneration"`
	State             InstallationInspectionState `json:"state"`
	Code              InstallationInspectionCode  `json:"code"`
}
type InstallationInspectionResponse struct {
	APIVersion A.Version                    `json:"apiVersion"`
	Scope      InstallationInspectionScope  `json:"scope"`
	Result     InstallationInspectionResult `json:"result"`
}

var installationCodes = []string{"PLATFORM_UNQUALIFIED", "SOURCE_UNQUALIFIED", "PATH_UNQUALIFIED", "PUBLISHER_UNQUALIFIED", "FILE_CHANGED", "INSPECTION_UNKNOWN", "INSPECTION_TIMED_OUT"}

func installationValid(v InstallationInspectionResult) error {
	if v.State == "candidate" && v.Code == "CANDIDATE_PRESENT" || v.State == "missing" && v.Code == "TOOL_MISSING" {
		return nil
	}
	if v.State == "unqualified" {
		for _, c := range installationCodes {
			if string(v.Code) == c {
				return nil
			}
		}
	}
	return fmt.Errorf("invalid installation observation")
}
func installationSchema(t reflect.Type, m map[string]any) {
	if t == reflect.TypeFor[InstallationInspectionResponse]() {
		m["x-max-utf8-bytes"] = InstallationInspectionResponseLimit
	}
	if t == reflect.TypeFor[InstallationInspectionResult]() {
		m["oneOf"] = []any{
			map[string]any{"type": "object", "properties": map[string]any{"state": map[string]any{"const": "candidate"}, "code": map[string]any{"const": "CANDIDATE_PRESENT"}}},
			map[string]any{"type": "object", "properties": map[string]any{"state": map[string]any{"const": "missing"}, "code": map[string]any{"const": "TOOL_MISSING"}}},
			map[string]any{"type": "object", "properties": map[string]any{"state": map[string]any{"const": "unqualified"}, "code": map[string]any{"enum": installationCodes}}}}
	}
}
