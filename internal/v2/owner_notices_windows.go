package v2

import (
	"os/exec"
	"syscall"
)

const ownerNoticeExecutable = "codex.exe"

func configureOwnerNoticeCommand(command *exec.Cmd) {
	const createNoWindow = 0x08000000
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
