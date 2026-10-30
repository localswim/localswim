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

func frozenStatements(b []byte) []string {
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}
