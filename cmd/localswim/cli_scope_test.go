package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBoardResourceCommandsRequireBoardContext(t *testing.T) {
	for _, args := range [][]string{
		{"relationship", "get", t03Lane},
		{"relationship", "patch", t03Lane, "-"},
		{"relationship", "create", t03Card, "blocks", t03Lane},
		{"attachment", "get", t03Lane},
		{"attachment", "download", t03Lane, "-"},
		{"attachment", "detach", t03Lane},
		{"lane", "get", t03Lane},
		{"lane", "rename", t03Lane, "Ready"},
		{"lane", "set-key", t03Lane, "ready"},
		{"lane", "retire", t03Lane},
		{"lane", "set-approval-button", t03Lane, "enabled"},
		{"backup", "info", t06Operation},
		{"backup", "download", t06Operation, "-"},
		{"restoration", "get", t06Operation},
		{"publication", "get", t06Operation},
		{"ticket-order", "preview", t03Lane},
		{"ticket-order", "apply", t03Lane, "-"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			if _, err := parseCLI(args); err == nil {
				t.Fatalf("accepted command without board context: %v", args)
			}
		})
	}
}

func TestTicketOrderPreviewRejectsDuplicateSelectors(t *testing.T) {
	for _, selectors := range [][]string{{t03Card, strings.ToUpper(t03Card)}, {"1", t03Card}} {
		t.Run(strings.Join(selectors, "/"), func(t *testing.T) {
			previews := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case t03Root + "/cards/" + t03Card:
					writeScopeCard(w, t03Card, t03Board, 12)
				case t03Root + "/boards/" + t03Board:
					writeScopeView(w, r)
				case t03Root + "/lanes/" + t03Lane:
					fmt.Fprintf(w, `{"id":%q,"board_id":%q}`, t03Lane, t03Board)
				default:
					previews++
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			args := append([]string{"ticket-order", "preview", t03Board, t03Lane}, selectors...)
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput(args, nil, &stdout, &stderr, t03Env(server.URL))
			if code != 1 || previews != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "distinct cards") {
				t.Fatalf("duplicate preview code=%d previews=%d stdout=%q stderr=%q", code, previews, &stdout, &stderr)
			}
		})
	}
}

func TestTicketOrderPreviewRejectsChangedBoardRevision(t *testing.T) {
	for _, selector := range []string{t03Card, "1"} {
		t.Run(selector, func(t *testing.T) {
			previews := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case t03Root + "/cards/" + t03Card:
					writeScopeCard(w, t03Card, t03Board, 12)
				case t03Root + "/boards/" + t03Board:
					writeScopeView(w, r)
				case t03Root + "/lanes/" + t03Lane:
					fmt.Fprintf(w, `{"id":%q,"board_id":%q}`, t03Lane, t03Board)
				case t03Root + "/ticket-orders/" + t03Lane:
					previews++
					w.Header().Set("X-Board-ETag", fmt.Sprintf(`"%s-13"`, t03Board))
					fmt.Fprintf(w, `{"board_id":%q,"lane_id":%q,"board_revision":13,"selected_card_ids":[%q],"current":[],"proposed":[],"changed":false}`, t03Board, t03Lane, t03Card)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput([]string{"ticket-order", "preview", t03Board, t03Lane, selector}, nil, &stdout, &stderr, t03Env(server.URL))
			if code != 1 || previews != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("changed preview code=%d previews=%d stdout=%q stderr=%q", code, previews, &stdout, &stderr)
			}
		})
	}
}

