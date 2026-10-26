package transport

import (
	"fmt"
	"reflect"
	"strings"
)

func sessionCodeDeclaration() (map[string]any, string) {
	variants := []any{}
	declarations := []string{}
	for _, outcome := range []string{"presented", "refused", "failed", "unavailable", "unknown"} {
		m := schema(reflect.TypeFor[SessionCodeCLI](), true)
		p := m["properties"].(map[string]any)
		p["outcome"] = map[string]any{"const": outcome}
		o := map[string]string{"outcome": fmt.Sprintf("%q", outcome)}
		result := schema(reflect.TypeFor[SessionCodeResult](), true)
		rp := result["properties"].(map[string]any)
		ro := map[string]string{}
		presented := outcome == "presented"
		rp["presented"] = map[string]any{"const": presented}
		ro["presented"] = fmt.Sprint(presented)
		if presented {
			for _, f := range fields(reflect.TypeFor[SessionCodeResult]()) {
				if f.Type.Kind() == reflect.Pointer {
					rp[fieldName(f)] = schema(f.Type.Elem(), false)
					ro[fieldName(f)] = typescript(f.Type.Elem(), false)
				}
			}
			p["result"] = result
			p["error"] = map[string]any{"type": "null"}
			o["result"] = replacedDeclaration(reflect.TypeFor[SessionCodeResult](), ro)
			o["error"] = "null"
		} else {
			p["result"] = map[string]any{"anyOf": []any{result, map[string]any{"type": "null"}}}
			p["error"] = schema(reflect.TypeFor[CredentialError](), false)
			o["result"] = replacedDeclaration(reflect.TypeFor[SessionCodeResult](), ro) + " | null"
			o["error"] = "CredentialError"
		}
		variants = append(variants, m)
		declarations = append(declarations, replacedDeclaration(reflect.TypeFor[SessionCodeCLI](), o))
	}
	return map[string]any{"oneOf": variants}, strings.Join(declarations, " | ")
}
