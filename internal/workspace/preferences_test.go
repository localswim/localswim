package workspace

import (
	"strings"
	"testing"

	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestPreferencesPrivateSchemaAndTupleBinding(t *testing.T) {
	c := newConfiguration(8820)
	c.Projects = []project{{"harbor", "Harbor", `C:\p`, "project-physical"}}
	c.Members = []member{{"harbor", "main", "Main", `C:\p\main`, "member-physical", T.WorkspaceIdentity{InstallationID: "11111111-1111-4111-8111-111111111111", BoardID: "22222222-2222-4222-8222-222222222222", Incarnation: "33333333-3333-4333-8333-333333333333"}}}
	p := preferences{SchemaVersion: 1, WorkspaceID: c.WorkspaceID, PreferencesRevision: 1, Pins: []preferencePin{{"harbor", "main", c.Members[0].Identity}}}
	b, e := preferencesBytes(p, c)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{
		strings.Replace(string(b), `"schemaVersion":1`, `"schemaVersion":2`, 1),
		strings.Replace(string(b), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1),
		strings.Replace(string(b), `"pins":[`, `"unknown":1,"pins":[`, 1),
		strings.Replace(string(b), `"main"`, `"missing"`, 1),
		strings.Replace(string(b), string(c.Members[0].Identity.Incarnation), "44444444-4444-4444-8444-444444444444", 1),
		strings.Replace(string(b), `"preferencesRevision":1`, `"preferencesRevision":0`, 1),
		strings.Repeat(" ", int(PreferencesLimit)+1),
	} {
		if _, e := decodePreferences([]byte(bad), c); e == nil {
			t.Fatal("invalid preferences accepted")
		}
	}
	p.Pins = append(p.Pins, p.Pins[0])
	if _, e := preferencesBytes(p, c); e == nil {
		t.Fatal("duplicate private pin accepted")
	}
	p.Pins = nil
	if _, e := preferencesBytes(p, c); e == nil {
		t.Fatal("nil private pin list accepted")
	}
}
