package v2

import (
	"os/exec"
	"testing"
)

func TestM92WindowsPublicationGitProcessFlags(t *testing.T) {
	command := exec.Command("git")
	tree, err := newPublicationProcessTree(command)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.close()
	const createNoWindow, createSuspended = 0x08000000, 0x00000004
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags != createNoWindow|createSuspended {
		t.Fatalf("expected hidden suspended publication Git, got %+v", command.SysProcAttr)
	}
}
