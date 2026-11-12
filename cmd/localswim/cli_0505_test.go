package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const t505BoardTag = `"2c525683-4c2f-4d98-9945-cead23c534ad-12"`

func t505ServeBatchResolution(w http.ResponseWriter, r *http.Request) bool {
	const other = "a162ddc4-dddc-4ebd-8a19-426ee18cae4f"
	if r.Method != http.MethodGet {
		return false
	}
	id, ticket := t03Card, 1
	if strings.HasSuffix(r.URL.Path, other) || r.URL.Query().Get("ticket") == "2" {
		id, ticket = other, 2
	}
	if r.URL.Path == t03Root+"/cards/"+id {
		w.Header().Set("ETag", fmt.Sprintf(`"%s-12"`, id))
		fmt.Fprintf(w, `{"id":%q,"board_id":%q,"ticket":%d,"revision":12}`, id, t03Board, ticket)
		return true
	}
	if r.URL.Path == t03Root+"/boards/"+t03Board && r.URL.Query().Get("view") == "ticket" {
		w.Header().Set("X-Board-ETag", t505BoardTag)
		fmt.Fprintf(w, `{"board_id":%q,"board_revision":12,"card":{"id":%q,"ticket":%d,"revision":12}}`, t03Board, id, ticket)
		return true
	}
	return false
}

func TestWholeBoardConfirmation(t *testing.T) {
	commands := []cliCommand{
		{t06Action: "card set-priorities", boardID: t03Board},
		{t07Action: "board restore", boardID: t03Board},
		{t07Action: "restoration create", boardID: t03Board},
		{recoveryMode: "same_id", boardID: t03Board},
	}
	for _, cmd := range commands {
		for _, tc := range []struct {
			answer       string
			accept, fail bool
		}{
			{"\n", true, false}, {"y\n", true, false}, {"YES\n", true, false}, {"n\n", false, false}, {"No\n", false, false},
			{"", false, true}, {"yes", false, true}, {"maybe\n", false, true},
		} {
			var out bytes.Buffer
			got, err := confirmWholeBoardWrite(cmd, strings.NewReader(tc.answer), &out)
			if got != tc.accept || (err != nil) != tc.fail || !strings.HasPrefix(out.String(), "INFO ") || !strings.Contains(out.String(), t03Board) || !strings.Contains(out.String(), "Continue? [Y/n]") {
				t.Fatalf("command %+v answer %q accepted=%t error=%v output=%q", cmd, tc.answer, got, err, &out)
			}
		}
		cmd.force = true
		var out bytes.Buffer
		if got, err := confirmWholeBoardWrite(cmd, nil, &out); !got || err != nil || !strings.HasPrefix(out.String(), "INFO ") || strings.Contains(out.String(), "Continue?") {
			t.Fatalf("forced command: %t %v %q", got, err, &out)
		}
	}
}

func TestWholeBoardPayloadRequiresForce(t *testing.T) {
	for _, cmd := range []cliCommand{{t07Action: "board restore", inputPath: "-"}, {recoveryMode: "same_id", inputPath: "-"}} {
		if got, err := confirmWholeBoardWrite(cmd, strings.NewReader("yes\n"), io.Discard); got || err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("stdin payload accepted: %t %v", got, err)
		}
	}
	cmd := cliCommand{t06Action: "card set-priorities", cardSelectors: []string{"1"}}
	var out bytes.Buffer
	if got, err := confirmWholeBoardWrite(cmd, nil, &out); !got || err != nil || out.Len() != 0 {
		t.Fatal("explicit selector prompted")
	}
}

func TestWholeBoardPriorityConfirmationTransport(t *testing.T) {
	for _, tc := range []struct {
		answer           string
		force            bool
		wantCode, writes int
	}{
		{"\n", false, 0, 1}, {"yes\n", false, 0, 1}, {"n\n", false, 0, 0}, {"", false, 1, 0}, {"invalid\n", false, 1, 0}, {"", true, 0, 1},
	} {
		writes, reads := 0, 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				reads++
				w.Header().Set("ETag", t03BoardTag)
				io.WriteString(w, `{"revision":12}`)
				return
			}
			writes++
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if string(body["all_unfinished"]) != "true" {
				t.Error("whole-board request omitted explicit all_unfinished intent")
			}
			if _, exists := body["card_ids"]; exists {
				t.Error("whole-board request supplied card_ids")
			}
			if string(body["priority_id"]) != fmt.Sprintf("%q", t03Lane) {
				t.Error("priority missing")
			}
			w.Header().Set("X-Board-ETag", t03BoardTag)
			fmt.Fprintf(w, `{"board_id":%q,"board_revision":12,"cards":[],"changes":[]}`, t03Board)
		}))
		args := []string{"card", "set-priorities"}
		if tc.force {
			args = append(args, "--force")
		}
		args = append(args, t03Board, t03Lane)
		var out, stderr bytes.Buffer
		code := runCLIWithInput(args, strings.NewReader(tc.answer), &out, &stderr, t03Env(server.URL))
		server.Close()
		if code != tc.wantCode || writes != tc.writes || (tc.writes == 0 && reads != 0) || !strings.HasPrefix(stderr.String(), "INFO ") {
			t.Fatalf("answer=%q force=%t code=%d reads=%d writes=%d stderr=%q", tc.answer, tc.force, code, reads, writes, &stderr)
		}
	}
}

