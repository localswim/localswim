package transport

import (
	"encoding/json"
	"os"
	"testing"
)

func TestInstallationInspectionVectors(t *testing.T) {
	b, e := os.ReadFile("testdata/installation-inspection-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Name  string
		Valid bool
		JSON  string
	}
	if json.Unmarshal(b, &rows) != nil {
		t.Fatal("vectors")
	}
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			v, e := Decode[InstallationInspectionResponse]([]byte(row.JSON))
			if (e == nil) != row.Valid {
				t.Fatal("acceptance mismatch", e)
			}
			if e == nil {
				b, e := Readable(v)
				if e != nil || len(b) > 2048 {
					t.Fatal("encode", e)
				}
			}
		})
	}
}
