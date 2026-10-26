// Package contract owns boundary validation and canonical semantic encoding.
// It performs no authentication, permission admission, persistence or dispatch.
package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxInteger uint64 = 9007199254740991

// DecodeJSON rejects ambiguous JSON before encoding/json can repair Unicode or
// collapse duplicate object keys. Numbers retain their original lexical form.
func DecodeJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	// Validate escaped UTF-16 code units before the standard decoder replaces them.
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		for i++; i < len(data) && data[i] != '"'; i++ {
			if data[i] != '\\' {
				continue
			}
			i++
			if i >= len(data) {
				return nil, fmt.Errorf("truncated escape")
			}
			if data[i] != 'u' {
				continue
			}
			x, e := unit(data, i)
			if e != nil {
				return nil, e
			}
			i += 4
			if x >= 0xdc00 && x <= 0xdfff {
				return nil, fmt.Errorf("unpaired surrogate")
			}
			if x >= 0xd800 && x <= 0xdbff {
				if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return nil, fmt.Errorf("unpaired surrogate")
				}
				y, e := unit(data, i+2)
				if e != nil || y < 0xdc00 || y > 0xdfff {
					return nil, fmt.Errorf("unpaired surrogate")
				}
				i += 6
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, e := decodeValue(d, 0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return v, nil
}

func unit(b []byte, i int) (uint64, error) {
	if i+4 >= len(b) {
		return 0, fmt.Errorf("truncated Unicode escape")
	}
	v, e := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
	if e != nil {
		return 0, fmt.Errorf("invalid Unicode escape")
	}
	return v, nil
}

func decodeValue(d *json.Decoder, depth int) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	switch x := t.(type) {
	case json.Delim:
		if depth >= 32 {
			return nil, fmt.Errorf("JSON depth exceeds 32")
		}
		if x == '{' {
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("object key")
				}
				if _, ok = m[key]; ok {
					return nil, fmt.Errorf("duplicate key %q", key)
				}
				v, e := decodeValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			if _, e = d.Token(); e != nil {
				return nil, e
			}
			return m, nil
		}
		if x == '[' {
			a := []any{}
			for d.More() {
				v, e := decodeValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			if _, e = d.Token(); e != nil {
				return nil, e
			}
			return a, nil
		}
		return nil, fmt.Errorf("unexpected delimiter")
	case json.Number:
		if _, e := integer(x); e != nil {
			return nil, e
		}
		return x, nil
	case string, bool, nil:
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported JSON value")
	}
}

func integer(n json.Number) (uint64, error) {
	s := string(n)
	if s == "" || strings.Trim(s, "0123456789") != "" || (len(s) > 1 && s[0] == '0') {
		return 0, fmt.Errorf("expected unsigned decimal integer")
	}
	v, e := strconv.ParseUint(s, 10, 64)
	if e != nil || v > MaxInteger {
		return 0, fmt.Errorf("integer exceeds safe range")
	}
	return v, nil
}

// Canonical accepts only the strict JSON tree returned by DecodeJSON (or trees
// with the same primitive types). It never invokes Go's default string encoder.
func Canonical(v any) ([]byte, error) {
	var b bytes.Buffer
	if e := encode(&b, v, 0); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}
func Hash(v any) (string, error) {
	b, e := Canonical(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func encode(b *bytes.Buffer, v any, depth int) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		if !utf8.ValidString(x) {
			return fmt.Errorf("invalid UTF-8")
		}
		b.WriteByte('"')
		for _, r := range x {
			switch {
			case r == '"' || r == '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case r < 32:
				fmt.Fprintf(b, "\\u%04x", r)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
	case json.Number:
		if _, e := integer(x); e != nil {
			return e
		}
		b.WriteString(string(x))
	case map[string]any:
		if depth >= 32 {
			return fmt.Errorf("JSON depth exceeds 32")
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			for _, r := range k {
				if r > 127 {
					return fmt.Errorf("non-ASCII schema key")
				}
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if e := encode(b, k, depth+1); e != nil {
				return e
			}
			b.WriteByte(':')
			if e := encode(b, x[k], depth+1); e != nil {
				return e
			}
		}
		b.WriteByte('}')
	case []any:
		if depth >= 32 {
			return fmt.Errorf("JSON depth exceeds 32")
		}
		b.WriteByte('[')
		for i, v := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if e := encode(b, v, depth+1); e != nil {
				return e
			}
		}
		b.WriteByte(']')
	default:
		return fmt.Errorf("unsupported canonical type %T", v)
	}
	return nil
}
