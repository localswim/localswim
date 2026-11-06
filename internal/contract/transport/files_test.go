package transport

import (
	"encoding/json"
	A "github.com/localswim/localswim/internal/contract/archive"
	"testing"
)

func TestCardFileContracts(t *testing.T) {
	id := A.UUID("10000000-0000-0000-0000-000000000001")
	inc := A.UUID("20000000-0000-0000-0000-000000000001")
	card := A.UUID("30000000-0000-0000-0000-000000000001")
	file := CardFile{Attachment: A.Attachment{AttachmentID: id, CardID: card, Name: "", MediaLabel: "", SHA256: A.Digest("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"), CreatedAt: "2026-11-05T00:00:00Z", CreatedIn: A.Identity{BoardID: id, Incarnation: inc}}, ReferenceState: "current", DownloadState: "current_reference", Attribution: FileAttribution{State: "unavailable"}}
	view := CardFilesRead{APIVersion: 1, Scope: "card-files", Result: CardFilesView{Identity: A.Identity{BoardID: id, Incarnation: inc}, Revision: 1, CardID: card, CardRevision: 1, View: "current", Items: []CardFile{file}}}
	raw, e := Readable(view)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Decode[CardFilesRead](raw); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*CardFilesRead){func(v *CardFilesRead) { v.Result.Items = nil }, func(v *CardFilesRead) { v.Result.Items[0].DownloadState = "not_current" }, func(v *CardFilesRead) { v.Result.Items[0].Attribution.State = "known" }, func(v *CardFilesRead) { v.Result.View = "retained" }, func(v *CardFilesRead) { v.Result.Items = append(v.Result.Items, v.Result.Items[0]) }} {
		var v CardFilesRead
		json.Unmarshal(raw, &v)
		mutate(&v)
		bad, _ := json.Marshal(v)
		if _, e := Decode[CardFilesRead](bad); e == nil {
			t.Fatal("invalid file read accepted")
		}
	}
	var object map[string]any
	json.Unmarshal(raw, &object)
	object["unknown"] = true
	bad, _ := json.Marshal(object)
	if _, e := Decode[CardFilesRead](bad); e == nil {
		t.Fatal("unknown field accepted")
	}
}
