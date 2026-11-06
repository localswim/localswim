package archive

import (
	"testing"
)

func TestFileReadCLIGrammar(t *testing.T) {
	prefix := []string{"--json", "--installation", testContext.InstallationID, "--board", testBoard, "--incarnation", testInc, "--grant", "00000000-0000-4000-8000-000000000050"}
	for _, tail := range [][]string{{"attachment", "list", testCard}, {"attachment", "list", testCard, "--view", "retained", "--limit", "1"}, {"attachment", "inspect", testCard, testCard}} {
		if _, code, _ := parse(append(append([]string{}, prefix...), tail...)); code != "" {
			t.Fatal("valid read rejected")
		}
	}
	for _, tail := range [][]string{{"attachment", "list", testCard, "--limit", "01"}, {"attachment", "inspect", testCard}, {"attachment", "inspect", testCard, testCard, "--destination", "x"}, {"attachment", "list", testCard, "--limit", "1", "--limit", "2"}} {
		if _, code, _ := parse(append(append([]string{}, prefix...), tail...)); code == "" {
			t.Fatal("invalid read accepted")
		}
	}
}
