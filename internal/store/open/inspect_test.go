package open

import "testing"

func TestInspectionSelectVocabulary(t *testing.T) {
	for _, q := range []string{"SELECT 1", "select(1)", "SELECT ';--/*'", "SELECT \"semi;colon\" FROM x", "SELECT [column] FROM x", "SELECT 'it''s;literal'", "SELECT * FROM (SELECT 1)"} {
		if !inspectionSelect(q) {
			t.Fatalf("supported SELECT refused: %q", q)
		}
	}
	for _, q := range []string{"", "SELECT", "SELECTED 1", "PRAGMA query_only=OFF", "UPDATE board SET revision=2", "DELETE FROM blob RETURNING bytes", "ATTACH DATABASE 'x' AS x", "WITH x AS (SELECT 1) DELETE FROM blob", "SELECT 1; PRAGMA query_only=OFF", "SELECT 1;ATTACH 'x' AS x", "SELECT 1 -- comment", "SELECT 1 /*comment*/", "SELECT 'unterminated", "SELECT 1\x00;DELETE FROM blob"} {
		if inspectionSelect(q) {
			t.Fatalf("unsupported query accepted: %q", q)
		}
	}
}
