package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestOperationIDVersionsAndCanonicalInputs(t *testing.T) {
	for _, args := range [][]string{
		{"backup", "create", "--id", strings.ToUpper(t06Operation), t03Board},
		{"publication", "create", "--id", strings.ToUpper(t06Operation), t03Board, strings.ToUpper(t06Operation)},
		{"restoration", "create", "--id", strings.ToUpper(t06Operation), t03Board, strings.ToUpper(t06Operation)},
	} {
		command, err := parseCLI(args)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]string
		if err := json.Unmarshal(command.body, &body); err != nil {
			t.Fatal(err)
		}
		if body["id"] != t06Operation {
			t.Fatal("operation input was not canonicalized")
		}
	}
	for _, args := range [][]string{
		{"backup", "info", t03Card},
		{"backup", "create", "--id", t03Card, t03Board},
		{"publication", "create", t03Board, t03Card},
		{"restoration", "get", t03Card},
		{"service", "activation", t03Card},
		{"service", "activate", "--id", t03Card, testBuildID},
		{"service", "activate", t06Operation},
		{"card", "get", t06Operation},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Fatalf("accepted wrong ID version: %v", args)
		}
	}
	for _, args := range [][]string{
		{"backup", "create", t03Board},
		{"restoration", "create", t03Board, t06Operation},
		{"publication", "create", t03Board, t06Operation},
	} {
		command, err := parseCLI(args)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]string
		if err := json.Unmarshal(command.body, &body); err != nil {
			t.Fatal(err)
		}
		if !operationIDPattern.MatchString(body["id"]) {
			t.Fatal("generated ID is not UUIDv7")
		}
	}
	if !boardIDPattern.MatchString(newUploadID()) {
		t.Fatal("attachment IDs must remain UUIDv4")
	}
}

func TestTicketOrderPreviewRetainsUUIDv4CardIDs(t *testing.T) {
	for _, cardID := range []string{t03Card, strings.ToUpper(t03Card), t06Operation} {
		preview := []byte(fmt.Sprintf(`{"board_id":%q,"lane_id":%q,"board_revision":12,"proposed":[{"card_id":%q,"ticket":1}]}`, t03Board, t03Lane, cardID))
		body, err := ticketOrderApplyBody(preview, t03Board, t03Lane)
		if cardID == t06Operation {
			if err == nil {
				t.Fatal("accepted a UUIDv7 operation ID as a card ID")
			}
			continue
		}
		if err != nil {
			t.Fatalf("rejected valid UUIDv4 card ID: %v", err)
		}
		var input struct {
			CardIDs []string `json:"card_ids"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Fatal(err)
		}
		if len(input.CardIDs) != 1 || input.CardIDs[0] != t03Card {
			t.Fatal("lost canonical UUIDv4 card ID")
		}
	}
}