func TestPriorityBatchMixedDuplicateRejected(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t505ServeBatchResolution(w, r) {
			return
		}
		writes++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	var out, stderr bytes.Buffer
	code := runCLIWithInput([]string{"card", "set-priorities", t03Board, t03Lane, "1", t03Card}, nil, &out, &stderr, t03Env(server.URL))
	if code != 1 || writes != 0 || !strings.Contains(stderr.String(), "same card") {
		t.Fatalf("code=%d writes=%d stderr=%q", code, writes, &stderr)
	}
}

func TestWholeBoardForceFlagParsing(t *testing.T) {
	for _, flag := range []string{"-f", "--force"} {
		for _, args := range [][]string{{"card", "set-priorities", flag, t03Board, t03Lane}, {"board", "restore", flag, t03Board, "-"}, {"board", "recover", flag, t03Board, "same_id", "-"}, {"restoration", "create", flag, t03Board, t06Operation}} {
			cmd, err := parseCLI(args)
			if err != nil || !cmd.force {
				t.Fatalf("%v: %+v %v", args, cmd, err)
			}
		}
	}
}

func TestWholeBoardCancellationDoesNotConnect(t *testing.T) {
	for _, args := range [][]string{
		{"card", "set-priorities", t03Board, t03Lane},
		{"board", "restore", t03Board, "unused.json"},
		{"board", "recover", t03Board, "same_id", "unused.json"},
		{"restoration", "create", t03Board, t06Operation},
	} {
		for _, answer := range []string{"no\n", "", "invalid\n"} {
			var out, stderr bytes.Buffer
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(http.StatusBadRequest) }))
			code := runCLIWithInput(args, strings.NewReader(answer), &out, &stderr, t03Env(server.URL))
			server.Close()
			wantCode := 1
			if answer == "no\n" {
				wantCode = 0
			}
			if code != wantCode || requests != 0 || out.Len() != 0 || !strings.HasPrefix(stderr.String(), "INFO ") {
				t.Fatalf("%v answer %q code=%d stdout=%q stderr=%q", args, answer, code, &out, &stderr)
			}
		}
	}
}

func TestPriorityBatchSelectorGrammar(t *testing.T) {
	for _, selectors := range [][]string{nil, {"1"}, {t03Card}, {"1", "a162ddc4-dddc-4ebd-8a19-426ee18cae4f"}} {
		got, body, err := parsePriorityBatch(t03Board, strings.ToUpper(t03Lane), "", selectors)
		if err != nil || len(got) != len(selectors) || !bytes.Contains(body, []byte(t03Lane)) || bytes.Contains(body, []byte("card_ids")) {
			t.Fatalf("selectors %v got %v body %s error %v", selectors, got, body, err)
		}
		if (len(selectors) == 0) != bytes.Contains(body, []byte(`"all_unfinished":true`)) {
			t.Fatalf("selection intent missing or unexpected: %s", body)
		}
	}
	for _, selectors := range [][]string{{"1", "0001"}, {t03Card, strings.ToUpper(t03Card)}, {"0"}, {"9007199254740992"}, {"invalid"}} {
		if _, _, err := parsePriorityBatch(t03Board, t03Lane, "", selectors); err == nil {
			t.Fatalf("invalid selectors accepted: %v", selectors)
		}
	}
	selectors := make([]string, 1001)
	for i := range selectors {
		selectors[i] = fmt.Sprint(i + 1)
	}
	if _, _, err := parsePriorityBatch(t03Board, t03Lane, "", selectors); err == nil {
		t.Fatal("accepted more than 1000 selectors")
	}
}
