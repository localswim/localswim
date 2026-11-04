package archive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// workflowRawLimits runs only after strict JSON and shape validation. It retains
// the original bytes of typed Workflow values, including internal whitespace;
// decoding/re-encoding a definition would erase the submitted size evidence.
// RawMessage is deliberately opaque and prose is never parsed as embedded JSON.
func workflowRawLimits(t reflect.Type, raw []byte) error {
	if !containsWorkflow(t, map[reflect.Type]bool{}) {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		t = t.Elem()
	}
	if t == reflect.TypeFor[Workflow]() {
		if len(raw) > 65536 {
			return fmt.Errorf("workflow exceeds 64 KiB")
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return fmt.Errorf("invalid workflow container")
		}
		for _, field := range fields(t) {
			if value, present := object[fieldName(field)]; present {
				if err := workflowRawLimits(field.Type, value); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("invalid workflow collection")
		}
		for _, value := range values {
			if err := workflowRawLimits(t.Elem(), value); err != nil {
				return err
			}
		}
	}
	return nil
}

func containsWorkflow(t reflect.Type, seen map[reflect.Type]bool) bool {
	if t == reflect.TypeFor[json.RawMessage]() || seen[t] {
		return false
	}
	if t == reflect.TypeFor[Workflow]() {
		return true
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Array, reflect.Slice:
		return containsWorkflow(t.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if containsWorkflow(t.Field(i).Type, seen) {
				return true
			}
		}
	}
	return false
}
