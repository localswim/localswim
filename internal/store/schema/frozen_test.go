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
				if strings.Contains(q, "'related'") {
					t.Fatal("new kinds inherited by frozen source")
				}
			}
		})
	}
	if Version != 7 || BaseSchemaVersion != 5 {
		t.Fatal("version domains changed")
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
	for _, prefix := range []string{"CREATE TABLE board (", "CREATE TABLE operation (", "CREATE TABLE activity ("} {
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
