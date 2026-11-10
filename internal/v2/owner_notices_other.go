//go:build !windows

package v2

import "os/exec"

const ownerNoticeExecutable = "codex"

func configureOwnerNoticeCommand(*exec.Cmd) {}
