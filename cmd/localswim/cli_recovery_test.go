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
		{"board", "recover", "--mode", "same_id", t03Board},
		{"board", "recover", "--mode", "auto", t03Board, "-"},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Fatal("invalid recovery command was accepted")
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
			code := runCLIWithInput([]string{"board", "recover", "--force", "--mode", "same_id", t03Board, "-"}, strings.NewReader(`{}`), &out, &stderr, t03Env(server.URL))
			if reads != 1 || writes != 1 || (code == 0) != (status == 201) {
				t.Fatalf("reads=%d writes=%d code=%d: %s", reads, writes, code, &stderr)
			}
		})
	}
}
