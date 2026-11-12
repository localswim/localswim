package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLIBoardRenameUsesRevisionCheckedPatch(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const tag = `"board-revision-1"`
	const name = "  Client \"North\" \\ 東京  "
	for _, tc := range []struct {
		name         string
		args         []string
		status       int
		wantExit     int
		wantRequests int32
	}{
		{"automatic revision", []string{"board", "rename", id, name}, 200, 0, 2},
		{"explicit revision", []string{"board", "rename", "--revision", "1", id, name}, 200, 0, 2},
		{"stale revision", []string{"board", "rename", id, name}, 412, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/api/2026-11-10/boards/"+id || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("wrong rename route or authorization")
				}
				w.Header().Set("ETag", tag)
				if r.Method == http.MethodGet {
					w.Write([]byte(`{"name":"Before","revision":1}`))
					return
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["name"] != name ||
					r.Method != http.MethodPatch || r.Header.Get("If-Match") != tag || r.Header.Get("Content-Type") != "application/json" {
					t.Error("rename did not preserve the name or revision")
				}
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					json.NewEncoder(w).Encode(body)
				}
			}))
			defer server.Close()
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(tc.args, strings.NewReader("not JSON"), &stdout, &stderr, func(key string) string { return env[key] })
			if exit != tc.wantExit || requests.Load() != tc.wantRequests {
				t.Fatalf("rename exit %d, requests %d, stderr %q", exit, requests.Load(), &stderr)
			}
			if tc.wantExit == 0 {
				var body map[string]string
				if json.Unmarshal(stdout.Bytes(), &body) != nil || body["name"] != name || stderr.Len() != 0 {
					t.Fatal("rename did not return the updated board")
				}
			} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), "412") {
				t.Fatal("stale rename did not return a clear refusal")
			}
		})
	}
}

func TestCLIBoardRenameRequiresIDAndOneNonemptyName(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, args := range [][]string{
		{"board", "rename", id},
		{"board", "rename", id, ""},
		{"board", "rename", id, string([]byte{0xff})},
		{"board", "rename", id, "too", "many"},
		{"board", "rename", "invalid", "Name"},
	} {
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr,
			func(string) string { t.Fatal("invalid rename reached execution"); return "" })
		if exit != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("invalid rename accepted: %v", args)
		}
	}
}

func TestCLIExportUsesSnapshotEndpoint(t *testing.T) {
	const boardID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const snapshot = `{"format":"localswim-snapshot","format_version":2}`
	for _, args := range [][]string{
		{"board", "export", boardID, "-"},
		{"board", "export", "--type", "json", boardID, "-"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/2026-11-10/snapshots/"+boardID ||
					r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("wrong export route, method or authorization")
				}
				w.Write([]byte(snapshot))
			}))
			defer server.Close()
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, func(key string) string { return env[key] })
			if exit != 0 || !equalCLIJSON(t, stdout.Bytes(), []byte(snapshot)) || stderr.Len() != 0 || requests.Load() != 1 {
				t.Fatalf("export failed: exit %d, requests %d, stderr %q", exit, requests.Load(), &stderr)
			}
		})
	}
}

func TestCLIExportRejectsUnsupportedTypeBeforeDispatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLIWithInput(
		[]string{"board", "export", "--type", "csv", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		strings.NewReader(""), &stdout, &stderr,
		func(string) string { t.Fatal("invalid export type reached execution"); return "" },
	)
	if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `unsupported export type "csv"`) {
		t.Fatalf("wrong type refusal: exit %d, stderr %q", exit, &stderr)
	}
}

