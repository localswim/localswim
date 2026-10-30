package hierarchy

import (
	"fmt"
	"testing"
)

func TestForestBoundsCyclesAndSubtree(t *testing.T) {
	p := map[string]string{"root": ""}
	for i := 1; i <= 64; i++ {
		previous := "root"
		if i > 1 {
			previous = fmt.Sprint(i - 1)
		}
		p[fmt.Sprint(i)] = previous
	}
	if e := Validate(p); e != nil {
		t.Fatal("depth64", e)
	}
	p["65"] = "64"
	if Validate(p) == nil {
		t.Fatal("depth65 accepted")
	}
	delete(p, "65")
	p["root"] = "64"
	if Validate(p) == nil {
		t.Fatal("cycle accepted")
	}
	p["root"] = ""
	for i := 65; i < 1000; i++ {
		p[fmt.Sprint(i)] = "root"
	}
	if e := Validate(p); e != nil {
		t.Fatal("family1000", e)
	}
	p["extra"] = "root"
	if Validate(p) == nil {
		t.Fatal("family1001 accepted")
	}
	p["extra"] = ""
	if e := Validate(p); e != nil {
		t.Fatal("independent roots share quota", e)
	}
	p["extra"] = "absent"
	if Validate(p) == nil {
		t.Fatal("dangling accepted")
	}
}
