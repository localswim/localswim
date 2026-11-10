package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestT04CLIArchiveAndCover(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		field string
		value string
	}{
		{[]string{"list", "--archived", "only", t03Board}, "archived", "only"},
		{[]string{"archive", t03Card}, "archived", "true"},
		{[]string{"unarchive", t03Card}, "archived", "false"},
		{[]string{"cover", t03Card, t03Lane}, "cover_attachment_id", fmt.Sprintf("%q", t03Lane)},
		{[]string{"cover", t03Card, "none"}, "cover_attachment_id", "null"},
	} {
		t.Run(tc.args[0]+"/"+tc.value, func(t *testing.T) {
			cmd, err := parseCLI(append([]string{"card"}, tc.args...))
			if err != nil {
				t.Fatal(err)
			}
			if tc.args[0] == "list" {
				if cmd.resource != "/cards?archived=only&board_id="+t03Board+"&limit=50" {
					t.Fatalf("unexpected archive filter: %s", cmd.resource)
				}
				return
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(cmd.body, &fields); err != nil || string(fields[tc.field]) != tc.value ||
				cmd.method != http.MethodPatch || cmd.edit.validatorPath != "/cards/"+t03Card {
				t.Fatalf("unexpected card patch: %+v", cmd)
			}
		})
	}
}

func TestT04CLIRelationshipValidatorsAndUnknownOutcome(t *testing.T) {
	for _, tc := range []struct {
		action, method, revision, input string
		status, code, writes            int
		boardResponseTag                bool
	}{
		{"create", http.MethodPost, "12", fmt.Sprintf("{\"source_card_id\":%q,\"target_card_id\":%q,\"kind\":\"references\"}", t03Card, t03Lane), http.StatusCreated, 0, 1, true},
		{"patch", http.MethodPatch, "12", "{\"kind\":\"referenced_by\"}", http.StatusOK, 0, 1, true},
		{"remove", http.MethodDelete, "12", "", http.StatusNoContent, 0, 1, true},
		{"remove", http.MethodDelete, "11", "", http.StatusNoContent, 1, 0, true},
		{"remove", http.MethodDelete, "12", "", http.StatusNoContent, 1, 1, false},
	} {
		t.Run(fmt.Sprintf("%s/%s/%t", tc.action, tc.revision, tc.boardResponseTag), func(t *testing.T) {
			var mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/boards/"+t03Board:
					w.Header().Set("ETag", t03BoardTag)
					_, _ = io.WriteString(w, "{\"revision\":12}")
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/relationships/"+t03Lane:
					w.Header().Set("ETag", t03ResourceTag)
					_, _ = fmt.Fprintf(w, "{\"board_id\":%q}", t03Board)
				case r.Method == tc.method:
					mutations.Add(1)
					if tc.method == http.MethodPost {
						if r.URL.Path != t03Root+"/relationships" || r.Header.Get("If-Match") != "" {
							t.Error("unexpected create target or resource validator")
						}
					} else if r.URL.Path != t03Root+"/relationships/"+t03Lane || r.Header.Get("If-Match") != t03ResourceTag {
						t.Error("relationship mutation did not bind the addressed relationship")
					}
					if tc.method == http.MethodDelete {
						if r.Header.Get("X-Expected-Board-ETag") != t03BoardTag || r.ContentLength != 0 {
							t.Error("relationship removal must bind the board header with no body")
						}
					} else {
						var fields map[string]string
						if err := json.NewDecoder(r.Body).Decode(&fields); err != nil || fields["expected_board_etag"] != t03BoardTag ||
							(tc.method == http.MethodPost && fields["board_id"] != t03Board) {
							t.Error("relationship mutation did not bind the board JSON validator")
						}
					}
					w.Header().Set("ETag", t03ResourceTag)
					if tc.boardResponseTag {
						w.Header().Set("X-Board-ETag", t03BoardTag)
					}
					w.WriteHeader(tc.status)
					if tc.status != http.StatusNoContent {
						_, _ = io.WriteString(w, "{}")
					}
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()

			id := t03Lane
			if tc.method == http.MethodPost {
				id = t03Board
			}
			args := []string{"relationship", tc.action, "--revision", tc.revision, id}
			if tc.method != http.MethodDelete {
				args = append(args, "-")
			}
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput(args, strings.NewReader(tc.input), &stdout, &stderr, t03Env(server.URL))
			if code != tc.code || mutations.Load() != int32(tc.writes) {
				t.Fatalf("code %d, writes %d: %s", code, mutations.Load(), &stderr)
			}
			if !tc.boardResponseTag && !strings.Contains(stderr.String(), "Mutation outcome unknown") {
				t.Fatalf("missing unknown outcome warning: %s", &stderr)
			}
			if tc.method == http.MethodDelete && stdout.Len() != 0 {
				t.Fatalf("unexpected stdout for delete: %s", &stdout)
			}
		})
	}
}

func TestT04CLIDownloadVerifiesBeforePublishing(t *testing.T) {
	want := []byte{0xff, 0x00, '\n'}
	for _, tc := range []struct {
		name     string
		data     []byte
		file     bool
		existing bool
		code     int
	}{
		{"stdout", want, false, false, 0},
		{"file", want, true, false, 0},
		{"existing file", want, true, true, 1},
		{"mismatch stdout", []byte{0xfe, 0x00, '\n'}, false, false, 1},
		{"mismatch file", []byte{0xfe, 0x00, '\n'}, true, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case t03Root + "/attachments/" + t03Lane:
					_, _ = fmt.Fprintf(w, "{\"id\":%q,\"active\":true,\"size\":%d,\"sha256\":\"%x\"}", t03Lane, len(want), sha256.Sum256(want))
				case t03Root + "/attachments/" + t03Lane + "/content":
					if r.URL.Query().Get("disposition") != "attachment" || r.Header.Get("Authorization") != "Bearer t03-test-token" {
						t.Error("download did not request authenticated attachment content")
					}
					w.Header().Set("Content-Type", "application/octet-stream")
					_, _ = w.Write(tc.data)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			output := "-"
			if tc.file {
				output = filepath.Join(t.TempDir(), "verified.bin")
				if tc.existing {
					if err := os.WriteFile(output, []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput([]string{"attachment", "download", t03Lane, output}, nil, &stdout, &stderr, t03Env(server.URL))
			if code != tc.code {
				t.Fatalf("code %d: %s", code, &stderr)
			}
			if tc.file {
				data, err := os.ReadFile(output)
				if tc.existing {
					if stdout.Len() != 0 || err != nil || string(data) != "preserve" {
						t.Fatal("download overwrote an existing output file")
					}
					return
				}
				if stdout.Len() != 0 || (code == 0 && (err != nil || !bytes.Equal(data, want))) ||
					(code != 0 && !os.IsNotExist(err)) {
					t.Fatal("download did not respect file output or verification")
				}
			} else if (code == 0 && !bytes.Equal(stdout.Bytes(), want)) || (code != 0 && stdout.Len() != 0) {
				t.Fatal("download did not respect binary stdout or verification")
			}
		})
	}
}
