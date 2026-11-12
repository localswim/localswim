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
		{[]string{"archive", t03Board, t03Card}, "archived", "true"},
		{[]string{"unarchive", t03Board, t03Card}, "archived", "false"},
		{[]string{"cover", t03Board, t03Card, t03Lane}, "cover_attachment_id", fmt.Sprintf("%q", t03Lane)},
		{[]string{"cover", t03Board, t03Card, "none"}, "cover_attachment_id", "null"},
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
				cmd.method != http.MethodPatch || (cmd.ticketPatch == nil && cmd.edit.validatorPath != "/cards/"+t03Card) {
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
		{"patch", http.MethodPatch, "7", "{\"kind\":\"references\"}", http.StatusOK, 0, 1, true},
		{"patch", http.MethodPatch, "12", "{\"kind\":\"references\"}", http.StatusOK, 1, 0, true},
		{"create", http.MethodPost, "7", fmt.Sprintf("{\"source_card_id\":%q,\"target_card_id\":%q,\"kind\":\"references\"}", t03Card, t03Lane), http.StatusCreated, 1, 0, true},
		{"remove", http.MethodDelete, "7", "", http.StatusNoContent, 0, 1, true},
		{"remove", http.MethodDelete, "12", "", http.StatusNoContent, 1, 0, true},
		{"remove", http.MethodDelete, "7", "", http.StatusNoContent, 1, 1, false},
	} {
		t.Run(fmt.Sprintf("%s/%s/%t", tc.action, tc.revision, tc.boardResponseTag), func(t *testing.T) {
			var mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && (r.URL.Path == t03Root+"/cards/"+t03Card || r.URL.Path == t03Root+"/cards/"+t03Lane):
					writeScopeCard(w, strings.TrimPrefix(r.URL.Path, t03Root+"/cards/"), t03Board, 12)
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/boards/"+t03Board:
					if tc.method == http.MethodPost {
						writeScopeView(w, r)
						return
					}
					w.Header().Set("ETag", t03BoardTag)
					_, _ = fmt.Fprintf(w, `{"id":%q,"revision":12}`, t03Board)
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/relationships/"+t03Lane:
					w.Header().Set("ETag", t03ResourceTag)
					_, _ = fmt.Fprintf(w, "{\"id\":%q,\"board_id\":%q,\"revision\":7}", t03Lane, t03Board)
				case r.Method == tc.method:
					mutations.Add(1)
					var createdID string
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
						expectedBoardTag := t03BoardTag
						if tc.method == http.MethodPost {
							expectedBoardTag = scopeBoardTag
						}
						if err := json.NewDecoder(r.Body).Decode(&fields); err != nil || fields["expected_board_etag"] != expectedBoardTag ||
							(tc.method == http.MethodPost && fields["board_id"] != t03Board) {
							t.Error("relationship mutation did not bind the board JSON validator")
						}
						createdID = fields["id"]
					}
					w.Header().Set("ETag", t03ResourceTag)
					if tc.boardResponseTag {
						w.Header().Set("X-Board-ETag", t03BoardTag)
					}
					w.WriteHeader(tc.status)
					if tc.method == http.MethodPost {
						_, _ = w.Write(relationshipCreateFixture(createdID, "references"))
					} else if tc.status != http.StatusNoContent {
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
				id = t03Card
			}
			args := []string{"relationship", tc.action, "--revision", tc.revision, t03Board, id}
			if tc.method == http.MethodPost {
				args = append(args, "references", t03Lane)
			} else if tc.method == http.MethodDelete {
				args = []string{"relationship", tc.action, "--revision", tc.revision, t03Board, id}
			} else {
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

func TestRelationshipRemoveBoardContext(t *testing.T) {
	command, err := parseCLI([]string{"relationship", "remove", "--revision", "7", strings.ToUpper(t03Board), strings.ToUpper(t03Lane)})
	if err != nil || command.boardID != t03Board || command.resource != "/relationships/"+t03Lane ||
		command.revision == nil || *command.revision != 7 {
		t.Fatalf("invalid board-scoped removal: %+v, %v", command, err)
	}
	for _, args := range [][]string{
		{t03Lane},
		{t03Board},
		{t03Board, "invalid"},
		{"invalid", t03Lane},
		{t03Board, t03Lane, "extra"},
	} {
		if _, err := parseCLI(append([]string{"relationship", "remove"}, args...)); err == nil {
			t.Fatalf("accepted malformed removal: %v", args)
		}
	}

	for _, tc := range []struct {
		name, response, message string
	}{
		{"wrong board", fmt.Sprintf(`{"id":%q,"board_id":%q,"revision":7}`, t03Lane, t03Card), "did not confirm"},
		{"wrong relationship", fmt.Sprintf(`{"id":%q,"board_id":%q,"revision":7}`, t03Card, t03Board), "did not confirm"},
		{"missing board", fmt.Sprintf(`{"id":%q,"revision":7}`, t03Lane), "did not confirm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != t03Root+"/relationships/"+t03Lane {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("ETag", t03ResourceTag)
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput([]string{"relationship", "remove", t03Board, t03Lane}, nil, &stdout, &stderr, t03Env(server.URL))
			if code != 1 || requests.Load() != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.message) {
				t.Fatalf("code=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), &stdout, &stderr)
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
					_, _ = w.Write(attachmentFixture(t03Lane, want))
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
			code := runCLIWithInput([]string{"attachment", "download", t03Board, t03Lane, output}, nil, &stdout, &stderr, t03Env(server.URL))
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

func attachmentFixture(id string, data []byte) []byte {
	digest := sha256.Sum256(data)
	result, _ := json.Marshal(map[string]any{
		"id": id, "board_id": t03Board, "card_id": t03Card, "revision": 23,
		"created_at": "2026-11-10 18:00:00+00:00", "updated_at": "2026-11-10 18:00:00+00:00",
		"original_name": "test.bin", "size": len(data), "sha2_256": fmt.Sprintf("%x", digest),
		"media_type": "application/octet-stream", "active": true, "previewable": false, "added_by": t03Board,
	})
	return result
}

func TestRelationshipCreatePositionalArguments(t *testing.T) {
	for _, kind := range []string{"blocks", "duplicates", "references", "relates_to"} {
		cmd, err := parseCLI([]string{"relationship", "create", "--id", strings.ToUpper(t03Board), "--revision", "12", strings.ToUpper(t03Board), strings.ToUpper(t03Card), strings.ToUpper(kind), strings.ToUpper(t03Lane)})
		if err != nil {
			t.Fatal(err)
		}
		request := cmd.ticketRelationship
		if request == nil || request.linkID != t03Board || request.boardID != t03Board || request.cardID != t03Card || request.relatedID != t03Lane || request.kind != kind || cmd.inputPath != "" || cmd.revision == nil || *cmd.revision != 12 {
			t.Fatalf("unexpected parsed create: %+v", cmd)
		}
	}
	for _, args := range [][]string{
		{t03Board, "-"}, {t03Card, "blocks"}, {t03Board, t03Card, "blocks", t03Lane, "extra"},
		{"--id", "", t03Board, t03Card, "blocks", t03Lane}, {"--id", "invalid", t03Board, t03Card, "blocks", t03Lane},
		{"--revision", "0", t03Board, t03Card, "blocks", t03Lane}, {t03Board, t03Card, "blocks", strings.ToUpper(t03Card)},
		{t03Board, t03Card, "blocked_by", t03Lane}, {t03Board, t03Card, "DUPLICATED_BY", t03Lane}, {t03Board, t03Card, "referenced_by", t03Lane},
	} {
		if _, err := parseCLI(append([]string{"relationship", "create"}, args...)); err == nil {
			t.Fatalf("accepted invalid arguments: %q", args)
		}
	}
}

func TestRelationshipCreatePreflightAndSingleAttempt(t *testing.T) {
	for _, scenario := range []string{"wrong source", "wrong target", "cross board", "source failure", "target failure", "board failure", "missing board tag", "revision conflict", "server conflict", "unknown", "empty success", "saved id", "saved board", "saved source", "saved target", "saved kind", "missing revision", "output failure", "success"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			submittedID := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					writes++
					var fields map[string]string
					if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
						t.Fatal(err)
					}
					submittedID = fields["id"]
					if !boardIDPattern.MatchString(submittedID) || fields["source_card_id"] != t03Card || fields["target_card_id"] != t03Lane || fields["kind"] != "blocks" || fields["board_id"] != t03Board || fields["expected_board_etag"] != scopeBoardTag {
						t.Errorf("unexpected create body: %v", fields)
					}
					if scenario == "server conflict" {
						w.WriteHeader(http.StatusPreconditionFailed)
						return
					}
					if scenario == "unknown" {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.Header().Set("ETag", t03ResourceTag)
					w.Header().Set("X-Board-ETag", scopeBoardTag)
					w.WriteHeader(http.StatusCreated)
					var saved map[string]any
					if err := json.Unmarshal(relationshipCreateFixture(submittedID, "blocks"), &saved); err != nil {
						t.Fatal(err)
					}
					switch scenario {
					case "empty success":
						saved = map[string]any{}
					case "saved id":
						saved["id"] = t03Board
					case "saved board":
						saved["board_id"] = t03Card
					case "saved source":
						saved["source_card_id"] = t03Lane
					case "saved target":
						saved["target_card_id"] = t03Card
					case "saved kind":
						saved["kind"] = "references"
					case "missing revision":
						delete(saved, "revision")
					}
					_ = json.NewEncoder(w).Encode(saved)
					return
				}
				switch r.URL.Path {
				case t03Root + "/cards/" + t03Card, t03Root + "/cards/" + t03Lane:
					source := strings.HasSuffix(r.URL.Path, t03Card)
					if (source && scenario == "source failure") || (!source && scenario == "target failure") {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					id := strings.TrimPrefix(r.URL.Path, t03Root+"/cards/")
					if (source && scenario == "wrong source") || (!source && scenario == "wrong target") {
						id = t03Board
					}
					board := t03Board
					if !source && scenario == "cross board" {
						board = t03Card
					}
					writeScopeCard(w, id, board, 12)
				case t03Root + "/boards/" + t03Board:
					if scenario == "board failure" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					if scenario != "missing board tag" {
						w.Header().Set("X-Board-ETag", scopeBoardTag)
					}
					if scenario == "missing board tag" {
						_, _ = io.WriteString(w, `{}`)
					} else {
						writeScopeView(w, r)
					}
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			revision := "12"
			if scenario == "revision conflict" {
				revision = "11"
			}
			var out, errout bytes.Buffer
			var stdout io.Writer = &out
			if scenario == "output failure" {
				stdout = relationshipFailedWriter{}
			}
			code := runCLIWithInput([]string{"relationship", "create", "--revision", revision, t03Board, t03Card, "BLOCKS", t03Lane}, nil, stdout, &errout, t03Env(server.URL))
			wantsWrite := scenario == "server conflict" || scenario == "unknown" || scenario == "success" || scenario == "empty success" || strings.HasPrefix(scenario, "saved ") || scenario == "missing revision" || scenario == "output failure"
			if writes != 0 && !wantsWrite || wantsWrite && writes != 1 || (code == 0) != (scenario == "success") {
				t.Fatalf("code=%d writes=%d: %s", code, writes, &errout)
			}
			if wantsWrite && scenario != "server conflict" && scenario != "success" && scenario != "output failure" && (!strings.Contains(errout.String(), "Mutation outcome unknown") || !strings.Contains(errout.String(), "relationship get "+t03Board+" "+submittedID)) {
				t.Fatalf("missing readback ID: %s", &errout)
			}
			if scenario == "output failure" && !strings.Contains(errout.String(), "relationship get "+t03Board+" "+submittedID) {
				t.Fatalf("output failure lost readback ID: %s", &errout)
			}
		})
	}
}

type relationshipFailedWriter struct{}

func (relationshipFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func relationshipCreateFixture(id, kind string) []byte {
	body, _ := json.Marshal(map[string]any{
		"id": id, "revision": 13, "board_id": t03Board, "source_card_id": t03Card, "target_card_id": t03Lane, "kind": kind,
		"created_at": "2026-11-11 00:00:00+00:00", "updated_at": "2026-11-11 00:00:00+00:00",
	})
	return body
}
