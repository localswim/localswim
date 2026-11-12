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

func TestLanePrioritySelection(t *testing.T) {
	for _, flag := range []string{"--lane", "-l"} {
		t.Run(flag, func(t *testing.T) {
			cmd, err := parseCLI([]string{"card", "set-priorities", flag, strings.ToUpper(t03Lane), t03Board, t03Lane})
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				LaneID        string `json:"lane_id"`
				AllUnfinished bool   `json:"all_unfinished"`
			}
			if json.Unmarshal(cmd.body, &body) != nil || body.LaneID != t03Lane || body.AllUnfinished {
				t.Fatalf("unexpected lane request %s", cmd.body)
			}
			var notice bytes.Buffer
			if proceed, err := confirmWholeBoardWrite(cmd, nil, &notice); !proceed || err != nil || notice.Len() != 0 {
				t.Fatalf("explicit lane unexpectedly prompted: %t %v %s", proceed, err, &notice)
			}
		})
	}
	for _, args := range [][]string{
		{"--lane", "", t03Board, t03Lane},
		{"--lane", "invalid", t03Board, t03Lane},
		{"--lane", t03Lane, t03Board, t03Lane, "1"},
	} {
		if _, err := parseCLI(append([]string{"card", "set-priorities"}, args...)); err == nil {
			t.Fatalf("accepted invalid lane selection %v", args)
		}
	}
}

func TestLanePriorityCLIUsesOneGuardedBatchWithoutPrompt(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != t03Root+"/boards/"+t03Board {
				t.Errorf("unexpected read %s", r.URL.Path)
			}
			w.Header().Set("ETag", t03BoardTag)
			_, _ = io.WriteString(w, `{"revision":12}`)
			return
		}
		writes++
		var request struct {
			BoardID       string          `json:"board_id"`
			Expected      string          `json:"expected_board_etag"`
			LaneID        string          `json:"lane_id"`
			AllUnfinished bool            `json:"all_unfinished"`
			CardIDs       json.RawMessage `json:"card_ids"`
		}
		if r.Method != http.MethodPatch || r.URL.Path != t03Root+"/cards" ||
			json.NewDecoder(r.Body).Decode(&request) != nil || request.BoardID != t03Board ||
			request.Expected != t03BoardTag || request.LaneID != t03Lane || request.AllUnfinished || request.CardIDs != nil {
			t.Error("lane batch did not preserve its exact scope and board validator")
		}
		w.Header().Set("X-Board-ETag", t03BoardTag)
		_, _ = fmt.Fprintf(w, `{"board_id":%q,"board_revision":12,"cards":[],"changes":[]}`, t03Board)
	}))
	defer server.Close()
	var output, diagnostic bytes.Buffer
	code := runCLIWithInput([]string{"card", "set-priorities", "--lane", t03Lane, t03Board, t03Lane}, nil, &output, &diagnostic, t03Env(server.URL))
	if code != 0 || writes != 1 || diagnostic.Len() != 0 || output.Len() == 0 {
		t.Fatalf("code=%d writes=%d diagnostic=%s", code, writes, &diagnostic)
	}
}
