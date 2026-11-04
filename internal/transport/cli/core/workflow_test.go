package core

import "testing"

func TestMoveAliasRetainsExactInputGrammar(t *testing.T) {
	id := "10000000-0000-4000-8000-000000000001"
	if !moveAlias([]byte(`{"input":{"laneId":"` + id + `"}}`)) {
		t.Fatal("lane UUID rejected")
	}
	for _, wire := range []string{`{"input":{"laneId":null}}`, `{"input":{"laneId":"label"}}`, `{"input":{"lane":"In Progress"}}`, `{"input":{"laneId":"` + id + `","description":"mixed"}}`, `{"input":{"laneId":"` + id + `","laneId":"` + id + `"}}`} {
		if moveAlias([]byte(wire)) {
			t.Fatal("invalid move accepted")
		}
	}
	q, code, _ := parse(args("card", "move", id, "--input", "-"))
	if code != "" || q.command != "card.edit" {
		t.Fatal("move is not card.edit alias")
	}
	q, code, _ = parse(args("card", "search", "--lane", id))
	if code != "" || q.lane != id {
		t.Fatal("custom lane filter rejected")
	}
}
