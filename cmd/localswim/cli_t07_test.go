package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestT07ParseRequiredInputs(t *testing.T) {
	for _, args := range [][]string{
		{"card", "search", "--search", "text", "--search-content", t03Board},
		{"card", "triage-order", "--lane", t03Lane, t03Board},
		{"card", "lookup", t03Board},
		{"ticket-order", "apply", t03Lane, "-"},
		{"board", "restore", "--revision", "12", t03Board, "-"},
		{"restoration", "create", "--id", t06Operation, t03Board, t06Operation},
		{"publication", "create", "--id", t06Operation, t03Board, t06Operation},
		{"restoration", "get", t06Operation},
		{"publication", "get", t06Operation},
	} {
		if _, err := parseCLI(args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"board", "restore", t03Board},
		{"ticket-order", "apply", t03Lane},
		{"publication", "create", t03Board},
		{"restoration", "create", "--id", "bad", t03Board, t06Operation},
		{"restoration", "create", "--id", "bad", "--revision", "12", t03Board, t06Operation},
		{"card", "search", "--lane", "bad", t03Board},
		{"card", "triage-order", "--limit", "0", t03Board},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestT07SearchOrderAndPrettyJSON(t *testing.T) {
	for _, action := range []string{"search", "triage-order", "lookup"} {
		t.Run(action, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				q := r.URL.Query()
				sort := "triage"
				if action == "lookup" {
					sort = "ticket"
				}
				if r.Method != http.MethodGet || r.URL.Path != t03Root+"/cards" || q.Get("sort") != sort || q.Get("q") != "literal [text]" || q.Get("search_content") != "true" {
					t.Error("incorrect search request")
				}
				io.WriteString(w, `{"cards":[],"next_cursor":null}`)
			}))
			defer server.Close()
			var out, errout bytes.Buffer
			code := runCLIWithInput([]string{"card", action, "--search", "literal [text]", "--search-content", t03Board}, strings.NewReader(""), &out, &errout, t03Env(server.URL))
			if code != 0 || requests != 1 || !strings.Contains(out.String(), "\n  \"cards\"") {
				t.Fatalf("code=%d requests=%d error=%s", code, requests, &errout)
			}
		})
	}
}

