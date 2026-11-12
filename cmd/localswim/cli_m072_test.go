package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestM072RoutineWritesReuseValidators(t *testing.T) {
	const text = " café 🚩\r\n\n"
	const otherCard = "a162ddc4-dddc-4ebd-8a19-426ee18cae4f"
	for _, tc := range []struct {
		name, action, readPath, validator, method, writePath, response string
		args                                                           []string
		body                                                           map[string]any
	}{
		{
			name: "create", action: "create", readPath: "/boards/" + t03Board, validator: t03BoardTag,
			method: http.MethodPost, writePath: "/cards", response: `{"board_id":"` + t03Board + `"}`,
			args: []string{"--description", text, strings.ToUpper(t03Board), strings.ToUpper(t03Lane), text},
			body: map[string]any{"board_id": t03Board, "lane_id": t03Lane, "title": text, "description": text, "expected_board_etag": t03BoardTag},
		},
		{
			name: "comment", action: "post", readPath: "/cards/" + t03Card, validator: t03CardTag,
			method: http.MethodPost, writePath: "/comments", response: `{"board_id":"` + t03Board + `","card_id":"` + t03Card + `"}`,
			args: []string{"--text", text, t03Board, t03Card},
			body: map[string]any{"card_id": t03Card, "text": text, "expected_card_etag": t03CardTag},
		},
		{
			name: "batch", action: "set-priorities", readPath: "/boards/" + t03Board, validator: t03BoardTag,
			method: http.MethodPatch, writePath: "/cards",
			response: `{"board_id":"` + t03Board + `","board_revision":13,"cards":[],"changes":[]}`,
			args:     []string{t03Board, strings.ToUpper(t03Lane), strings.ToUpper(t03Card), otherCard},
			body:     map[string]any{"board_id": t03Board, "card_ids": []any{t03Card, otherCard}, "priority_id": t03Lane, "expected_board_etag": t505BoardTag},
		},
	} {
		for _, revision := range []string{"12", "11"} {
			t.Run(tc.name+"/"+revision, func(t *testing.T) {
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.name == "batch" && t505ServeBatchResolution(w, r) {
						reads.Add(1)
						return
					}
					if tc.name == "comment" && t03ServeCardResolution(w, r) {
						reads.Add(1)
						return
					}
					switch {
					case r.Method == http.MethodGet && r.URL.Path == t03Root+tc.readPath:
						reads.Add(1)
						w.Header().Set("ETag", tc.validator)
						_, _ = io.WriteString(w, `{"id":"`+t03Board+`","revision":12}`)
					case r.Method == tc.method && r.URL.Path == t03Root+tc.writePath:
						writes.Add(1)
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, tc.body) {
							t.Errorf("write body = %#v, want %#v (decode error %v)", body, tc.body, err)
						}
						w.Header().Set("ETag", t03ResourceTag)
						w.Header().Set("X-Board-ETag", t03BoardTag)
						if tc.method == http.MethodPost {
							w.WriteHeader(http.StatusCreated)
						}
						_, _ = io.WriteString(w, tc.response)
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer server.Close()
				group := "card"
				if tc.action == "post" {
					group = "comment"
				}
				args := append([]string{group, tc.action, "--revision", revision}, tc.args...)
				var out, stderr bytes.Buffer
				code := runCLIWithInput(args, nil, &out, &stderr, t03Env(server.URL))
				wantReads := int32(1)
				if tc.name == "comment" {
					wantReads = 2
				}
				if tc.name == "batch" {
					wantReads = 4
					if revision == "11" {
						wantReads = 2
					}
				}
				if reads.Load() != wantReads {
					t.Fatalf("preflight reads = %d, want %d", reads.Load(), wantReads)
				}
				if revision == "12" {
					var compact bytes.Buffer
					if code != 0 || writes.Load() != 1 || json.Compact(&compact, out.Bytes()) != nil || compact.String() != tc.response {
						t.Fatalf("result = %d, %d writes, %q, %s", code, writes.Load(), out.String(), &stderr)
					}
				} else if code != 1 || writes.Load() != 0 || !strings.Contains(stderr.String(), "revision mismatch") {
					t.Fatalf("stale result = %d, %d writes, %s", code, writes.Load(), &stderr)
				}
			})
		}
	}
}

