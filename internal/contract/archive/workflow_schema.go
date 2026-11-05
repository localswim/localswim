package archive

import "reflect"

// The schema describes structural constraints; graph reachability, role counts,
// Unicode EqualFold labels and edge/right relationships remain Go invariants.
func workflowSchema(t reflect.Type, p, m map[string]any) {
	switch t {
	case reflect.TypeFor[AttentionAssignments]():
		for _, key := range []string{"blocked", "needsTerry", "readyForReview"} {
			limit := 12
			if key == "readyForReview" {
				limit = 1
			}
			p[key] = map[string]any{"type": "array", "items": scalarSchema(reflect.TypeFor[Lane](), ""), "maxItems": limit, "uniqueItems": true, "x-order": "exact lane ID ascending"}
		}
	case reflect.TypeFor[Workflow]():
		p["profile"] = map[string]any{"const": "default"}
		p["lanes"].(map[string]any)["minItems"] = 4
		p["lanes"].(map[string]any)["maxItems"] = 16
		p["transitions"].(map[string]any)["minItems"] = 3
		p["transitions"].(map[string]any)["maxItems"] = 64
		p["transitions"].(map[string]any)["x-order"] = "strict (from,to) exact endpoint ascending"
		m["x-max-utf8-bytes"] = 65536
	case reflect.TypeFor[WorkflowLane]():
		p["id"] = scalarSchema(reflect.TypeFor[Lane](), "")
		p["label"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 80, "pattern": `^[^\u0000-\u001f\u007f]+$`, "x-untrimmed": true, "x-unique-labels": "Go strings.EqualFold"}
		p["role"] = map[string]any{"enum": []string{"initial", "in_progress", "review", "completed", "waiting"}}
	case reflect.TypeFor[WorkflowTransition]():
		p["from"] = scalarSchema(reflect.TypeFor[Lane](), "")
		p["to"] = scalarSchema(reflect.TypeFor[Lane](), "")
		p["actors"] = map[string]any{"type": "array", "items": map[string]any{"enum": []string{"automation", "human"}}, "minItems": 1, "maxItems": 2, "uniqueItems": true, "x-order": "ASCII ascending"}
	case reflect.TypeFor[WorkflowActorRights]():
		for _, key := range []string{"human", "automation"} {
			rights := append([]string{}, ordinaryWorkflowRights...)
			if key == "human" {
				rights = append([]string{"approve"}, rights...)
			}
			p[key] = map[string]any{"type": "array", "items": map[string]any{"enum": rights}, "uniqueItems": true, "x-order": "ASCII ascending"}
		}
	}
}
