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
			method: http.MethodPost, writePath: "/cards", response: "{}",
			args: []string{"--title", text, "--description", text, strings.ToUpper(t03Board), strings.ToUpper(t03Lane)},
			body: map[string]any{"board_id": t03Board, "lane_id": t03Lane, "title": text, "description": text, "expected_board_etag": t03BoardTag},
		},
		{
			name: "comment", action: "post", readPath: "/cards/" + t03Card, validator: t03ResourceTag,
			method: http.MethodPost, writePath: "/comments", response: "{}",
			args: []string{"--text", text, t03Card},
			body: map[string]any{"card_id": t03Card, "text": text, "expected_card_etag": t03ResourceTag},
		},
		{
			name: "batch", action: "set-priorities", readPath: "/boards/" + t03Board, validator: t03BoardTag,
			method: http.MethodPatch, writePath: "/cards",
			response: `{"board_id":"` + t03Board + `","board_revision":13,"cards":[],"changes":[]}`,
			args:     []string{t03Board, strings.ToUpper(t03Lane), strings.ToUpper(t03Card), otherCard},
			body:     map[string]any{"board_id": t03Board, "card_ids": []any{t03Card, otherCard}, "priority_id": t03Lane, "expected_board_etag": t03BoardTag},
		},
	} {
		for _, revision := range []string{"12", "11"} {
			t.Run(tc.name+"/"+revision, func(t *testing.T) {
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodGet && r.URL.Path == t03Root+tc.readPath:
						reads.Add(1)
						w.Header().Set("ETag", tc.validator)
						_, _ = io.WriteString(w, `{"revision":12}`)
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
				if reads.Load() != 1 {
					t.Fatalf("preflight reads = %d, want 1", reads.Load())
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
		{"card", "create", "--title", "JSON_FILE|-"},
		{"comment", "post", "--text", "JSON_FILE|-"},
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
		{[]string{"card", "create"}, "accepts one JSON file or - for standard input"},
		{[]string{"comment", "post", t03Card}, "followed by FILE or -"},
		{[]string{"card", "set-priorities", t03Board}, "requires BOARD_ID PRIORITY_ID"},
		{[]string{"card", "create", "--description", "text", t03Board, t03Lane}, "requires --title"},
		{[]string{"card", "create", "--title", "\xff", t03Board, t03Lane}, "valid UTF-8"},
		{[]string{"card", "create", "--title", "text", "card.json"}, "UUIDv4 board ID"},
		{[]string{"comment", "post", "--text=", t03Card}, "nonempty valid UTF-8"},
		{[]string{"comment", "post", "--text", "\xff", t03Card}, "nonempty valid UTF-8"},
		{[]string{"comment", "post", "--text", "text", t03Card, "-"}, "documented arguments"},
		{[]string{"card", "set-priorities", t03Board, "", t03Card}, "UUIDv4 priority ID"},
		{[]string{"card", "set-priorities", t03Board, t03Lane, "-"}, "distinct UUIDv4 card IDs"},
		{[]string{"card", "set-priorities", t03Board, t03Lane, t03Card, strings.ToUpper(t03Card)}, "distinct UUIDv4 card IDs"},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput(tc.args, nil, &out, &stderr, noService)
		if code != 2 || out.Len() != 0 || !strings.HasPrefix(stderr.String(), "ERROR ") || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%v result = %d, %s, %s", tc.args, code, &out, &stderr)
		}
	}
}

func TestPriorityBatchUsesOnlyPositionalPriority(t *testing.T) {
	for _, args := range [][]string{
		{"card", "set-priorities", t03Board, "-"},
		{"card", "set-priorities", t03Board, "priorities.json"},
		{"card", "set-priorities", "--priority", t03Lane, t03Board, t03Card},
		{"card", "set-priorities", t03Board, t03Lane},
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
