package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/v2"
)

func TestCLIBoardRecoverRequiresExplicitModeAndInput(t *testing.T) {
	for _, args := range [][]string{
		{"board", "recover", t03Board, "-"},
		{"board", "recover", t03Board, "same_id"},
		{"board", "recover", t03Board, "auto", "-"},
		{"board", "recover", t03Board, "SAME_ID", "-"},
		{"board", "recover", t03Board, "fresh", ""},
		{"board", "recover", t03Board, "fresh", "-", "extra"},
		{"board", "recover", "--mode", "same_id", t03Board, "-"},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput(args, nil, &out, &stderr, func(string) string {
			t.Fatal("invalid recovery command reached transport")
			return ""
		})
		if code != 2 || out.Len() != 0 {
			t.Fatalf("invalid recovery command was accepted: %v", args)
		}
	}
}

func TestCLIBoardRecoverPositionalModes(t *testing.T) {
	for _, mode := range []string{"same_id", "fresh"} {
		for _, force := range []string{"-f", "--force"} {
			cmd, err := parseCLI([]string{"board", "recover", force, t03Board, mode, "-"})
			if err != nil || cmd.recoveryMode != mode || cmd.inputPath != "-" || !cmd.force || cmd.resource != "/recoveries/"+t03Board+"?mode="+mode {
				t.Fatalf("recovery %s %s: %#v, %v", force, mode, cmd, err)
			}
		}
	}
}

func TestCLIBoardRecoverUsesVerifiedFailureAndSingleWrite(t *testing.T) {
	for _, status := range []int{201, 412, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			reads, writes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					reads++
					if r.URL.Path != "/api/"+v2.Version+"/verifications/"+t03Board {
						t.Error("incorrect recovery verification route")
					}
					w.Header().Set("ETag", `"recovery-observation"`)
					fmt.Fprintf(w, `{"board_id":%q,"at":"2026-11-10 00:00:00+00:00","valid":false,"revision":null,"issues":[{"field":"snapshot","code":"integrity_failure"}],"recovery":{"mode":"same_id","revision_floor":2}}`, t03Board)
					return
				}
				writes++
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != `{}` || r.Method != http.MethodPost ||
					r.URL.Path != "/api/"+v2.Version+"/recoveries/"+t03Board || r.URL.Query().Get("mode") != "same_id" ||
					r.Header.Get("If-Match") != `"recovery-observation"` {
					t.Error("incorrect recovery request")
				}
				w.Header().Set("ETag", fmt.Sprintf(`"%s-3"`, t03Board))
				w.Header().Set("X-Board-ETag", fmt.Sprintf(`"%s-3"`, t03Board))
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"id":%q,"revision":3,"availability":"open","publishing":{"enabled":false}}`, t03Board)
			}))
			defer server.Close()
			var out, stderr bytes.Buffer
			code := runCLIWithInput([]string{"board", "recover", "--force", t03Board, "same_id", "-"}, strings.NewReader(`{}`), &out, &stderr, t03Env(server.URL))
			if reads != 1 || writes != 1 || (code == 0) != (status == 201) {
				t.Fatalf("reads=%d writes=%d code=%d: %s", reads, writes, code, &stderr)
			}
		})
	}
}
