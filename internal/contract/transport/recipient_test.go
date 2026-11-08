package transport

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRecipientSharedVectors(t *testing.T) {
	b, e := os.ReadFile("testdata/recipient-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Name  string
		Valid bool
		JSON  string
	}
	if e = json.Unmarshal(b, &rows); e != nil {
		t.Fatal(e)
	}
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			_, e := Decode[RecipientInput]([]byte(row.JSON))
			if (e == nil) != row.Valid {
				t.Fatalf("acceptance mismatch valid=%v", row.Valid)
			}
		})
	}
}
func TestRecipientResponseAndRawInputBounds(t *testing.T) {
	var rows []struct {
		JSON  string
		Valid bool
	}
	b, _ := os.ReadFile("testdata/recipient-vectors.json")
	_ = json.Unmarshal(b, &rows)
	var input RecipientInput
	for _, r := range rows {
		if r.Valid {
			input, _ = Decode[RecipientInput]([]byte(r.JSON))
			break
		}
	}
	id := UUIDv4("00000000-0000-4000-8000-000000000016")
	v := RecipientView{RuntimeGeneration: id, State: "configured", ConfigurationRevision: 1, ConfigurationGeneration: &id, RequestID: &id, Input: &input}
	v.Identity.BoardID = "00000000-0000-4000-8000-000000000017"
	v.Identity.Incarnation = "00000000-0000-4000-8000-000000000018"
	valid := RecipientResponse{APIVersion: 1, Scope: "recipient", Result: v}
	encoded, e := Readable(valid)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Decode[RecipientResponse](encoded); e != nil {
		t.Fatal(e)
	}
	for _, state := range []RecipientState{"unconfigured", "unknown", "unavailable"} {
		bad := valid
		bad.Result.State = state
		if Validate(bad) == nil {
			t.Fatal("private redaction invalid accepted", state)
		}
	}
	request := RecipientConfigureRequest{Incarnation: v.Identity.Incarnation, ExpectedRuntimeGeneration: id, RequestID: id, Input: input}
	raw, e := Readable(request)
	if e != nil {
		t.Fatal(e)
	}
	padded := strings.Replace(string(raw), `"input":{`, `"input":{`+strings.Repeat(" ", 1024), 1)
	if len(padded) > 2048 {
		t.Fatal("fixture too large")
	}
	if _, e = Decode[RecipientConfigureRequest]([]byte(padded)); e == nil {
		t.Fatal("raw input budget ignored")
	}
	if recipientByteLimit(reflect.TypeFor[RecipientResponse]()) != 4096 {
		t.Fatal("response limit")
	}
	if _, e = Decode[RecipientResponse](append(encoded, []byte(strings.Repeat(" ", 4096))...)); e == nil {
		t.Fatal("oversize response")
	}
}
