package transport

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func sessionCodeFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/session-code-cli/" + name + ".json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestSessionCodeCLIPositive(t *testing.T) {
	for _, name := range []string{"presented", "unknown", "unavailable"} {
		t.Run(name, func(t *testing.T) {
			raw := sessionCodeFixture(t, name)
			x, e := Decode[SessionCodeCLI](raw)
			if e != nil {
				t.Fatal(e)
			}
			if e = Validate(x); e != nil {
				t.Fatal(e)
			}
			readable, e := Readable(x)
			if e != nil || !bytes.HasSuffix(readable, []byte("\n")) || bytes.HasSuffix(readable, []byte("\n\n")) {
				t.Fatal("readable status must have one final LF", e)
			}
			if _, e = Decode[SessionCodeCLI](readable); e != nil {
				t.Fatal(e)
			}
			for _, secretKey := range []string{"codeHash", "token", "bearer", "credentialId", "path"} {
				if bytes.Contains(readable, []byte(`"`+secretKey+`"`)) {
					t.Fatalf("unexpected public key %s", secretKey)
				}
			}
		})
	}
	unknown := sessionCodeFixture(t, "unknown")
	for _, outcome := range []string{"refused", "failed", "unavailable", "unknown"} {
		raw := bytes.Replace(unknown, []byte(`"outcome": "unknown"`), []byte(`"outcome": "`+outcome+`"`), 1)
		if _, e := Decode[SessionCodeCLI](raw); e != nil {
			t.Fatalf("diagnostic outcome %s: %v", outcome, e)
		}
	}
	// Only one time may have been observed. The absent fact stays null, and is
	// never inferred by adding/subtracting the lifetime.
	partial := bytes.Replace(unknown, []byte(`"issuedAt": null`), []byte(`"issuedAt": "2026-10-26T12:00:00Z"`), 1)
	if _, e := Decode[SessionCodeCLI](partial); e != nil {
		t.Fatal("known time with unavailable counterpart", e)
	}
}

func TestSessionCodeCLIRejectsInvalidFacts(t *testing.T) {
	presented := string(sessionCodeFixture(t, "presented"))
	unknown := string(sessionCodeFixture(t, "unknown"))
	mutate := func(raw, from, to string) string { return strings.Replace(raw, from, to, 1) }
	cases := map[string]string{
		"version":                  mutate(presented, `"apiVersion": 1`, `"apiVersion": 2`),
		"scope":                    mutate(presented, `"session-code"`, `"session"`),
		"command":                  mutate(presented, `"issue"`, `"revoke"`),
		"outcome":                  mutate(presented, `"presented",`, `"confirmed",`),
		"presented-false":          mutate(presented, `"presented": true`, `"presented": false`),
		"presented-null-result":    mutate(presented, presented[strings.Index(presented, "{\n    \"actorId\""):strings.LastIndex(presented, "\n  },")+4], "null"),
		"presented-with-error":     mutate(presented, `"error": null`, `"error": {"code":"synthetic","message":"Synthetic refusal"}`),
		"missing-error":            mutate(unknown, `"error": {"code": "UNVERIFIED", "message": "Presentation is unverified"}`, `"error": null`),
		"unknown-presented":        mutate(unknown, `"presented": false`, `"presented": true`),
		"empty-diagnostic-code":    mutate(unknown, `"UNVERIFIED"`, `""`),
		"empty-diagnostic-text":    mutate(unknown, `"Presentation is unverified"`, `""`),
		"diagnostic-field":         mutate(unknown, `"code": "UNVERIFIED"`, `"field": null, "code": "UNVERIFIED"`),
		"non-v4-installation":      mutate(presented, `"00000000-0000-4000-8000-000000000001"`, `"00000000-0000-1000-8000-000000000001"`),
		"zero-board":               mutate(presented, `"00000000-0000-4000-8000-000000000002"`, `"00000000-0000-0000-0000-000000000000"`),
		"non-utc-time":             mutate(presented, `"2026-10-26T12:00:00Z"`, `"2026-10-26T12:00:00+00:00"`),
		"fractional-time":          mutate(presented, `"2026-10-26T12:00:00Z"`, `"2026-10-26T12:00:00.000Z"`),
		"invalid-calendar":         mutate(presented, `"2026-10-26T12:00:00Z"`, `"2026-02-30T12:00:00Z"`),
		"wrong-expiry":             mutate(presented, `"2026-10-26T12:01:00Z"`, `"2026-10-26T12:01:01Z"`),
		"backwards-expiry":         mutate(presented, `"2026-10-26T12:01:00Z"`, `"2026-10-26T11:59:00Z"`),
		"wrong-known-unknown-time": mutate(mutate(unknown, `"issuedAt": null`, `"issuedAt": "2026-10-26T12:00:00Z"`), `"expiresAt": null`, `"expiresAt": "2026-10-26T12:02:00Z"`),
		"duplicate-key":            mutate(presented, `"apiVersion": 1`, `"apiVersion": 1, "apiVersion": 1`),
		"extra-top-key":            mutate(presented, `"apiVersion": 1`, `"apiVersion": 1, "extra": null`),
	}
	for _, key := range []string{"code", "codeHash", "bearer", "token", "credentialId", "privatePath"} {
		cases["top-secret-"+key] = mutate(presented, `"apiVersion": 1`, `"`+key+`": "synthetic-secret-sentinel", "apiVersion": 1`)
		cases["result-secret-"+key] = mutate(presented, `"presented": true`, `"`+key+`": "synthetic-secret-sentinel", "presented": true`)
	}
	var base map[string]json.RawMessage
	if e := json.Unmarshal([]byte(presented), &base); e != nil {
		t.Fatal(e)
	}
	var result map[string]json.RawMessage
	if e := json.Unmarshal(base["result"], &result); e != nil {
		t.Fatal(e)
	}
	for key, value := range result {
		var incomplete map[string]json.RawMessage
		json.Unmarshal(base["result"], &incomplete)
		delete(incomplete, key)
		raw, e := json.Marshal(incomplete)
		if e != nil {
			t.Fatal(e)
		}
		cases["missing-result-"+key] = mutate(presented, string(base["result"]), string(raw))
		if key != "presented" {
			cases["null-presented-"+key] = mutate(presented, `"`+key+`": `+string(value), `"`+key+`": null`)
		}
	}
	for key := range base {
		var incomplete map[string]json.RawMessage
		json.Unmarshal([]byte(presented), &incomplete)
		delete(incomplete, key)
		raw, e := json.Marshal(incomplete)
		if e != nil {
			t.Fatal(e)
		}
		cases["missing-envelope-"+key] = string(raw)
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if raw == presented || raw == unknown {
				t.Fatal("test did not modify fixture")
			}
			if _, e := Decode[SessionCodeCLI]([]byte(raw)); e == nil {
				t.Fatal("accepted invalid code status")
			} else if strings.Contains(e.Error(), "synthetic-secret-sentinel") {
				t.Fatal("validation exposed unsafe input")
			}
		})
	}
}

