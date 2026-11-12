package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCLIRejectsWhitespaceOnlyCardTitles(t *testing.T) {
	for _, title := range []string{"", " \t\r\n", "\u0085\u00a0\u1680\u2003\u2028\u202f\u205f\u3000"} {
		for _, args := range [][]string{
			{"card", "create", t03Board, t03Lane, title},
			{"card", "title", t03Board, "1", title},
		} {
			var output, diagnostic bytes.Buffer
			code := runCLIWithInput(args, nil, &output, &diagnostic, func(string) string {
				t.Fatal("blank title reached transport")
				return ""
			})
			if code != 2 || output.Len() != 0 || diagnostic.Len() == 0 {
				t.Fatalf("blank title accepted: %q (%d)", args, code)
			}
		}
	}
	const title = "\u00a0  Café 🐝  \u3000"
	for _, args := range [][]string{
		{"card", "create", t03Board, t03Lane, title},
		{"card", "title", t03Board, "1", title},
	} {
		command, err := parseCLI(args)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]string
		if err := json.Unmarshal(command.body, &body); err != nil || body["title"] != title {
			t.Fatalf("meaningful title changed: %s, %v", command.body, err)
		}
	}
}

func TestBoardAndPolicyModesAreExplicitEnums(t *testing.T) {
	for _, args := range [][]string{
		{"board", "create", "Name", "Example"},
		{"board", "create", "other", "Example"},
		{"board", "create", "--name", "Example"},
		{"board", "create", "--json", "-"},
		{"policy", "grant", t03Board, "Create", "1", t03Card, t03Lane},
		{"policy", "grant", t03Board, "other", "-"},
		{"policy", "grant", t03Board, "create", "0", t03Card, t03Lane},
		{"policy", "grant", "--create", t03Board, t03Card, t03Lane},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Fatalf("accepted invalid mode or revision: %q", args)
		}
	}
}
