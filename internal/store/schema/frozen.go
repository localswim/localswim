package schema

import (
	_ "embed"
	"encoding/json"
)

// Each manifest contains the complete independently frozen exact schema inventory.
//
//go:embed layout5-statements.json
var frozenFive []byte

//go:embed layout6-statements.json
var frozenSix []byte

//go:embed layout7-statements.json
var frozenSeven []byte

//go:embed layout8-statements.json
var frozenEight []byte

func frozenStatements(b []byte) []string {
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

//go:embed layout9-statements.json
var frozenNine []byte

func StatementsV9() []string { return frozenStatements(frozenNine) }

//go:embed layout10-statements.json
var frozenTen []byte

func StatementsV10() []string { return frozenStatements(frozenTen) }
