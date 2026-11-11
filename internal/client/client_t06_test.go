package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBackupContentBoundsAndFraming(t *testing.T) {
	for _, tc := range []struct {
		name, data, contentType, encoding string
		size                              uint64
		valid                             bool
	}{
		{"exact", "zip", "application/zip", "", 3, true},
		{"short", "zi", "application/zip", "", 3, false},
		{"long", "zips", "application/zip", "", 3, false},
		{"wrong type", "zip", "application/octet-stream", "", 3, false},
		{"encoded", "zip", "application/zip", "gzip", 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/zip" {
					t.Error("unexpected download request")
				}
				w.Header().Set("Content-Type", tc.contentType)
				if tc.encoding != "" {
					w.Header().Set("Content-Encoding", tc.encoding)
				}
				io.WriteString(w, tc.data)
			}))
			defer server.Close()
			api, err := New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			err = api.BackupContent("/backups/00000000-0000-7000-8000-000000000000/content", tc.size, io.Discard)
			if (err == nil) != tc.valid || requests != 1 {
				t.Fatalf("valid=%v error=%v requests=%d", tc.valid, err, requests)
			}
		})
	}
}

func TestBackupContentSupportsArchivesLargerThanJSON(t *testing.T) {
	const size = 97 << 20
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		chunk := strings.Repeat("x", 64<<10)
		for remaining := size; remaining > 0; remaining -= len(chunk) {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	api, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BackupContent("/backups/00000000-0000-7000-8000-000000000000/content", size, io.Discard); err != nil {
		t.Fatal(err)
	}
}
