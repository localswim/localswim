package schema

import (
	"strings"
)

// WorkflowStatements is the additive immutable definition inventory in physical8.
func WorkflowStatements() []string {
	return []string{expand(`CREATE TABLE workflow_definition (
 incarnation TEXT PRIMARY KEY REFERENCES board(incarnation),
 workflow_id TEXT NOT NULL CHECK({{u:workflow_id}}),
 revision INTEGER NOT NULL CHECK(revision=1),
 definition TEXT NOT NULL CHECK(json_valid(definition) AND length(CAST(definition AS BLOB))<=65536)
) STRICT`),
		`CREATE TRIGGER workflow_definition_immutable BEFORE UPDATE ON workflow_definition BEGIN SELECT RAISE(ABORT,'workflow definition immutable'); END`,
		`CREATE TRIGGER workflow_definition_retained BEFORE DELETE ON workflow_definition BEGIN SELECT RAISE(ABORT,'workflow definition retained'); END`}
}

// CardStatements returns the current card objects for explicit physical rebuilds.
func CardStatements() []string {
	var out []string
	for _, q := range Statements() {
		if strings.HasPrefix(q, "CREATE TABLE card (") || strings.HasPrefix(q, "CREATE TRIGGER ") && strings.Contains(q, " ON card BEGIN ") {
			out = append(out, q)
		}
	}
	return out
}