func TestBoardResourceCommandsRejectUnconfirmedScope(t *testing.T) {
	commands := []struct {
		args            []string
		path, id, input string
	}{
		{[]string{"relationship", "get", t03Board, t03Lane}, "/relationships/" + t03Lane, t03Lane, ""},
		{[]string{"relationship", "patch", t03Board, t03Lane, "-"}, "/relationships/" + t03Lane, t03Lane, `{"kind":"blocks"}`},
		{[]string{"relationship", "remove", t03Board, t03Lane}, "/relationships/" + t03Lane, t03Lane, ""},
		{[]string{"attachment", "get", t03Board, t03Lane}, "/attachments/" + t03Lane, t03Lane, ""},
		{[]string{"attachment", "download", t03Board, t03Lane, "-"}, "/attachments/" + t03Lane, t03Lane, ""},
		{[]string{"attachment", "detach", t03Board, t03Lane}, "/attachments/" + t03Lane, t03Lane, ""},
		{[]string{"lane", "get", t03Board, t03Lane}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"lane", "rename", t03Board, t03Lane, "Ready"}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"lane", "set-key", t03Board, t03Lane, "ready"}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"lane", "retire", t03Board, t03Lane}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"lane", "set-approval-button", t03Board, t03Lane, "enabled"}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"backup", "info", t03Board, t06Operation}, "/backups/" + t06Operation, t06Operation, ""},
		{[]string{"backup", "download", t03Board, t06Operation, "-"}, "/backups/" + t06Operation, t06Operation, ""},
		{[]string{"restoration", "get", t03Board, t06Operation}, "/restorations/" + t06Operation, t06Operation, ""},
		{[]string{"publication", "get", t03Board, t06Operation}, "/publications/" + t06Operation, t06Operation, ""},
		{[]string{"restoration", "create", "--force", t03Board, t06Operation}, "/backups/" + t06Operation, t06Operation, ""},
		{[]string{"publication", "create", t03Board, t06Operation}, "/backups/" + t06Operation, t06Operation, ""},
		{[]string{"card", "search", "--lane", t03Lane, t03Board}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"card", "lookup", "--lane", t03Lane, t03Board}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"card", "triage-order", "--lane", t03Lane, t03Board}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"ticket-order", "preview", t03Board, t03Lane}, "/lanes/" + t03Lane, t03Lane, ""},
		{[]string{"ticket-order", "apply", t03Board, t03Lane, "-"}, "/lanes/" + t03Lane, t03Lane,
			fmt.Sprintf(`{"board_id":%q,"lane_id":%q,"board_revision":12,"proposed":[{"card_id":%q,"ticket":1}]}`, t03Board, t03Lane, t03Card)},
	}
	for _, command := range commands {
		for _, mismatch := range []string{"board", "resource", "missing board"} {
			t.Run(strings.Join(command.args[:2], " ")+"/"+mismatch, func(t *testing.T) {
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					if r.Method != http.MethodGet || r.URL.Path != t03Root+command.path {
						t.Errorf("unexpected follow-up request %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					id, boardID := command.id, t03Board
					if mismatch == "board" {
						boardID = t03Card
					}
					if mismatch == "resource" {
						id = t03Board
					}
					if mismatch == "missing board" {
						boardID = ""
					}
					w.Header().Set("ETag", t03ResourceTag)
					_, _ = fmt.Fprintf(w, `{"id":%q,"board_id":%q,"revision":12}`, id, boardID)
				}))
				defer server.Close()
				var stdout, stderr bytes.Buffer
				code := runCLIWithInput(command.args, strings.NewReader(command.input), &stdout, &stderr, t03Env(server.URL))
				if code != 1 || requests != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
					t.Fatalf("scope refusal code=%d requests=%d stdout=%q stderr=%q", code, requests, &stdout, &stderr)
				}
			})
		}
	}
}

func TestTicketOrderApplyRejectsPreviewFromAnotherBoard(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("ETag", t03ResourceTag)
		_, _ = fmt.Fprintf(w, `{"id":%q,"board_id":%q}`, t03Lane, t03Board)
	}))
	defer server.Close()
	input := fmt.Sprintf(`{"board_id":%q,"lane_id":%q,"board_revision":12,"proposed":[{"card_id":%q,"ticket":1}]}`, t03Card, t03Lane, t03Card)
	var stdout, stderr bytes.Buffer
	code := runCLIWithInput([]string{"ticket-order", "apply", t03Board, t03Lane, "-"}, strings.NewReader(input), &stdout, &stderr, t03Env(server.URL))
	if code != 1 || requests != 1 || stdout.Len() != 0 {
		t.Fatalf("foreign preview code=%d requests=%d stdout=%q stderr=%q", code, requests, &stdout, &stderr)
	}
}

func TestBoardResourceReadsPublishConfirmedScope(t *testing.T) {
	for _, group := range []string{"relationship", "attachment", "lane", "restoration", "publication"} {
		t.Run(group, func(t *testing.T) {
			id := t03Lane
			if group == "restoration" || group == "publication" {
				id = t06Operation
			}
			body := fmt.Sprintf(`{"id":%q,"board_id":%q}`, id, t03Board)
			if group == "attachment" {
				body = string(attachmentFixture(id, nil))
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("read performed a mutation")
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput([]string{group, "get", t03Board, id}, nil, &stdout, &stderr, t03Env(server.URL))
			if code != 0 || !equalCLIJSON(t, stdout.Bytes(), []byte(body)) {
				t.Fatalf("confirmed read code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
		})
	}
}

const scopeBoardTag = `"` + t03Board + `-12"`

func writeScopeCard(w http.ResponseWriter, id, boardID string, revision int) {
	ticket := 1
	if id == t03Lane {
		ticket = 2
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%s-%d"`, id, revision))
	fmt.Fprintf(w, `{"id":%q,"board_id":%q,"ticket":%d,"revision":%d}`, id, boardID, ticket, revision)
}

func writeScopeView(w http.ResponseWriter, r *http.Request) {
	id, ticket := t03Card, 1
	if r.URL.Query().Get("ticket") == "2" {
		id, ticket = t03Lane, 2
	}
	w.Header().Set("X-Board-ETag", scopeBoardTag)
	fmt.Fprintf(w, `{"board_id":%q,"board_revision":12,"card":{"id":%q,"ticket":%d,"revision":12},"related_card":{"id":%q,"ticket":2,"revision":12}}`, t03Board, id, ticket, t03Lane)
}
