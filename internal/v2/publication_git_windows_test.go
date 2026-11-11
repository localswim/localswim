package v2

import (
	"os/exec"
	"testing"
)

func TestM92WindowsPublicationGitProcessFlags(t *testing.T) {
	command := exec.Command("git")
	configurePublicationGitCommand(command)
	const createNoWindow = 0x08000000
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags != createNoWindow {
		t.Fatalf("expected CREATE_NO_WINDOW for publication Git, got %+v", command.SysProcAttr)
	}
}