func TestCLIWorkspacePinsUsesOneRevisionBoundMutation(t *testing.T) {
	const pinBody = `{"pinned_board_ids":["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"]}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/2026-11-10/workspace" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("wrong API route or authorization")
		}
		switch requests.Add(1) {
		case 1:
			if r.Method != http.MethodGet {
				t.Error("expected initial GET")
			}
			w.Header().Set("ETag", `"workspace-revision-2"`)
			w.Write([]byte(pinBody))
		case 2:
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != pinBody || r.Method != http.MethodPatch ||
				r.Header.Get("If-Match") != `"workspace-revision-2"` || r.Header.Get("Content-Type") != "application/json" {
				t.Error("expected PATCH bound to the initial ETag")
			}
			w.WriteHeader(http.StatusPreconditionFailed)
		default:
			t.Error("unexpected additional request")
		}
	}))
	defer server.Close()

	env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
	getenv := func(key string) string { return env[key] }
	var stdout, stderr bytes.Buffer
	exit := runCLIWithInput([]string{"workspace", "pin", "set", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, strings.NewReader(pinBody), &stdout, &stderr, getenv)
	if exit != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "412") || requests.Load() != 2 {
		t.Fatalf("wrong stale mutation result: exit %d, requests %d, stderr %q", exit, requests.Load(), &stderr)
	}
}

func TestCLIUnknownCommandIsRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLIWithInput(
		[]string{"board", "unknown", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		strings.NewReader(""), &stdout, &stderr,
		func(string) string { t.Fatal("unknown command reached execution"); return "" },
	)
	if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("unknown command accepted: exit %d, stderr %q", exit, &stderr)
	}
}

func TestCLICreationModesSendOneCreateRequest(t *testing.T) {
	const name = "  Personal \"東京\"  "
	const jsonInput = `{"name":"JSON board","id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`
	for _, tc := range []struct {
		label, input, name string
		args               []string
	}{
		{"name", "invalid JSON must not be read", name, []string{"board", "create", "name", name}},
		{"json", jsonInput, "JSON board", []string{"board", "create", "json", "-"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				raw, err := io.ReadAll(r.Body)
				var body map[string]any
				if err != nil || json.Unmarshal(raw, &body) != nil || body["name"] != tc.name ||
					r.Method != http.MethodPost || r.URL.Path != "/api/2026-11-10/boards" ||
					r.Header.Get("Content-Type") != "application/json" {
					t.Error("incorrect creation request")
				}
				if tc.label == "json" && string(raw) != jsonInput {
					t.Error("JSON definition was changed")
				}
				if tc.label == "name" && len(body) != 1 {
					t.Error("name mode sent additional fields")
				}
				w.Header().Set("ETag", `"created-board-tag"`)
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{"name": tc.name})
			}))
			defer server.Close()
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(tc.args, strings.NewReader(tc.input), &stdout, &stderr, func(key string) string { return env[key] })
			var result map[string]string
			if exit != 0 || requests.Load() != 1 || stderr.Len() != 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result["name"] != tc.name {
				t.Fatalf("creation failed: exit %d, calls %d, stderr %q", exit, requests.Load(), &stderr)
			}
		})
	}
}

func TestCLICreationRequiresExactlyOneInputMode(t *testing.T) {
	for _, args := range [][]string{
		{"board", "create"},
		{"board", "create", "name", "Personal", "json"},
		{"board", "create", "name", "Personal", "--json=false"},
		{"board", "create", "--json=false"},
		{"board", "create", "name", ""},
		{"board", "create", "name", string([]byte{0xff})},
		{"board", "create", "Personal"},
	} {
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput(args, strings.NewReader(`{"name":"Do not create"}`), &stdout, &stderr,
			func(string) string { t.Fatal("invalid creation modes reached execution"); return "" })
		if exit != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("invalid creation mode accepted: %v", args)
		}
	}
}

func TestCLICommandHelpDescribesCreationModes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLIWithInput([]string{"board", "create", "--help"}, strings.NewReader(""), &stdout, &stderr,
		func(string) string { t.Fatal("help reached execution"); return "" })
	if exit != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "board create [--preset terry] name NAME") ||
		!strings.Contains(stdout.String(), "board create json <JSON_FILE|->") {
		t.Fatalf("unhelpful command help: exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
	}
}

