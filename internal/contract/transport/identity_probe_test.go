package transport

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestIdentityProbeSharedVectors(t *testing.T) {
	b, e := os.ReadFile("testdata/identity-probe-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Name, Type, JSON, CaseKey string
		Valid                     bool
	}
	if json.Unmarshal(b, &rows) != nil || len(rows) == 0 {
		t.Fatal("invalid vector inventory")
	}
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			var value any
			var err error
			switch row.Type {
			case "ProbeRequest":
				value, err = Decode[ProbeRequest]([]byte(row.JSON))
			case "ProbeResult":
				value, err = Decode[ProbeResult]([]byte(row.JSON))
			case "ProbeControlResponse":
				value, err = Decode[ProbeControlResponse]([]byte(row.JSON))
			case "ProbeStopResponse":
				value, err = Decode[ProbeStopResponse]([]byte(row.JSON))
			case "ProbeOutcomeUnknown":
				value, err = Decode[ProbeOutcomeUnknown]([]byte(row.JSON))
			default:
				t.Fatal("unknown vector family")
			}
			if (err == nil) != row.Valid {
				t.Fatalf("acceptance mismatch: %v", err)
			}
			if err == nil {
				if err = Validate(value); err != nil {
					t.Fatal(err)
				}
				encoded, err := Readable(value)
				if err != nil || len(encoded) > probeByteLimit(reflect.TypeOf(value)) {
					t.Fatal("invalid encoder", err)
				}
			}
		})
	}
}
func TestIdentityProbeInvalidUTF8(t *testing.T) {
	b, _ := Readable(ProbeOutcomeUnknown{APIVersion: 1, Outcome: "unknown", Error: ProbeUnknownError{Code: "OUTCOME_UNKNOWN", Message: ProbeUnknownMessage}})
	bad := bytes.Replace(b, []byte("Identity"), []byte{0xff}, 1)
	if _, e := Decode[ProbeOutcomeUnknown](bad); e == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}
func TestIdentityProbeSchemaRegistration(t *testing.T) {
	files, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	var root struct {
		Defs map[string]map[string]any `json:"$defs"`
	}
	if json.Unmarshal(files["schemas/transport/contracts.schema.json"], &root) != nil {
		t.Fatal("schema")
	}
	for _, name := range []string{"ProbeResult", "ProbeRequest", "ProbeControlView", "ProbeStopView", "ProbeControlResponse", "ProbeStopResponse", "ProbeOutcomeUnknown", "ProbeUnknownError"} {
		s, ok := root.Defs[name]
		if !ok || s["additionalProperties"] != false {
			t.Fatal("missing strict schema", name)
		}
		if !strings.Contains(string(files["web/generated/transport/contracts.d.ts"]), "export type "+name+" =") {
			t.Fatal("missing TS", name)
		}
	}
	for name, limit := range map[string]int{"ProbeRequest": 512, "ProbeResult": 2048, "ProbeControlResponse": 2048, "ProbeStopResponse": 2048, "ProbeOutcomeUnknown": 2048} {
		if root.Defs[name]["x-max-utf8-bytes"] != float64(limit) {
			t.Fatal("schema byte limit", name)
		}
	}
	if len(root.Defs["ProbeResult"]["oneOf"].([]any)) != 5 {
		t.Fatal("result state schema")
	}
}

func TestIdentityProbeContextBindings(t *testing.T) {
	b, e := os.ReadFile("testdata/identity-probe-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct{ Name, JSON string }
	json.Unmarshal(b, &rows)
	var result ProbeResult
	var control ProbeControlResponse
	var stop ProbeStopResponse
	for _, row := range rows {
		switch row.Name {
		case "ProbeResult-valid":
			result, e = Decode[ProbeResult]([]byte(row.JSON))
		case "ProbeControlResponse-valid":
			control, e = Decode[ProbeControlResponse]([]byte(row.JSON))
		case "ProbeStopResponse-valid":
			stop, e = Decode[ProbeStopResponse]([]byte(row.JSON))
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	identity, runtime, configuration, probe := result.Identity, result.RuntimeGeneration, result.ConfigurationGeneration, result.ProbeGeneration
	if ValidateProbeResultContext(result, identity, runtime, configuration, probe) != nil || ValidateProbeControlContext(control, identity, runtime, configuration) != nil || ValidateProbeStopContext(stop, identity, runtime, configuration, probe) != nil {
		t.Fatal("valid context refused")
	}
	other := UUIDv4("00000000-0000-4000-8000-000000000099")
	for _, field := range []string{"board", "incarnation", "runtime", "configuration", "probe"} {
		t.Run(field, func(t *testing.T) {
			i, r, c, p := identity, runtime, configuration, probe
			switch field {
			case "board":
				i.BoardID = "00000000-0000-4000-8000-000000000099"
			case "incarnation":
				i.Incarnation = "00000000-0000-4000-8000-000000000099"
			case "runtime":
				r = other
			case "configuration":
				c = other
			case "probe":
				p = other
			}
			if ValidateProbeResultContext(result, i, r, c, p) == nil || ValidateProbeStopContext(stop, i, r, c, p) == nil {
				t.Fatal("mismatch accepted")
			}
			if field != "probe" && ValidateProbeControlContext(control, i, r, c) == nil {
				t.Fatal("control mismatch accepted")
			}
		})
	}
	if ValidateProbeResultContext(result, identity, "", configuration, probe) == nil || ValidateProbeStopContext(stop, identity, runtime, configuration, "") == nil {
		t.Fatal("invalid expected generation accepted")
	}
}
