package core

import "testing"

func TestWorkflowCommandGrammar(t *testing.T) {
	for _, action := range []string{"show", "preview", "edit"} {
		command := []string{"workflow", action}
		if action != "show" {
			command = append(command, "--input", "-")
		}
		q, code, _ := parse(args(command...))
		if code != "" || q.id != "" {
			t.Fatal("workflow command grammar refused")
		}
		if action == "preview" && (q.method != "POST" || q.path != "/api/v1/boards/"+testBoard+"/workflow/preview") {
			t.Fatal("preview route changed")
		}
		if action == "edit" && (q.method != "PATCH" || q.command != "workflow.edit") {
			t.Fatal("edit bypassed ordinary mutation command")
		}
	}
	for _, command := range [][]string{{"workflow", "preview"}, {"workflow", "edit"}, {"workflow", "show", "--input", "-"}, {"workflow", "edit", testActor, "--input", "-"}, {"workflow", "preview", "--input", "-", "--actor", testActor}} {
		if _, code, _ := parse(args(command...)); code == "" {
			t.Fatal("invalid workflow grammar admitted")
		}
	}
}

func TestMoveAliasRetainsExactInputGrammar(t *testing.T) {
	id := "10000000-0000-4000-8000-000000000001"
	if !moveAlias([]byte(`{"input":{"laneId":"` + id + `"}}`)) {
		t.Fatal("lane UUID rejected")
	}
	if !moveAlias([]byte(`{"input":{"laneId":"In Progress"}}`)) {
		t.Fatal("edited-default stable lane ID refused before policy admission")
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
