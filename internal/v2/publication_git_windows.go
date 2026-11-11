package v2

import (
	"os/exec"
	"syscall"
)

func configurePublicationGitCommand(command *exec.Cmd) {
	// A console application launched by a detached service can open its own
	// console window. Keep Git's standard streams and suppress the window.
	const createNoWindow = 0x08000000
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