func TestSessionCodeCLIInMemoryValidation(t *testing.T) {
	x, e := Decode[SessionCodeCLI](sessionCodeFixture(t, "presented"))
	if e != nil {
		t.Fatal(e)
	}
	x.Result.Presented = false
	if Validate(x) == nil {
		t.Fatal("in-memory false presentation accepted")
	}
	if _, e = Readable(x); e == nil {
		t.Fatal("invalid construction rendered")
	}
	x.Result.Presented = true
	x.Outcome = "unknown"
	x.Error = &CredentialError{Code: "UNVERIFIED", Message: "Presentation is unverified"}
	if Validate(x) == nil {
		t.Fatal("unknown in-memory presentation accepted")
	}
}

func TestSessionCodeGeneratedDiscriminants(t *testing.T) {
	files, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	var root struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if e = json.Unmarshal(files["schemas/transport/contracts.schema.json"], &root); e != nil {
		t.Fatal(e)
	}
	var envelope struct {
		OneOf []struct {
			AdditionalProperties bool
			Properties           map[string]json.RawMessage
			Required             []string
		}
	}
	if e = json.Unmarshal(root.Defs["SessionCodeCLI"], &envelope); e != nil {
		t.Fatal(e)
	}
	if len(envelope.OneOf) != 5 {
		t.Fatal("expected exact five outcome branches")
	}
	for i, branch := range envelope.OneOf {
		if branch.AdditionalProperties || len(branch.Required) != 6 {
			t.Fatal("envelope is not exact")
		}
		var outcome struct{ Const string }
		if e = json.Unmarshal(branch.Properties["outcome"], &outcome); e != nil || outcome.Const != []string{"presented", "refused", "failed", "unavailable", "unknown"}[i] {
			t.Fatal("wrong outcome literal")
		}
		if !bytes.Contains(branch.Properties["result"], []byte(`"const": `+map[bool]string{true: "true", false: "false"}[i == 0])) {
			t.Fatal("wrong presentation literal")
		}
	}
	decl := string(files["web/generated/transport/contracts.d.ts"])
	for _, literal := range []string{`readonly scope: "session-code"`, `readonly command: "issue"`, `readonly outcome: "presented"`, `readonly presented: true`, `readonly presented: false`} {
		if !strings.Contains(decl, literal) {
			t.Fatalf("missing generated literal %s", literal)
		}
	}
}
