package export

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/localswim/localswim/internal/contract"
)

// readableLength counts the shared R representation before allocating its bytes.
// Object key order cannot change its length. It is a size preflight only: shared
// C03 validation and encoding remain authoritative for all values and wire bytes.
func readableLength(v any) (int, error) {
	n, e := valueLength(reflect.ValueOf(v), 0)
	if e != nil {
		return 0, e
	}
	if n >= MaxJSONBytes {
		return 0, ErrLimit
	}
	return n + 1, nil
}
func quotedLength(s string) int {
	n := 2
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] < 32:
			n += 6
		case s[i] == '"' || s[i] == '\\':
			n += 2
		default:
			n++
		}
	}
	return n
}
func valueLength(v reflect.Value, depth int) (int, error) {
	if !v.IsValid() {
		return 4, nil
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		raw := v.Bytes()
		if len(raw) > MaxJSONBytes {
			return 0, ErrLimit
		}
		x, e := contract.DecodeJSON(raw)
		if e != nil {
			return 0, e
		}
		return valueLength(reflect.ValueOf(x), depth)
	}
	if v.Type() == reflect.TypeFor[json.Number]() {
		return len(v.String()), nil
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 4, nil
		}
		return valueLength(v.Elem(), depth)
	}
	switch v.Kind() {
	case reflect.String:
		n := quotedLength(v.String())
		if n > MaxJSONBytes {
			return 0, ErrLimit
		}
		return n, nil
	case reflect.Bool:
		if v.Bool() {
			return 4, nil
		}
		return 5, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return len(strconv.FormatUint(v.Uint(), 10)), nil
	case reflect.Slice, reflect.Array, reflect.Struct, reflect.Map:
	default:
		return 0, fmt.Errorf("unsupported archive size value")
	}
	if depth >= 32 {
		return 0, fmt.Errorf("JSON depth exceeds 32")
	}
	depth++
	n, count := 2, 0
	add := func(key string, child reflect.Value, object bool) error {
		part, e := valueLength(child, depth)
		if e != nil {
			return e
		}
		// Each member begins with LF+indent. All except the first also add a comma.
		over := 1 + 2*depth
		if count > 0 {
			over++
		}
		if object {
			over += quotedLength(key) + 2
		}
		if part > MaxJSONBytes-over || n > MaxJSONBytes-over-part {
			return ErrLimit
		}
		n += over + part
		count++
		return nil
	}
	switch v.Kind() {
	case reflect.Array, reflect.Slice:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return 4, nil
		}
		for i := 0; i < v.Len(); i++ {
			if e := add("", v.Index(i), false); e != nil {
				return 0, e
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return 4, nil
		}
		it := v.MapRange()
		for it.Next() {
			if it.Key().Kind() != reflect.String {
				return 0, fmt.Errorf("non-string key")
			}
			if e := add(it.Key().String(), it.Value(), true); e != nil {
				return 0, e
			}
		}
	case reflect.Struct:
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			if len(tag) > 1 && tag[1] == "omitempty" && v.Field(i).IsZero() {
				continue
			}
			if e := add(name, v.Field(i), true); e != nil {
				return 0, e
			}
		}
	}
	if count > 0 {
		n += 1 + 2*(depth-1)
	}
	if n > MaxJSONBytes {
		return 0, ErrLimit
	}
	return n, nil
}