func TestM072ContextualHelpAndInputErrors(t *testing.T) {
	noService := func(string) string {
		t.Helper()
		t.Fatal("command tried to connect")
		return ""
	}
	for _, tc := range []struct {
		group, action, option, input string
	}{
		{"card", "create", "LANE_ID TITLE", "FILE|-"},
		{"comment", "post", "--text", "FILE|-"},
		{"card", "set-priorities", "PRIORITY_ID", "CARD_ID"},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput([]string{tc.group, tc.action, "--help"}, nil, &out, &stderr, noService)
		if code != 0 || stderr.Len() != 0 || strings.Contains(out.String(), "workspace info") {
			t.Fatalf("unexpected %s %s help: %d, %s, %s", tc.group, tc.action, code, &out, &stderr)
		}
		for _, required := range []string{tc.option, tc.input, "--revision N", "Examples:"} {
			if !strings.Contains(out.String(), required) {
				t.Errorf("%s %s help missing %q", tc.group, tc.action, required)
			}
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"card", "create"}, "requires BOARD_ID LANE_ID TITLE or BOARD_ID FILE|-"},
		{[]string{"comment", "post", t03Board, t03Card}, "followed by FILE or -"},
		{[]string{"card", "set-priorities", t03Board}, "requires BOARD_ID PRIORITY_ID"},
		{[]string{"card", "create", "--description", "text", t03Board, t03Lane}, "requires BOARD_ID LANE_ID TITLE"},
		{[]string{"card", "create", t03Board, t03Lane, "\xff"}, "valid UTF-8"},
		{[]string{"card", "create", t03Board, t03Lane, ""}, "nonempty valid UTF-8 title"},
		{[]string{"card", "create", t03Board, "invalid-lane", "Title"}, "requires BOARD_ID LANE_ID TITLE"},
		{[]string{"card", "create", t03Board, t03Lane, "Title", "Extra"}, "requires BOARD_ID LANE_ID TITLE"},
		{[]string{"card", "create", "--title", "text", t03Board, t03Lane}, "flag provided but not defined"},
		{[]string{"comment", "post", "--text=", t03Board, t03Card}, "nonempty valid UTF-8"},
		{[]string{"comment", "post", "--text", "\xff", t03Board, t03Card}, "nonempty valid UTF-8"},
		{[]string{"comment", "post", "--text", "text", t03Board, t03Card, "-"}, "documented arguments"},
		{[]string{"card", "set-priorities", t03Board, "", t03Card}, "UUIDv4 priority ID"},
		{[]string{"card", "set-priorities", t03Board, t03Lane, "-"}, "ticket requires an integer"},
		{[]string{"card", "set-priorities", t03Board, t03Lane, t03Card, strings.ToUpper(t03Card)}, "duplicate card selector"},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput(tc.args, nil, &out, &stderr, noService)
		if code != 2 || out.Len() != 0 || !strings.HasPrefix(stderr.String(), "ERROR ") || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%v result = %d, %s, %s", tc.args, code, &out, &stderr)
		}
	}
}

func TestCardCreatePositionalTitleAndDocumentForms(t *testing.T) {
	for _, title := range []string{"A title", "  Café 🐝  ", "-", "--title", "card.json"} {
		cmd, err := parseCLI([]string{"card", "create", t03Board, t03Lane, title})
		if err != nil {
			t.Fatalf("title %q: %v", title, err)
		}
		var body map[string]string
		if err := json.Unmarshal(cmd.body, &body); err != nil {
			t.Fatal(err)
		}
		if body["title"] != title || body["board_id"] != t03Board || body["lane_id"] != t03Lane || cmd.inputPath != "" {
			t.Fatalf("positional title changed: %#v", cmd)
		}
		if _, exists := body["description"]; exists {
			t.Fatal("omitted description was added")
		}
	}
	for _, path := range []string{"card.json", "-"} {
		cmd, err := parseCLI([]string{"card", "create", t03Board, path})
		if err != nil || cmd.inputPath != path || cmd.body != nil || cmd.boardID != t03Board {
			t.Fatalf("document form %q: %#v, %v", path, cmd, err)
		}
	}
}

func TestPriorityBatchUsesOnlyPositionalPriority(t *testing.T) {
	for _, args := range [][]string{
		{"card", "set-priorities", t03Board, "-"},
		{"card", "set-priorities", t03Board, "priorities.json"},
		{"card", "set-priorities", "--priority", t03Lane, t03Board, t03Card},
		{"card", "set-priorities", "--tickets", t03Board, t03Lane},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput(args, strings.NewReader(`{"card_ids":[],"priority_id":"ignored"}`), &out, &stderr, func(string) string {
			t.Fatal("invalid positional input reached transport")
			return ""
		})
		if code != 2 || out.Len() != 0 {
			t.Fatalf("accepted invalid arguments %v: %d %s", args, code, &stderr)
		}
	}
	if strings.Contains(priorityBatchHelp, "JSON_FILE") || strings.Contains(priorityBatchHelp, "--priority") {
		t.Fatal("priority help retains a removed argument form")
	}
}
