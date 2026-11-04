package main

import "testing"

func TestWorkspaceWorkflowFlagIsCreateOnly(t *testing.T) {
	base := []string{"--workspace", `C:\workspace`, "--project", "harbor"}
	create := append(append([]string{}, base...), "board", "create", "--key", "custom", "--label", "Custom", "--root", `C:\boards\custom`, "--workflow", "workflow.json")
	q, err := parseLocal(create)
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := parseWorkspaceCommand(q)
	if err != nil || c.workflowFile != "workflow.json" {
		t.Fatal("workflow missing")
	}
	for _, action := range []string{"register", "status"} {
		bad := append([]string{}, create...)
		bad[6] = action
		if _, err := parseLocal(bad); err == nil {
			t.Fatal("workflow accepted outside create")
		}
	}
}