func TestCLIExplicitRevisionUsesTheMatchingResourceTag(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const tag = `"opaque-resource-tag"`
	for _, command := range []struct {
		name, path, input, body string
		args                    []string
	}{
		{"rename", "/boards/" + id, "", `{"name":"New name"}`, []string{"board", "rename", id, "New name"}},
		{"patch", "/boards/" + id, `{"name":"New name"}`, `{"name":"New name"}`, []string{"board", "patch", id, "-"}},
		{"pins", "/workspace", "", `{"pinned_board_ids":["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"]}`, []string{"workspace", "pin", "set", id}},
	} {
		for _, expected := range []string{"1", "3"} {
			t.Run(command.name+" revision "+expected, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.URL.Path != "/api/2026-11-10"+command.path {
						t.Error("wrong resource")
					}
					w.Header().Set("ETag", tag)
					if r.Method == http.MethodGet {
						w.Write([]byte(`{"revision":1}`))
						return
					}
					body, _ := io.ReadAll(r.Body)
					if expected != "1" || r.Method != http.MethodPatch || r.Header.Get("If-Match") != tag || string(body) != command.body {
						t.Error("mutation did not use the matching revision's actual ETag and body")
					}
					w.Write([]byte(`{"revision":2}`))
				}))
				defer server.Close()
				offset := 2
				if command.args[1] == "pin" {
					offset = 3
				}
				args := append([]string{}, command.args[:offset]...)
				args = append(args, "--revision", expected)
				args = append(args, command.args[offset:]...)
				env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
				var stdout, stderr bytes.Buffer
				exit := runCLIWithInput(args, strings.NewReader(command.input), &stdout, &stderr, func(key string) string { return env[key] })
				if expected == "1" {
					if exit != 0 || requests.Load() != 2 || !equalCLIJSON(t, stdout.Bytes(), []byte(`{"revision":2}`)) || stderr.Len() != 0 {
						t.Fatalf("matching revision failed: exit %d, calls %d, stderr %q", exit, requests.Load(), &stderr)
					}
				} else if exit != 1 || requests.Load() != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "revision mismatch") {
					t.Fatalf("mismatching revision was not refused before mutation: exit %d, calls %d", exit, requests.Load())
				}
			})
		}
	}
}

func TestCLIRevisionRequiresAnExactValidNumber(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "true", "0x10", "9007199254740992", "18446744073709551616"} {
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput([]string{"workspace", "pin", "set", "--revision", value, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, strings.NewReader(`{"pinned_board_ids":[]}`),
			&stdout, &stderr, func(string) string { t.Fatal("invalid revision reached execution"); return "" })
		if exit != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("invalid revision %q was accepted", value)
		}
	}
}

func TestCLIExplicitRevisionRefusesInvalidResourceRevision(t *testing.T) {
	for _, body := range []string{`{}`, `{"revision":0}`, `{"revision":null}`, `{"revision":true}`, `{"revision":"1"}`, `{"revision":1.5}`, `{"revision":9007199254740992}`} {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.Method != http.MethodGet {
				t.Error("invalid resource revision triggered a mutation")
			}
			w.Header().Set("ETag", `"opaque-resource-tag"`)
			w.Write([]byte(body))
		}))
		env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput([]string{"workspace", "pin", "set", "--revision", "1", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, strings.NewReader(`{"pinned_board_ids":[]}`),
			&stdout, &stderr, func(key string) string { return env[key] })
		server.Close()
		if exit != 1 || requests.Load() != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "valid resource revision") {
			t.Fatalf("invalid resource revision was accepted: %s", body)
		}
	}
}

