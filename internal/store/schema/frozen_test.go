package schema

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestFrozenLayoutsRemainIndependent(t *testing.T) {
	for _, v := range []struct {
		name       string
		data       []byte
		hash       string
		statements func() []string
	}{
		{"five", frozenFive, "c861603906f958245553a543e2b34198a7bb73f8d9fb5fed165c9a8de0dbb9ec", StatementsV5},
		{"six", frozenSix, "976025eb1565d378044ae8697a8eb45080fbaf6d290f5e74725c6357f40a9f68", StatementsV6},
		{"seven", frozenSeven, "a544db45d15621ee1a2ff574d32f9713eeb2f66f9d8df7d0f4fbffd6d7d898c1", StatementsV7},
		{"nine", frozenNine, "c9f1346e1ea4a0d1534005649ade96352536eca35f59c8b667da94ac914f54c0", StatementsV9},
		{"ten", frozenTen, "c9f1346e1ea4a0d1534005649ade96352536eca35f59c8b667da94ac914f54c0", StatementsV10},
		{"eleven", frozenEleven, "d2e8d8f8b968d18482baf6b5349a5cd61c9a2a8e5bdc14dc665c8be5beb082ef", StatementsV11},
		{"eight", frozenEight, "36dd653f214938aebbf92c4bb4ab4c760d67c0d2a59739e5c47bda77370af9f7", StatementsV8},
	} {
		t.Run(v.name, func(t *testing.T) {
			if fmt.Sprintf("%x", sha256.Sum256(v.data)) != v.hash {
				t.Fatal("accepted frozen inventory changed")
			}
			original := v.statements()
			changed := v.statements()
			changed[0] = "mutation"
			if v.statements()[0] != original[0] {
				t.Fatal("shared mutable statements")
			}
			for _, q := range original {
				if (v.name == "five" || v.name == "six") && strings.Contains(q, "'related'") {
					t.Fatal("new kinds inherited by frozen source")
				}
			}
		})
	}
	if Version != 12 || BaseSchemaVersion != 5 {
		t.Fatal("version domains changed")
	}
	previous := StatementsV11()
	current := Statements()
	if len(current) != len(previous)+1 || strings.Join(previous, "\n") != strings.Join(current[:len(previous)], "\n") || current[len(previous)] != CoverStatement() {
		t.Fatal("cover layout changed the previous inventory")
	}
	link := strings.Join(LinkStatements(), "\n")
	for _, kind := range []string{"blocking", "informational", "related", "reference", "duplicate"} {
		if !strings.Contains(link, "'"+kind+"'") {
			t.Fatal("missing kind", kind)
		}
	}
	if !strings.Contains(link, "CREATE UNIQUE INDEX active_related_pair") {
		t.Fatal("missing symmetric constraint")
	}
	for _, prefix := range []string{"CREATE TABLE board (", "CREATE TABLE grant_record (", "CREATE TABLE operation_full (", "CREATE TABLE attachment ("} {
		table := func(statements []string) string {
			for _, q := range statements {
				if strings.HasPrefix(q, prefix) {
					return q
				}
			}
			return ""
		}
		if old, current := table(StatementsV6()), table(Statements()); old == "" || old != current {
			t.Fatal("unrelated frozen table changed", prefix)
		}
	}

}
