package archive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/localswim/localswim/internal/contract"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
var timeRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
var commandValues = []string{"card.create", "card.edit", "comment.add", "attachment.add", "link.add", "link.remove", "review-handoff", "approve", "grant.create", "grant.revoke", "history.clear", "retention.set"}
var enums = map[string][]string{
	"Priority": {"P0", "P1", "P2", "P3", "P4", "P5"}, "Lane": {"Ready for Work", "In Progress", "Ready for Review", "Completed"}, "Kind": {"human", "automation"}, "Reason": {"cleared", "expired"}, "GapClass": {"history", "activity", "outcome_payload"}, "FactKind": {"board", "card", "comment", "link", "attachment", "approval", "grant"}, "Command": commandValues, "RecoveryCommand": {"history.clear", "retention.set"}, "TerminalClass": {"COMMITTED", "NOT_COMMITTED"}, "Phase": {"preparing", "prepared", "published"}, "ReceiptResult": {"incomplete", "complete", "failed"}, "RestoreOutcome": {"confirmed", "failed", "unknown"}, "HashAlgorithm": {"sha256"}, "RestoreScope": {"restore"}, "LinkKind": {"blocking", "informational"}, "EntityKind": {"card", "comment", "link", "attachment"},
}

// Decode validates exact fields, required nulls, scalar rules and local semantic
// invariants before extracting a typed value. Graph validation is separate.
func Decode[T any](data []byte) (T, error) {
	var out T
	v, e := contract.DecodeJSON(data)
	if e != nil {
		return out, e
	}
	t := reflect.TypeFor[T]()
	if e = shape(t, v, "$", ""); e != nil {
		return out, e
	}
	if e = json.Unmarshal(data, &out); e != nil {
		return out, e
	}
	if e = semantics(reflect.ValueOf(out)); e != nil {
		return *new(T), e
	}
	return out, nil
}

