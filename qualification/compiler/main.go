package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// This descriptor is synthetic. It does not define any application contract.
type field struct {
	Name, Kind         string
	Optional, Nullable bool
}

var descriptor = []field{{"label", "string", false, false}, {"count", "integer", false, false}, {"note", "string", true, true}}

func generate() (map[string][]byte, error) {
	properties := map[string]any{}
	required := []string{}
	var ts bytes.Buffer
	ts.WriteString("// Generated synthetic sample; no application contract.\nexport interface Sample {\n")
	for _, f := range descriptor {
		p := map[string]any{"type": f.Kind}
		kind := f.Kind
		if kind == "integer" {
			kind = "number"
			p["minimum"] = 0
			p["maximum"] = int64(9007199254740991)
		}
		if f.Nullable {
			p["type"] = []string{f.Kind, "null"}
			kind += " | null"
		}
		properties[f.Name] = p
		optional := ""
		if f.Optional {
			optional = "?"
		} else {
			required = append(required, f.Name)
		}
		fmt.Fprintf(&ts, "  %s%s: %s;\n", f.Name, optional, kind)
	}
	ts.WriteString("}\n")
	schema := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "title": "SyntheticSample", "type": "object", "additionalProperties": false, "properties": properties, "required": required}
	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"sample.schema.json": append(b, '\n'), "sample.d.ts": ts.Bytes()}, nil
}

func validate(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v map[string]any
	if d.Decode(&v) != nil || v == nil {
		return false
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return false
	}
	for k := range v {
		known := false
		for _, f := range descriptor {
			if f.Name == k {
				known = true
			}
		}
		if !known {
			return false
		}
	}
	for _, f := range descriptor {
		x, ok := v[f.Name]
		if !ok {
			if f.Optional {
				continue
			}
			return false
		}
		if x == nil {
			if f.Nullable {
				continue
			}
			return false
		}
		switch f.Kind {
		case "string":
			if _, ok := x.(string); !ok {
				return false
			}
		case "integer":
			n, ok := x.(json.Number)
			if !ok {
				return false
			}
			z, exact := new(big.Rat).SetString(n.String())
			if !exact || !z.IsInt() || z.Sign() < 0 || z.Num().Cmp(big.NewInt(9007199254740991)) > 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func main() {
	out := flag.String("out", "", "output directory")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}
	files, e := generate()
	if e == nil {
		e = os.MkdirAll(*out, 0700)
	}
	if e == nil {
		for name, b := range files {
			if e = os.WriteFile(filepath.Join(*out, name), b, 0600); e != nil {
				break
			}
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