func TestT07ApplyUsesSavedValidatorAndNeverRetries(t *testing.T) {
	for _, status := range []int{http.StatusPreconditionFailed, http.StatusInternalServerError} {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			var body struct {
				BoardID  string   `json:"board_id"`
				Expected string   `json:"expected_board_etag"`
				IDs      []string `json:"card_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.Method != http.MethodPut || body.Expected != fmt.Sprintf("\"%s-12\"", t03Board) || body.BoardID != t03Board || len(body.IDs) != 1 || body.IDs[0] != t03Card {
				t.Error("incorrect saved preview request")
			}
			w.WriteHeader(status)
		}))
		input := fmt.Sprintf(`{"board_id":%q,"lane_id":%q,"board_revision":12,"proposed":[{"card_id":%q,"ticket":1}]}`, t03Board, t03Lane, t03Card)
		var out, errout bytes.Buffer
		code := runCLIWithInput([]string{"ticket-order", "apply", t03Lane, "-"}, strings.NewReader(input), &out, &errout, t03Env(server.URL))
		server.Close()
		if code == 0 || requests != 1 {
			t.Fatalf("code=%d requests=%d", code, requests)
		}
	}
}

func TestT07RestoreNativeBytesAndStaleRefusal(t *testing.T) {
	for _, input := range []string{`{"format_version":2}`, "PK\x03\x04native-zip"} {
		for _, revision := range []string{"12", "11"} {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.Header().Set("ETag", t03BoardTag)
					if r.URL.Path != t03Root+"/snapshots/"+t03Board {
						t.Error("restore preflight must read snapshot validator")
					}
					io.WriteString(w, `{"board":{"revision":12}}`)
					return
				}
				writes++
				received, _ := io.ReadAll(r.Body)
				contentType := "application/json"
				if strings.HasPrefix(input, "PK") {
					contentType = "application/zip"
				}
				if string(received) != input || r.Header.Get("Content-Type") != contentType || r.Header.Get("If-Match") != t03BoardTag {
					t.Error("restore bytes or validator changed")
				}
				w.Header().Set("X-Board-ETag", t03ResourceTag)
				io.WriteString(w, `{"revision":13}`)
			}))
			var out, errout bytes.Buffer
			code := runCLIWithInput([]string{"board", "restore", "--revision", revision, t03Board, "-"}, strings.NewReader(input), &out, &errout, t03Env(server.URL))
			server.Close()
			if revision == "12" && (code != 0 || writes != 1) {
				t.Fatalf("restore failed %s", &errout)
			}
			if revision == "11" && (code == 0 || writes != 0) {
				t.Fatal("stale restore wrote")
			}
		}
	}
}

func TestT07OperationUnknownKeepsOriginalID(t *testing.T) {
	for _, group := range []string{"publication", "restoration"} {
		writes := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Header().Set("ETag", t03BoardTag)
				io.WriteString(w, `{"revision":12}`)
				return
			}
			writes++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		var out, errout bytes.Buffer
		code := runCLIWithInput([]string{group, "create", "--id", t06Operation, t03Board, t06Operation}, strings.NewReader(""), &out, &errout, t03Env(server.URL))
		server.Close()
		if code == 0 || writes != 1 || !strings.Contains(errout.String(), t06Operation) || !strings.Contains(errout.String(), "Mutation outcome unknown") {
			t.Fatalf("lost operation identity %s", &errout)
		}
	}
}

func TestM067CLISelection(t *testing.T) {
	for _, tc := range []struct {
		name, action             string
		options, lanes           []string
		archived, unfinishedOnly string
	}{
		{"search selected lanes", "search", []string{"--lane", strings.ToUpper(t03Lane), "--lane", t03Card, "--archived", "INCLUDE"}, []string{t03Lane, t03Card}, "include", ""},
		{"triage-order selected lanes", "triage-order", []string{"--lane", t03Lane, "--lane", t03Card, "--archived", "only"}, []string{t03Lane, t03Card}, "only", ""},
		{"unscoped triage-order", "triage-order", nil, nil, "", "true"},
		{"unscoped search", "search", nil, nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"card", tc.action}, tc.options...)
			cmd, err := parseCLI(append(args, t03Board))
			if err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(cmd.resource)
			if err != nil {
				t.Fatal(err)
			}
			query := target.Query()
			if !slices.Equal(query["lane_id"], tc.lanes) || query.Get("archived") != tc.archived ||
				query.Get("unfinished_only") != tc.unfinishedOnly || query.Get("sort") != "triage" {
				t.Fatalf("unexpected filters: %v", query)
			}
		})
	}
	for _, opts := range [][]string{
		{"--lane", "invalid", "--lane", t03Lane},
		{"--lane", t03Lane, "--lane", ""},
		{"--archived", "invalid"},
		{"--archived", ""},
	} {
		args := append([]string{"card", "triage-order"}, opts...)
		if _, err := parseCLI(append(args, t03Board)); err == nil {
			t.Errorf("accepted invalid options: %v", opts)
		}
	}
}

func TestCLITriageOrderReplacesNext(t *testing.T) {
	var out, stderr bytes.Buffer
	exit := runCLIWithInput([]string{"card", "next", t03Board}, nil, &out, &stderr, func(string) string {
		t.Fatal("removed command reached execution")
		return ""
	})
	if exit != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("removed command result: exit=%d stdout=%q stderr=%q", exit, &out, &stderr)
	}
	out.Reset()
	stderr.Reset()
	exit = runCLIWithInput([]string{"card", "triage-order", "--help"}, nil, &out, &stderr, func(string) string {
		t.Fatal("help reached execution")
		return ""
	})
	if exit != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "localswim card triage-order") || strings.Contains(out.String(), "localswim card next ") {
		t.Fatalf("unexpected help: exit=%d stdout=%q stderr=%q", exit, &out, &stderr)
	}
	command, err := parseCLI([]string{"card", "triage-order", t03Board})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.Parse(command.resource)
	if err != nil {
		t.Fatal(err)
	}
	query := endpoint.Query()
	if query.Get("limit") != "1" || query.Get("sort") != "triage" || query.Get("unfinished_only") != "true" {
		t.Fatalf("unexpected default ranking query: %v", query)
	}
}
