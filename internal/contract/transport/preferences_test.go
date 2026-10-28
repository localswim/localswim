package transport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPreferencesStrictRequestAndPublicReferences(t *testing.T) {
	ref := WorkspacePinReference{ProjectKey: "harbor", BoardKey: "main"}
	for _, action := range []WorkspacePreferenceAction{"pin", "unpin", "reorder"} {
		q := WorkspacePreferencesRequest{APIVersion: 1, ExpectedPreferencesRevision: 1, Action: action, Pin: &ref, Order: []WorkspacePinReference{}}
		if action == "reorder" {
			q.Pin = nil
			q.Order = []WorkspacePinReference{ref}
		}
		b, _ := json.Marshal(q)
		if _, e := Decode[WorkspacePreferencesRequest](b); e != nil {
			t.Fatal(e)
		}
	}
	good := `{"apiVersion":1,"expectedPreferencesRevision":1,"action":"pin","pin":{"projectKey":"harbor","boardKey":"main"},"order":[]}`
	for _, b := range []string{
		strings.Replace(good, `"apiVersion":1`, `"apiVersion":2`, 1),
		strings.Replace(good, `"apiVersion":1`, `"apiVersion":1,"apiVersion":1`, 1),
		strings.Replace(good, `"action":"pin"`, `"action":"reorder"`, 1),
		strings.Replace(good, `"action":"pin"`, `"action":"set"`, 1),
		strings.Replace(good, `"pin":{"projectKey":"harbor","boardKey":"main"}`, `"pin":null`, 1),
		strings.Replace(good, `"order":[]`, `"order":[{"projectKey":"harbor","boardKey":"main"}]`, 1),
		strings.Replace(good, `"order":[]`, `"order":null`, 1),
		strings.Replace(good, `"order":[]`, `"order":[],"path":"private"`, 1),
		strings.Replace(good, `"expectedPreferencesRevision":1`, `"expectedPreferencesRevision":0`, 1),
	} {
		if _, e := Decode[WorkspacePreferencesRequest]([]byte(b)); e == nil {
			t.Fatal("invalid preference request accepted")
		}
	}
	if Validate(WorkspacePreferencesRequest{APIVersion: 1, ExpectedPreferencesRevision: 1, Action: "reorder", Order: []WorkspacePinReference{ref, ref}}) == nil {
		t.Fatal("duplicate reorder accepted")
	}
	p := WorkspacePreferences{APIVersion: 1, WorkspaceID: "11111111-1111-4111-8111-111111111111", PreferencesRevision: 1, Pins: []WorkspacePinReference{ref}}
	b, e := Readable(p)
	if e != nil || strings.Contains(string(b), "identity") {
		t.Fatal("public preferences disclose identity")
	}
	b, _ = json.Marshal(p)
	if _, e = Decode[WorkspacePreferences]([]byte(strings.Replace(string(b), `"pins":[`, `"identity":{},"pins":[`, 1))); e == nil {
		t.Fatal("extra public fields accepted")
	}
}
