package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAttachmentNumericMetadataBoundary(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	const tag = `"opaque_attachment_revision"`
	fixture := func() map[string]any {
		return map[string]any{
			"id": id, "board_id": id, "card_id": id, "revision": 19,
			"created_at": "2026-11-10 18:00:00+00:00", "updated_at": "2026-11-10 18:00:00+00:00",
			"original_name": "hello.txt", "size": 5,
			"sha2_256":   "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
			"media_type": "text/plain", "active": true, "previewable": false, "added_by": id,
		}
	}
	cases := []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{"numeric", func(map[string]any) {}, true},
		{"maximum safe revision", func(v map[string]any) { v["revision"] = uint64(9007199254740991) }, true},
		{"above maximum safe revision", func(v map[string]any) { v["revision"] = uint64(9007199254740992) }, false},
		{"missing revision", func(v map[string]any) { delete(v, "revision") }, false},
		{"zero revision", func(v map[string]any) { v["revision"] = 0 }, false},
		{"negative revision", func(v map[string]any) { v["revision"] = -1 }, false},
		{"string revision", func(v map[string]any) { v["revision"] = "19" }, false},
		{"boolean revision", func(v map[string]any) { v["revision"] = true }, false},
		{"fractional revision", func(v map[string]any) { v["revision"] = 1.5 }, false},
		{"body etag", func(v map[string]any) { v["etag"] = tag }, false},
		{"old digest", func(v map[string]any) { v["sha256"] = v["sha2_256"]; delete(v, "sha2_256") }, false},
		{"digest alias", func(v map[string]any) { v["sha256"] = v["sha2_256"] }, false},
		{"invalid digest", func(v map[string]any) { v["sha2_256"] = "bad" }, false},
	}
	for _, tc := range cases {
		for _, operation := range []string{"get", "list", "upload"} {
			t.Run(tc.name+"/"+operation, func(t *testing.T) {
				data := fixture()
				tc.change(data)
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("ETag", tag)
					w.Header().Set("X-Board-ETag", tag)
					if operation == "upload" {
						w.WriteHeader(http.StatusCreated)
					}
					var response any = data
					if operation == "list" {
						response = map[string]any{"attachments": []any{data}, "next_cursor": nil}
					}
					_ = json.NewEncoder(w).Encode(response)
				}))
				defer server.Close()
				api, err := New(server.URL, "test-token")
				if err != nil {
					t.Fatal(err)
				}
				method, resource := http.MethodGet, "/attachments/"+id
				if operation == "list" {
					resource = "/attachments?card_id=" + id
				}
				if operation == "upload" {
					method, resource = http.MethodPost, "/attachments"
				}
				result, err := api.Do(method, resource, nil, "")
				if (err == nil) != tc.valid {
					t.Fatalf("valid=%v error=%v", tc.valid, err)
				}
				if tc.valid && result.ETag != tag {
					t.Fatal("lost response header validator")
				}
				if !tc.valid && errors.Is(err, ErrMutationOutcomeUnknown) != (operation == "upload") {
					t.Fatal("incorrect mutation outcome classification")
				}
				if calls != 1 {
					t.Fatal("attachment request was replayed")
				}
			})
		}
	}
}