// Validate checks in-memory values through exactly the same strict boundary.
// A nil required slice is rejected instead of silently defaulting it to [].
func Validate(v any) error {
	if v == nil {
		return fmt.Errorf("nil boundary")
	}
	if e := validStrings(reflect.ValueOf(v)); e != nil {
		return e
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	x, e := contract.DecodeJSON(b)
	if e != nil {
		return e
	}
	if e = shape(reflect.TypeOf(v), x, "$", ""); e != nil {
		return e
	}
	return semantics(reflect.ValueOf(v))
}
func validStrings(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return validStrings(v.Elem())
	}
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return fmt.Errorf("floating-point JSON value")
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("non-string JSON key")
		}
		iter := v.MapRange()
		for iter.Next() {
			if e := validStrings(iter.Key()); e != nil {
				return e
			}
			if e := validStrings(iter.Value()); e != nil {
				return e
			}
		}
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return fmt.Errorf("invalid UTF-8")
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if e := validStrings(v.Field(i)); e != nil {
				return e
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Type() == reflect.TypeFor[json.RawMessage]() {
			_, e := contract.DecodeJSON(v.Bytes())
			return e
		}
		for i := 0; i < v.Len(); i++ {
			if e := validStrings(v.Index(i)); e != nil {
				return e
			}
		}
	}
	return nil
}
func shape(t reflect.Type, v any, path, format string) error {
	fail := func() error { return fmt.Errorf("%s: invalid %s", path, t.Name()) }
	if t.Kind() == reflect.Pointer {
		if v == nil {
			return nil
		}
		return shape(t.Elem(), v, path, format)
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		if _, ok := v.(map[string]any); !ok {
			return fail()
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return fail()
		}
		if t == reflect.TypeFor[Target]() {
			if len(m) != 1 {
				return fail()
			}
			for k, x := range m {
				if k != "cardId" && k != "grantId" {
					return fail()
				}
				return shape(reflect.TypeFor[UUID](), x, path+"."+k, "")
			}
		}
		known := map[string]bool{}
		for _, f := range fields(t) {
			known[fieldName(f)] = true
		}
		for n := range m {
			if !known[n] {
				return fmt.Errorf("%s: unknown field", path)
			}
		}
		for _, f := range fields(t) {
			n := fieldName(f)
			x, ok := m[n]
			if !ok {
				if optionalField(f) {
					continue
				}
				return fmt.Errorf("%s.%s: required", path, n)
			}
			if e := shape(f.Type, x, path+"."+n, f.Tag.Get("validate")); e != nil {
				return e
			}
		}
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return fail()
		}
		prior := ""
		for i, x := range a {
			if e := shape(t.Elem(), x, path, ""); e != nil {
				return e
			}
			if t.Elem() == reflect.TypeFor[UUID]() {
				s := x.(string)
				if i > 0 && s <= prior {
					return fmt.Errorf("%s: UUID order/duplicate", path)
				}
				prior = s
			}
		}
	case reflect.String:
		s, ok := v.(string)
		if !ok || !utf8.ValidString(s) {
			return fail()
		}
		if a := enums[t.Name()]; len(a) > 0 {
			found := false
			for _, x := range a {
				found = found || x == s
			}
			if !found {
				return fail()
			}
		}
		switch t.Name() {
		case "UUID":
			if !uuidRE.MatchString(s) || s == "00000000-0000-0000-0000-000000000000" {
				return fail()
			}
		case "Digest":
			if !digestRE.MatchString(s) {
				return fail()
			}
		case "Timestamp":
			if !timeRE.MatchString(s) {
				return fail()
			}
			x, e := time.Parse("2006-01-02T15:04:05Z", s)
			if e != nil || x.Year() < 1 {
				return fail()
			}
		}
		switch format {
		case "title":
			if n := utf8.RuneCountInString(s); n < 1 || n > 200 || strings.TrimSpace(s) == "" {
				return fail()
			}
		case "prose", "comment":
			if len(s) > 65536 || (format == "comment" && strings.TrimSpace(s) == "") {
				return fail()
			}
		case "attachment-name":
			if len(s) > 255 {
				return fail()
			}
		case "media-label":
			if len(s) > 127 {
				return fail()
			}
			for _, r := range s {
				if r > 127 {
					return fail()
				}
			}
		}
	case reflect.Uint64:
		n, ok := v.(json.Number)
		if !ok {
			return fail()
		}
		u, e := strconv.ParseUint(string(n), 10, 64)
		if e != nil || u > contract.MaxInteger {
			return fail()
		}
		if format == "blob-length" && u > 10485760 {
			return fail()
		}
		switch t.Name() {
		case "Positive":
			if u == 0 {
				return fail()
			}
		case "Version":
			if u != 1 {
				return fail()
			}
		case "Days":
			if u < 1 || u > 3650 {
				return fail()
			}
		}
	case reflect.Bool:
		if _, ok := v.(bool); !ok {
			return fail()
		}
	default:
		return fail()
	}
	return nil
}

// Canonical and Readable reuse the C01 strict scalar encoder. They do not
// normalize Unicode, arrays, optional fields or line endings.
func Canonical(v any) ([]byte, error) {
	x, e := jsonValue(v)
	if e != nil {
		return nil, e
	}
	return contract.Canonical(x)
}
func jsonValue(v any) (any, error) {
	if e := validStrings(reflect.ValueOf(v)); e != nil {
		return nil, e
	}
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return contract.DecodeJSON(b)
}
func Readable(v any) ([]byte, error) {
	c, e := Canonical(v)
	if e != nil {
		return nil, e
	}
	var b bytes.Buffer
	if e = json.Indent(&b, c, "", "  "); e != nil {
		return nil, e
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// DecodeReadable additionally enforces exact R bytes for an archive JSON member.
func DecodeReadable[T any](data []byte) (T, error) {
	v, e := Decode[T](data)
	if e != nil {
		return v, e
	}
	b, e := Readable(v)
	if e != nil {
		return v, e
	}
	if !bytes.Equal(b, data) {
		return *new(T), fmt.Errorf("noncanonical readable bytes")
	}
	return v, nil
}
