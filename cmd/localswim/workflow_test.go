package main

import (
	"strings"
	"testing"
)

func TestWorkflowHelpExplainsExistingBoardEditsAndUpgrade(t *testing.T) {
	for _, text := range []string{"workflow show", "workflow preview --input FILE", "workflow edit --input FILE", "attention show", "configured revision1 workflow for a new board", "replacement candidates for an existing board", "--from-version 9 --to-version 10", "Physical10 retains base-schema marker5"} {
		if !strings.Contains(localDomainCLIHelp, text) {
			t.Fatalf("workflow help missing %q", text)
		}
	}
	for _, text := range []string{"complete immutable definitions, valid only for new boards", "Physical8 retains"} {
		if strings.Contains(localDomainCLIHelp, text) {
			t.Fatalf("workflow help retained stale text %q", text)
		}
	}
}

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