func TestCLIPinActionsPreserveOrderAndUseOneGuardedSave(t *testing.T) {
	const a = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const b = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const c = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const d = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	for _, tc := range []struct {
		name   string
		args   []string
		want   []string
		refuse bool
		warn   bool
	}{
		{"replace order", []string{"set", b, a}, []string{b, a}, false, false},
		{"append", []string{"add", c}, []string{a, b, c}, false, false},
		{"append batch order", []string{"add", d, c}, []string{a, b, d, c}, false, false},
		{"append skips existing", []string{"add", c, a, d}, []string{a, b, c, d}, false, false},
		{"already pinned", []string{"add", strings.ToUpper(b)}, []string{a, b}, false, false},
		{"remove", []string{"remove", a}, []string{b}, false, false},
		{"remove absent", []string{"remove", c}, []string{a, b}, false, true},
		{"remove partial", []string{"remove", a, c}, []string{b}, false, true},
		{"remove repeated", []string{"remove", a, strings.ToUpper(a)}, []string{b}, false, true},
		{"remove malformed", []string{"remove", a, "bad"}, []string{b}, false, true},
		{"remove all malformed", []string{"remove", "bad"}, []string{a, b}, false, true},
		{"remove all", []string{"remove", a, b}, []string{}, false, false},
		{"stale unchanged add", []string{"add", b}, []string{a, b}, true, false},
		{"stale unchanged remove", []string{"remove", c}, []string{a, b}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/2026-11-10/workspace" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("wrong pin resource or authorization")
				}
				switch requests.Add(1) {
				case 1:
					if r.Method != http.MethodGet {
						t.Error("expected one initial read")
					}
					w.Header().Set("ETag", `"workspace-current-tag"`)
					json.NewEncoder(w).Encode(map[string]any{"revision": 1, "pinned_board_ids": []string{a, b}})
				case 2:
					var body struct {
						Pins []string `json:"pinned_board_ids"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || !slices.Equal(body.Pins, tc.want) || body.Pins == nil ||
						r.Method != http.MethodPatch || r.Header.Get("If-Match") != `"workspace-current-tag"` {
						t.Error("wrong ordered list or revision guard")
					}
					if tc.refuse {
						w.WriteHeader(http.StatusPreconditionFailed)
						return
					}
					w.Header().Set("ETag", `"workspace-result-tag"`)
					json.NewEncoder(w).Encode(map[string]any{"revision": 2, "pinned_board_ids": body.Pins})
				default:
					t.Error("pin operation performed extra requests")
				}
			}))
			defer server.Close()
			args := append([]string{"workspace", "pin"}, tc.args...)
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(args, strings.NewReader("invalid JSON must not be read"), &stdout, &stderr, func(key string) string { return env[key] })
			if requests.Load() != 2 || (tc.refuse && (exit != 1 || !strings.Contains(stderr.String(), "412"))) ||
				(!tc.refuse && (exit != 0 || strings.Contains(stderr.String(), "WARNING") != tc.warn)) {
				t.Fatalf("pin result exit %d, requests %d, stderr %q", exit, requests.Load(), &stderr)
			}
			if tc.refuse && !strings.HasPrefix(stderr.String(), "ERROR ") {
				t.Fatal("refused operation did not report ERROR")
			}
		})
	}
}

func TestCLIPinArgumentsFailBeforeExecution(t *testing.T) {
	const a = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, args := range [][]string{
		{"workspace", "pin"},
		{"workspace", "pin", "invalid"},
		{"workspace", "pin", "set"},
		{"workspace", "pin", "add"},
		{"workspace", "pin", "add", a, a},
		{"workspace", "pin", "remove"},
		{"workspace", "pin", "set", "bad"},
		{"workspace", "pin", "set", a, strings.ToUpper(a)},
	} {
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr,
			func(string) string { t.Fatal("invalid pin arguments reached execution"); return "" })
		if exit != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestCLICardCreateUsesBoardRevisionAndDoesNotReplay(t *testing.T) {
	const boardID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const cardID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const boardTag = `"board-revision-1"`
	const prefix = "/api/2026-11-10"
	for _, tc := range []struct {
		name, revision, stderr, source string
		status, requests, exit         int
	}{
		{"created", "1", "", "-", 201, 2, 0},
		{"created from file", "1", "", "file", 201, 2, 0},
		{"stale inspection", "2", "revision mismatch", "-", 0, 1, 1},
		{"stale mutation", "1", "HTTP 412", "-", 412, 2, 1},
		{"uncertain result", "1", "Mutation outcome unknown", "-", 500, 2, 1},
		{"invalid revision", "0", "revision requires", "-", 0, 0, 2},
		{"empty explicit revision", "", "revision requires", "-", 0, 0, 2},
		{"missing board tag", "1", "Mutation outcome unknown", "-", 201, 2, 1},
		{"duplicate board tags", "1", "Mutation outcome unknown", "-", 201, 2, 1},
		{"weak board tag", "1", "Mutation outcome unknown", "-", 201, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch requests.Add(1) {
				case 1:
					if r.Method != http.MethodGet || r.URL.Path != prefix+"/boards/"+boardID {
						t.Error("expected the board preflight")
					}
					w.Header().Set("ETag", boardTag)
					w.Write([]byte(`{"id":"` + boardID + `","revision":1}`))
				case 2:
					var body map[string]string
					if r.Method != http.MethodPost || r.URL.Path != prefix+"/cards" ||
						r.Header.Get("If-Match") != "" || json.NewDecoder(r.Body).Decode(&body) != nil ||
						body["expected_board_etag"] != boardTag || body["board_id"] != boardID ||
						body["id"] != cardID || body["title"] != "Example" {
						t.Error("invalid card creation request")
					}
					w.Header().Set("ETag", `"card-revision--1"`)
					switch tc.name {
					case "missing board tag":
					case "duplicate board tags":
						w.Header().Add("X-Board-ETag", boardTag)
						w.Header().Add("X-Board-ETag", boardTag)
					case "weak board tag":
						w.Header().Set("X-Board-ETag", "W/"+boardTag)
					default:
						w.Header().Set("X-Board-ETag", boardTag)
					}
					w.WriteHeader(tc.status)
					w.Write([]byte(`{"id":"` + cardID + `","board_id":"` + boardID + `"}`))
				default:
					t.Error("CLI replayed the mutation")
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
			args := []string{"card", "create", "--revision", tc.revision}
			input := `{"board_id":"` + strings.ToUpper(boardID) + `","id":"` + cardID + `","title":"Example","lane_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc"}`
			body := strings.NewReader(input)
			source := tc.source
			if source == "file" {
				source = filepath.Join(t.TempDir(), "card definition.json")
				if err := os.WriteFile(source, []byte(input), 0600); err != nil {
					t.Fatal(err)
				}
				body = strings.NewReader("file input must replace stdin")
			}
			if source != "" {
				args = append(args, boardID, source)
			}
			exit := runCLIWithInput(args, body, &stdout, &stderr, func(key string) string { return env[key] })
			if exit != tc.exit || int(requests.Load()) != tc.requests || !strings.Contains(stderr.String(), tc.stderr) {
				t.Fatalf("exit %d, requests %d, stderr %q", exit, requests.Load(), stderr.String())
			}
			if tc.exit == 0 && !strings.Contains(stdout.String(), cardID) {
				t.Fatal("created card missing from stdout")
			}
		})
	}
}

func TestCLIJSONFileSelection(t *testing.T) {
	const boardID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, source := range []string{"definition with spaces.json", "-"} {
		for _, args := range [][]string{
			{"board", "create", "json", source},
			{"board", "patch", boardID, source},
			{"policy", "grant", boardID, "json", source},
		} {
			command, err := parseCLI(args)
			if err != nil || command.inputPath != source {
				t.Fatalf("file selection %v: %+v, %v", args, command, err)
			}
		}
	}
}

func TestCLIJSONCommandsRequireInputSource(t *testing.T) {
	const boardID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, args := range [][]string{
		{"card", "create"},
		{"board", "create", "json"},
		{"board", "patch", boardID},
		{"policy", "grant", boardID},
	} {
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput(args, nil, &stdout, &stderr, func(string) string {
			t.Fatal("missing input source reached execution")
			return ""
		})
		if exit != 2 || !strings.HasPrefix(stderr.String(), "ERROR ") {
			t.Fatalf("missing source %v: exit %d, stderr %s", args, exit, stderr.String())
		}
	}
}

func TestCLICardCreateRejectsInvalidBoardInputBeforeNetwork(t *testing.T) {
	const boardID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, body := range []string{
		`{"board_id":null}`, `{"board_id":12}`, `{"board_id":"invalid"}`,
		`{"board_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}`,
		`{"expected_board_etag":"untrusted"}`,
	} {
		var stdout, stderr bytes.Buffer
		exit := runCLIWithInput([]string{"card", "create", boardID, "-"}, strings.NewReader(body), &stdout, &stderr, func(key string) string {
			if key == "LOCALSWIM_URL" {
				return "http://127.0.0.1:1"
			}
			return "test-token"
		})
		if exit != 1 || !strings.Contains(stderr.String(), "card create") || strings.Contains(stderr.String(), "connect") {
			t.Fatalf("invalid input %s: exit %d, stderr %s", body, exit, stderr.String())
		}
	}
}

func TestCLICardGetPaths(t *testing.T) {
	const board = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const card = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	for _, ref := range []string{card, "19"} {
		command, err := parseCLI([]string{"card", "get", board, ref})
		if err != nil || command.cardTarget == nil || command.cardTarget.boardID != board || command.method != http.MethodGet {
			t.Fatalf("card get %s: %+v, %v", ref, command, err)
		}
		if ref == card && command.cardTarget.cardID != card {
			t.Fatal("lost card ID")
		}
		if ref == "19" && command.cardTarget.ticket != 19 {
			t.Fatal("lost ticket")
		}
	}
}

func TestCLIPolicyGrantPreservesOtherGrantsAndUsesBothValidators(t *testing.T) {
	const boardID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const policyID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const priorityID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const laneID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	const userID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	const secondLaneID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	const boardTag = `"board-revision-1"`
	const policyTag = `"policy-revision-1"`
	const prefix = "/api/2026-11-10"
	const oldGrant = `{"user_id":"` + userID + `","lane_id":"` + laneID + `"}`
	const newGrant = `{"user_id":"` + userID + `","lane_id":"` + secondLaneID + `"}`
	const resources = `[{"user_id":"` + userID + `","capabilities":["read"]}]`
	const policy = `{"default_priority_id":"` + priorityID + `","create_grants":[` + oldGrant + `],"move_grants":[],"resource_grants":` + resources + `}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1, 4:
			if r.Method != http.MethodGet || r.URL.Path != prefix+"/boards/"+boardID {
				t.Error("expected board inspection")
			}
			w.Header().Set("ETag", boardTag)
			w.Write([]byte(`{"id":"` + boardID + `","revision":1,"policy_id":"` + policyID + `"}`))
		case 2, 5:
			if r.Method != http.MethodGet || r.URL.Path != prefix+"/policies/"+policyID {
				t.Error("expected policy read")
			}
			w.Header().Set("ETag", policyTag)
			w.Write([]byte(policy))
		case 3:
			var body map[string]json.RawMessage
			if r.Method != http.MethodPut || r.URL.Path != prefix+"/policies/"+policyID ||
				r.Header.Get("If-Match") != policyTag || json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("expected revision-bound policy replacement")
			}
			if len(body) != 5 || string(body["board_etag"]) != `"\"board-revision-1\""` ||
				string(body["default_priority_id"]) != `"`+priorityID+`"` ||
				string(body["create_grants"]) != "["+oldGrant+","+newGrant+"]" ||
				string(body["move_grants"]) != "[]" || string(body["resource_grants"]) != resources {
				t.Error("policy replacement did not preserve the existing permissions")
			}
			w.Header().Set("X-Board-ETag", `"board-revision-2"`)
			w.Write([]byte(`{"updated":true}`))
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	env := map[string]string{"LOCALSWIM_URL": server.URL, "LOCALSWIM_TOKEN": "test-token"}
	getenv := func(key string) string { return env[key] }
	var stdout, stderr bytes.Buffer
	args := []string{"policy", "grant", "--revision", "1", strings.ToUpper(boardID), "json", "-"}
	body := strings.NewReader(`{"create_grants":[` + newGrant + `]}`)
	if exit := runCLIWithInput(args, body, &stdout, &stderr, getenv); exit != 0 || requests.Load() != 3 ||
		!equalCLIJSON(t, stdout.Bytes(), []byte(`{"updated":true}`)) || stderr.Len() != 0 {
		t.Fatalf("policy grant exit %d, requests %d, stderr %q", exit, requests.Load(), stderr.String())
	}
	stdout.Reset()
	if exit := runCLIWithInput([]string{"policy", "get", strings.ToUpper(boardID)}, strings.NewReader(""), &stdout, &stderr, getenv); exit != 0 ||
		requests.Load() != 5 || !equalCLIJSON(t, stdout.Bytes(), []byte(policy)) || stderr.Len() != 0 {
		t.Fatalf("policy get exit %d, requests %d, stderr %q", exit, requests.Load(), stderr.String())
	}
}

func equalCLIJSON(t *testing.T, actual, expected []byte) bool {
	t.Helper()
	var actualValue, expectedValue any
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		t.Fatalf("invalid expected JSON: %v", err)
	}
	return reflect.DeepEqual(actualValue, expectedValue)
}
